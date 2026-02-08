package telegram

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type Downloader struct {
	Bot *tgbotapi.BotAPI
	HTTP *http.Client
}

func NewDownloader(bot *tgbotapi.BotAPI) *Downloader {
	return &Downloader{
		Bot: bot,
		HTTP: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

func (d *Downloader) DownloadFileByID(ctx context.Context, fileID string, maxBytes int64) ([]byte, string, error) {
	if d == nil || d.Bot == nil {
		return nil, "", errors.New("downloader is not configured")
	}
	fileID = strings.TrimSpace(fileID)
	if fileID == "" {
		return nil, "", errors.New("fileID is empty")
	}

	f, err := d.Bot.GetFile(tgbotapi.FileConfig{FileID: fileID})
	if err != nil {
		return nil, "", fmt.Errorf("getFile: %w", err)
	}
	if strings.TrimSpace(f.FilePath) == "" {
		return nil, "", errors.New("empty file_path from telegram")
	}

	// tgbotapi provides direct URL (contains bot token); do not log it.
	urlStr := d.Bot.FileURL(f.FilePath)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := d.httpClient().Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("telegram file download failed: status=%d", resp.StatusCode)
	}

	var rdr io.Reader = resp.Body
	if maxBytes > 0 {
		rdr = io.LimitReader(resp.Body, maxBytes+1)
	}
	b, err := io.ReadAll(rdr)
	if err != nil {
		return nil, "", err
	}
	if maxBytes > 0 && int64(len(b)) > maxBytes {
		return nil, f.FilePath, fmt.Errorf("file too large: %d bytes (limit %d)", len(b), maxBytes)
	}
	return b, f.FilePath, nil
}

func (d *Downloader) httpClient() *http.Client {
	if d.HTTP != nil {
		return d.HTTP
	}
	return http.DefaultClient
}

