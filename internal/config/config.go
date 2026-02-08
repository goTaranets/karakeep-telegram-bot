package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	ListenAddr string

	TelegramBotToken     string
	TelegramWebhookPath  string
	TelegramWebhookSecret string
	TelegramDebug        bool

	DBPath          string
	APIKeyMasterKey string
}

func FromEnv() (Config, error) {
	var cfg Config

	cfg.ListenAddr = envString("LISTEN_ADDR", ":8080")
	cfg.TelegramWebhookPath = envString("TELEGRAM_WEBHOOK_PATH", "/telegram/webhook")
	cfg.TelegramWebhookSecret = envString("TELEGRAM_WEBHOOK_SECRET", "")
	cfg.DBPath = envString("DB_PATH", "./data/bot.sqlite")
	cfg.APIKeyMasterKey = strings.TrimSpace(os.Getenv("API_KEY_MASTER_KEY"))

	cfg.TelegramBotToken = strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN"))
	if cfg.TelegramBotToken == "" {
		return Config{}, errors.New("TELEGRAM_BOT_TOKEN is required")
	}

	cfg.TelegramDebug = envBool("TELEGRAM_DEBUG", false)

	return cfg, nil
}

func envString(key, def string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	return v
}

func envBool(key string, def bool) bool {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		// safer default than crashing during boot: return default, but caller can validate if needed
		return def
	}
	return b
}

func (c Config) Validate() error {
	if c.TelegramBotToken == "" {
		return errors.New("telegram bot token is empty")
	}
	if !strings.HasPrefix(c.TelegramWebhookPath, "/") {
		return fmt.Errorf("TELEGRAM_WEBHOOK_PATH must start with '/': %q", c.TelegramWebhookPath)
	}
	if strings.TrimSpace(c.APIKeyMasterKey) == "" {
		return errors.New("API_KEY_MASTER_KEY is required (used to encrypt api_key in SQLite)")
	}
	return nil
}

