package agctl

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

const catalogueID = "00000000-0000-4000-e000-000000000001"

func catalogueFixture(kind, name, description, version string) string {
	payload := `"scopes":["repositories:read"]`
	if kind == "template" {
		payload = `"grants":[{"principal":"service-account:ci","scopes":["repositories:read"],"resourcePrefix":"releases/"}]`
	}
	return fmt.Sprintf(`{"id":%q,"name":%q,"description":%q,"version":%q,"createdAt":"2026-10-28T08:00:00Z","updatedAt":"2026-10-28T08:00:00Z",%s}`, catalogueID, name, description, version, payload)
}

func catalogueGrantsFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "grants.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func catalogueCreateArgs(t *testing.T, kind string) []string {
	t.Helper()
	args := []string{kind, "create", "readers"}
	if kind == "role" {
		return append(args, "--scope", "repositories:read")
	}
	return append(args, "--grants-file", catalogueGrantsFile(t, `[{"principal":"service-account:ci","scopes":["repositories:read"],"resourcePrefix":"releases/"}]`))
}

func TestAuthorizationCatalogueCRUDAndPlans(t *testing.T) {
	for _, kind := range []string{"role", "template"} {
		t.Run(kind, func(t *testing.T) {
			api := newRouteAPI(t, "")
			base := "/api/v2/authorization-" + map[string]string{"role": "roles", "template": "templates"}[kind]
			path := base + "/" + catalogueID
			args := catalogueCreateArgs(t, kind)
			code, stdout, stderr := runCLI(t, append(args, "--dry-run", "--json")...)
			var plan writePlan
			_ = json.Unmarshal(decodeEnvelope(t, stdout).Data, &plan)
			if code != ExitOK || stderr != "" || len(api.recorded()) != 0 || plan.Method != "POST" || plan.Path != base || plan.Headers != nil || plan.Mode != "client-plan" {
				t.Fatalf("create plan: exit=%d requests=%+v stdout=%s stderr=%s", code, api.recorded(), stdout, stderr)
			}
			api.on("POST", base, 201, catalogueFixture(kind, "readers", "", "1"))
			code, stdout, _ = runCLI(t, append(args, "--json")...)
			if code != ExitOK || !decodeEnvelope(t, stdout).OK || api.count("POST") != 1 {
				t.Fatalf("create: exit=%d stdout=%s", code, stdout)
			}
			api.on("GET", path, 200, catalogueFixture(kind, "readers", "old description", "1"))
			code, stdout, _ = runCLI(t, kind, "update", catalogueID, "--description", "changed", "--dry-run", "--json")
			_ = json.Unmarshal(decodeEnvelope(t, stdout).Data, &plan)
			if code != ExitOK || plan.Headers["If-Match"] != "1" || !strings.Contains(stdout, `"diff"`) || !strings.Contains(stdout, `"scope": "authorization catalogue only; existing repository grants are unchanged"`) || api.count("PUT") != 0 {
				t.Fatalf("update plan: exit=%d stdout=%s", code, stdout)
			}
			api.on("PUT", path, 200, catalogueFixture(kind, "readers", "", "2"))
			code, stdout, _ = runCLI(t, kind, "update", catalogueID, "--description=", "--yes", "--json")
			if code != ExitOK || !strings.Contains(stdout, `"version": "2"`) {
				t.Fatalf("update: exit=%d stdout=%s", code, stdout)
			}
			for _, request := range api.recorded() {
				if request.Method != "PUT" {
					continue
				}
				var body map[string]any
				_ = json.Unmarshal([]byte(request.Body), &body)
				if request.IfMatch != "1" || body["name"] != "readers" || body["description"] != "" || body["id"] != nil || body["version"] != nil {
					t.Fatalf("replacement did not preserve fields or clear description: %+v", request)
				}
				if kind == "role" && fmt.Sprint(body["scopes"]) != "[repositories:read]" || kind == "template" && !strings.Contains(request.Body, `"resourcePrefix":"releases/"`) {
					t.Fatalf("replacement lost its unchanged scopes/grants: %+v", request)
				}
			}
			api.on("GET", path, 200, catalogueFixture(kind, "readers", "", "2"))
			code, stdout, _ = runCLI(t, kind, "delete", catalogueID, "--dry-run", "--json")
			if code != ExitOK || !strings.Contains(stdout, "no If-Match support") || api.count("DELETE") != 0 {
				t.Fatalf("delete plan: exit=%d stdout=%s", code, stdout)
			}
			api.on("DELETE", path, 204, "")
			code, stdout, _ = runCLI(t, kind, "delete", catalogueID, "--yes", "--json")
			if code != ExitOK || !strings.Contains(stdout, `"deleted": true`) || api.count("DELETE") != 1 {
				t.Fatalf("delete: exit=%d stdout=%s", code, stdout)
			}
			if last := api.recorded()[len(api.recorded())-1]; last.IfMatch != "" || last.Body != "" {
				t.Fatalf("delete invented a precondition/body: %+v", last)
			}
		})
	}
}

