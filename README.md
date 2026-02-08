# karakeep-telegram-bot

Telegram-бот (Go, webhook) для сохранения сообщений в Karakeep по **API key**.

## ENV

- `TELEGRAM_BOT_TOKEN` (обязательно)
- `TELEGRAM_WEBHOOK_PATH` (опционально, по умолчанию `/telegram/webhook`)
- `TELEGRAM_WEBHOOK_SECRET` (рекомендуется) — проверяется по заголовку `X-Telegram-Bot-Api-Secret-Token`
- `LISTEN_ADDR` (по умолчанию `:8080`)
- `DB_PATH` (по умолчанию `./data/bot.sqlite`)
- `API_KEY_MASTER_KEY` (обязательно) — мастер‑ключ для шифрования Karakeep API key в SQLite
- `BOT_VERSION` (опционально) — показывается в `/status`

## Запуск

1) Поднимите сервер с публичным HTTPS (reverse proxy + сертификат) и направьте webhook path на приложение.

2) Зарегистрируйте webhook в Telegram:

```bash
TELEGRAM_BOT_TOKEN=... \
TELEGRAM_WEBHOOK_URL=https://bot.example.com/telegram/webhook \
TELEGRAM_WEBHOOK_SECRET=... \
go run ./cmd/setwebhook --drop-pending=true
```

3) Запустите бота:

```bash
TELEGRAM_BOT_TOKEN=... \
API_KEY_MASTER_KEY=... \
TELEGRAM_WEBHOOK_SECRET=... \
go run ./cmd/bot
```

## Запуск через Docker

1) Скопируйте `deploy/env.docker.example` → `deploy/env.docker` и заполните секреты (не коммитьте).

2) Запустите:

```bash
docker compose up -d --build
```

3) (Опционально) Регистрация webhook (можно запускать локально, где есть доступ в Telegram API):

```bash
TELEGRAM_BOT_TOKEN=... \
TELEGRAM_WEBHOOK_URL=https://bot.example.com/telegram/webhook \
TELEGRAM_WEBHOOK_SECRET=... \
go run ./cmd/setwebhook --drop-pending=true
```

## Настройка в Telegram

В личке с ботом:
- `/server https://<ваш_karakeep>`
- `/key <API_KEY>`

Дальше можно присылать ссылки/текст/медиа.

## Karakeep API docs

Используются официальные страницы:
- `POST /bookmarks` — [Create a new bookmark](https://docs.karakeep.app/api/create-a-new-bookmark)
- `PATCH /bookmarks/:bookmarkId` — [Update a bookmark](https://docs.karakeep.app/api/update-a-bookmark)
- `POST /bookmarks/:bookmarkId/summarize` — [Summarize a bookmark](https://docs.karakeep.app/api/summarize-a-bookmark)
- `POST /assets` — [Upload a new asset](https://docs.karakeep.app/api/upload-a-new-asset)
- `POST /bookmarks/:bookmarkId/assets` — [Attach asset](https://docs.karakeep.app/api/attach-asset)
- `GET /bookmarks/:bookmarkId` — [Get a single bookmark](https://docs.karakeep.app/api/get-a-single-bookmark)

