package instagram

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/govdbot/govd/internal/database"
	"github.com/govdbot/govd/internal/logger"
	"github.com/govdbot/govd/internal/models"
	"github.com/govdbot/govd/internal/networking"
	"github.com/govdbot/govd/internal/util"

	"github.com/bytedance/sonic"
	"github.com/titanous/json5"
)

const (
	graphQLEndpoint = "https://www.instagram.com/api/graphql"
	polarisAction   = "PolarisLoggedOutDesktopWWWPostRootContentQuery"
	graphQLDocID    = "27130156389949648"

	// webAppID is the Instagram web application id.
	webAppID = "936619743392459"

	// desktopUserAgent must stay coherent with the sec-ch-ua headers below,
	// otherwise Instagram serves the logged-out shell instead of GraphQL data.
	desktopUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"

	igramHostname = "api-wh.igram.world"
	igramAPIBase  = "api.igram.world"
	igramHMACKey  = "75f2d70d3724f98e4a7d1ffd0ba9cfd907f3ae2632ee159980e2c521bff62358"
	igramStaticTS = 1771418815381 // parseInt("mls10xp1", 36)
)

var (
	embedPattern = regexp.MustCompile(
		`new ServerJS\(\)\);s\.handle\(({.*})\);requireLazy`)

	webHeaders = map[string]string{
		"Accept":                    "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7",
		"Accept-Language":           "en-GB,en;q=0.9",
		"Cache-Control":             "max-age=0",
		"Dnt":                       "1",
		"Priority":                  "u=0, i",
		"Sec-Ch-Ua":                 `Chromium";v="124", "Google Chrome";v="124", "Not-A.Brand";v="99`,
		"Sec-Ch-Ua-Mobile":          "?0",
		"Sec-Ch-Ua-Platform":        "macOS",
		"Sec-Fetch-Dest":            "document",
		"Sec-Fetch-Mode":            "navigate",
		"Sec-Fetch-Site":            "none",
		"Sec-Fetch-User":            "?1",
		"Upgrade-Insecure-Requests": "1",
	}

	igramHeaders = map[string]string{
		"Referer": "https://igram.world/",
	}

	// graphQLHeaders mirror a logged-out desktop browser fetch. Instagram
	// rejects requests whose UA and client hints do not agree.
	graphQLHeaders = map[string]string{
		"User-Agent":         desktopUserAgent,
		"Accept":             "*/*",
		"Accept-Language":    "en-US,en;q=0.9",
		"Sec-Ch-Ua":          `"Chromium";v="124", "Google Chrome";v="124", "Not-A.Brand";v="99"`,
		"Sec-Ch-Ua-Mobile":   "?0",
		"Sec-Ch-Ua-Platform": `"Windows"`,
		"Sec-Fetch-Dest":     "empty",
		"Sec-Fetch-Mode":     "cors",
		"Sec-Fetch-Site":     "same-origin",
		"Origin":             "https://www.instagram.com",
		"X-IG-App-ID":        webAppID,
		"X-FB-Friendly-Name": polarisAction,
		"X-Requested-With":   "XMLHttpRequest",
		"Content-Type":       "application/x-www-form-urlencoded",
	}

	// apiHeaders are used for the authenticated mobile/web API endpoints.
	apiHeaders = map[string]string{
		"X-IG-App-ID":    webAppID,
		"X-ASBD-ID":      "359341",
		"X-IG-WWW-Claim": "0",
		"Accept":         "*/*",
		"Referer":        "https://www.instagram.com/",
	}
)

