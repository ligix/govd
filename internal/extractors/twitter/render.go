package twitter

import (
	"archive/zip"
	"bytes"
	_ "embed"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/rivo/uniseg"
	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"

	"github.com/govdbot/govd/internal/models"

	_ "image/jpeg"
)

//go:embed assets/fonts/Inter-Regular.ttf
var interRegular []byte

//go:embed assets/fonts/Inter-Bold.ttf
var interBold []byte

//go:embed assets/twemoji/72x72.zip
var twemojiZip []byte

const (
	cardWidth   = 1200
	cardPadding = 48
	avatarSize  = 112
	headerGap   = 24

	nameSize    = 44
	handleSize  = 40
	bodySize    = 44
	metricsSize = 38

	lineHeightFactor = 1.45
	maxCardHeight    = 5000
)

var (
	textColor      = color.RGBA{R: 0xE7, G: 0xE9, B: 0xEA, A: 0xFF}
	secondaryColor = color.RGBA{R: 0x71, G: 0x76, B: 0x7B, A: 0xFF}
	accentColor    = color.RGBA{R: 0x1D, G: 0x9B, B: 0xF0, A: 0xFF}
	cardColor      = color.RGBA{R: 0x15, G: 0x20, B: 0x2B, A: 0xFF}
	avatarFill     = color.RGBA{R: 0x2F, G: 0x33, B: 0x36, A: 0xFF}
)

var (
	loadRegular = sync.OnceValues(func() (*opentype.Font, error) {
		return opentype.Parse(interRegular)
	})
	loadBold = sync.OnceValues(func() (*opentype.Font, error) {
		return opentype.Parse(interBold)
	})
	loadEmojiZip = sync.OnceValues(func() (*zip.Reader, error) {
		return zip.NewReader(bytes.NewReader(twemojiZip), int64(len(twemojiZip)))
	})

	avatarCache, _ = lru.New[string, image.Image](128)
	emojiCache, _  = lru.New[string, image.Image](512)
)

// TweetCard is the data rendered into an image for posts without media.
type TweetCard struct {
	Name      string
	Handle    string
	Verified  bool
	Text      string
	CreatedAt string
	Replies   int
	Reposts   int
	Likes     int
	Views     string
}

// RenderTweetCard fetches the tweet for the given context and renders it
// as a PNG image.
func RenderTweetCard(ctx *models.ExtractorContext) ([]byte, error) {
	tweet, err := GetTweetAPI(ctx)
	if err != nil {
		return nil, err
	}
	return renderTweetCard(ctx, tweet)
}

func renderTweetCard(ctx *models.ExtractorContext, tweet *Tweet) ([]byte, error) {
	card := TweetCard{
		Name:      tweet.AuthorName,
		Handle:    tweet.AuthorHandle,
		Verified:  tweet.AuthorVerified,
		Text:      SanitizeCaption(tweet.FullText),
		CreatedAt: tweet.CreatedAt,
		Replies:   tweet.ReplyCount,
		Reposts:   tweet.RetweetCount,
		Likes:     tweet.FavoriteCount,
		Views:     tweet.ViewCount,
	}

	var avatar image.Image
	if tweet.AuthorAvatar != "" {
		avatar = fetchAvatar(ctx, tweet.AuthorAvatar)
	}

	return renderCard(card, avatar)
}

func fetchAvatar(ctx *models.ExtractorContext, url string) image.Image {
	if img, ok := avatarCache.Get(url); ok {
		return img
	}
	resp, err := ctx.Fetch(http.MethodGet, url, nil)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	img, _, err := image.Decode(resp.Body)
	if err != nil {
		return nil
	}
	avatarCache.Add(url, img)
	return img
}

// cluster is a single grapheme, either rendered as text or as a Twemoji image.
type cluster struct {
	text  string
	emoji bool
	key   string
}

