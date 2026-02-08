package app

import (
	"context"
	"database/sql"
	"encoding/json"
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
	log.Info("processing message",
		"user_id", msg.From.ID,
		"chat_id", msg.Chat.ID,
		"message_id", msg.MessageID,
		"kind", res.Kind,
		"urls_count", len(res.URLs),
		"has_media", res.HasMedia,
		"attachments_count", len(attachments),
	)

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
		// Text note: create text-type bookmark. If text contains URLs and server requires link-type, fallback to first URL.
		b, status, err = client.CreateBookmark(ctx, "", "", res.Text)
		if err != nil && len(res.URLs) > 0 {
			b, status, err = client.CreateBookmark(ctx, res.URLs[0], "", res.Text)
		}
	case classifier.KindFile:
		notes := fmt.Sprintf("Telegram media (%s)", time.Unix(int64(msg.Date), 0).UTC().Format(time.RFC3339))
		b, status, err = client.CreateBookmark(ctx, "", "", notes)
	}

	if err != nil {
		log.Warn("karakeep create failed", "status", status, "err", err)
		_ = a.editAck(msg.Chat.ID, ackMsg.MessageID, userFacingKarakeepError(status, err))
		return
	}
	log.Info("karakeep created", "bookmark_id", b.ID, "status", status)

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
				log.Warn("telegram download failed", "err", err)
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
				log.Warn("karakeep upload asset failed", "status", st, "err", err)
				_ = a.editAck(msg.Chat.ID, ackMsg.MessageID, fmt.Sprintf("❌ Ошибка загрузки в Karakeep (%d): %v", st, err))
				return
			}
			if strings.TrimSpace(asset.ID) == "" {
				log.Warn("karakeep upload asset returned empty id")
				_ = a.editAck(msg.Chat.ID, ackMsg.MessageID, "❌ Karakeep вернул asset без id (проверьте схему Upload a new asset).")
				return
			}
			_, st, err = client.AttachAsset(ctx, b.ID, asset.ID)
			if err != nil {
				log.Warn("karakeep attach asset failed", "status", st, "err", err)
				_ = a.editAck(msg.Chat.ID, ackMsg.MessageID, fmt.Sprintf("❌ Ошибка attach asset (%d): %v", st, err))
				return
			}
		}
	}

	_ = a.Store.SetLastSuccess(ctx, msg.From.ID, b.ID)

	_ = a.editAck(msg.Chat.ID, ackMsg.MessageID, fmt.Sprintf("✅ Сохранено (id=%s). Жду загрузку контента…", b.ID))

	// Enrichment:
	// - For link bookmarks: poll until Karakeep extracted content, then summarize.
	// - For text notes: summarize immediately.
	if b.ID == "" {
		_ = a.editAck(msg.Chat.ID, ackMsg.MessageID, "✅ Сохранено.")
		return
	}

	ready := true
	if res.Kind == classifier.KindBookmark {
		ready = a.waitForExtractedContent(ctx, client, b.ID, 3*time.Second, 3*time.Minute)
	}

	if !ready {
		_ = a.editAck(msg.Chat.ID, ackMsg.MessageID, "⚠️ Контент не загрузился за 3 минуты. Смотрите саммари в приложении.")
		return
	}

	got, ok := a.waitForNonEmptySummary(ctx, client, b.ID, 3*time.Second, 3*time.Minute)
	if ok {
		final := formatFinalMessage(res.Kind, got)
		_ = a.editAck(msg.Chat.ID, ackMsg.MessageID, final)
		return
	}
	_ = a.editAck(msg.Chat.ID, ackMsg.MessageID, "⚠️ Саммари ещё не готово. Смотрите саммари в приложении.")
}

func (a *App) editAck(chatID int64, messageID int, text string) error {
	edit := tgbotapi.NewEditMessageText(chatID, messageID, text)
	_, err := a.Bot.Send(edit)
	if err != nil {
		log := a.Logger
		if log == nil {
			log = slog.Default()
		}
		log.Warn("failed to edit ack", "chat_id", chatID, "message_id", messageID, "err", err)
	}
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

func userFacingKarakeepError(status int, err error) string {
	// Avoid Telegram MESSAGE_TOO_LONG and avoid leaking large HTML bodies to the user.
	msg := fmt.Sprintf("❌ Ошибка Karakeep (%d).", status)

	// Common misconfig: user entered UI base url; client now auto-adds /api/v1, but still help the user.
	if status == 404 {
		msg += " Похоже, указан не тот адрес сервера. Укажи домен Karakeep без /api: /server https://<host>"
		return msg
	}
	if status == 401 || status == 403 {
		msg += " Проверь API key (/key) и права ключа."
		return msg
	}
	if status == 400 {
		msg += " Похоже, Karakeep не принял payload. Обычно это означает неверные поля запроса (url/title/notes). Я починю формат после уточнения текста ошибки в логах."
	}
	if err != nil {
		// Keep error short; APIError already contains a short preview.
		msg += " " + strings.TrimSpace(err.Error())
	}
	if len(msg) > 800 {
		msg = msg[:800] + "…"
	}
	return msg
}

func (a *App) waitForExtractedContent(ctx context.Context, client *karakeep.Client, bookmarkID string, interval time.Duration, timeout time.Duration) bool {
	log := a.Logger
	if log == nil {
		log = slog.Default()
	}

	if interval <= 0 {
		interval = 3 * time.Second
	}
	if timeout <= 0 {
		timeout = 3 * time.Minute
	}

	deadline := time.Now().Add(timeout)
	t := time.NewTicker(interval)
	defer t.Stop()

	iter := 0
	for {
		got, _, err := client.GetBookmark(ctx, bookmarkID)
		if err == nil {
			ok, signals := hasExtractedContent(got.Raw)
			iter++
			if iter == 1 || iter%5 == 0 || ok {
				log.Info("extract poll",
					"bookmark_id", bookmarkID,
					"ready", ok,
					"signals", signals,
				)
			}
			if ok {
				return true
			}
		} else {
			log.Warn("karakeep get bookmark during poll failed", "err", err)
		}

		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-t.C:
		}
	}
}