func ParseGQLMedia(ctx *models.ExtractorContext, data *Media) (*models.Media, error) {
	var caption string
	if data.EdgeMediaToCaption != nil && len(data.EdgeMediaToCaption.Edges) > 0 {
		caption = data.EdgeMediaToCaption.Edges[0].Node.Text
	}

	media := ctx.NewMedia()
	media.SetCaption(caption)

	switch data.Typename {
	case "GraphVideo", "XDTGraphVideo":
		item := media.NewItem()
		item.AddFormats(&models.MediaFormat{
			FormatID:     "video",
			Type:         database.MediaTypeVideo,
			VideoCodec:   database.MediaCodecAvc,
			AudioCodec:   database.MediaCodecAac,
			URL:          []string{data.VideoURL},
			ThumbnailURL: []string{data.DisplayURL},
			Width:        data.Dimensions.Width,
			Height:       data.Dimensions.Height,
		})
	case "GraphImage", "XDTGraphImage":
		item := media.NewItem()
		item.AddFormats(&models.MediaFormat{
			FormatID: "image",
			Type:     database.MediaTypePhoto,
			URL:      []string{data.DisplayURL},
		})
	case "GraphSidecar", "XDTGraphSidecar":
		if data.EdgeSidecarToChildren != nil && len(data.EdgeSidecarToChildren.Edges) > 0 {
			edges := data.EdgeSidecarToChildren.Edges

			for i := range edges {
				item := media.NewItem()
				node := edges[i].Node

				switch node.Typename {
				case "GraphVideo", "XDTGraphVideo":
					item.AddFormats(&models.MediaFormat{
						FormatID:     "video",
						Type:         database.MediaTypeVideo,
						VideoCodec:   database.MediaCodecAvc,
						AudioCodec:   database.MediaCodecAac,
						URL:          []string{node.VideoURL},
						ThumbnailURL: []string{node.DisplayURL},
						Width:        node.Dimensions.Width,
						Height:       node.Dimensions.Height,
					})

				case "GraphImage", "XDTGraphImage":
					item.AddFormats(&models.MediaFormat{
						FormatID: "image",
						Type:     database.MediaTypePhoto,
						URL:      []string{node.DisplayURL},
					})
				}
			}
		}
	}

	return media, nil
}

func ParseEmbedGQL(body []byte) (*Media, error) {
	match := embedPattern.FindSubmatch(body)
	if len(match) < 2 {
		return nil, fmt.Errorf("gql json not found")
	}
	jsonData := match[1]

	var data map[string]any
	if err := json5.Unmarshal(jsonData, &data); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON: %w", err)
	}
	igCtx := util.TraverseJSON(data, "contextJSON")
	if igCtx == nil {
		return nil, fmt.Errorf("contextJSON not found")
	}
	var ctxJSON ContextJSON
	switch v := igCtx.(type) {
	case string:
		if err := json5.Unmarshal([]byte(v), &ctxJSON); err != nil {
			return nil, fmt.Errorf("failed to unmarshal contextJSON: %w", err)
		}
	default:
		return nil, fmt.Errorf("unexpected type for contextJSON: %T", v)
	}
	if ctxJSON.GqlData == nil {
		return nil, fmt.Errorf("gql_data not found")
	}
	if ctxJSON.GqlData.ShortcodeMedia == nil {
		return nil, fmt.Errorf("shortcode_media not found")
	}
	return ctxJSON.GqlData.ShortcodeMedia, nil
}

func IGramBodyFromURL(contentURL string) (io.Reader, error) {
	return igramBuildPayload(map[string]string{
		"target_url": contentURL,
	})
}

func IGramBodyFromParams(params map[string]string) (io.Reader, error) {
	return igramBuildPayload(params)
}

func igramBuildPayload(urlParams map[string]string) (io.Reader, error) {
	nowMs := time.Now().UnixMilli()
	serverMs := getIGramServerTime()

	drift := serverMs - nowMs
	var correction int64
	if drift >= 60000 || drift <= -60000 {
		correction = drift
	}
	ts := nowMs + correction

	// partial payload fields that get signed
	partial := map[string]any{
		"_sc": 0,
		"_ef": 0,
		"_df": 0,
	}
	for k, v := range urlParams {
		partial[k] = v
	}

	sig, err := igramSign(partial, ts)
	if err != nil {
		return nil, err
	}

	// assemble final payload
	final := make(map[string]any, len(partial)+5)
	for k, v := range partial {
		final[k] = v
	}
	final["ts"] = ts
	final["_ts"] = igramStaticTS
	final["_tsc"] = correction
	final["_sv"] = 2
	final["_s"] = sig

	jsonBytes, err := sonic.ConfigFastest.Marshal(final)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal payload: %w", err)
	}

	return strings.NewReader(string(jsonBytes)), nil
}

func igramSign(partial map[string]any, ts int64) (string, error) {
	// sonic.ConfigStd sorts map keys alphabetically, matching
	// the signing: JSON.stringify(sorted_partial) + String(ts)
	jsonBytes, err := sonic.ConfigStd.Marshal(partial)
	if err != nil {
		return "", fmt.Errorf("failed to marshal partial payload: %w", err)
	}

	data := string(jsonBytes) + strconv.FormatInt(ts, 10)

	keyBytes, err := hex.DecodeString(igramHMACKey)
	if err != nil {
		return "", fmt.Errorf("failed to decode HMAC key: %w", err)
	}

	mac := hmac.New(sha256.New, keyBytes)
	mac.Write([]byte(data))
	return hex.EncodeToString(mac.Sum(nil)), nil
}

