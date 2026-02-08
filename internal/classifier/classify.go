package classifier

import (
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type Kind string

const (
	KindBookmark Kind = "bookmark"
	KindNote     Kind = "note"
	KindFile     Kind = "file"
)

type Result struct {
	Kind Kind

	// For KindBookmark
	URL   string
	Notes string

	// For KindNote
	Text string

	URLs []string

	HasMedia bool
}

func ClassifyMessage(msg *tgbotapi.Message) Result {
	if msg == nil {
		return Result{Kind: KindNote}
	}

	text := strings.TrimSpace(firstNonEmpty(msg.Text, msg.Caption))
	urls := ExtractURLsFromMessage(msg)

	hasMedia := messageHasMedia(msg)

	// Photo-only (no text/caption) => file/media object
	if hasMedia && text == "" && len(urls) == 0 {
		return Result{Kind: KindFile, URLs: nil, HasMedia: true}
	}

	// Text + any media => note with attachments
	if hasMedia {
		return Result{Kind: KindNote, Text: text, URLs: urls, HasMedia: true}
	}

	// No media, only text/caption.
	switch len(urls) {
	case 0:
		return Result{Kind: KindNote, Text: text, URLs: urls}
	case 1:
		onlyURL := strings.TrimSpace(urls[0])
		// If user pasted only the URL and nothing else -> bookmark
		if text == onlyURL {
			return Result{Kind: KindBookmark, URL: onlyURL}
		}
		// Your chosen rule: 1 URL + additional text -> bookmark + Notes.
		return Result{Kind: KindBookmark, URL: onlyURL, Notes: text, URLs: urls}
	default:
		return Result{Kind: KindNote, Text: text, URLs: urls}
	}
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func messageHasMedia(msg *tgbotapi.Message) bool {
	if msg == nil {
		return false
	}
	if len(msg.Photo) > 0 {
		return true
	}
	if msg.Document != nil || msg.Video != nil || msg.Animation != nil || msg.Audio != nil || msg.Voice != nil || msg.VideoNote != nil {
		return true
	}
	// Stickers can also be treated as media (user asked: any media should upload).
	if msg.Sticker != nil {
		return true
	}
	return false
}