func hasExtractedContent(raw json.RawMessage) (bool, map[string]any) {
	if len(raw) == 0 {
		return false, map[string]any{"raw": "empty"}
	}

	// Fast path: substring checks survive even if the server returns huge JSON and we truncate it.
	s := string(raw)
	if strings.Contains(s, "\"crawlStatus\":\"success\"") || strings.Contains(s, "\"taggingStatus\":\"success\"") {
		return true, map[string]any{"status": "success"}
	}

	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return false, map[string]any{"raw": "invalid_json"}
	}
	ok, sig := findNonTrivialContent(m, 0, map[string]any{})
	return ok, sig
}

func findNonTrivialContent(v any, depth int, sig map[string]any) (bool, map[string]any) {
	if depth > 6 || v == nil {
		return false, sig
	}
	switch x := v.(type) {
	case map[string]any:
		for k, vv := range x {
			kl := strings.ToLower(k)
			if kl == "notes" || kl == "note" || kl == "summary" {
				// not a signal of extracted page content
				continue
			}
			if kl == "content" || kl == "html" || kl == "text" || kl == "textcontent" || kl == "readablecontent" || kl == "excerpt" || kl == "description" || kl == "markdown" || kl == "article" {
				if s, ok := vv.(string); ok && len(strings.TrimSpace(s)) >= 200 {
					sig[k] = len(strings.TrimSpace(s))
					return true, sig
				}
				// track presence even if small
				if s, ok := vv.(string); ok {
					sig[k] = len(strings.TrimSpace(s))
				} else if vv != nil {
					sig[k] = fmt.Sprintf("<%T>", vv)
				}
			}
			if ok, sig2 := findNonTrivialContent(vv, depth+1, sig); ok {
				return true, sig2
			}
		}
	case []any:
		for _, vv := range x {
			if ok, sig2 := findNonTrivialContent(vv, depth+1, sig); ok {
				return true, sig2
			}
		}
	case string:
		// fallback: a large string deep in payload can also count as content
		if len(strings.TrimSpace(x)) >= 400 {
			sig["large_string"] = len(strings.TrimSpace(x))
			return true, sig
		}
	}
	return false, sig
}

func (a *App) waitForNonEmptySummary(ctx context.Context, client *karakeep.Client, bookmarkID string, interval time.Duration, timeout time.Duration) (karakeep.Bookmark, bool) {
	log := a.Logger
	if log == nil {
		log = slog.Default()
	}
	if interval <= 0 {
		interval = 3 * time.Second
	}
	if timeout <= 0 {
		timeout = 3 * time.Minute
	}

	deadline := time.Now().Add(timeout)
	t := time.NewTicker(interval)
	defer t.Stop()

	iter := 0
	for {
		// summarize is idempotent-ish; if content isn't ready it may return empty-message summary.
		_, _, _ = client.Summarize(ctx, bookmarkID)
		got, _, err := client.GetBookmark(ctx, bookmarkID)
		if err == nil {
			s := strings.TrimSpace(got.SummaryText())
			iter++
			if iter == 1 || iter%5 == 0 || (s != "" && !looksEmptySummary(s)) {
				log.Info("summary poll", "bookmark_id", bookmarkID, "len", len(s))
			}
			if s != "" && !looksEmptySummary(s) {
				return got, true
			}
		} else {
			log.Warn("karakeep get bookmark during summary poll failed", "err", err)
		}

		if time.Now().After(deadline) {
			return karakeep.Bookmark{}, false
		}
		select {
		case <-ctx.Done():
			return karakeep.Bookmark{}, false
		case <-t.C:
		}
	}
}

func looksEmptySummary(s string) bool {
	s = strings.ToLower(s)
	return strings.Contains(s, "content is empty") || strings.Contains(s, "no information to summarize")
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