func getIGramServerTime() int64 {
	apiURL := fmt.Sprintf("https://%s/msec", igramAPIBase)
	resp, err := http.Get(apiURL)
	if err != nil {
		return time.Now().UnixMilli()
	}
	defer resp.Body.Close()

	var result struct {
		Msec float64 `json:"msec"`
	}
	decoder := sonic.ConfigFastest.NewDecoder(resp.Body)
	if err := decoder.Decode(&result); err != nil {
		return time.Now().UnixMilli()
	}
	return int64(result.Msec * 1000)
}

func ParseIGramResponse(body []byte) (*IGramResponse, error) {
	// try to unmarshal as a single IGramMedia and then as a slice
	var media IGramMedia

	if err := sonic.ConfigFastest.Unmarshal(body, &media); err != nil {
		// try with slice
		var mediaList []*IGramMedia
		if err := sonic.ConfigFastest.Unmarshal(body, &mediaList); err != nil {
			return nil, fmt.Errorf("failed to decode response: %w", err)
		}
		return &IGramResponse{
			Items: mediaList,
		}, nil
	}
	if media.Success != nil && !(*media.Success) {
		return nil, util.ErrUnavailable
	}
	return &IGramResponse{
		Items: []*IGramMedia{&media},
	}, nil
}

func GetCDNURL(contentURL string) (string, error) {
	parsedURL, err := url.Parse(contentURL)
	if err != nil {
		return "", fmt.Errorf("can't parse igram URL: %w", err)
	}
	queryParams, err := url.ParseQuery(parsedURL.RawQuery)
	if err != nil {
		return "", fmt.Errorf("can't unescape igram URL: %w", err)
	}
	cdnURL := queryParams.Get("uri")
	return cdnURL, nil
}

// ShortcodeToPK converts an Instagram shortcode (e.g. "DdSNkXyjM2y") into its
// numeric media id using Instagram's base64url-like alphabet.
func ShortcodeToPK(shortcode string) (string, error) {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"

	if shortcode == "" {
		return "", fmt.Errorf("empty shortcode")
	}

	var pk uint64
	for _, char := range shortcode {
		index := strings.IndexRune(alphabet, char)
		if index < 0 {
			return "", fmt.Errorf("invalid character %q in shortcode", char)
		}
		if pk > (math.MaxUint64-uint64(index))/64 {
			return "", fmt.Errorf("shortcode is too large")
		}
		pk = pk*64 + uint64(index)
	}

	return strconv.FormatUint(pk, 10), nil
}