func TestAuthorizationCatalogueRejectsInputBeforeHTTP(t *testing.T) {
	api := newRouteAPI(t, "")
	expectUsage(t, api,
		[]string{"role", "create", "readers"},
		[]string{"role", "create", "readers", "--scope", "repositories:root"},
		[]string{"role", "create", "readers", "--scope", "repositories:read", "--scope", "repositories:read"},
		[]string{"role", "create", " ", "--scope", "repositories:read"},
		[]string{"role", "create", strings.Repeat("a", 129), "--scope", "repositories:read"},
		[]string{"role", "create", "readers", "--scope", "repositories:read", "--description", strings.Repeat("d", 1001)},
		[]string{"template", "create", "readers"},
		[]string{"template", "create", "readers", "--grants-file", filepath.Join(t.TempDir(), "absent")},
	)
	for _, kind := range []string{"role", "template"} {
		expectUsage(t, api,
			[]string{kind, "update", catalogueID, "--name", "changed"},
			[]string{kind, "update", catalogueID, "--yes"},
			[]string{kind, "update", catalogueID, "--name", "changed", "--if-match=", "--yes"},
			[]string{kind, "update", catalogueID, "--name=", "--yes"},
			[]string{kind, "delete", catalogueID},
			[]string{kind, "delete", catalogueID, "--if-match", "1", "--yes"},
		)
		for _, id := range []string{"admin", "member", "none", "00000000-0000-0000-0000-000000000000"} {
			expectUsage(t, api, []string{kind, "update", id, "--name", "changed", "--yes"}, []string{kind, "delete", id, "--yes"})
		}
	}
	for _, body := range []string{
		`null`, `{}`, `[] {}`, `[`, `[{"principal":"user:alice","scopes":[]}]`,
		`[{"principal":"user:alice","scopes":["repositories:read"],"role":"admin"}]`,
		`[{"principal":"user:alice","scopes":["repositories:read","repositories:read"]}]`,
		`[{"principal":"user:alice","scopes":["repositories:root"]}]`,
		`[{"principal":"user:alice","scopes":["repositories:read"],"resourcePrefix":"a\nb"}]`,
		`[{"principal":"user:\nalice","scopes":["repositories:read"]}]`,
		`[{"principal":"user:alice","scopes":["repositories:read"]},{"principal":"user:alice","scopes":["repositories:write"]}]`,
		"[" + strings.Repeat(`{"principal":"a","scopes":["repositories:read"]},`, 500) + `{"principal":"b","scopes":["repositories:read"]}]`,
		strings.Repeat(" ", (128<<10)+1),
	} {
		file := catalogueGrantsFile(t, body)
		expectUsage(t, api, []string{"template", "create", "readers", "--grants-file", file}, []string{"template", "update", catalogueID, "--grants-file", file, "--yes"})
	}
}

