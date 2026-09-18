package twitter

import (
	"bytes"
	"image/png"
	"os"
	"strings"
	"testing"
)

func TestLayoutTextWrapsOnWords(t *testing.T) {
	f, err := loadRegular()
	if err != nil {
		t.Fatalf("failed to load font: %v", err)
	}
	face, err := newFace(f, bodySize)
	if err != nil {
		t.Fatalf("failed to create face: %v", err)
	}
	defer face.Close()

	lines := layoutText(strings.Repeat("hello world ", 60), face, 400, bodySize)
	for _, line := range lines {
		var sb strings.Builder
		for _, cl := range line {
			sb.WriteString(cl.text)
		}
		for _, word := range strings.Fields(sb.String()) {
			if word != "hello" && word != "world" {
				t.Fatalf("word wrapping split a word: line contains %q", word)
			}
		}
	}
}

func TestEmojiKey(t *testing.T) {
	tests := map[string]string{
		"😀":     "1f600",
		"❤️":    "2764",
		"🇺🇸":    "1f1fa-1f1f8",
		"👨‍👩‍👧": "1f468-200d-1f469-200d-1f467",
		"1️⃣":   "31-20e3",
	}
	for in, want := range tests {
		got, ok := emojiKey(in)
		if !ok {
			t.Fatalf("emojiKey(%q) not detected as emoji", in)
		}
		if got != want {
			t.Errorf("emojiKey(%q) = %q, want %q", in, got, want)
		}
	}
	if _, ok := emojiKey("hello"); ok {
		t.Error("emojiKey(\"hello\") should not be an emoji")
	}
}

func TestEmojiAssets(t *testing.T) {
	for _, key := range []string{"1f600", "2764", "1f1fa-1f1f8", "1f468-200d-1f469-200d-1f467"} {
		if emojiImage(key) == nil {
			t.Errorf("emoji asset %q not found in bundle", key)
		}
	}
}

func TestRenderCard(t *testing.T) {
	img, err := renderCard(sampleCard, nil)
	if err != nil {
		t.Fatalf("renderCard returned error: %v", err)
	}

	decoded, err := png.Decode(bytes.NewReader(img))
	if err != nil {
		t.Fatalf("failed to decode rendered card: %v", err)
	}
	if decoded.Bounds().Dx() != cardWidth {
		t.Fatalf("card width = %d, want %d", decoded.Bounds().Dx(), cardWidth)
	}
	if decoded.Bounds().Dy() == 0 {
		t.Fatal("card height is 0")
	}
}

// TestRenderCardDump writes the rendered card to TWEET_RENDER_DUMP when set.
func TestRenderCardDump(t *testing.T) {
	path := os.Getenv("TWEET_RENDER_DUMP")
	if path == "" {
		t.Skip("TWEET_RENDER_DUMP not set")
	}
	img, err := renderCard(sampleCard, nil)
	if err != nil {
		t.Fatalf("renderCard returned error: %v", err)
	}
	if err := os.WriteFile(path, img, 0o644); err != nil {
		t.Fatalf("failed to write dump: %v", err)
	}
}

var sampleCard = TweetCard{
	Name:      "Test User",
	Handle:    "testuser",
	Verified:  true,
	Text:      "Hello world 😀🎉🚀 with a flag 🇺🇸, a ZWJ family 👨‍👩‍👧 and a heart ❤️.",
	CreatedAt: "Mon Jan 02 15:04:05 +0000 2024",
	Replies:   12,
	Reposts:   3400,
	Likes:     56789,
	Views:     "1234567",
}
