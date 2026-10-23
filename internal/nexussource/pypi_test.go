package nexussource

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	pypi "github.com/ccsert/artifact-gateway/internal/protocol/pypi"
)

func TestPyPIHostedInventoryAndSameOriginSimple(t *testing.T) {
	for _, mode := range []string{"valid", "foreign-url", "query", "duplicate", "unsupported", "unauthorized", "source-version"} {
		t.Run(mode, func(t *testing.T) {
			filename := "widget-1.0-py3-none-any.whl"
			sourcePath := "packages/ab/" + filename
			service, id, _ := fixtureService(t, func(w http.ResponseWriter, r *http.Request) bool {
				switch r.URL.Path {
				case "/service/rest/v1/repositories":
					_ = json.NewEncoder(w).Encode([]map[string]string{{"name": "source", "format": "pypi", "type": "hosted"}})
				case "/service/rest/v1/repositories/pypi/hosted/source":
					_, _ = w.Write([]byte(`{"name":"source"}`))
				case "/service/rest/v1/components":
					_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{{"repository": "source", "format": "pypi", "name": "Widget", "version": "1.0", "assets": []map[string]any{{"repository": "source", "format": "pypi", "path": sourcePath, "downloadUrl": "https://example.com/repository/source/" + sourcePath, "fileSize": 7, "checksum": map[string]string{"sha256": strings.Repeat("a", 64)}}}}}, "continuationToken": nil})
				case "/repository/source/simple/widget/":
					if mode == "unauthorized" {
						w.WriteHeader(403)
						return true
					}
					w.Header().Set("Content-Type", "text/html")
					href := "../../" + sourcePath + "#sha256=" + strings.Repeat("a", 64)
					if mode == "foreign-url" {
						href = "https://foreign.test/" + sourcePath + "#sha256=" + strings.Repeat("a", 64)
					}
					if mode == "query" {
						href = strings.Replace(href, "#", "?secret=x#", 1)
					}
					if mode == "unsupported" {
						href = strings.Replace(href, ".whl", ".zip", 1)
					}
					link := `<a href="` + href + `" data-requires-python="&gt;=3.8" data-yanked>file</a>`
					if mode == "duplicate" {
						link += link
					}
					_, _ = w.Write([]byte(link))
				default:
					return false
				}
				return true
			})
			repos, err := service.InventoryRepositories(context.Background(), id, "1", []string{"source"})
			if err != nil {
				t.Fatal(err)
			}
			page, err := service.ReadInventoryPage(context.Background(), id, "1", repos[0], "")
			if err != nil || len(page.Assets) != 1 || page.Assets[0].PackageName != "widget" || page.Assets[0].Version != "1.0" || page.Assets[0].DownloadURL != "" {
				t.Fatalf("inventory=%+v %v", page, err)
			}
			version := "1"
			if mode == "source-version" {
				version = "2"
			}
			got, err := service.ReadPyPISimple(context.Background(), id, version, "source", "widget")
			if mode != "valid" {
				if err == nil {
					t.Fatal("unsafe Simple accepted")
				}
				return
			}
			snapshot, err := pypi.DecodeSnapshot(got.Body)
			if err != nil || snapshot.Files[0].SourcePath != sourcePath || !snapshot.Files[0].Yanked {
				t.Fatalf("snapshot=%+v %v", snapshot, err)
			}
		})
	}
}
