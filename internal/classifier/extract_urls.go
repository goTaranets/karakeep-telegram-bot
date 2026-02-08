package classifier

import (
	"regexp"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

var trailingPunctRE = regexp.MustCompile(`[)\].,!?:;]+$`)

func ExtractURLs(text string, entities []tgbotapi.MessageEntity) []string {
	var out []string
	seen := make(map[string]struct{}, 4)

	add := func(u string) {
		u = strings.TrimSpace(u)
		u = trailingPunctRE.ReplaceAllString(u, "")
		u = strings.TrimSpace(u)
		if u == "" {
			return
		}
		if _, ok := seen[u]; ok {
			return
		}
		seen[u] = struct{}{}
		out = append(out, u)
	}

	for _, e := range entities {
		switch e.Type {
		case "text_link":
			add(e.URL)
		case "url":
			sub := SliceByUTF16(text, e.Offset, e.Length)
			add(sub)
		}
	}

	return out
}

// ExtractURLsFromMessage combines message text+entities or caption+caption_entities depending on what exists.
func ExtractURLsFromMessage(msg *tgbotapi.Message) []string {
	if msg == nil {
		return nil
	}
	if msg.Text != "" {
		return ExtractURLs(msg.Text, msg.Entities)
	}
	if msg.Caption != "" {
		return ExtractURLs(msg.Caption, msg.CaptionEntities)
	}
	return nil
}

