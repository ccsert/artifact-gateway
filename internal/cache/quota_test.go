package cache

import (
	"context"
	"errors"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/objectstore"
)

func TestQuotaRejectsEntryAboveConfiguredLimit(t *testing.T) {
	quota := NewQuota(objectstore.NewMemoryStore(), map[string]int64{"releases": 5})
	published := false
	err := quota.Admit(context.Background(), "releases", "oci/index/new.json", 6, func() error {
		published = true
		return nil
	})
	if !errors.Is(err, ErrQuotaExceeded) || published {
		t.Fatalf("err=%v published=%t", err, published)
	}
}
