package core

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"testing"

	"github.com/govdbot/govd/internal/config"
	"github.com/govdbot/govd/internal/database"
	"github.com/govdbot/govd/internal/logger"
	"github.com/govdbot/govd/internal/models"

	"go.uber.org/zap"
)

func TestDownloadRenderedFormat(t *testing.T) {
	logger.L = zap.NewNop().Sugar()
	config.Env = config.GetDefaultConfig()
	config.Env.DownloadsDirectory = t.TempDir()

	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := range 16 {
		for x := range 16 {
			img.Set(x, y, color.RGBA{R: 0xFF, A: 0xFF})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("failed to encode source image: %v", err)
	}

	ctx := &models.ExtractorContext{
		Extractor:    &models.Extractor{ID: "test"},
		FilesTracker: models.NewFilesTracker(),
	}
	defer ctx.FilesTracker.Cleanup()

	format := &models.MediaFormat{
		FormatID: "tweet_card",
		Type:     database.MediaTypePhoto,
		Rendered: buf.Bytes(),
	}

	downloaded, err := downloadFormat(ctx, 0, format)
	if err != nil {
		t.Fatalf("downloadFormat returned error: %v", err)
	}
	if downloaded.FilePath == "" {
		t.Fatal("rendered format was not written to a file")
	}
	if _, err := os.Stat(downloaded.FilePath); err != nil {
		t.Fatalf("rendered file does not exist: %v", err)
	}
	if format.Width != 16 || format.Height != 16 {
		t.Fatalf("dimensions = %dx%d, want 16x16", format.Width, format.Height)
	}
}