func TestAuthorizationCatalogueReplacesScopesAndAllowsExplicitEmptyTemplate(t *testing.T) {
	for _, kind := range []string{"role", "template"} {
		t.Run(kind, func(t *testing.T) {
			api := newRouteAPI(t, "")
			path := "/api/v2/authorization-" + map[string]string{"role": "roles", "template": "templates"}[kind] + "/" + catalogueID
			api.on("GET", path, 200, catalogueFixture(kind, "readers", "keep", "3"))
			next := catalogueFixture(kind, "changed", "keep", "4")
			args := []string{kind, "update", catalogueID, "--name", "changed", "--if-match", "3", "--yes", "--json"}
			if kind == "role" {
				args = append(args, "--scope", "repositories:write", "--scope", "repositories:intelligence")
				next = strings.Replace(next, `["repositories:read"]`, `["repositories:write","repositories:intelligence"]`, 1)
			} else {
				args = append(args, "--grants-file", catalogueGrantsFile(t, `[]`))
				next = strings.Replace(next, `[{"principal":"service-account:ci","scopes":["repositories:read"],"resourcePrefix":"releases/"}]`, `[]`, 1)
			}
			api.on("PUT", path, 200, next)
			if code, stdout, _ := runCLI(t, args...); code != ExitOK {
				t.Fatalf("replacement: exit=%d stdout=%s", code, stdout)
			}
			request := api.recorded()[1]
			if request.IfMatch != "3" || !strings.Contains(request.Body, `"description":"keep"`) || kind == "template" && !strings.Contains(request.Body, `"grants":[]`) {
				t.Fatalf("bad replacement: %+v", request)
			}
		})
	}
}

func TestAuthorizationCatalogueConflictReadsLatestWithoutOverwriting(t *testing.T) {
	for _, kind := range []string{"role", "template"} {
		t.Run(kind, func(t *testing.T) {
			api := newRouteAPI(t, "")
			path := "/api/v2/authorization-" + map[string]string{"role": "roles", "template": "templates"}[kind] + "/" + catalogueID
			var reads atomic.Int32
			api.onFunc("GET", path, func(*http.Request) (int, string) {
				if reads.Add(1) == 1 {
					return 200, catalogueFixture(kind, "readers", "before", "1")
				}
				return 200, catalogueFixture(kind, "winner", "concurrent change", "2")
			})
			api.on("PUT", path, 412, `{"code":"version_conflict","message":"stale"}`)
			code, stdout, _ := runCLI(t, kind, "update", catalogueID, "--description", "proposed", "--yes", "--json")
			var data struct {
				Outcome, ExpectedVersion string
				Current                  struct{ Name, Description, Version string }
				Proposed                 struct{ Name, Description string }
			}
			env := decodeEnvelope(t, stdout)
			_ = json.Unmarshal(env.Data, &data)
			if code != ExitConflict || env.Error.Code != "version_conflict" || data.Outcome != "conflict" || data.ExpectedVersion != "1" || data.Current.Name != "winner" || data.Current.Version != "2" || data.Proposed.Description != "proposed" || api.count("PUT") != 1 || reads.Load() != 2 {
				t.Fatalf("conflict: exit=%d requests=%+v stdout=%s", code, api.recorded(), stdout)
			}
		})
	}
}

func TestAuthorizationCatalogueUnknownWritesAreNotRetried(t *testing.T) {
	for _, kind := range []string{"role", "template"} {
		for _, operation := range []string{"create", "update", "delete"} {
			t.Run(kind+"/"+operation, func(t *testing.T) {
				api := newRouteAPI(t, "")
				base := "/api/v2/authorization-" + map[string]string{"role": "roles", "template": "templates"}[kind]
				path := base + "/" + catalogueID
				api.on("GET", path, 200, catalogueFixture(kind, "readers", "", "1"))
				method := map[string]string{"create": "POST", "update": "PUT", "delete": "DELETE"}[operation]
				args := []string{kind, operation, catalogueID, "--yes"}
				if operation == "create" {
					path, args = base, catalogueCreateArgs(t, kind)
				}
				if operation == "update" {
					args = append(args, "--description", "changed")
				}
				api.on(method, path, 503, `{"code":"unavailable","message":"lost response"}`)
				code, stdout, stderr := runCLI(t, append(args, "--json")...)
				var data unknownOutcome
				_ = json.Unmarshal(decodeEnvelope(t, stdout).Data, &data)
				if code == ExitOK || data.Outcome != "unknown" || data.Operation != kind+" "+operation || data.ReadBack == "" || data.IdempotencyKey != "" || api.count(method) != 1 {
					t.Fatalf("unknown write: exit=%d requests=%+v stdout=%s", code, api.recorded(), stdout)
				}
				noSecret(t, repoTestToken, stdout, stderr)
			})
		}
	}
}

