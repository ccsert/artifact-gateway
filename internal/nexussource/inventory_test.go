package nexussource

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func TestInventoryPagesExhaustMoreThanLegacyDiscoveryWindow(t *testing.T) {
	const total = 10001
	service, id, _ := fixtureService(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/service/rest/v1/assets" {
			return false
		}
		offset, _ := strconv.Atoi(r.URL.Query().Get("continuationToken"))
		end := min(offset+100, total)
		items := []map[string]any{}
		for i := offset; i < end; i++ {
			p := fmt.Sprintf("asset-%05d.bin", i)
			items = append(items, map[string]any{"repository": "raw-a", "format": "raw", "path": p, "downloadUrl": "https://example.com/repository/raw-a/" + p, "fileSize": 1, "checksum": map[string]string{"sha256": strings.Repeat("a", 64)}})
		}
		var next any
		if end < total {
			next = strconv.Itoa(end)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": items, "continuationToken": next})
		return true
	})
	repos, err := service.InventoryRepositories(context.Background(), id, "1", []string{"raw-a"})
	if err != nil {
		t.Fatal(err)
	}
	count, pages, cursor := 0, 0, ""
	for {
		page, err := service.ReadInventoryPage(context.Background(), id, "1", repos[0], cursor)
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range page.Assets {
			if a.DownloadURL != "" {
				t.Fatal("durable inventory retained an unnecessary upstream URL")
			}
		}
		count += len(page.Assets)
		pages++
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if count != total || pages != 101 {
		t.Fatalf("inventory=%d pages=%d", count, pages)
	}
}

func TestInventoryPageRejectsChangedSourceAndInvalidCursor(t *testing.T) {
	service, id, _ := fixtureService(t, nil)
	repos, err := service.InventoryRepositories(context.Background(), id, "1", []string{"raw-a"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.ReadInventoryPage(context.Background(), id, "1", repos[0], "bad\ncursor")
	requireCode(t, err, "invalid_request")
	_, err = service.ReadInventoryPage(context.Background(), id, "2", repos[0], "")
	requireCode(t, err, "source_version_changed")
	repos[0].Format = "maven2"
	_, err = service.ReadInventoryPage(context.Background(), id, "1", repos[0], "")
	requireCode(t, err, "source_changed")
}

func TestInventoryPageBindsUnsupportedMetadata(t *testing.T) {
	checksum := strings.Repeat("a", 64)
	var mu sync.Mutex
	service, id, _ := fixtureService(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/service/rest/v1/assets" {
			return false
		}
		mu.Lock()
		defer mu.Unlock()
		path := "com/acme/lib/1.1-SNAPSHOT/lib-1.1-20260101.123456-1.jar"
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{{"repository": "mixed", "format": "maven2", "path": path, "downloadUrl": "https://example.com/repository/mixed/" + path, "fileSize": 1, "checksum": map[string]string{"sha256": checksum}}}, "continuationToken": nil})
		return true
	})
	repos, err := service.InventoryRepositories(context.Background(), id, "1", []string{"mixed"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.ReadInventoryPage(context.Background(), id, "1", repos[0], "")
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	checksum = strings.Repeat("b", 64)
	mu.Unlock()
	second, err := service.ReadInventoryPage(context.Background(), id, "1", repos[0], "")
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Assets) != 0 || len(first.UnsupportedAssets) != 1 || first.MetadataSHA256 == "" || first.MetadataSHA256 == second.MetadataSHA256 {
		t.Fatal("unsupported source metadata drift was absent from private inventory fingerprint")
	}
}
