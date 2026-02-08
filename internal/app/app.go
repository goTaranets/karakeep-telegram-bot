package app

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"karakeep-telegram-bot/internal/classifier"
	"karakeep-telegram-bot/internal/karakeep"
	"karakeep-telegram-bot/internal/security"
	"karakeep-telegram-bot/internal/storage"
	"karakeep-telegram-bot/internal/telegram"
)

type App struct {
	Bot    *tgbotapi.BotAPI
	Store  *storage.Store
	Logger *slog.Logger

	Version string

	MediaGroups *telegram.MediaGroupCollector

	Downloader *telegram.Downloader

	MaxUploadBytes int64
}

func (a *App) HandleUpdate(ctx context.Context, upd tgbotapi.Update) {
	log := a.Logger
	if log == nil {
		log = slog.Default()
	}
	if upd.Message == nil {
		return
	}
	msg := upd.Message
	if msg.From == nil {
		return
	}

	// Ensure user record exists.
	if err := a.Store.UpsertUser(ctx, msg.From.ID); err != nil {
		log.Warn("upsert user failed", "err", err)
	}

	// Commands: only in private chats.
	if msg.IsCommand() {
		if msg.Chat == nil || !msg.Chat.IsPrivate() {
			_, _ = a.Bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Команды настройки доступны только в личных сообщениях с ботом."))
			return
		}

		switch strings.ToLower(msg.Command()) {
		case "start":
			a.cmdStart(ctx, msg)
		case "help":
			a.cmdHelp(ctx, msg)
		case "server":
			a.cmdServer(ctx, msg)
		case "key":
			a.cmdKey(ctx, msg)
		case "status":
			a.cmdStatus(ctx, msg)
		default:
			_, _ = a.Bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Неизвестная команда. /help"))
		}
		return
	}

	// Non-command message: in the next todo we'll actually save it to Karakeep.
	// We do a fast ACK, then process in background and edit the ACK message when enrichment is done.
	if msg.MediaGroupID != "" && a.MediaGroups != nil {
		a.MediaGroups.Collect(msg)
		return
	}
	go a.processSingleMessage(context.Background(), msg)
}

func (a *App) HandleMediaGroup(groupID string, msgs []*tgbotapi.Message) {
	log := a.Logger
	if log == nil {
		log = slog.Default()
	}
	if len(msgs) == 0 {
		return
	}

	go a.processMediaGroup(context.Background(), groupID, msgs)
}

func (a *App) processMediaGroup(ctx context.Context, groupID string, msgs []*tgbotapi.Message) {
	// Pick the message that has caption/text if any, otherwise first.
	pick := msgs[0]
	for _, m := range msgs {
		if strings.TrimSpace(m.Caption) != "" || strings.TrimSpace(m.Text) != "" {
			pick = m
			break
		}
	}
	// Process album as a single unit: caption from pick, attachments from all messages.
	a.processMessageBatch(ctx, pick, msgs)
}

func (a *App) processSingleMessage(ctx context.Context, msg *tgbotapi.Message) {
	a.processMessageBatch(ctx, msg, []*tgbotapi.Message{msg})
}