func renderCard(card TweetCard, avatar image.Image) ([]byte, error) {
	regular, err := loadRegular()
	if err != nil {
		return nil, fmt.Errorf("failed to load regular font: %w", err)
	}
	bold, err := loadBold()
	if err != nil {
		return nil, fmt.Errorf("failed to load bold font: %w", err)
	}

	nameFace, err := newFace(bold, nameSize)
	if err != nil {
		return nil, err
	}
	defer nameFace.Close()

	handleFace, err := newFace(regular, handleSize)
	if err != nil {
		return nil, err
	}
	defer handleFace.Close()

	bodyFace, err := newFace(regular, bodySize)
	if err != nil {
		return nil, err
	}
	defer bodyFace.Close()

	metricsFace, err := newFace(regular, metricsSize)
	if err != nil {
		return nil, err
	}
	defer metricsFace.Close()

	maxTextWidth := cardWidth - cardPadding*2
	lines := layoutText(card.Text, bodyFace, maxTextWidth, bodySize)

	bodyMetrics := bodyFace.Metrics()
	lineHeight := int(math.Ceil(float64(bodySize) * lineHeightFactor))
	textHeight := len(lines) * lineHeight

	headerHeight := avatarSize
	textTop := cardPadding + headerHeight + headerGap
	metricsTop := textTop + textHeight + headerGap

	headerLine := "@" + card.Handle
	metricsLine := formatMetrics(card)
	timestamp := formatTimestamp(card.CreatedAt)

	totalHeight := metricsTop + int(metricsFace.Metrics().Height.Ceil()) + cardPadding
	if timestamp != "" {
		totalHeight += int(metricsFace.Metrics().Height.Ceil()) + 8
	}
	if totalHeight < cardPadding*2+avatarSize {
		totalHeight = cardPadding*2 + avatarSize
	}
	if totalHeight > maxCardHeight {
		totalHeight = maxCardHeight
	}

	img := image.NewRGBA(image.Rect(0, 0, cardWidth, totalHeight))
	drawSolid(img, img.Bounds(), cardColor)

	// header
	avatarX := cardPadding
	avatarY := cardPadding
	if avatar != nil {
		drawImage(img, circularAvatar(avatar, avatarSize), avatarX, avatarY)
	} else {
		fillCircle(img, avatarX+avatarSize/2, avatarY+avatarSize/2, avatarSize/2, avatarFill)
	}

	nameX := cardPadding + avatarSize + headerGap
	nameAscent := int(nameFace.Metrics().Ascent.Ceil())
	nameBaseline := cardPadding + nameAscent
	drawString(img, nameFace, textColor, card.Name, nameX, nameBaseline)

	if card.Verified {
		badgeSize := 36
		badgeX := nameX + font.MeasureString(nameFace, card.Name).Ceil() + 12
		drawVerifiedBadge(img, badgeX, nameBaseline-nameAscent+4, badgeSize)
	}

	nameHeight := int(nameFace.Metrics().Height.Ceil())
	handleBaseline := cardPadding + nameHeight + int(handleFace.Metrics().Ascent.Ceil())
	drawString(img, handleFace, secondaryColor, headerLine, nameX, handleBaseline)

	// body
	drawColor := textColor
	for i, line := range lines {
		baseline := textTop + i*lineHeight + int(bodyMetrics.Ascent.Ceil())
		drawClusters(img, line, bodyFace, bodySize, cardPadding, baseline, drawColor)
	}

	// metrics
	drawString(img, metricsFace, secondaryColor, metricsLine, cardPadding, metricsTop+int(metricsFace.Metrics().Ascent.Ceil()))
	if timestamp != "" {
		tsTop := metricsTop + int(metricsFace.Metrics().Height.Ceil()) + 8
		drawString(img, metricsFace, secondaryColor, timestamp, cardPadding, tsTop+int(metricsFace.Metrics().Ascent.Ceil()))
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("failed to encode image: %w", err)
	}
	return buf.Bytes(), nil
}

func newFace(f *opentype.Font, size float64) (font.Face, error) {
	return opentype.NewFace(f, &opentype.FaceOptions{
		Size:    size,
		DPI:     72,
		Hinting: font.HintingFull,
	})
}

func layoutText(text string, face font.Face, maxWidth, emojiSize int) [][]cluster {
	var lines [][]cluster
	spaceWidth := font.MeasureString(face, " ").Ceil()

	pushWord := func(line *[]cluster, lineWidth *int, word []cluster) {
		wordWidth := clustersWidth(word, face, emojiSize)

		// a single word wider than the line is broken as a last resort
		if wordWidth > maxWidth {
			for _, cl := range word {
				w := clusterWidth(cl, face, emojiSize)
				if *lineWidth > 0 && *lineWidth+w > maxWidth {
					lines = append(lines, *line)
					*line = nil
					*lineWidth = 0
				}
				*line = append(*line, cl)
				*lineWidth += w
			}
			return
		}

		if *lineWidth > 0 && *lineWidth+spaceWidth+wordWidth > maxWidth {
			lines = append(lines, *line)
			*line = nil
			*lineWidth = 0
		}
		if *lineWidth > 0 {
			*line = append(*line, cluster{text: " "})
			*lineWidth += spaceWidth
		}
		*line = append(*line, word...)
		*lineWidth += wordWidth
	}

	for _, paragraph := range strings.Split(text, "\n") {
		var line []cluster
		lineWidth := 0
		for _, word := range splitWords(paragraph) {
			pushWord(&line, &lineWidth, word)
		}
		lines = append(lines, line)
	}

	return lines
}