func GetGQLData(ctx *models.ExtractorContext) (*PolarisMediaItem, error) {
	mediaID, err := ShortcodeToPK(ctx.ContentID)
	if err != nil {
		return nil, fmt.Errorf("failed to convert shortcode: %w", err)
	}

	variablesJSON, err := sonic.ConfigFastest.Marshal(map[string]string{
		"media_id": mediaID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal variables: %w", err)
	}

	formData := url.Values{}
	formData.Set("fb_api_caller_class", "RelayModern")
	formData.Set("fb_api_req_friendly_name", polarisAction)
	formData.Set("server_timestamps", "true")
	formData.Set("variables", string(variablesJSON))
	formData.Set("doc_id", graphQLDocID)

	headers := make(map[string]string, len(graphQLHeaders)+1)
	for key, value := range graphQLHeaders {
		headers[key] = value
	}
	headers["Referer"] = fmt.Sprintf(
		"https://www.instagram.com/p/%s/", ctx.ContentID,
	)

	resp, err := ctx.Fetch(
		http.MethodPost,
		graphQLEndpoint,
		&networking.RequestParams{
			Headers: headers,
			Body:    strings.NewReader(formData.Encode()),
			// this endpoint must be requested anonymously: sending the
			// logged-in session cookies yields an HTML page instead of JSON.
			SkipCookies: true,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	logger.WriteFile("iggql_api_response", resp)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("invalid response code: %s", resp.Status)
	}

	var response PolarisGraphQLResponse
	decoder := sonic.ConfigFastest.NewDecoder(resp.Body)
	if err := decoder.Decode(&response); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}
	if response.Data == nil || response.Data.XIGPolarisMedia == nil {
		return nil, util.ErrUnavailable
	}
	media := response.Data.XIGPolarisMedia.IfNotGatedLoggedOut
	if media == nil {
		return nil, util.ErrUnavailable
	}
	return media, nil
}

// ParsePolarisMedia converts a Polaris media node into the extractor's media
// model. A carousel becomes one item per child node, while single photos and
// videos become a single item.
func ParsePolarisMedia(ctx *models.ExtractorContext, data *PolarisMediaItem) (*models.Media, error) {
	media := ctx.NewMedia()
	if data.Caption != nil {
		media.SetCaption(data.Caption.Text)
	}

	if data.MediaType == 8 && len(data.CarouselMedia) > 0 {
		for _, node := range data.CarouselMedia {
			addPolarisItem(media, node)
		}
	} else {
		addPolarisItem(media, data)
	}

	if len(media.Items) == 0 {
		return nil, util.ErrUnavailable
	}
	return media, nil
}

func addPolarisItem(media *models.Media, data *PolarisMediaItem) {
	if data == nil {
		return
	}

	if video := GetBestVideoVersion(data.VideoVersions); video != nil && video.URL != "" {
		item := media.NewItem()
		item.AddFormats(&models.MediaFormat{
			FormatID:     "video",
			Type:         database.MediaTypeVideo,
			VideoCodec:   database.MediaCodecAvc,
			AudioCodec:   database.MediaCodecAac,
			URL:          []string{video.URL},
			ThumbnailURL: bestThumbnailURL(data.ImageVersions),
			Width:        int32(video.Width),
			Height:       int32(video.Height),
		})
		return
	}

	var candidates []*Candidates
	if data.ImageVersions != nil {
		candidates = data.ImageVersions.Candidates
	}
	if image := GetBestCandidate(candidates); image != nil && image.URL != "" {
		item := media.NewItem()
		item.AddFormats(&models.MediaFormat{
			FormatID: "image",
			Type:     database.MediaTypePhoto,
			URL:      []string{image.URL},
			Width:    int32(image.Width),
			Height:   int32(image.Height),
		})
	}
}

func bestThumbnailURL(imageVersions *ImageVersions) []string {
	if imageVersions == nil {
		return nil
	}
	image := GetBestCandidate(imageVersions.Candidates)
	if image == nil || image.URL == "" {
		return nil
	}
	return []string{image.URL}
}

// GetNativeStory fetches a story through Instagram's authenticated API.
// Highlights are fetched as a whole reel, while a regular story is fetched
// directly by its media id (the id from the URL).
func GetNativeStory(ctx *models.ExtractorContext) (*models.Media, error) {
	if ctx.MatchGroups["user"] == "highlights" {
		return GetHighlightMedia(ctx)
	}
	return GetStoryMediaByID(ctx)
}

func GetStoryMediaByID(ctx *models.ExtractorContext) (*models.Media, error) {
	apiURL := fmt.Sprintf(
		"https://www.instagram.com/api/v1/media/%s/info/",
		ctx.ContentID,
	)
	resp, err := ctx.Fetch(
		http.MethodGet,
		apiURL,
		&networking.RequestParams{Headers: apiHeaders},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	logger.WriteFile("ig_story_info_response", resp)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("invalid response code: %s", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}
	var data MediaInfoResponse
	if err := sonic.ConfigFastest.Unmarshal(body, &data); err != nil {
		return nil, util.ErrAuthenticationNeeded
	}
	if len(data.Items) == 0 {
		return nil, util.ErrUnavailable
	}
	return ParsePolarisMedia(ctx, data.Items[0])
}

func GetHighlightMedia(ctx *models.ExtractorContext) (*models.Media, error) {
	reelID := "highlight:" + ctx.ContentID
	apiURL := "https://www.instagram.com/api/v1/feed/reels_media/?reel_ids=" + reelID
	resp, err := ctx.Fetch(
		http.MethodGet,
		apiURL,
		&networking.RequestParams{Headers: apiHeaders},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	logger.WriteFile("ig_highlight_response", resp)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("invalid response code: %s", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}
	var data ReelsMediaResponse
	if err := sonic.ConfigFastest.Unmarshal(body, &data); err != nil {
		return nil, util.ErrAuthenticationNeeded
	}
	reel := data.Reels[reelID]
	if reel == nil || len(reel.Items) == 0 {
		return nil, util.ErrUnavailable
	}
	media := ctx.NewMedia()
	for _, item := range reel.Items {
		addPolarisItem(media, item)
	}
	if len(media.Items) == 0 {
		return nil, util.ErrUnavailable
	}
	return media, nil
}

func GetBestCandidate(candidates []*Candidates) *Candidates {
	if len(candidates) == 0 {
		return nil
	}
	best := candidates[0]
	for _, candidate := range candidates {
		if candidate.Width > best.Width {
			best = candidate
		}
	}
	return best
}

func GetBestVideoVersion(versions []*VideoVersions) *VideoVersions {
	if len(versions) == 0 {
		return nil
	}
	best := versions[0]
	for _, version := range versions {
		if version.Width > best.Width {
			best = version
		}
	}
	return best
}
