package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/protocol/cargo"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestNativeCargoHostedSearchYankAndDownload(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	if _, err := store.CreateHostedRepository(ctx, repository.HostedRepository{
		ID: "cargo-yank", Name: "cargo-yank", Format: repository.FormatCargo,
	}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewGatewayHandler(Dependencies{NativeCargoObjectStore: NewMemoryOCIObjectStore()},
		store, TestAdapter{}, testAuthenticator()))
	defer server.Close()
	request := func(method, path, token string, body []byte, headers map[string]string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(method, server.URL+"/cargo/cargo-yank/"+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if token != "" {
			req.Header.Set("Authorization", token)
		}
		for key, value := range headers {
			req.Header.Set(key, value)
		}
		response, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	read := func(response *http.Response) []byte {
		t.Helper()
		defer func() { _ = response.Body.Close() }()
		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		return body
	}
	crates := make(map[string][]byte)
	for _, version := range []string{"1.2.0", "1.10.0"} {
		body, crate := cargoC0PublishFixture(t, "demo", version, "demo")
		crates[version] = crate
		response := request(http.MethodPut, "api/v1/crates/new", "admin-secret", body, nil)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("publish %s=%d body=%s", version, response.StatusCode, read(response))
		}
		_ = read(response)
	}
	indexPath, err := cargo.SparseIndexPath("demo")
	if err != nil {
		t.Fatal(err)
	}
	readIndex := func(headers map[string]string) (*http.Response, []cargo.IndexEntry) {
		t.Helper()
		response := request(http.MethodGet, indexPath, "resolver-secret", nil, headers)
		data := read(response)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("index=%d body=%s", response.StatusCode, data)
		}
		lines := bytes.Split(bytes.TrimSpace(data), []byte("\n"))
		entries := make([]cargo.IndexEntry, 0, len(lines))
		for _, line := range lines {
			var entry cargo.IndexEntry
			if err := json.Unmarshal(line, &entry); err != nil {
				t.Fatal(err)
			}
			entries = append(entries, entry)
		}
		return response, entries
	}
	before, original := readIndex(nil)
	if len(original) != 2 || original[0].Yanked || original[1].Yanked {
		t.Fatalf("original index=%+v", original)
	}
	if before.Header.Get("Last-Modified") != "" {
		t.Fatal("mutable Cargo index must rely on the strong ETag rather than a second-resolution modification time")
	}
	search := func(expected string) {
		t.Helper()
		response := request(http.MethodGet, "api/v1/crates?q=demo&per_page=10", "resolver-secret", nil, nil)
		data := read(response)
		var result struct {
			Crates []struct {
				Name       string `json:"name"`
				MaxVersion string `json:"max_version"`
			} `json:"crates"`
			Meta struct {
				Total int `json:"total"`
			} `json:"meta"`
		}
		if response.StatusCode != http.StatusOK || json.Unmarshal(data, &result) != nil || result.Meta.Total != 1 ||
			len(result.Crates) != 1 || result.Crates[0].Name != "demo" || result.Crates[0].MaxVersion != expected {
			t.Fatalf("search=%d body=%s", response.StatusCode, data)
		}
	}
	search("1.10.0")
	yankPath := "api/v1/crates/demo/1.10.0/yank"
	for _, denied := range []struct {
		token string
		want  int
	}{{"", http.StatusUnauthorized}, {"resolver-secret", http.StatusForbidden}} {
		response := request(http.MethodDelete, yankPath, denied.token, nil, nil)
		if response.StatusCode != denied.want {
			t.Fatalf("denied yank=%d body=%s", response.StatusCode, read(response))
		}
		_ = read(response)
	}
	response := request(http.MethodDelete, yankPath, "admin-secret", nil, nil)
	if response.StatusCode != http.StatusOK || !bytes.Contains(read(response), []byte(`"ok":true`)) {
		t.Fatalf("yank=%d", response.StatusCode)
	}
	if conditional := request(http.MethodGet, indexPath, "resolver-secret", nil, map[string]string{"If-None-Match": before.Header.Get("ETag")}); conditional.StatusCode != http.StatusOK {
		t.Fatalf("stale index validator=%d body=%s", conditional.StatusCode, read(conditional))
	} else {
		_ = read(conditional)
	}
	if conditional := request(http.MethodGet, indexPath, "resolver-secret", nil, map[string]string{"If-Modified-Since": "Wed, 21 Oct 2030 07:28:00 GMT"}); conditional.StatusCode != http.StatusOK {
		t.Fatalf("index incorrectly accepted a timestamp validator=%d body=%s", conditional.StatusCode, read(conditional))
	} else {
		_ = read(conditional)
	}
	_, yanked := readIndex(nil)
	if len(yanked) != 2 || !yanked[0].Yanked || yanked[0].Version != "1.10.0" || yanked[1].Yanked {
		t.Fatalf("yanked index=%+v", yanked)
	}
	original[0].Yanked = true
	if got, want := mustJSON(t, yanked), mustJSON(t, original); !bytes.Equal(got, want) {
		t.Fatalf("yank changed other index fields: got=%s want=%s", got, want)
	}
	search("1.2.0")
	download := request(http.MethodGet, "api/v1/crates/demo/1.10.0/download", "resolver-secret", nil, nil)
	if download.StatusCode != http.StatusOK || !bytes.Equal(read(download), crates["1.10.0"]) {
		t.Fatalf("yanked crate download=%d", download.StatusCode)
	}
	undo := request(http.MethodPut, "api/v1/crates/demo/1.10.0/unyank", "admin-secret", nil, nil)
	if undo.StatusCode != http.StatusOK || !strings.Contains(string(read(undo)), `"ok":true`) {
		t.Fatalf("unyank=%d", undo.StatusCode)
	}
	search("1.10.0")
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
