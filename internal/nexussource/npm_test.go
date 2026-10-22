package nexussource

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestNPMHostedInventoryUsesComponentIdentityAndPackumentBoundary(t *testing.T) {
	for _, name := range []string{"widget", "@scope/widget"} {
		t.Run(name, func(t *testing.T) {
			nameOnly, group := name, ""
			if strings.HasPrefix(name, "@") {
				group, nameOnly, _ = strings.Cut(name, "/")
			}
			service, id, _ := fixtureService(t, func(w http.ResponseWriter, r *http.Request) bool {
				switch r.URL.Path {
				case "/service/rest/v1/repositories":
					_ = json.NewEncoder(w).Encode([]map[string]string{{"name": "source", "format": "npm", "type": "hosted"}})
				case "/service/rest/v1/repositories/npm/hosted/source":
					_, _ = w.Write([]byte(`{"name":"source"}`))
				case "/service/rest/v1/components":
					_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{{"repository": "source", "format": "npm", "name": nameOnly, "group": group, "version": "7.8.9", "assets": []map[string]any{{"repository": "source", "format": "npm", "path": name + "/-/not-a-version.tgz", "downloadUrl": "https://example.com/repository/source/" + name + "/-/not-a-version.tgz", "fileSize": 7, "checksum": map[string]string{"sha256": strings.Repeat("a", 64)}}}}}, "continuationToken": nil})
				case "/repository/source/" + name:
					_ = json.NewEncoder(w).Encode(map[string]any{"name": name, "dist-tags": map[string]string{"latest": "7.8.9"}, "versions": map[string]any{"7.8.9": map[string]any{"name": name, "version": "7.8.9", "dist": map[string]string{"tarball": "https://example.com/repository/source/" + name + "/-/not-a-version.tgz", "shasum": strings.Repeat("b", 40)}}}})
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
			if err != nil {
				t.Fatal(err)
			}
			if len(page.Assets) != 1 || page.Assets[0].PackageName != name || page.Assets[0].Version != "7.8.9" || page.Assets[0].DownloadURL != "" {
				t.Fatalf("component identity=%+v", page)
			}
			p, err := service.ReadNPMPackument(context.Background(), id, "1", "source", name)
			if err != nil || p.TarballPaths["7.8.9"] != name+"/-/not-a-version.tgz" {
				t.Fatalf("packument=%+v %v", p, err)
			}
		})
	}
}
func TestNPMPackumentRejectsForeignCredentialAndAmbiguousURLs(t *testing.T) {
	for _, dist := range []string{"https://denied.invalid/repository/source/widget/-/a.tgz", "https://user:secret@example.com/repository/source/widget/-/a.tgz", "https://example.com/repository/other/widget/-/a.tgz", "https://example.com/repository/source/other/-/a.tgz", "https://example.com/repository/source/widget/-/a.tgz?key=secret", "https://example.com/repository/source/widget/-/../a.tgz"} {
		t.Run(dist, func(t *testing.T) {
			service, id, _ := fixtureService(t, func(w http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path != "/repository/source/widget" {
					return false
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"name": "widget", "dist-tags": map[string]string{"latest": "1.0.0"}, "versions": map[string]any{"1.0.0": map[string]any{"name": "widget", "version": "1.0.0", "dist": map[string]string{"tarball": dist, "shasum": strings.Repeat("b", 40)}}}})
				return true
			})
			_, err := service.ReadNPMPackument(context.Background(), id, "1", "source", "widget")
			requireCode(t, err, "source_download_url_denied")
			if strings.Contains(err.Error(), "secret") {
				t.Fatal("source error leaked URL credentials")
			}
		})
	}
}
