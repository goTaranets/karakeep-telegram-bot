package classifier

import (
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func TestExtractURLs_URL_Entity_UTF16Offsets(t *testing.T) {
	// "a😊 " is 4 UTF-16 code units: a(1) + emoji(2) + space(1)
	text := "a😊 https://example.com."

	entities := []tgbotapi.MessageEntity{
		{
			Type:   "url",
			Offset: 4,
			Length: len([]rune("https://example.com")), // ASCII => same for UTF-16 code units
		},
	}

	urls := ExtractURLs(text, entities)
	if len(urls) != 1 {
		t.Fatalf("expected 1 url, got %d: %#v", len(urls), urls)
	}
	if urls[0] != "https://example.com" {
		t.Fatalf("unexpected url: %q", urls[0])
	}
}

func TestExtractURLs_TextLink(t *testing.T) {
	text := "click here"
	entities := []tgbotapi.MessageEntity{
		{Type: "text_link", Offset: 0, Length: 5, URL: "https://example.com"},
	}
	urls := ExtractURLs(text, entities)
	if len(urls) != 1 || urls[0] != "https://example.com" {
		t.Fatalf("unexpected urls: %#v", urls)
	}
}

