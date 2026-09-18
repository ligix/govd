// Command extracttest is a small development harness used to exercise
// extractors against real URLs. It is intentionally not wired into the bot.
//
// Usage:
//
//	go run -tags=lint ./cmd/extracttest <url> [url...]
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/govdbot/govd/internal/extractors"
	"github.com/govdbot/govd/internal/logger"
	"github.com/govdbot/govd/internal/models"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func main() {
	// logger.Init writes to logs/app.log which may not be writable in a dev
	// checkout; a console-only logger keeps the harness dependency-free.
	encoder := zapcore.NewConsoleEncoder(zap.NewDevelopmentEncoderConfig())
	core := zapcore.NewCore(
		encoder,
		zapcore.AddSync(os.Stdout),
		zap.NewAtomicLevelAt(zap.DebugLevel),
	)
	logger.L = zap.New(core).Sugar()

	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: extracttest <url> [url...]")
		os.Exit(2)
	}

	var failed bool
	for _, rawURL := range os.Args[1:] {
		if err := testURL(rawURL); err != nil {
			fmt.Printf("  ERROR: %v\n\n", err)
			failed = true
		}
	}
	if failed {
		os.Exit(1)
	}
}

func testURL(rawURL string) error {
	fmt.Printf("== %s\n", rawURL)

	start := time.Now()
	ctx := extractors.FromURL(rawURL)
	if ctx == nil {
		return fmt.Errorf("no extractor matched")
	}
	defer ctx.CancelFunc()

	fmt.Printf("  extractor: %s (%s)\n", ctx.Extractor.DisplayName, ctx.Extractor.ID)
	fmt.Printf("  contentID: %s\n", ctx.ContentID)
	fmt.Printf("  cookies:   %d\n", len(ctx.HTTPClient.Cookies))

	resp, err := ctx.Extractor.GetFunc(ctx)
	if err != nil {
		return fmt.Errorf("GetFunc: %w", err)
	}
	if resp == nil {
		return fmt.Errorf("nil response")
	}
	if resp.Media == nil {
		return fmt.Errorf("no media (response URL: %q)", resp.URL)
	}

	printMedia(resp.Media)
	fmt.Printf("  elapsed: %s\n\n", time.Since(start).Round(time.Millisecond))
	return nil
}

func printMedia(media *models.Media) {
	fmt.Printf("  items:   %d\n", len(media.Items))
	if media.Caption != "" {
		fmt.Printf("  caption: %q\n", media.Caption)
	}
	for i, item := range media.Items {
		for _, format := range item.Formats {
			fmt.Printf("    [%d] %s\n", i, format.ToString())
			for _, u := range format.URL {
				fmt.Printf("        %s\n", u)
			}
		}
	}
}
