package twitter

import (
	"testing"

	"github.com/bytedance/sonic"
)

const noteText = "POLL: ICE has been abused by the Fake News Media...\n\nThank you for your attention to this matter! President DJT"

func TestTweetFromResultPrefersNoteTweetText(t *testing.T) {
	const payload = `{
		"legacy": {"full_text": "POLL: ICE has been abused by the Fake News Media at levels never seen before. The concept I have had for quite some"},
		"note_tweet": {"note_tweet_results": {"result": {"text": "POLL: ICE has been abused by the Fake News Media...\n\nThank you for your attention to this matter! President DJT"}}}
	}`

	var result TweetResult
	if err := sonic.ConfigFastest.UnmarshalFromString(payload, &result); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}

	tweet, err := tweetFromResult(&result)
	if err != nil {
		t.Fatalf("tweetFromResult returned error: %v", err)
	}
	if tweet.FullText != noteText {
		t.Fatalf("note text not used\n got: %q\nwant: %q", tweet.FullText, noteText)
	}
}

func TestTweetFromResultFallsBackToLegacyText(t *testing.T) {
	const payload = `{"legacy": {"full_text": "a short tweet"}}`

	var result TweetResult
	if err := sonic.ConfigFastest.UnmarshalFromString(payload, &result); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}

	tweet, err := tweetFromResult(&result)
	if err != nil {
		t.Fatalf("tweetFromResult returned error: %v", err)
	}
	if tweet.FullText != "a short tweet" {
		t.Fatalf("unexpected full text: %q", tweet.FullText)
	}
}

func TestTweetFromResultNestedTweet(t *testing.T) {
	const payload = `{
		"tweet": {
			"legacy": {"full_text": "truncated"},
			"note_tweet": {"note_tweet_results": {"result": {"text": "full nested note"}}}
		}
	}`

	var result TweetResult
	if err := sonic.ConfigFastest.UnmarshalFromString(payload, &result); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}

	tweet, err := tweetFromResult(&result)
	if err != nil {
		t.Fatalf("tweetFromResult returned error: %v", err)
	}
	if tweet.FullText != "full nested note" {
		t.Fatalf("unexpected full text: %q", tweet.FullText)
	}
}

func TestTweetFromResultMissingLegacyDoesNotPanic(t *testing.T) {
	result := &TweetResult{Tweet: &TweetResultData{}}

	if _, err := tweetFromResult(result); err == nil {
		t.Fatal("expected an error for a result without legacy data")
	}
}
