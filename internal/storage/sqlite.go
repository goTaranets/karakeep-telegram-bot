package storage

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

	"karakeep-telegram-bot/internal/crypto"
)

type Store struct {
	db   *sql.DB
	aead *crypto.AEAD
}

type User struct {
	TelegramUserID int64

	ServerBaseURL string

	APIKeyCiphertextB64 string
	APIKeyNonceB64      string

	CreatedAt     time.Time
	UpdatedAt     time.Time
	LastSuccessAt sql.NullTime
	LastSuccessID sql.NullString
}

func Open(ctx context.Context, dbPath string, masterKey string) (*Store, error) {
	if stringsTrim(dbPath) == "" {
		return nil, errors.New("db path is empty")
	}
	if stringsTrim(masterKey) == "" {
		return nil, errors.New("master key is empty")
	}

	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		return nil, fmt.Errorf("mkdir db dir: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("sql.Open(sqlite): %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetConnMaxLifetime(0)

	k, err := crypto.DeriveKeyFromSecret(masterKey)
	if err != nil {
		return nil, err
	}
	a, err := crypto.NewAEAD(k)
	if err != nil {
		return nil, err
	}

	s := &Store{db: db, aead: a}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate(ctx context.Context) error {
	const ddl = `
CREATE TABLE IF NOT EXISTS users (
  telegram_user_id INTEGER PRIMARY KEY,
  server_base_url TEXT NOT NULL DEFAULT '',
  api_key_ciphertext_b64 TEXT NOT NULL DEFAULT '',
  api_key_nonce_b64 TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  last_success_at TEXT,
  last_success_id TEXT
);
`
	_, err := s.db.ExecContext(ctx, ddl)
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}

func (s *Store) UpsertUser(ctx context.Context, telegramUserID int64) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `
INSERT INTO users (telegram_user_id, created_at, updated_at)
VALUES (?, ?, ?)
ON CONFLICT(telegram_user_id) DO UPDATE SET updated_at=excluded.updated_at
`, telegramUserID, now, now)
	return err
}

func (s *Store) GetUser(ctx context.Context, telegramUserID int64) (User, error) {
	var u User
	u.TelegramUserID = telegramUserID

	var createdAt, updatedAt string
	var lastSuccessAt sql.NullString

	err := s.db.QueryRowContext(ctx, `
SELECT server_base_url, api_key_ciphertext_b64, api_key_nonce_b64, created_at, updated_at, last_success_at, last_success_id
FROM users WHERE telegram_user_id=?
`, telegramUserID).Scan(
		&u.ServerBaseURL,
		&u.APIKeyCiphertextB64,
		&u.APIKeyNonceB64,
		&createdAt,
		&updatedAt,
		&lastSuccessAt,
		&u.LastSuccessID,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, sql.ErrNoRows
		}
		return User{}, err
	}

	u.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	u.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	if lastSuccessAt.Valid {
		if t, err := time.Parse(time.RFC3339Nano, lastSuccessAt.String); err == nil {
			u.LastSuccessAt = sql.NullTime{Time: t, Valid: true}
		}
	}
	return u, nil
}

func (s *Store) SetServerBaseURL(ctx context.Context, telegramUserID int64, serverBaseURL string) error {
	if stringsTrim(serverBaseURL) == "" {
		return errors.New("server base url is empty")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `
UPDATE users SET server_base_url=?, updated_at=?
WHERE telegram_user_id=?
`, serverBaseURL, now, telegramUserID)
	return err
}

func (s *Store) SetAPIKey(ctx context.Context, telegramUserID int64, apiKey string) error {
	if stringsTrim(apiKey) == "" {
		return errors.New("api key is empty")
	}

	nonce, ct, err := s.aead.Encrypt([]byte(apiKey))
	if err != nil {
		return err
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = s.db.ExecContext(ctx, `
UPDATE users SET api_key_ciphertext_b64=?, api_key_nonce_b64=?, updated_at=?
WHERE telegram_user_id=?
`, base64.StdEncoding.EncodeToString(ct), base64.StdEncoding.EncodeToString(nonce), now, telegramUserID)
	return err
}

func (s *Store) DecryptAPIKey(u User) (string, bool, error) {
	if stringsTrim(u.APIKeyCiphertextB64) == "" || stringsTrim(u.APIKeyNonceB64) == "" {
		return "", false, nil
	}
	ct, err := base64.StdEncoding.DecodeString(u.APIKeyCiphertextB64)
	if err != nil {
		return "", false, fmt.Errorf("decode api_key ciphertext: %w", err)
	}
	nonce, err := base64.StdEncoding.DecodeString(u.APIKeyNonceB64)
	if err != nil {
		return "", false, fmt.Errorf("decode api_key nonce: %w", err)
	}
	pt, err := s.aead.Decrypt(nonce, ct)
	if err != nil {
		return "", false, err
	}
	return string(pt), true, nil
}

func (s *Store) SetLastSuccess(ctx context.Context, telegramUserID int64, bookmarkID string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `
UPDATE users SET last_success_at=?, last_success_id=?, updated_at=?
WHERE telegram_user_id=?
`, now, bookmarkID, now, telegramUserID)
	return err
}

func stringsTrim(s string) string {
	// tiny helper to avoid pulling strings in every file
	i := 0
	j := len(s)
	for i < j && (s[i] == ' ' || s[i] == '\n' || s[i] == '\r' || s[i] == '\t') {
		i++
	}
	for j > i && (s[j-1] == ' ' || s[j-1] == '\n' || s[j-1] == '\r' || s[j-1] == '\t') {
		j--
	}
	if i == 0 && j == len(s) {
		return s
	}
	return s[i:j]
}

