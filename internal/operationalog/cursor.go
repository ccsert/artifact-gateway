package operationalog

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"strings"
)

var ErrInvalidCursor = errors.New("invalid runtime log cursor")
var ErrCursorScopeChanged = errors.New("runtime log instance or session changed")

type cursor struct {
	Version  int    `json:"v"`
	Instance string `json:"i"`
	Session  string `json:"s"`
	Position uint64 `json:"p"`
	Filter   string `json:"f"`
}

// Cursor binds a scanned position to this process session and the caller's
// filter specification. The key is ephemeral and never persisted.
func (b *Buffer) Cursor(instance, session, filter string, position uint64) string {
	body, _ := json.Marshal(cursor{1, cursorDigest(instance), cursorDigest(session), position, cursorDigest(filter)})
	mac := hmac.New(sha256.New, b.cursorKey[:])
	_, _ = mac.Write(body)
	return base64.RawURLEncoding.EncodeToString(body) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (b *Buffer) ParseCursor(token, instance, session, filter string) (uint64, error) {
	if len(token) > 1024 {
		return 0, ErrInvalidCursor
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return 0, ErrInvalidCursor
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return 0, ErrInvalidCursor
	}
	var value cursor
	if json.Unmarshal(body, &value) != nil || value.Version != 1 || value.Position > math.MaxInt64 || !validCursorDigest(value.Instance) || !validCursorDigest(value.Session) || !validCursorDigest(value.Filter) {
		return 0, ErrInvalidCursor
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(signature) != sha256.Size {
		return 0, ErrInvalidCursor
	}
	if value.Instance != cursorDigest(instance) || value.Session != cursorDigest(session) {
		return 0, ErrCursorScopeChanged
	}
	mac := hmac.New(sha256.New, b.cursorKey[:])
	_, _ = mac.Write(body)
	if !hmac.Equal(signature, mac.Sum(nil)) || value.Filter != cursorDigest(filter) {
		return 0, ErrInvalidCursor
	}
	return value.Position, nil
}

func cursorDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

func validCursorDigest(value string) bool {
	digest, err := base64.RawURLEncoding.DecodeString(value)
	return err == nil && len(value) == 43 && len(digest) == sha256.Size
}
