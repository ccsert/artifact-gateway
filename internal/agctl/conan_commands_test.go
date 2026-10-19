package agctl

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

const conanReference = "widget/1.0/user/stable"
const conanRecipeJSON = `{"reference":"widget/1.0/user/stable","revision":"r1","digest":"` + testDigest + `","state":"visible","createdAt":"2026-10-19T00:00:00Z"}`
const conanPackageJSON = `{"reference":"widget/1.0/user/stable","recipeRevision":"r1","packageId":"pkg1","revision":"p1","digest":"` + testDigest + `","state":"visible","createdAt":"2026-10-19T00:00:00Z"}`

func conanPackageArgs(command string) []string {
	args := append([]string{"conan"}, strings.Fields(command)...)
	return append(args, "--repo", testRepoID, "--reference", conanReference, "--recipe-revision", "r1", "--package-id", "pkg1")
}

func TestConanCommandsValidateBeforeRequest(t *testing.T) {
	api := newRouteAPI(t, "")
	expectUsage(t, api,
		[]string{"conan", "references"},
		[]string{"conan", "recipe-revisions", "--q", "r1"},
		[]string{"conan", "recipe-revisions", "--repo", testRepoID},
		[]string{"conan", "package-ids", "--repo", testRepoID, "--reference", conanReference},
		[]string{"conan", "package-revisions", "--repo", testRepoID, "--reference", conanReference, "--recipe-revision", "r1"},
		[]string{"conan", "package-ids", "--repo", testRepoID, "--reference", conanReference, "--recipe-revision", "r1", "--all"},
		append(conanPackageArgs("package-revisions"), "--page-size", "1"),
		conanPackageArgs("package-revision delete"),
		append(conanPackageArgs("package-revision delete"), "--revision", "p1"),
	)
}

func TestConanReferencesUseServerCursorsAndPartialBudget(t *testing.T) {
	api := newRouteAPI(t, "/prefix")
	api.onFunc("GET", repoBase+"/conan/references", func(r *http.Request) (int, string) {
		if r.URL.Query().Get("q") != "widget/" || r.URL.Query().Get("pageSize") != "1" {
			t.Errorf("unexpected query: %s", r.URL.RawQuery)
		}
		if r.URL.Query().Get("pageToken") == "next/ref" {
			return 200, `{"items":[{"reference":"widget/2.0/user/stable"}]}`
		}
		return 200, `{"items":[{"reference":"widget/1.0/user/stable","publisher":"user:ci"}],"nextPageToken":"next/ref"}`
	})
	args := []string{"conan", "references", "--repo", testRepoID, "--q", "widget/", "--page-size", "1", "--all", "--json"}
	code, stdout, stderr := runCLI(t, args...)
	if code != ExitOK {
		t.Fatalf("exit=%d %s %s", code, stdout, stderr)
	}
	assertGolden(t, "conan_references_all", stdout)
	code, stdout, _ = runCLI(t, append(args, "--max-pages", "1")...)
	if code != ExitPartial || !strings.Contains(stdout, `"nextCursor": "next/ref"`) || !strings.Contains(stdout, `"complete": false`) {
		t.Fatalf("partial exit=%d %s", code, stdout)
	}
	api.on("GET", repoBase+"/conan/references", 404, `{"code":"not_found","message":"hidden repository"}`)
	code, stdout, _ = runCLI(t, args...)
	if code != ExitNotFound || decodeEnvelope(t, stdout).Error.Code != "not_found" {
		t.Fatalf("hidden exit=%d %s", code, stdout)
	}
}

func TestConanRecipeRevisionsBindFiltersAndReportScope(t *testing.T) {
	api := newRouteAPI(t, "")
	api.on("GET", repoBase+"/conan/recipe-revisions", 200, `{"items":[`+conanRecipeJSON+`],"nextPageToken":"r-next"}`)
	args := []string{"conan", "recipe-revisions", "--repo", testRepoID, "--reference", conanReference, "--q", "r1+digest", "--page-size", "1", "--cursor", "r/start", "--json"}
	code, stdout, stderr := runCLI(t, args...)
	if code != ExitOK {
		t.Fatalf("exit=%d %s %s", code, stdout, stderr)
	}
	assertGolden(t, "conan_recipe_revisions", stdout)
	request := api.recorded()[0]
	if request.Query != "pageSize=1&pageToken=r%2Fstart&q=r1%2Bdigest&reference=widget%2F1.0%2Fuser%2Fstable" {
		t.Fatalf("query=%s", request.Query)
	}
	api.on("GET", repoBase+"/conan/recipe-revisions", 403, `{"code":"access_denied","message":"no permission"}`)
	code, stdout, _ = runCLI(t, args...)
	if code != ExitForbidden || decodeEnvelope(t, stdout).Error.Code != "access_denied" || len(api.recorded()) != 2 {
		t.Fatalf("permission denial exit=%d %s requests=%+v", code, stdout, api.recorded())
	}
}