func TestAuthorizationCatalogueRejectsIncompleteAndWrongWriteIdentity(t *testing.T) {
	for _, kind := range []string{"role", "template"} {
		for _, body := range []string{"", `null`, `{}`, `{"id":"` + catalogueID + `","name":"readers","version":"1"}`, catalogueFixture(kind, "another-entry", "", "1")} {
			t.Run(kind+"/"+fmt.Sprint(len(body)), func(t *testing.T) {
				api := newRouteAPI(t, "")
				base := "/api/v2/authorization-" + map[string]string{"role": "roles", "template": "templates"}[kind]
				api.on("POST", base, 201, body)
				code, stdout, _ := runCLI(t, append(catalogueCreateArgs(t, kind), "--json")...)
				var data unknownOutcome
				_ = json.Unmarshal(decodeEnvelope(t, stdout).Data, &data)
				if code == ExitOK || data.Outcome != "unknown" || api.count("POST") != 1 {
					t.Fatalf("incomplete write: exit=%d stdout=%s", code, stdout)
				}
			})
		}
	}
}

func TestAuthorizationCatalogueErrorExitCodes(t *testing.T) {
	for _, kind := range []string{"role", "template"} {
		for status, want := range map[int]int{401: ExitUnauthenticated, 403: ExitForbidden, 404: ExitNotFound, 409: ExitConflict} {
			t.Run(fmt.Sprintf("%s/%d", kind, status), func(t *testing.T) {
				api := newRouteAPI(t, "")
				base := "/api/v2/authorization-" + map[string]string{"role": "roles", "template": "templates"}[kind]
				api.on("POST", base, status, `{"code":"access_denied","message":"refused"}`)
				code, stdout, stderr := runCLI(t, append(catalogueCreateArgs(t, kind), "--json")...)
				if code != want || decodeEnvelope(t, stdout).OK || api.count("POST") != 1 {
					t.Fatalf("error: exit=%d stdout=%s", code, stdout)
				}
				noSecret(t, repoTestToken, stdout, stderr)
			})
		}
	}
}

func TestAuthorizationCatalogueWrongUpdateIdentityCannotReportSuccess(t *testing.T) {
	for _, kind := range []string{"role", "template"} {
		for _, wrong := range []string{"id", "version", "contents"} {
			t.Run(kind+"/"+wrong, func(t *testing.T) {
				api := newRouteAPI(t, "")
				path := "/api/v2/authorization-" + map[string]string{"role": "roles", "template": "templates"}[kind] + "/" + catalogueID
				api.on("GET", path, 200, catalogueFixture(kind, "readers", "before", "1"))
				response := catalogueFixture(kind, "readers", "changed", "2")
				switch wrong {
				case "id":
					response = strings.Replace(response, catalogueID, templateID, 1)
				case "version":
					response = strings.Replace(response, `"version":"2"`, `"version":"1"`, 1)
				case "contents":
					response = strings.Replace(response, "repositories:read", "repositories:admin", 1)
				}
				api.on("PUT", path, 200, response)
				code, stdout, _ := runCLI(t, kind, "update", catalogueID, "--description", "changed", "--yes", "--json")
				var data unknownOutcome
				_ = json.Unmarshal(decodeEnvelope(t, stdout).Data, &data)
				if code == ExitOK || data.Outcome != "unknown" || data.ID != catalogueID || data.Version != "1" || api.count("PUT") != 1 {
					t.Fatalf("wrong update identity: exit=%d stdout=%s", code, stdout)
				}
			})
		}
	}
}

func TestAuthorizationCatalogueFailedConflictReadBackKeepsPreconditionError(t *testing.T) {
	api := newRouteAPI(t, "")
	path := "/api/v2/authorization-roles/" + catalogueID
	var reads atomic.Int32
	api.onFunc("GET", path, func(*http.Request) (int, string) {
		if reads.Add(1) == 1 {
			return 200, catalogueFixture("role", "readers", "before", "1")
		}
		return 404, `{"code":"not_found","message":"deleted concurrently"}`
	})
	api.on("PUT", path, 412, `{"code":"version_conflict","message":"stale"}`)
	code, stdout, _ := runCLI(t, "role", "update", catalogueID, "--description", "changed", "--yes", "--json")
	var data map[string]any
	env := decodeEnvelope(t, stdout)
	_ = json.Unmarshal(env.Data, &data)
	if code != ExitConflict || env.Error.Code != "version_conflict" || data["current"] != nil || data["readBack"] != "agctl role show "+catalogueID || api.count("PUT") != 1 {
		t.Fatalf("failed conflict read-back: exit=%d stdout=%s", code, stdout)
	}
}
