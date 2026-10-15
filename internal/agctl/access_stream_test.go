package agctl

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const (
	grantsPath   = repoBase + "/grants"
	templateID   = "00000000-0000-4000-b000-000000000001"
	readerGrant  = `{"principal":"service-account:ci","scopes":["repositories:read"]}`
	adminGrant   = `{"principal":"user:alice","scopes":["repositories:admin"]}`
	prefixGrant  = `{"principal":"service-account:ci","scopes":["repositories:write"],"resourcePrefix":"releases/"}`
	templateBody = `{"id":"` + templateID + `","name":"ci-readers","version":"4","createdAt":"2026-10-15T08:00:00Z","updatedAt":"2026-10-15T08:00:00Z","grants":[{"principal":"service-account:ci","scopes":["repositories:read","repositories:write"]},{"principal":"group:auditors","scopes":["repositories:read"]}]}`
)

func TestGrantListShowsVersion(t *testing.T) {
	api := newRouteAPI(t, "")
	expectUsage(t, api, []string{"grant", "list"}, []string{"grant", "list", "--repo", testRepoID, "--all-repositories"})
	api.onETag("GET", grantsPath, 200, `[`+readerGrant+`,`+prefixGrant+`]`, "7")
	code, stdout, _ := runCLI(t, "grant", "list", "--repo", testRepoID, "--json")
	if code != ExitOK {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
	assertGolden(t, "grant_list_repo", stdout)
	api.on("GET", "/api/v2/repository-grants", 200, `[{"repositoryId":"`+testRepoID+`","repositoryName":"raw-hosted","format":"raw","principal":"service-account:ci","scopes":["repositories:read"]}]`)
	code, stdout, _ = runCLI(t, "grant", "list", "--all-repositories")
	if code != ExitOK || !strings.Contains(stdout, "raw-hosted") || !strings.Contains(stdout, "service-account:ci") {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
}

func TestGrantUpsertValidatesAndPlans(t *testing.T) {
	api := newRouteAPI(t, "")
	expectUsage(t, api,
		[]string{"grant", "upsert", "--principal", "user:alice", "--scope", "repositories:read"},
		[]string{"grant", "upsert", "--repo", testRepoID, "--scope", "repositories:read"},
		[]string{"grant", "upsert", "--repo", testRepoID, "--principal", "user:alice"},
		[]string{"grant", "upsert", "--repo", testRepoID, "--principal", "user:alice", "--scope", "repositories:root"},
		[]string{"grant", "upsert", "--repo", testRepoID, "--principal", "user:alice", "--scope", "repositories:read", "--scope", "repositories:read"},
		[]string{"grant", "upsert", "--repo", testRepoID, "--principal", "user:\nalice", "--scope", "repositories:read"},
	)
	api.onETag("GET", grantsPath, 200, `[`+readerGrant+`]`, "7")
	code, stdout, _ := runCLI(t, "grant", "upsert", "--repo", testRepoID, "--principal", "service-account:ci", "--scope", "repositories:read", "--scope", "repositories:write", "--dry-run", "--json")
	if code != ExitOK || api.count("POST") != 0 {
		t.Fatalf("dry run exit = %d, posts = %d, stdout = %s", code, api.count("POST"), stdout)
	}
	assertGolden(t, "grant_upsert_dry_run", stdout)
}

func TestGrantUpsertRetriesAndChecksTheWrittenRow(t *testing.T) {
	api := newRouteAPI(t, "")
	var attempts atomic.Int32
	api.onFunc("POST", grantsPath, func(*http.Request) (int, string) {
		if attempts.Add(1) == 1 {
			return 503, `{"code":"unavailable","message":"try again"}`
		}
		return 200, `[` + readerGrant + `,` + adminGrant + `]`
	})
	code, stdout, _ := runCLI(t, "grant", "upsert", "--repo", testRepoID, "--principal", "user:alice", "--scope", "repositories:admin", "--json")
	// The fake sends no ETag: a list without its version cannot confirm the
	// write, so the outcome is unknown even after the retry succeeded.
	var data unknownOutcome
	_ = json.Unmarshal(decodeEnvelope(t, stdout).Data, &data)
	if code == ExitOK || data.Outcome != "unknown" || attempts.Load() != 2 {
		t.Fatalf("exit = %d, attempts = %d, stdout = %s", code, attempts.Load(), stdout)
	}
	if bodies := api.recorded(); bodies[0].Body != bodies[1].Body {
		t.Fatalf("a retried upsert must resend the same row: %+v", bodies)
	}

	api.onETag("POST", grantsPath, 200, `[`+readerGrant+`,`+adminGrant+`]`, "8")
	code, stdout, _ = runCLI(t, "grant", "upsert", "--repo", testRepoID, "--principal", "user:alice", "--scope", "repositories:admin", "--json")
	if code != ExitOK || !strings.Contains(stdout, `"version": "8"`) {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
	// A list that does not contain the written row cannot confirm it.
	api.onETag("POST", grantsPath, 200, `[`+readerGrant+`]`, "9")
	code, stdout, _ = runCLI(t, "grant", "upsert", "--repo", testRepoID, "--principal", "user:alice", "--scope", "repositories:admin", "--json")
	_ = json.Unmarshal(decodeEnvelope(t, stdout).Data, &data)
	if code == ExitOK || data.Outcome != "unknown" {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
}

func TestGrantDeleteIsConfirmedAndNotRetried(t *testing.T) {
	api := newRouteAPI(t, "")
	expectUsage(t, api,
		[]string{"grant", "delete", "--repo", testRepoID, "--principal", "user:alice"},
		[]string{"grant", "delete", "--repo", testRepoID, "--yes"},
	)
	api.onETag("GET", grantsPath, 200, `[`+readerGrant+`]`, "7")
	code, stdout, _ := runCLI(t, "grant", "delete", "--repo", testRepoID, "--principal", "user:alice", "--dry-run", "--json")
	if code != ExitOK || !strings.Contains(stdout, "no such grant exists") {
		t.Fatalf("dry run exit = %d, stdout = %s", code, stdout)
	}
	api.on("DELETE", grantsPath, 503, `{"code":"unavailable","message":"try again"}`)
	code, stdout, _ = runCLI(t, "grant", "delete", "--repo", testRepoID, "--principal", "service-account:ci", "--resource-prefix", "releases/", "--yes", "--json")
	var data unknownOutcome
	_ = json.Unmarshal(decodeEnvelope(t, stdout).Data, &data)
	if code == ExitOK || data.Outcome != "unknown" || api.count("DELETE") != 1 {
		t.Fatalf("exit = %d, deletes = %d, stdout = %s", code, api.count("DELETE"), stdout)
	}
	for _, request := range api.recorded() {
		if request.Method == "DELETE" && request.Query != "principal=service-account%3Aci&resourcePrefix=releases%2F" {
			t.Fatalf("delete query = %s", request.Query)
		}
	}
	api.onETag("DELETE", grantsPath, 204, ``, "8")
	code, stdout, _ = runCLI(t, "grant", "delete", "--repo", testRepoID, "--principal", "service-account:ci", "--yes", "--json")
	if code != ExitOK || !strings.Contains(stdout, `"change": "deleted"`) || !strings.Contains(stdout, `"version": "8"`) {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
	api.on("DELETE", grantsPath, 404, `{"code":"not_found","message":"grant not found"}`)
	if code, _, _ := runCLI(t, "grant", "delete", "--repo", testRepoID, "--principal", "user:nobody", "--yes"); code != ExitNotFound {
		t.Fatalf("missing grant exit = %d", code)
	}
}

func TestTemplateApplyDiffsAndIsConditional(t *testing.T) {
	api := newRouteAPI(t, "")
	expectUsage(t, api,
		[]string{"template", "apply", templateID, "--repo", testRepoID},
		[]string{"template", "apply", templateID, "--yes"},
		[]string{"template", "apply", templateID, "--repo", testRepoID, "--yes", "--if-match", ""},
	)
	api.onETag("GET", grantsPath, 200, `[`+readerGrant+`,`+adminGrant+`]`, "7")
	api.on("GET", "/api/v2/authorization-templates/"+templateID, 200, templateBody)
	code, stdout, _ := runCLI(t, "template", "apply", templateID, "--repo", testRepoID, "--dry-run", "--json")
	if code != ExitOK || api.count("POST") != 0 {
		t.Fatalf("dry run exit = %d, stdout = %s", code, stdout)
	}
	assertGolden(t, "template_apply_dry_run", stdout)

	applyPath := "/api/v2/authorization-templates/" + templateID + "/apply"
	api.on("POST", applyPath, 412, `{"code":"version_conflict","message":"If-Match does not match current repository grant version"}`)
	code, stdout, _ = runCLI(t, "template", "apply", templateID, "--repo", testRepoID, "--yes", "--json")
	if code != ExitConflict || !strings.Contains(decodeEnvelope(t, stdout).Error.Hint, "were not replaced") || api.count("POST") != 1 {
		t.Fatalf("exit = %d, posts = %d, stdout = %s", code, api.count("POST"), stdout)
	}
	for _, request := range api.recorded() {
		if request.Method == "POST" && (request.IfMatch != "7" || request.Body != `{"repositoryId":"`+testRepoID+`"}`) {
			t.Fatalf("apply request = %+v", request)
		}
	}
	api.onETag("POST", applyPath, 200, `[{"principal":"service-account:ci","scopes":["repositories:read","repositories:write"]}]`, "8")
	code, stdout, _ = runCLI(t, "template", "apply", templateID, "--repo", testRepoID, "--yes", "--if-match", "7", "--json")
	if code != ExitOK || !strings.Contains(stdout, `"version": "8"`) {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
	api.onETag("POST", applyPath, 200, `{"oops":true}`, "9")
	code, stdout, _ = runCLI(t, "template", "apply", templateID, "--repo", testRepoID, "--yes", "--if-match", "8", "--json")
	var data unknownOutcome
	_ = json.Unmarshal(decodeEnvelope(t, stdout).Data, &data)
	if code == ExitOK || data.Outcome != "unknown" {
		t.Fatalf("a non-list body must be unknown: exit = %d, stdout = %s", code, stdout)
	}
}

func TestRolesAndTemplatesRead(t *testing.T) {
	api := newRouteAPI(t, "")
	api.on("GET", "/api/v2/authorization-roles", 200, `[{"id":"`+templateID+`","name":"reader","scopes":["repositories:read"],"version":"1","createdAt":"2026-10-15T08:00:00Z","updatedAt":"2026-10-15T08:00:00Z"}]`)
	api.on("GET", "/api/v2/authorization-templates", 200, `[`+templateBody+`]`)
	api.on("GET", "/api/v2/authorization-templates/"+templateID, 200, templateBody)
	for args, want := range map[string]string{
		"role list":                   "reader",
		"template list":               "ci-readers",
		"template show " + templateID: "group:auditors",
	} {
		code, stdout, _ := runCLI(t, strings.Fields(args)...)
		if code != ExitOK || !strings.Contains(stdout, want) {
			t.Fatalf("%s exit = %d, stdout = %s", args, code, stdout)
		}
	}
	if code, _, _ := runCLI(t, "role", "show", "missing"); code != ExitNotFound {
		t.Fatalf("missing role exit = %d", code)
	}
}

const effectiveAccess = `{"actor":"service-account:ci","identity":{"actor":"service-account:ci","kind":"service-account","administrator":false},"repository":{"id":"` + testRepoID + `","name":"raw-hosted","format":"raw","type":"hosted","state":"active"},"resource":"","simulated":false,"anonymousRead":{"allowed":false,"source":"policy","reason":"disabled"},"permissions":{"read":{"allowed":true,"source":"grant","reason":"granted"},"write":{"allowed":false,"source":"grant","reason":"scope_not_granted"},"admin":{"allowed":false,"source":"grant","reason":"scope_not_granted"},"intelligence":{"allowed":false,"source":"grant","reason":"scope_not_granted"}}}`

func TestAccessCheckRequire(t *testing.T) {
	api := newRouteAPI(t, "")
	expectUsage(t, api,
		[]string{"access", "check"},
		[]string{"access", "check", "--repo", testRepoID, "--role", "admin"},
		[]string{"access", "check", "--repo", testRepoID, "--require", "delete"},
	)
	api.on("GET", repoBase+"/effective-access", 200, effectiveAccess)
	code, stdout, _ := runCLI(t, "access", "check", "--repo", testRepoID, "--require", "read")
	if code != ExitOK || !strings.Contains(stdout, "scope_not_granted") {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
	code, stdout, _ = runCLI(t, "access", "check", "--repo", testRepoID, "--actor", "service-account:ci", "--resource", "releases/app.txt", "--require", "read", "--require", "write", "--json")
	if code != ExitForbidden {
		t.Fatalf("a denied requirement exits 6: exit = %d, stdout = %s", code, stdout)
	}
	assertGolden(t, "access_check_denied", stdout)
	if query := api.recorded()[len(api.recorded())-1].Query; query != "actor=service-account%3Aci&resource=releases%2Fapp.txt" {
		t.Fatalf("query = %s", query)
	}
}

func TestAnonymousPolicy(t *testing.T) {
	api := newRouteAPI(t, "")
	expectUsage(t, api,
		[]string{"access", "anonymous", "set"},
		[]string{"access", "anonymous", "set", "--enabled=true"},
	)
	api.on("GET", "/api/v2/anonymous-access-policy", 200, `{"version":"3","enabled":false}`)
	api.on("PUT", "/api/v2/anonymous-access-policy", 200, `{"version":"4","enabled":true}`)
	code, stdout, _ := runCLI(t, "access", "anonymous", "set", "--enabled=true", "--dry-run", "--json")
	if code != ExitOK || api.count("PUT") != 0 || !strings.Contains(stdout, `"If-Match": "3"`) {
		t.Fatalf("dry run exit = %d, stdout = %s", code, stdout)
	}
	code, stdout, _ = runCLI(t, "access", "anonymous", "set", "--enabled=true", "--yes")
	if code != ExitOK || !strings.Contains(stdout, "enabled (version 4)") {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
	for _, request := range api.recorded() {
		if request.Method == "PUT" && (request.IfMatch != "3" || request.Body != `{"enabled":true,"version":"3"}`) {
			t.Fatalf("put = %+v", request)
		}
	}
	// Disabling narrows access and needs no confirmation; a mismatched answer
	// is an unknown outcome.
	code, stdout, _ = runCLI(t, "access", "anonymous", "set", "--enabled=false", "--if-match", "4", "--json")
	var data unknownOutcome
	_ = json.Unmarshal(decodeEnvelope(t, stdout).Data, &data)
	if code == ExitOK || data.Outcome != "unknown" {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
}

// ndjson decodes every line of a stream and fails unless each is one JSON
// object with schemaVersion 1 and the last one is the single end event.
func ndjson(t *testing.T, output string) []map[string]any {
	t.Helper()
	var events []map[string]any
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		var event map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatalf("line is not one JSON document: %q (%v)", scanner.Text(), err)
		}
		if event["schemaVersion"] != float64(1) {
			t.Fatalf("event without schemaVersion 1: %v", event)
		}
		events = append(events, event)
	}
	ends := 0
	for _, event := range events {
		if event["type"] == "end" {
			ends++
		}
	}
	if len(events) == 0 || ends != 1 || events[len(events)-1]["type"] != "end" {
		t.Fatalf("a stream ends with exactly one end event: %v", events)
	}
	return events
}

func logEntry(sequence int) string {
	return fmt.Sprintf(`{"sequence":%d,"time":"2026-10-15T08:00:%02dZ","level":"INFO","message":"m%d","instanceId":"i-1","sessionId":"s-1","component":"http","operation":"request","requestId":"r-%d","traceId":"t"}`, sequence, sequence, sequence, sequence)
}

func TestRuntimeLogsPage(t *testing.T) {
	api := newRouteAPI(t, "")
	expectUsage(t, api,
		[]string{"runtime", "logs", "--limit", "101"},
		[]string{"runtime", "logs", "--window", "15m", "--from", "2026-10-15T00:00:00Z"},
		[]string{"runtime", "logs", "--duration", "1m"},
		[]string{"runtime", "logs", "--follow"},
		[]string{"runtime", "logs", "--follow", "--max-events", "5", "--to", "2026-10-15T00:00:00Z"},
		[]string{"runtime", "logs", "--follow", "--max-events", "5", "--before", "9"},
		[]string{"runtime", "logs", "--follow", "--max-events", "5", "--interval", "10ms"},
	)
	api.on("GET", "/api/v2/runtime/logs", 200, `{"scope":"local","instanceId":"i-1","sessionId":"s-1","items":[`+logEntry(3)+`,`+logEntry(2)+`],"nextSequence":2,"afterCursor":"c-3","order":"descending","hasMore":true,"retention":{"earliestSequence":1,"latestSequence":3,"gap":false}}`)
	code, stdout, stderr := runCLI(t, "runtime", "logs", "--level", "WARN", "--limit", "2")
	if code != ExitOK || strings.Index(stdout, "m2") > strings.Index(stdout, "m3") || !strings.Contains(stderr, "--before 2") || !strings.Contains(stdout, "lost on restart") {
		t.Fatalf("exit = %d, stdout = %s, stderr = %s", code, stdout, stderr)
	}
	code, stdout, _ = runCLI(t, "runtime", "logs", "--limit", "2", "--json")
	if code != ExitOK || !strings.Contains(stdout, `"nextCursor": "2"`) || !strings.Contains(stdout, `"sessionId": "s-1"`) {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
}

func TestRuntimeLogsFollowStreamsNDJSON(t *testing.T) {
	api := newRouteAPI(t, "")
	api.onFunc("GET", "/api/v2/runtime/logs", func(r *http.Request) (int, string) {
		switch r.URL.Query().Get("afterCursor") {
		case "":
			return 200, `{"scope":"local","instanceId":"i-1","sessionId":"s-1","items":[` + logEntry(2) + `,` + logEntry(1) + `],"afterCursor":"c-2","order":"descending","hasMore":false}`
		case "c-2":
			return 200, `{"scope":"local","instanceId":"i-1","sessionId":"s-1","items":[` + logEntry(3) + `],"afterCursor":"c-3","order":"ascending","hasMore":true,"retention":{"earliestSequence":3,"latestSequence":9,"gap":true}}`
		default:
			return 200, `{"scope":"local","instanceId":"i-1","sessionId":"s-1","items":[` + logEntry(4) + `,` + logEntry(5) + `],"afterCursor":"c-5","order":"ascending","hasMore":false}`
		}
	})
	code, stdout, _ := runCLI(t, "runtime", "logs", "--follow", "--max-events", "4", "--interval", "1s", "--keyword", "x", "--json")
	if code != ExitOK {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
	events := ndjson(t, stdout)
	var kinds, sequences []string
	for _, event := range events {
		kinds = append(kinds, event["type"].(string))
		if event["type"] == "entry" {
			sequences = append(sequences, fmt.Sprint(event["data"].(map[string]any)["sequence"]))
		}
	}
	if strings.Join(sequences, ",") != "1,2,3,4" || strings.Join(kinds, ",") != "entry,entry,gap,entry,entry,end" {
		t.Fatalf("kinds = %v, sequences = %v", kinds, sequences)
	}
	end := events[len(events)-1]["data"].(map[string]any)
	if end["reason"] != "max_events" || end["events"] != float64(4) || end["resume"].(map[string]any)["afterCursor"] != "c-5" {
		t.Fatalf("end = %v", end)
	}
	for _, request := range api.recorded()[1:] {
		if !strings.Contains(request.Query, "keyword=x") {
			t.Fatalf("every poll must repeat the cursor's filters: %s", request.Query)
		}
	}
}

func TestRuntimeLogsFollowEndsOnScopeChange(t *testing.T) {
	api := newRouteAPI(t, "")
	api.onFunc("GET", "/api/v2/runtime/logs", func(r *http.Request) (int, string) {
		if r.URL.Query().Get("afterCursor") == "" {
			return 200, `{"scope":"local","instanceId":"i-1","sessionId":"s-1","items":[` + logEntry(1) + `],"afterCursor":"c-1","hasMore":true}`
		}
		return 409, `{"code":"log_cursor_scope_changed","message":"runtime log instance or session changed; reset the query"}`
	})
	code, stdout, stderr := runCLI(t, "runtime", "logs", "--follow", "--duration", "30s", "--json")
	events := ndjson(t, stdout)
	end := events[len(events)-1]
	if code != ExitConflict || end["data"].(map[string]any)["reason"] != "scope_changed" || end["error"].(map[string]any)["code"] != "log_cursor_scope_changed" || stderr != "" {
		t.Fatalf("exit = %d, stdout = %s, stderr = %s", code, stdout, stderr)
	}
}

func TestRuntimeLogsFollowDurationEndsCleanly(t *testing.T) {
	api := newRouteAPI(t, "")
	api.on("GET", "/api/v2/runtime/logs", 200, `{"scope":"local","instanceId":"i-1","sessionId":"s-1","items":[],"afterCursor":"c-0","hasMore":false}`)
	start := time.Now()
	code, stdout, stderr := runCLI(t, "runtime", "logs", "--follow", "--duration", "1500ms")
	if code != ExitOK || time.Since(start) > 5*time.Second || !strings.Contains(stderr, "stream ended (duration) after 0 entries") || stdout != "" {
		t.Fatalf("exit = %d, stdout = %q, stderr = %s", code, stdout, stderr)
	}
}

// auditRecord is a record that occurred `offset` after base.
func auditRecord(base time.Time, offset time.Duration, actor string) string {
	return fmt.Sprintf(`{"outcome":"access_denied","occurredAt":%q,"actor":%q,"operation":"read","format":"management","requestId":"req-%s"}`, base.Add(offset).Format(time.RFC3339Nano), actor, actor)
}

func TestAuditFollowDeliversNewRecordsOnce(t *testing.T) {
	api := newRouteAPI(t, "")
	expectUsage(t, api,
		[]string{"audit", "list", "--follow"},
		[]string{"audit", "list", "--follow", "--max-events", "1", "--bounded"},
		[]string{"audit", "list", "--follow", "--max-events", "1", "--csv"},
		[]string{"audit", "list", "--follow", "--max-events", "1", "--to", "2026-10-15T00:00:00Z"},
	)
	var polls atomic.Int32
	base := time.Now().UTC()
	old, a, b, c := auditRecord(base, -30*time.Second, "old"), auditRecord(base, time.Second, "a"), auditRecord(base, 2*time.Second, "b"), auditRecord(base, 3*time.Second, "c")
	// The fake ignores from; the stream must still skip what it already saw.
	api.onFunc("GET", "/api/v2/audits/page", func(r *http.Request) (int, string) {
		switch polls.Add(1) {
		case 1:
			// Existing records are the baseline and are not replayed.
			return 200, `{"items":[` + old + `]}`
		case 2:
			return 200, `{"items":[` + b + `,` + a + `,` + old + `]}`
		default:
			return 200, `{"items":[` + c + `,` + b + `]}`
		}
	})
	code, stdout, _ := runCLI(t, "audit", "list", "--follow", "--max-events", "3", "--interval", "1s", "--actor", "svc", "--json")
	if code != ExitOK {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
	var actors []string
	for _, event := range ndjson(t, stdout) {
		if event["type"] == "entry" {
			actors = append(actors, event["data"].(map[string]any)["actor"].(string))
		}
	}
	if strings.Join(actors, ",") != "a,b,c" {
		t.Fatalf("actors = %v", actors)
	}
	for _, request := range api.recorded() {
		if !strings.Contains(request.Query, "actor=svc") || !strings.Contains(request.Query, "from=") {
			t.Fatalf("poll query = %s", request.Query)
		}
	}
}

func TestAuditFollowFromReplaysFirst(t *testing.T) {
	api := newRouteAPI(t, "")
	base := time.Date(2026, 10, 15, 8, 0, 0, 0, time.UTC)
	api.on("GET", "/api/v2/audits/page", 200, `{"items":[`+auditRecord(base, 2*time.Second, "b")+`,`+auditRecord(base, time.Second, "a")+`]}`)
	code, stdout, _ := runCLI(t, "audit", "list", "--follow", "--from", "2026-10-15T08:00:00Z", "--max-events", "2", "--json")
	var actors []string
	for _, event := range ndjson(t, stdout) {
		if event["type"] == "entry" {
			actors = append(actors, event["data"].(map[string]any)["actor"].(string))
		}
	}
	if code != ExitOK || strings.Join(actors, ",") != "a,b" {
		t.Fatalf("--from replays oldest first: exit = %d, actors = %v", code, actors)
	}
	if query := api.recorded()[0].Query; !strings.Contains(query, "from=2026-10-15T08%3A00%3A00Z") {
		t.Fatalf("query = %s", query)
	}
}

func TestStreamCommandsAreRegisteredHonestly(t *testing.T) {
	for _, name := range []string{"runtime logs", "audit list"} {
		cmd, _ := lookupCommand(name)
		if !cmd.meta.Follow || cmd.meta.Wait || cmd.meta.Mutating {
			t.Fatalf("%s meta = %+v", name, cmd.meta)
		}
	}
	for name, wantRetry := range map[string]bool{"grant upsert": true, "grant delete": false, "template apply": false, "access anonymous set": false} {
		cmd, _ := lookupCommand(name)
		if !cmd.meta.Mutating || cmd.meta.DryRun != "client-plan" || cmd.meta.Retry != wantRetry {
			t.Fatalf("%s meta = %+v", name, cmd.meta)
		}
	}
}
