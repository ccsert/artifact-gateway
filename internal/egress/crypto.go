package egress

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

// KeyEnv names the environment variable holding the 32-byte data key used to
// encrypt stored egress proxy credentials. Accepts hex (64 chars), base64
// (44 chars), or a raw 32-byte string.
const KeyEnv = "GATEWAY_EGRESS_PROXY_KEY"

// ErrKeyNotConfigured is returned when credentials are used but no data key
// is configured on the process.
var ErrKeyNotConfigured = errors.New(KeyEnv + " is not configured")

// dataKey loads the AES-256 data key from the environment on each call so key
// rotation between processes never leaves stale key material in memory.
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
	return nil, errors.New(KeyEnv + " must decode to 32 bytes (hex, base64, or raw)")
}

// EncryptPassword seals a plaintext proxy password with AES-256-GCM and
// returns base64(nonce|ciphertext). An empty plaintext stays empty so
// credential-less proxies never require a configured key.
func EncryptPassword(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	key, err := dataKey()
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(gcm.Seal(nonce, nonce, []byte(plaintext), nil)), nil
}

// DecryptPassword opens a value produced by EncryptPassword. Empty input
// stays empty.
func DecryptPassword(encoded string) (string, error) {
	if encoded == "" {
		return "", nil
	}
	key, err := dataKey()
	if err != nil {
		return "", err
	}
	payload, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("decode egress proxy password: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(payload) < gcm.NonceSize()+gcm.Overhead() {
		return "", errors.New("egress proxy password ciphertext is truncated")
	}
	plaintext, err := gcm.Open(nil, payload[:gcm.NonceSize()], payload[gcm.NonceSize():], nil)
	if err != nil {
		return "", errors.New("egress proxy password cannot be decrypted with the configured key")
	}
	return string(plaintext), nil
}