func (a *App) processMessageBatch(ctx context.Context, msg *tgbotapi.Message, batch []*tgbotapi.Message) {
	log := a.Logger
	if log == nil {
		log = slog.Default()
	}
	if msg == nil || msg.From == nil || msg.Chat == nil {
		return
	}

	u, err := a.Store.GetUser(ctx, msg.From.ID)
	if err != nil {
		return
	}
	apiKey, ok, err := a.Store.DecryptAPIKey(u)
	if err != nil {
		log.Warn("decrypt api key failed", "err", err)
		return
	}
	if strings.TrimSpace(u.ServerBaseURL) == "" || !ok {
		_, _ = a.Bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "❌ Не настроено. Сначала: /server https://<host> и /key <API_KEY>"))
		return
	}

	res := classifier.ClassifyMessage(msg)
	attachments := ExtractAttachments(batch)

	ackText := ""
	switch res.Kind {
	case classifier.KindBookmark:
		ackText = "⏳ Сохраняю как закладку…"
	case classifier.KindNote:
		ackText = "⏳ Сохраняю как заметку…"
	case classifier.KindFile:
		ackText = "⏳ Загружаю файл…"
	default:
		ackText = "⏳ Сохраняю…"
	}

	ackMsg, err := a.Bot.Send(tgbotapi.NewMessage(msg.Chat.ID, ackText))
	if err != nil {
		log.Warn("failed to send ack", "err", err)
		return
	}

	client, err := karakeep.NewClient(karakeep.ClientOpts{
		BaseURL: u.ServerBaseURL,
		APIKey:  apiKey,
		Timeout: 60 * time.Second,
	})
	if err != nil {
		_ = a.editAck(msg.Chat.ID, ackMsg.MessageID, "❌ Ошибка конфигурации Karakeep: "+err.Error())
		return
	}

	var b karakeep.Bookmark
	var status int

	switch res.Kind {
	case classifier.KindBookmark:
		b, status, err = client.CreateBookmark(ctx, res.URL, "", res.Notes)
	case classifier.KindNote:
		// Prefer true "note" semantics: create bookmark without url, store text as notes.
		// If API rejects url-less bookmarks, fallback to first URL (if any).
		b, status, err = client.CreateBookmark(ctx, "", "", res.Text)
		if err != nil && len(res.URLs) > 0 {
			b, status, err = client.CreateBookmark(ctx, res.URLs[0], "", res.Text)
		}
	case classifier.KindFile:
		notes := fmt.Sprintf("Telegram media (%s)", time.Unix(int64(msg.Date), 0).UTC().Format(time.RFC3339))
		b, status, err = client.CreateBookmark(ctx, "", "", notes)
	}

	if err != nil {
		_ = a.editAck(msg.Chat.ID, ackMsg.MessageID, fmt.Sprintf("❌ Ошибка (%d): %v", status, err))
		return
	}

	// Upload + attach assets (if any)
	if b.ID != "" && len(attachments) > 0 {
		if a.Downloader == nil {
			a.Downloader = telegram.NewDownloader(a.Bot)
		}
		maxBytes := a.MaxUploadBytes
		if maxBytes <= 0 {
			maxBytes = 50 << 20
		}
		for _, att := range attachments {
			if att.SizeBytes > 0 && att.SizeBytes > maxBytes {
				_ = a.editAck(msg.Chat.ID, ackMsg.MessageID, fmt.Sprintf("❌ Слишком большой файл: %s (%d bytes), лимит %d bytes", att.Filename, att.SizeBytes, maxBytes))
				return
			}
			data, filePath, err := a.Downloader.DownloadFileByID(ctx, att.FileID, maxBytes)
			if err != nil {
				_ = a.editAck(msg.Chat.ID, ackMsg.MessageID, "❌ Ошибка скачивания файла из Telegram: "+err.Error())
				return
			}
			filename := att.Filename
			if strings.TrimSpace(filename) == "" {
				// fallback to filePath tail
				parts := strings.Split(filePath, "/")
				if len(parts) > 0 {
					filename = parts[len(parts)-1]
				}
			}
			asset, st, err := client.UploadAsset(ctx, data, filename, att.Mime)
			if err != nil {
				_ = a.editAck(msg.Chat.ID, ackMsg.MessageID, fmt.Sprintf("❌ Ошибка загрузки в Karakeep (%d): %v", st, err))
				return
			}
			if strings.TrimSpace(asset.ID) == "" {
				_ = a.editAck(msg.Chat.ID, ackMsg.MessageID, "❌ Karakeep вернул asset без id (проверьте схему Upload a new asset).")
				return
			}
			_, st, err = client.AttachAsset(ctx, b.ID, asset.ID)
			if err != nil {
				_ = a.editAck(msg.Chat.ID, ackMsg.MessageID, fmt.Sprintf("❌ Ошибка attach asset (%d): %v", st, err))
				return
			}
		}
	}

	_ = a.Store.SetLastSuccess(ctx, msg.From.ID, b.ID)

	_ = a.editAck(msg.Chat.ID, ackMsg.MessageID, fmt.Sprintf("✅ Сохранено (id=%s). Обогащаю…", b.ID))

	// Enrichment: summarize + get bookmark for title/summary/tags, then edit message.
	if b.ID != "" {
		_, _, _ = client.Summarize(ctx, b.ID)
		got, _, err := client.GetBookmark(ctx, b.ID)
		if err == nil {
			final := formatFinalMessage(res.Kind, got)
			_ = a.editAck(msg.Chat.ID, ackMsg.MessageID, final)
			return
		}
	}

	_ = a.editAck(msg.Chat.ID, ackMsg.MessageID, "✅ Сохранено.")
}

func (a *App) editAck(chatID int64, messageID int, text string) error {
	edit := tgbotapi.NewEditMessageText(chatID, messageID, text)
	_, err := a.Bot.Send(edit)
	return err
}

