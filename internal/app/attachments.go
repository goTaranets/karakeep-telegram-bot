package app

import (
	"fmt"
	"path/filepath"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type Attachment struct {
	FileID    string
	Filename  string
	Mime      string
	SizeBytes int64
}

func ExtractAttachments(msgs []*tgbotapi.Message) []Attachment {
	var out []Attachment
	seen := map[string]struct{}{}

	add := func(a Attachment) {
		if strings.TrimSpace(a.FileID) == "" {
			return
		}
		if _, ok := seen[a.FileID]; ok {
			return
		}
		seen[a.FileID] = struct{}{}
		if strings.TrimSpace(a.Filename) == "" {
			a.Filename = "upload.bin"
		}
		out = append(out, a)
	}

	for _, msg := range msgs {
		if msg == nil {
			continue
		}

		// Photos: pick the largest size (usually last).
		if len(msg.Photo) > 0 {
			p := msg.Photo[len(msg.Photo)-1]
			add(Attachment{
				FileID:    p.FileID,
				Filename:  "photo.jpg",
				Mime:      "image/jpeg",
				SizeBytes: int64(p.FileSize),
			})
		}

		if msg.Document != nil {
			fn := msg.Document.FileName
			if strings.TrimSpace(fn) == "" {
				fn = "document"
			}
			add(Attachment{
				FileID:    msg.Document.FileID,
				Filename:  safeFilename(fn),
				Mime:      msg.Document.MimeType,
				SizeBytes: int64(msg.Document.FileSize),
			})
		}
		if msg.Video != nil {
			add(Attachment{
				FileID:    msg.Video.FileID,
				Filename:  "video.mp4",
				Mime:      msg.Video.MimeType,
				SizeBytes: int64(msg.Video.FileSize),
			})
		}
		if msg.Audio != nil {
			fn := msg.Audio.FileName
			if strings.TrimSpace(fn) == "" {
				fn = "audio.mp3"
			}
			add(Attachment{
				FileID:    msg.Audio.FileID,
				Filename:  safeFilename(fn),
				Mime:      msg.Audio.MimeType,
				SizeBytes: int64(msg.Audio.FileSize),
			})
		}
		if msg.Voice != nil {
			add(Attachment{
				FileID:    msg.Voice.FileID,
				Filename:  "voice.ogg",
				Mime:      msg.Voice.MimeType,
				SizeBytes: int64(msg.Voice.FileSize),
			})
		}
		if msg.Animation != nil {
			fn := msg.Animation.FileName
			if strings.TrimSpace(fn) == "" {
				fn = "animation.mp4"
			}
			add(Attachment{
				FileID:    msg.Animation.FileID,
				Filename:  safeFilename(fn),
				Mime:      msg.Animation.MimeType,
				SizeBytes: int64(msg.Animation.FileSize),
			})
		}
		if msg.VideoNote != nil {
			add(Attachment{
				FileID:    msg.VideoNote.FileID,
				Filename:  "video_note.mp4",
				Mime:      "video/mp4",
				SizeBytes: int64(msg.VideoNote.FileSize),
			})
		}
		if msg.Sticker != nil {
			ext := "webp"
			if msg.Sticker.IsAnimated {
				ext = "tgs"
			}
			add(Attachment{
				FileID:    msg.Sticker.FileID,
				Filename:  fmt.Sprintf("sticker.%s", ext),
				Mime:      "",
				SizeBytes: int64(msg.Sticker.FileSize),
			})
		}
	}

	return out
}

func safeFilename(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "upload.bin"
	}
	// Strip any path fragments just in case.
	name = filepath.Base(name)
	return name
}