func splitWords(s string) [][]cluster {
	var words [][]cluster
	var current []cluster
	for _, cl := range splitClusters(s) {
		if cl.text == " " {
			if len(current) > 0 {
				words = append(words, current)
				current = nil
			}
			continue
		}
		current = append(current, cl)
	}
	if len(current) > 0 {
		words = append(words, current)
	}
	return words
}

func clustersWidth(clusters []cluster, face font.Face, emojiSize int) int {
	total := 0
	for _, cl := range clusters {
		total += clusterWidth(cl, face, emojiSize)
	}
	return total
}

func splitClusters(s string) []cluster {
	var out []cluster
	g := uniseg.NewGraphemes(s)
	for g.Next() {
		text := g.Str()
		if key, ok := emojiKey(text); ok {
			out = append(out, cluster{text: text, emoji: true, key: key})
			continue
		}
		out = append(out, cluster{text: text})
	}
	return out
}

func clusterWidth(cl cluster, face font.Face, emojiSize int) int {
	if cl.emoji {
		return emojiSize
	}
	return font.MeasureString(face, cl.text).Ceil()
}

func drawClusters(dst *image.RGBA, line []cluster, face font.Face, emojiSize, x, baseline int, textColor color.Color) {
	for i := 0; i < len(line); {
		if line[i].emoji {
			if emoji := emojiImage(line[i].key); emoji != nil {
				drawScaled(dst, emoji, x, baseline-emojiSize, emojiSize)
			} else {
				drawString(dst, face, textColor, line[i].text, x, baseline)
			}
			x += emojiSize
			i++
			continue
		}

		j := i
		var sb strings.Builder
		for j < len(line) && !line[j].emoji {
			sb.WriteString(line[j].text)
			j++
		}
		run := sb.String()
		drawString(dst, face, textColor, run, x, baseline)
		x += font.MeasureString(face, run).Ceil()
		i = j
	}
}

func emojiKey(s string) (string, bool) {
	runes := []rune(s)
	if len(runes) == 0 {
		return "", false
	}
	if !isEmojiRune(runes[0]) && !strings.ContainsRune(s, '\u20e3') {
		return "", false
	}
	parts := make([]string, 0, len(runes))
	for _, r := range runes {
		if r == '\ufe0f' {
			continue
		}
		parts = append(parts, strconv.FormatInt(int64(r), 16))
	}
	if len(parts) == 0 {
		return "", false
	}
	return strings.Join(parts, "-"), true
}

func isEmojiRune(r rune) bool {
	switch {
	case r >= 0x1F000 && r <= 0x1FAFF:
		return true
	case r >= 0x2600 && r <= 0x27BF:
		return true
	case r >= 0x2190 && r <= 0x21FF:
		return true
	case r >= 0x2300 && r <= 0x23FF:
		return true
	case r >= 0x2B00 && r <= 0x2BFF:
		return true
	case r >= 0x2900 && r <= 0x297F:
		return true
	case r == 0x00A9, r == 0x00AE, r == 0x203C, r == 0x2049:
		return true
	case r == 0x2122, r == 0x2139, r == 0x3030, r == 0x303D, r == 0x3297, r == 0x3299:
		return true
	default:
		return false
	}
}

func emojiImage(key string) image.Image {
	if img, ok := emojiCache.Get(key); ok {
		return img
	}
	zr, err := loadEmojiZip()
	if err != nil {
		return nil
	}
	f, err := zr.Open(key + ".png")
	if err != nil {
		return nil
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return nil
	}
	emojiCache.Add(key, img)
	return img
}

func circularAvatar(src image.Image, size int) *image.RGBA {
	scaled := image.NewRGBA(image.Rect(0, 0, size, size))
	xdraw.CatmullRom.Scale(scaled, scaled.Bounds(), src, src.Bounds(), xdraw.Over, nil)

	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	center := float64(size) / 2
	radius := center * center
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			dx := float64(x) + 0.5 - center
			dy := float64(y) + 0.5 - center
			if dx*dx+dy*dy <= radius {
				dst.Set(x, y, scaled.At(x, y))
			}
		}
	}
	return dst
}

