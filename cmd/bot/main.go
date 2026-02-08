package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"karakeep-telegram-bot/internal/app"
	"karakeep-telegram-bot/internal/config"
	"karakeep-telegram-bot/internal/storage"
	"karakeep-telegram-bot/internal/telegram"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg, err := config.FromEnv()
	if err != nil {
		logger.Error("config error", "err", err)
		os.Exit(2)
	}
	if err := cfg.Validate(); err != nil {
		logger.Error("config validation error", "err", err)
		os.Exit(2)
	}

	store, err := storage.Open(context.Background(), cfg.DBPath, cfg.APIKeyMasterKey)
	if err != nil {
		logger.Error("failed to open storage", "err", err)
		os.Exit(2)
	}
	defer store.Close()

	bot, err := tgbotapi.NewBotAPI(cfg.TelegramBotToken)
	if err != nil {
		logger.Error("failed to init telegram bot", "err", err)
		os.Exit(2)
	}
	bot.Debug = cfg.TelegramDebug
	logger.Info("telegram bot initialized", "username", bot.Self.UserName)

	application := &app.App{
		Bot:     bot,
		Store:   store,
		Logger:  logger,
		Version: os.Getenv("BOT_VERSION"),
	}
	application.Downloader = telegram.NewDownloader(bot)
	application.MaxUploadBytes = 50 << 20
	application.MediaGroups = telegram.NewMediaGroupCollector(2*time.Second, application.HandleMediaGroup)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	mux.Handle(cfg.TelegramWebhookPath, telegram.NewWebhookHandler(telegram.WebhookHandlerOpts{
		Bot:         bot,
		SecretToken: cfg.TelegramWebhookSecret,
		Logger:      logger,
		OnUpdate:    application.HandleUpdate,
	}))

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		logger.Info("http server listening", "addr", cfg.ListenAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server failed", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	logger.Info("shutdown complete")
}

