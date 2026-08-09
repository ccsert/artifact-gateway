// Package secrets encrypts small application settings before persistence.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
)

const KeyEnv = "GATEWAY_SETTINGS_ENCRYPTION_KEY"

var ErrKeyNotConfigured = errors.New(KeyEnv + " is not configured")
var ErrInvalidKey = errors.New(KeyEnv + " is invalid")

func Seal(purpose, plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	gcm, err := newGCM()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plaintext), []byte(purpose))
	return base64.StdEncoding.EncodeToString(sealed), nil
}

func Open(purpose, encoded string) (string, error) {
	if encoded == "" {
		return "", nil
	}
	payload, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("decode encrypted setting: %w", err)
	}
	gcm, err := newGCM()
	if err != nil {
		return "", err
	}
	if len(payload) < gcm.NonceSize()+gcm.Overhead() {
		return "", errors.New("encrypted setting is truncated")
	}
	plaintext, err := gcm.Open(nil, payload[:gcm.NonceSize()], payload[gcm.NonceSize():], []byte(purpose))
	if err != nil {
		return "", errors.New("encrypted setting cannot be decrypted with the configured key")
	}
	return string(plaintext), nil
}

func newGCM() (cipher.AEAD, error) {
	key, err := dataKey()
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func dataKey() ([]byte, error) {
	raw := strings.TrimSpace(os.Getenv(KeyEnv))
	if raw == "" {
		return nil, ErrKeyNotConfigured
	}
	if decoded, err := hex.DecodeString(raw); err == nil && len(decoded) == 32 {
		return decoded, nil
	}
	if decoded, err := base64.StdEncoding.DecodeString(raw); err == nil && len(decoded) == 32 {
		return decoded, nil
	}
	if len(raw) == 32 {
		return []byte(raw), nil
	}
	return nil, fmt.Errorf("%w: must decode to 32 bytes (hex, base64, or raw)", ErrInvalidKey)
}