func TestConanNonpagedViewsAndHumanRows(t *testing.T) {
	api := newRouteAPI(t, "")
	api.on("GET", repoBase+"/conan/package-ids", 200, `{"items":["pkg1","pkg2"]}`)
	api.on("GET", repoBase+"/conan/package-revisions", 200, `{"items":[`+conanPackageJSON+`]}`)
	for _, tc := range []struct {
		args   []string
		golden string
		human  string
	}{
		{[]string{"conan", "package-ids", "--repo", testRepoID, "--reference", conanReference, "--recipe-revision", "r1"}, "conan_package_ids", "pkg2"},
		{conanPackageArgs("package-revisions"), "conan_package_revisions", testDigest},
	} {
		code, stdout, stderr := runCLI(t, append(tc.args, "--json")...)
		if code != ExitOK {
			t.Fatalf("%v exit=%d %s %s", tc.args, code, stdout, stderr)
		}
		assertGolden(t, tc.golden, stdout)
		code, stdout, _ = runCLI(t, tc.args...)
		if code != ExitOK || !strings.Contains(stdout, tc.human) {
			t.Fatalf("human exit=%d %s", code, stdout)
		}
	}
	if request := api.recorded()[2]; request.Query != "packageId=pkg1&recipeRevision=r1&reference=widget%2F1.0%2Fuser%2Fstable" {
		t.Fatalf("package query=%s", request.Query)
	}
	for _, body := range []string{`{}`, `null`, `{"items":null}`} {
		api.on("GET", repoBase+"/conan/package-revisions", 200, body)
		code, stdout, _ := runCLI(t, append(conanPackageArgs("package-revisions"), "--json")...)
		if code == ExitOK || decodeEnvelope(t, stdout).Error.Code != "invalid_response" {
			t.Fatalf("incomplete collection %s: exit=%d %s", body, code, stdout)
		}
	}
}

func TestConanDeletionPlansWithoutMutationAndVerifiesExactIdentity(t *testing.T) {
	api := newRouteAPI(t, "")
	listPath := repoBase + "/conan/package-revisions"
	api.on("GET", listPath, 200, `{"items":[`+conanPackageJSON+`]}`)
	args := append(conanPackageArgs("package-revision delete"), "--revision", "p1", "--json")
	code, stdout, stderr := runCLI(t, append(args, "--dry-run")...)
	if code != ExitOK || api.count("DELETE") != 0 {
		t.Fatalf("dry-run exit=%d %s %s", code, stdout, stderr)
	}
	var plan struct {
		Current struct{ Digest string }
	}
	if err := json.Unmarshal(decodeEnvelope(t, stdout).Data, &plan); err != nil || plan.Current.Digest != testDigest {
		t.Fatalf("dry-run must retain structured identity: %v %s", err, stdout)
	}
	assertGolden(t, "conan_package_revision_delete_dry_run", stdout)
	api.on("DELETE", listPath+"/p1", 204, "")
	code, stdout, _ = runCLI(t, append(args, "--yes")...)
	if code == ExitOK || !strings.Contains(stdout, `"outcome": "unknown"`) {
		t.Fatalf("still visible must not succeed: exit=%d %s", code, stdout)
	}
	api.on("GET", listPath, 200, `{"items":[`+strings.ReplaceAll(conanPackageJSON, `"revision":"p1"`, `"revision":"p2"`)+`]}`)
	code, stdout, _ = runCLI(t, append(args, "--yes")...)
	if code != ExitOK {
		t.Fatalf("verified tombstone exit=%d %s", code, stdout)
	}
	assertGolden(t, "conan_package_revision_delete", stdout)
	for _, request := range api.recorded() {
		if request.Query != "packageId=pkg1&recipeRevision=r1&reference=widget%2F1.0%2Fuser%2Fstable" {
			t.Fatalf("lost selection: %+v", request)
		}
	}
	// A different package's list cannot establish this package's absence.
	api.on("GET", listPath, 200, `{"items":[`+strings.ReplaceAll(conanPackageJSON, `"packageId":"pkg1"`, `"packageId":"other"`)+`]}`)
	code, stdout, _ = runCLI(t, append(args, "--yes")...)
	if code == ExitOK || !strings.Contains(stdout, `"outcome": "unknown"`) {
		t.Fatalf("wrong identity exit=%d %s", code, stdout)
	}
}