func drawSolid(dst *image.RGBA, r image.Rectangle, c color.Color) {
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			dst.Set(x, y, c)
		}
	}
}

func drawString(dst *image.RGBA, face font.Face, c color.Color, s string, x, baseline int) {
	if s == "" {
		return
	}
	d := &font.Drawer{
		Dst:  dst,
		Src:  image.NewUniform(c),
		Face: face,
		Dot:  fixed.P(x, baseline),
	}
	d.DrawString(s)
}

func drawImage(dst *image.RGBA, src image.Image, x, y int) {
	xdraw.Draw(dst, image.Rect(x, y, x+src.Bounds().Dx(), y+src.Bounds().Dy()), src, src.Bounds().Min, xdraw.Over)
}

func drawScaled(dst *image.RGBA, src image.Image, x, y, size int) {
	xdraw.CatmullRom.Scale(dst, image.Rect(x, y, x+size, y+size), src, src.Bounds(), xdraw.Over, nil)
}

func fillCircle(dst *image.RGBA, cx, cy, radius int, c color.Color) {
	for y := cy - radius; y <= cy+radius; y++ {
		for x := cx - radius; x <= cx+radius; x++ {
			dx, dy := x-cx, y-cy
			if dx*dx+dy*dy <= radius*radius {
				dst.Set(x, y, c)
			}
		}
	}
}

func drawVerifiedBadge(dst *image.RGBA, x, y, size int) {
	radius := size / 2
	fillCircle(dst, x+radius, y+radius, radius, accentColor)

	white := color.RGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}
	thickness := size / 8
	drawThickLine(dst, x+size/4, y+size/2+size/10, x+size*2/5, y+size*2/3, thickness, white)
	drawThickLine(dst, x+size*2/5, y+size*2/3, x+size*3/4, y+size/3, thickness, white)
}

func drawThickLine(dst *image.RGBA, x1, y1, x2, y2, thickness int, c color.Color) {
	half := float64(thickness) / 2
	minX := min(x1, x2) - thickness
	maxX := max(x1, x2) + thickness
	minY := min(y1, y2) - thickness
	maxY := max(y1, y2) + thickness

	for y := minY; y <= maxY; y++ {
		for x := minX; x <= maxX; x++ {
			if distToSegment(float64(x), float64(y), float64(x1), float64(y1), float64(x2), float64(y2)) <= half {
				dst.Set(x, y, c)
			}
		}
	}
}

func distToSegment(px, py, x1, y1, x2, y2 float64) float64 {
	dx, dy := x2-x1, y2-y1
	lengthSq := dx*dx + dy*dy
	if lengthSq == 0 {
		return math.Hypot(px-x1, py-y1)
	}
	t := ((px-x1)*dx + (py-y1)*dy) / lengthSq
	t = math.Max(0, math.Min(1, t))
	return math.Hypot(px-(x1+t*dx), py-(y1+t*dy))
}

func formatMetrics(card TweetCard) string {
	parts := make([]string, 0, 4)
	if card.Replies > 0 {
		parts = append(parts, formatCount(card.Replies)+" replies")
	}
	if card.Reposts > 0 {
		parts = append(parts, formatCount(card.Reposts)+" reposts")
	}
	if card.Likes > 0 {
		parts = append(parts, formatCount(card.Likes)+" likes")
	}
	if card.Views != "" {
		parts = append(parts, formatCount(parseCount(card.Views))+" views")
	}
	return strings.Join(parts, "  ·  ")
}

func formatCount(n int) string {
	switch {
	case n >= 1_000_000:
		return trimZero(fmt.Sprintf("%.1fM", float64(n)/1_000_000))
	case n >= 1_000:
		return trimZero(fmt.Sprintf("%.1fK", float64(n)/1_000))
	default:
		return strconv.Itoa(n)
	}
}

func trimZero(s string) string {
	return strings.TrimSuffix(s, ".0")
}

func parseCount(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return n
}

func formatTimestamp(createdAt string) string {
	t, err := time.Parse("Mon Jan 02 15:04:05 -0700 2006", createdAt)
	if err != nil {
		return ""
	}
	return t.Format("3:04 PM · Jan 2, 2006")
}
