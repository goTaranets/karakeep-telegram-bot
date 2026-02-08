package telegram

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type WebhookHandlerOpts struct {
	Bot *tgbotapi.BotAPI

	// If provided, we will require X-Telegram-Bot-Api-Secret-Token to match.
	SecretToken string

	Logger *slog.Logger

	OnUpdate func(context.Context, tgbotapi.Update)
}

func NewWebhookHandler(opts WebhookHandlerOpts) http.Handler {
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		if opts.SecretToken != "" {
			got := r.Header.Get("X-Telegram-Bot-Api-Secret-Token")
			if got != opts.SecretToken {
				log.Warn("telegram webhook unauthorized", "remote", r.RemoteAddr)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
		}

		body, err := io.ReadAll(io.LimitReader(r.Body, 2<<20)) // 2MB is plenty for update JSON
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		defer r.Body.Close()

		var upd tgbotapi.Update
		if err := json.Unmarshal(body, &upd); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		// Respond to Telegram quickly.
		w.WriteHeader(http.StatusOK)

		if opts.OnUpdate != nil {
			go func(u tgbotapi.Update) {
				defer func() {
					// prevent panics from crashing the server
					if r := recover(); r != nil {
						log.Error("panic in update handler", "recover", r)
					}
				}()
				if u.UpdateID != 0 {
					log.Info("telegram update received", "update_id", u.UpdateID)
				} else {
					log.Info("telegram update received")
				}
				opts.OnUpdate(context.Background(), u)
			}(upd)
		}
	})
}