func TestConanDeletionNeverTurnsIncompleteAcknowledgementsIntoSuccess(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		body       string
		readStatus int
		readBody   string
		unknown    bool
	}{
		{"wrong 2xx", 200, `{}`, 200, `{"items":[]}`, true},
		{"read forbidden", 204, "", 403, `{"code":"access_denied","message":"read denied"}`, true},
		{"incomplete read", 204, "", 200, `{}`, true},
		{"refused", 403, `{"code":"access_denied","message":"write denied"}`, 200, `{"items":[]}`, false},
		{"missing", 404, `{"code":"not_found","message":"revision missing"}`, 200, `{"items":[]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := newRouteAPI(t, "")
			path := repoBase + "/conan/package-revisions"
			api.on("DELETE", path+"/p1", tc.status, tc.body)
			api.on("GET", path, tc.readStatus, tc.readBody)
			code, stdout, _ := runCLI(t, append(conanPackageArgs("package-revision delete"), "--revision", "p1", "--yes", "--json")...)
			if code == ExitOK || strings.Contains(stdout, `"outcome": "unknown"`) != tc.unknown {
				t.Fatalf("exit=%d %s", code, stdout)
			}
			if tc.status != 204 && api.count("GET") != 0 {
				t.Fatalf("unacknowledged deletion must not probe: %+v", api.recorded())
			}
			if api.count("DELETE") != 1 {
				t.Fatalf("a failed read must not repeat the deletion: %+v", api.recorded())
			}
		})
	}
}

func TestConanCommandDiscoveryDeclaresActualCapabilities(t *testing.T) {
	for _, tc := range []struct {
		name    string
		paged   bool
		mutates bool
	}{
		{"conan references", true, false},
		{"conan recipe-revisions", true, false},
		{"conan package-ids", false, false},
		{"conan package-revisions", false, false},
		{"conan package-revision delete", false, true},
	} {
		cmd, exists := lookupCommand(tc.name)
		if !exists || cmd.meta.Stage != "m2" || cmd.meta.Pagination != tc.paged || cmd.meta.Mutating != tc.mutates || !cmd.meta.Retry || cmd.meta.Wait {
			t.Fatalf("%s meta=%+v exists=%v", tc.name, cmd.meta, exists)
		}
	}
	code, stdout, _ := runCLI(t, "commands", "--json")
	if code != ExitOK || !json.Valid([]byte(stdout)) || !strings.Contains(stdout, `"name": "conan package-revision delete"`) {
		t.Fatalf("discovery exit=%d %s", code, stdout)
	}
}

func TestConanReferencesKeepSelectedScope(t *testing.T) {
	api := newRouteAPI(t, "")
	api.on("GET", repoBase+"/conan/references", 200, `{"items":[]}`)
	code, stdout, _ := runCLI(t, "conan", "references", "--repo", testRepoID, "--q", "widget/", "--json")
	var result struct {
		Meta struct {
			Scope map[string]any `json:"scope"`
		} `json:"meta"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatal(err)
	}
	if code != ExitOK || result.Meta.Scope["repositoryId"] != testRepoID || result.Meta.Scope["q"] != "widget/" {
		t.Fatalf("selected repository and filter must survive in scope: exit=%d %s", code, stdout)
	}
}

func TestConanDeleteKeepsUnknownAfterUncertainAttemptThenRefusal(t *testing.T) {
	api := newRouteAPI(t, "")
	var attempts atomic.Int32
	api.onFunc("DELETE", repoBase+"/conan/package-revisions/p1", func(*http.Request) (int, string) {
		if attempts.Add(1) == 1 {
			return 503, `{"code":"internal_error","message":"possibly applied before upstream failure"}`
		}
		return 403, `{"code":"access_denied","message":"permission revoked before retry"}`
	})
	code, stdout, _ := runCLI(t, append(conanPackageArgs("package-revision delete"), "--revision", "p1", "--yes", "--json")...)
	if code == ExitOK || !strings.Contains(stdout, `"outcome": "unknown"`) || !strings.Contains(stdout, `"readBack":`) {
		t.Fatalf("later refusal cannot disprove the first deletion attempt: attempts=%d exit=%d %s", attempts.Load(), code, stdout)
	}
	if api.count("GET") != 0 {
		t.Fatal("must not probe a now-inaccessible scope")
	}
}
