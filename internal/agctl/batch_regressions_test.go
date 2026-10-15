package agctl

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

func TestRepoConnectPyPINetrcMatchesHostnameWithExplicitPort(t *testing.T) {
	api := newRouteAPI(t, "/gw")
	api.on("GET", repoBase, 200, `{"id":"`+testRepoID+`","name":"python-private","format":"pypi","type":"hosted","state":"active","version":"1"}`)
	code, stdout, _ := runCLI(t, "repo", "connect", testRepoID, "--json")
	if code != ExitOK {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
	var guide connectGuide
	if err := json.Unmarshal(decodeEnvelope(t, stdout).Data, &guide); err != nil {
		t.Fatal(err)
	}
	server, err := url.Parse(api.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	for _, snippet := range guide.Snippets {
		if snippet.File == ".netrc" {
			want := "machine " + server.Hostname() + "\nlogin ci\npassword ${ARTIFACT_GATEWAY_TOKEN}"
			if snippet.Content != want {
				t.Fatalf("pip must match the URL hostname without its port: got %q, want %q", snippet.Content, want)
			}
			if !strings.Contains(guide.Snippets[0].Content, api.server.URL+"/gw/pypi/python-private/simple/") {
				t.Fatalf("the index URL must retain its port and prefix: %+v", guide.Snippets)
			}
			return
		}
	}
	t.Fatal("the PyPI guide must include .netrc credentials")
}

func TestStatusScannerUnhealthyFailsWithoutStrict(t *testing.T) {
	for _, state := range []string{"unhealthy", "unreachable"} {
		t.Run(state, func(t *testing.T) {
			api := statusAPI(t)
			scanner := `"scanner":{"name":"trivy","formats":["oci"],"status":"` + state + `","detail":"scanner failed","checkedAt":"2026-10-15T08:00:00Z","databaseFreshness":"fresh","databaseMaxAgeSeconds":86400},`
			api.on("GET", "/api/v2/diagnostics", 200, strings.Replace(healthyDiagnostics, `"queues":[]`, scanner+`"queues":[]`, 1))
			code, stdout, _ := runCLI(t, "status", "--json")
			envelope := decodeEnvelope(t, stdout)
			if code != ExitFailure || envelope.Error == nil || envelope.Error.Code != "unhealthy" {
				t.Fatalf("scanner %s must fail ordinary status: exit = %d, stdout = %s", state, code, stdout)
			}
			var report statusReport
			if err := json.Unmarshal(envelope.Data, &report); err != nil {
				t.Fatal(err)
			}
			if report.Overall != "unhealthy" {
				t.Fatalf("overall = %q", report.Overall)
			}
			for _, check := range report.Checks {
				if check.Name == "scanner" && check.Status != "unhealthy" {
					t.Fatalf("scanner check = %+v", check)
				}
			}
		})
	}
}

func TestRepoConnectGoNetrcRetainsExplicitPort(t *testing.T) {
	api := newRouteAPI(t, "/gw")
	api.on("GET", repoBase, 200, `{"id":"`+testRepoID+`","name":"go-private","format":"go","type":"proxy","state":"active","version":"1"}`)
	code, stdout, _ := runCLI(t, "repo", "connect", testRepoID, "--json")
	if code != ExitOK {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
	var guide connectGuide
	if err := json.Unmarshal(decodeEnvelope(t, stdout).Data, &guide); err != nil {
		t.Fatal(err)
	}
	server, err := url.Parse(api.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	for _, snippet := range guide.Snippets {
		if snippet.File == ".netrc" && strings.HasPrefix(snippet.Content, "machine "+server.Host+"\n") {
			return
		}
	}
	t.Fatalf("Go's netrc URL prefix must retain its port: %+v", guide.Snippets)
}

func TestLifecycleJobListReportsItsRealWindow(t *testing.T) {
	full := make([]string, 100)
	for i := range full {
		full[i] = strings.Replace(lifecycleJob, "job-1", fmt.Sprintf("job-%d", i), 1)
	}
	for _, tc := range []struct {
		name, path, body string
		args             []string
		limit            int
		complete         bool
	}{
		{"filtered global empty", "/api/v2/lifecycle-jobs", "[]", []string{"--limit", "10"}, 10, false},
		{"filtered global short", "/api/v2/lifecycle-jobs", `[{"repositoryId":"` + testRepoID + `","repositoryName":"raw-hosted","job":` + lifecycleJob + `}]`, []string{"--limit", "10"}, 10, false},
		{"repository full", repoBase + "/lifecycle-jobs", "[" + strings.Join(full, ",") + "]", []string{"--repo", testRepoID}, 100, false},
		{"repository short", repoBase + "/lifecycle-jobs", "[" + lifecycleJob + "]", []string{"--repo", testRepoID}, 100, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := newRouteAPI(t, "")
			api.on("GET", tc.path, 200, tc.body)
			args := append([]string{"lifecycle-job", "list"}, tc.args...)
			code, stdout, _ := runCLI(t, append(args, "--json")...)
			var meta envelopeMeta
			if err := json.Unmarshal(decodeEnvelope(t, stdout).Meta, &meta); err != nil {
				t.Fatal(err)
			}
			if code != ExitOK || meta.Pagination == nil || meta.Pagination.Mode != "bounded" || meta.Pagination.Limit != tc.limit {
				t.Fatalf("the server window must remain bounded: exit = %d, stdout = %s", code, stdout)
			}
			if tc.complete {
				if meta.Pagination.Complete == nil || !*meta.Pagination.Complete {
					t.Fatalf("the short repository window is complete: stdout = %s", stdout)
				}
			} else if meta.Pagination.Complete != nil {
				t.Fatalf("a filtered global or full repository window has unknown completeness: stdout = %s", stdout)
			}
		})
	}
}

func TestDiagnosticsHumanOutputPreservesMountObservations(t *testing.T) {
	api := statusAPI(t)
	capacity := `"localCapacity":{"checkedAt":"2026-10-15T08:00:00Z","source":"statfs","scope":"observer_mount_namespace","unit":"bytes","refreshIntervalSeconds":15,"maxSampleAgeSeconds":60,"timeoutMilliseconds":200,"mounts":[{"alias":"temporary","status":"available","sampleAt":"2026-10-15T07:59:59Z","totalBytes":4096,"availableBytes":0,"sharedWith":[]},{"alias":"logs","status":"unknown","reason":"read_failed","sharedWith":[]},{"alias":"backups","status":"stale","sampleAt":"2026-10-15T07:59:00Z","sharedWith":["temporary"]}]},`
	api.on("GET", "/api/v2/diagnostics", 200, strings.Replace(healthyDiagnostics, `"queues":[]`, capacity+`"queues":[]`, 1))
	code, stdout, _ := runCLI(t, "diagnostics")
	if code != ExitOK {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
	for _, want := range []string{"observer_mount_namespace", "bytes", "temporary:", "available, total 4096 bytes, available 0 bytes", "logs:", "unknown, reason read_failed", "backups:", "stale", "2026-10-15T07:59:00Z", "shared with temporary"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("diagnostics must retain %q: stdout = %s", want, stdout)
		}
	}
}