func formatFinalMessage(kind classifier.Kind, b karakeep.Bookmark) string {
	var sb strings.Builder
	switch kind {
	case classifier.KindBookmark:
		sb.WriteString("✅ Сохранено как закладка\n")
	case classifier.KindNote:
		sb.WriteString("✅ Сохранено как заметка\n")
	case classifier.KindFile:
		sb.WriteString("✅ Сохранено как файл\n")
	default:
		sb.WriteString("✅ Сохранено\n")
	}
	if strings.TrimSpace(b.Title) != "" {
		sb.WriteString("\nНазвание: ")
		sb.WriteString(strings.TrimSpace(b.Title))
		sb.WriteString("\n")
	}
	if s := strings.TrimSpace(b.SummaryText()); s != "" {
		sb.WriteString("\nСаммари:\n")
		sb.WriteString(s)
		sb.WriteString("\n")
	}
	if len(b.Tags) > 0 {
		sb.WriteString("\nТеги: ")
		for i, t := range b.Tags {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(strings.TrimSpace(t.Name))
		}
		sb.WriteString("\n")
	}
	return strings.TrimSpace(sb.String())
}

func (a *App) cmdStart(ctx context.Context, msg *tgbotapi.Message) {
	u, _ := a.Store.GetUser(ctx, msg.From.ID)
	server := strings.TrimSpace(u.ServerBaseURL)
	if server == "" {
		server = "(не задан)"
	}
	text := "Привет! Я сохраняю сообщения в Karakeep по API key.\n\n" +
		"Текущий сервер: " + server + "\n\n" +
		"Сначала настрой:\n" +
		"/server https://<ваш_karakeep>\n" +
		"/key <API_KEY>\n\n" +
		"Потом просто присылай ссылки/текст/медиа."
	_, _ = a.Bot.Send(tgbotapi.NewMessage(msg.Chat.ID, text))
}

func (a *App) cmdHelp(ctx context.Context, msg *tgbotapi.Message) {
	text := "Команды:\n" +
		"/server — показать текущий сервер\n" +
		"/server <url> — установить сервер (только https)\n" +
		"/key — проверить, задан ли API key\n" +
		"/key <token> — установить API key\n" +
		"/status — статус\n" +
		"/help — справка"
	_, _ = a.Bot.Send(tgbotapi.NewMessage(msg.Chat.ID, text))
}

func (a *App) cmdServer(ctx context.Context, msg *tgbotapi.Message) {
	arg := strings.TrimSpace(msg.CommandArguments())
	if arg == "" {
		u, err := a.Store.GetUser(ctx, msg.From.ID)
		if err != nil && err != sql.ErrNoRows {
			_, _ = a.Bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Ошибка чтения настроек."))
			return
		}
		server := strings.TrimSpace(u.ServerBaseURL)
		if server == "" {
			server = "(не задан)"
		}
		_, _ = a.Bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Текущий сервер: "+server+"\nУстановить: /server https://<host>"))
		return
	}

	norm, err := security.ValidateServerBaseURL(arg)
	if err != nil {
		_, _ = a.Bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Некорректный / небезопасный URL. Разрешён только публичный https. Пример: /server https://karakeep.example.com"))
		return
	}

	if err := a.Store.SetServerBaseURL(ctx, msg.From.ID, norm); err != nil {
		_, _ = a.Bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Не удалось сохранить сервер."))
		return
	}
	_, _ = a.Bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "✅ Сервер сохранён: "+norm))
}

func (a *App) cmdKey(ctx context.Context, msg *tgbotapi.Message) {
	arg := strings.TrimSpace(msg.CommandArguments())
	if arg == "" {
		u, err := a.Store.GetUser(ctx, msg.From.ID)
		if err != nil && err != sql.ErrNoRows {
			_, _ = a.Bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Ошибка чтения настроек."))
			return
		}
		_, ok, _ := a.Store.DecryptAPIKey(u)
		if ok {
			_, _ = a.Bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "API key: задан ✅"))
		} else {
			_, _ = a.Bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "API key: не задан ❌\nУстановить: /key <API_KEY>"))
		}
		return
	}

	if err := a.Store.SetAPIKey(ctx, msg.From.ID, arg); err != nil {
		_, _ = a.Bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Не удалось сохранить API key."))
		return
	}
	_, _ = a.Bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "✅ API key сохранён."))
}

func (a *App) cmdStatus(ctx context.Context, msg *tgbotapi.Message) {
	u, err := a.Store.GetUser(ctx, msg.From.ID)
	if err != nil && err != sql.ErrNoRows {
		_, _ = a.Bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Ошибка чтения настроек."))
		return
	}

	server := strings.TrimSpace(u.ServerBaseURL)
	if server == "" {
		server = "(не задан)"
	}

	_, keySet, _ := a.Store.DecryptAPIKey(u)
	keyStr := "нет"
	if keySet {
		keyStr = "да"
	}

	last := "нет"
	if u.LastSuccessAt.Valid {
		last = u.LastSuccessAt.Time.In(time.Local).Format(time.RFC3339)
	}

	text := fmt.Sprintf("Сервер: %s\nКлюч: %s\nПоследняя успешная запись: %s\nВерсия: %s", server, keyStr, last, strings.TrimSpace(a.Version))
	_, _ = a.Bot.Send(tgbotapi.NewMessage(msg.Chat.ID, text))
}

