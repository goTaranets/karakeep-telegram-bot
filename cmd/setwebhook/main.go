package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func main() {
	var (
		token       = flag.String("token", strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN")), "Telegram bot token (or env TELEGRAM_BOT_TOKEN)")
		webhookURL  = flag.String("url", strings.TrimSpace(os.Getenv("TELEGRAM_WEBHOOK_URL")), "Public webhook URL, e.g. https://bot.example.com/telegram/webhook (or env TELEGRAM_WEBHOOK_URL)")
		secretToken = flag.String("secret", strings.TrimSpace(os.Getenv("TELEGRAM_WEBHOOK_SECRET")), "Webhook secret token (or env TELEGRAM_WEBHOOK_SECRET)")
		dropPending = flag.Bool("drop-pending", true, "Drop pending updates when setting webhook")
	)
	flag.Parse()

	if *token == "" {
		fatal(errors.New("missing -token / TELEGRAM_BOT_TOKEN"))
	}
	if *webhookURL == "" {
		fatal(errors.New("missing -url / TELEGRAM_WEBHOOK_URL"))
	}
	if !strings.HasPrefix(*webhookURL, "https://") {
		fatal(fmt.Errorf("webhook url must be https:// : %q", *webhookURL))
	}

	bot, err := tgbotapi.NewBotAPI(*token)
	if err != nil {
		fatal(err)
	}

	cfg := tgbotapi.NewWebhook(*webhookURL)
	if *secretToken != "" {
		cfg.SecretToken = *secretToken
	}
	cfg.DropPendingUpdates = *dropPending

	if _, err := bot.Request(cfg); err != nil {
		fatal(err)
	}

	fmt.Println("ok")
}

func fatal(err error) {
	_, _ = fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(2)
}

