package instagram

import (
	"fmt"
	"io"
	"net/http"
	"regexp"

	"github.com/govdbot/govd/internal/logger"
	"github.com/govdbot/govd/internal/models"
	"github.com/govdbot/govd/internal/networking"
)

var instagramHost = []string{"instagram", "ddinstagram"}

var Extractor = &models.Extractor{
	ID:          "instagram",
	DisplayName: "Instagram",

	URLPattern: regexp.MustCompile(`https:\/\/(www\.)?(?:dd)?instagram\.com\/(reels?|p|tv)\/(?P<id>[a-zA-Z0-9_-]+)`),
	Host:       instagramHost,
	Redirect:   false,

	GetFunc: func(ctx *models.ExtractorContext) (*models.ExtractorResponse, error) {
		// method 1: get media from GQL web API
		media, err1 := GetGQLMedia(ctx)
		if err1 == nil {
			return &models.ExtractorResponse{
				Media: media,
			}, nil
		}
		// method 2: get media from embed page
		media, err2 := GetEmbedMedia(ctx)
		if err2 == nil {
			return &models.ExtractorResponse{
				Media: media,
			}, nil
		}
		return nil, fmt.Errorf("all methods failed: %w; %w", err1, err2)
	},
}

var StoriesExtractor = &models.Extractor{
	ID:          "instagram",
	DisplayName: "Instagram Stories",

	URLPattern: regexp.MustCompile(`https:\/\/(www\.)?(?:dd)?instagram\.com\/stories\/(?P<user>[a-zA-Z0-9._]+)\/(?P<id>\d+)`),
	Host:       instagramHost,
	Hidden:     true,

	GetFunc: func(ctx *models.ExtractorContext) (*models.ExtractorResponse, error) {
		media, err := GetNativeStory(ctx)
		if err != nil {
			return nil, err
		}
		return &models.ExtractorResponse{
			Media: media,
		}, nil
	},
}

var ShareURLExtractor = &models.Extractor{
	ID:          "instagram",
	DisplayName: "Instagram (Share)",

	URLPattern: regexp.MustCompile(`https?:\/\/(www\.)?(?:dd)?instagram\.com\/share\/((reels?|video|s|p)\/)?(?P<id>[^\/\?]+)`),
	Host:       instagramHost,

	Redirect: true,

	GetFunc: func(ctx *models.ExtractorContext) (*models.ExtractorResponse, error) {
		redirectURL, err := ctx.FetchLocation(ctx.ContentURL, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to get url location: %w", err)
		}
		return &models.ExtractorResponse{URL: redirectURL}, nil
	},
}

func GetGQLMedia(ctx *models.ExtractorContext) (*models.Media, error) {
	graphData, err := GetGQLData(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get graph data: %w", err)
	}
	return ParsePolarisMedia(ctx, graphData)
}

func GetEmbedMedia(ctx *models.ExtractorContext) (*models.Media, error) {
	embedURL := fmt.Sprintf(
		"https://www.instagram.com/p/%s/embed/captioned",
		ctx.ContentID,
	)
	resp, err := ctx.Fetch(
		http.MethodGet,
		embedURL,
		&networking.RequestParams{
			Headers: webHeaders,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	logger.WriteFile("ig_embed_response", resp)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get embed page: %s", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}
	graphData, err := ParseEmbedGQL(body)
	if err != nil {
		return nil, fmt.Errorf("failed to parse embed page: %w", err)
	}
	return ParseGQLMedia(ctx, graphData)
}
