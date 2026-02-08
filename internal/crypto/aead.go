package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

// Key is a 32-byte AEAD key.
type Key [32]byte

func DeriveKeyFromSecret(secret string) (Key, error) {
	// For ops simplicity: allow either base64(32 bytes) or an arbitrary passphrase.
	// - If it's base64 and decodes to 32 bytes -> use directly.
	// - Otherwise -> SHA-256(passphrase).
	var k Key

	if secret == "" {
		return Key{}, errors.New("empty master key")
	}

	if raw, err := base64.StdEncoding.DecodeString(secret); err == nil && len(raw) == len(k) {
		copy(k[:], raw)
		return k, nil
	}

	sum := sha256.Sum256([]byte(secret))
	copy(k[:], sum[:])
	return k, nil
}

type AEAD struct {
	aead cipher.AEAD
}

func NewAEAD(key Key) (*AEAD, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("aes.NewCipher: %w", err)
	}
	a, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("cipher.NewGCM: %w", err)
	}
	return &AEAD{aead: a}, nil
}

func (a *AEAD) Encrypt(plaintext []byte) (nonce []byte, ciphertext []byte, err error) {
	nonce = make([]byte, a.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, fmt.Errorf("nonce: %w", err)
	}
	ciphertext = a.aead.Seal(nil, nonce, plaintext, nil)
	return nonce, ciphertext, nil
}

func (a *AEAD) Decrypt(nonce, ciphertext []byte) ([]byte, error) {
	pt, err := a.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}
	return pt, nil
}

