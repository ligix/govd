// Command tweetrender renders a tweet as a PNG image. It is a development
// helper for iterating on the tweet card used for posts without media.
//
//	go run -tags=lint ./cmd/tweetrender <tweet-url> [out.png]
package main

import (
	"fmt"
	"os"

	"github.com/govdbot/govd/internal/extractors"
	"github.com/govdbot/govd/internal/extractors/twitter"
	"github.com/govdbot/govd/internal/logger"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func main() {
	encoder := zapcore.NewConsoleEncoder(zap.NewDevelopmentEncoderConfig())
	core := zapcore.NewCore(encoder, zapcore.AddSync(os.Stdout), zap.NewAtomicLevelAt(zap.InfoLevel))
	logger.L = zap.New(core).Sugar()

	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: tweetrender <tweet-url> [out.png]")
		os.Exit(2)
	}

	ctx := extractors.FromURL(os.Args[1])
	if ctx == nil {
		fmt.Fprintln(os.Stderr, "no extractor matched")
		os.Exit(1)
	}
	defer ctx.CancelFunc()

	data, err := twitter.RenderTweetCard(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "render failed: %v\n", err)
		os.Exit(1)
	}

	out := "tweet.png"
	if len(os.Args) > 2 {
		out = os.Args[2]
	}
	if err := os.WriteFile(out, data, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "failed to write %s: %v\n", out, err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s (%d bytes)\n", out, len(data))
}
