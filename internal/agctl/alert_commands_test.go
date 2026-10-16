package agctl

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const (
	ruleID     = "00000000-0000-4000-f000-000000000001"
	targetID   = "00000000-0000-4000-f000-0000000000e1"
	deliveryID = "00000000-0000-4000-f000-0000000000d1"
	recipient  = "synthetic-recipient-marker@example.test"
)

const policyJSON = `{"warningBasisPoints":8500,"criticalBasisPoints":9500,"recoveryBelowBasisPoints":8000,"warningForSeconds":600,"criticalForSeconds":300,"recoveryForSeconds":600,"maxSampleAgeSeconds":600}`

func ruleJSON(enabled bool, version string) string {
	state := `{"severity":"normal","phase":"normal","dataState":"available"}`
	return `{"id":"` + ruleID + `","repositoryId":"` + testRepoID + `","targetId":"` + targetID + `","targetVersion":"1","enabled":` + map[bool]string{true: "true", false: "false"}[enabled] +
		`,"deleted":false,"version":"` + version + `","stateVersion":"1","policy":` + policyJSON + `,"state":` + state + `,"sequence":0,"createdAt":"2026-10-15T08:00:00Z","updatedAt":"2026-10-15T08:00:00Z"}`
}

func targetJSON(name string, enabled bool, version string) string {
	return `{"id":"` + targetID + `","name":"` + name + `","locale":"en","enabled":` + map[bool]string{true: "true", false: "false"}[enabled] + `,"recipientConfigured":true,"version":"` + version + `","createdAt":"2026-10-15T08:00:00Z","updatedAt":"2026-10-15T08:00:00Z"}`
}

func deliveryJSON(state, version string) string {
	return `{"id":"` + deliveryID + `","eventId":"` + ruleID + `","targetId":"` + targetID + `","targetVersion":"1","scenario":"warning","locale":"en","templateVersion":"t1","state":"` + state + `","attempts":1,"possibleDuplicate":false,"version":"` + version + `","createdAt":"2026-10-15T08:00:00Z","updatedAt":"2026-10-15T08:00:00Z","kind":"test"}`
}

var policyArgs = []string{"--warning-bp", "8500", "--critical-bp", "9500", "--recovery-below-bp", "8000", "--warning-for", "10m", "--critical-for", "5m", "--recovery-for", "10m", "--max-sample-age", "10m"}

func TestQuotaAlertCreateNeedsAnExplicitPolicy(t *testing.T) {
	api := newRouteAPI(t, "")
	base := []string{"quota-alert", "create", "--repo", testRepoID, "--target", targetID}
	expectUsage(t, api,
		append(append([]string{}, base...), policyArgs[:12]...),
		append(append([]string{}, base...), "--warning-bp", "9600", "--critical-bp", "9500", "--recovery-below-bp", "8000", "--warning-for", "1m", "--critical-for", "1m", "--recovery-for", "1m", "--max-sample-age", "1m"),
		append(append([]string{}, base...), append(append([]string{}, policyArgs...), "--max-sample-age", "10s")...),
		append(append(append([]string{}, base...), policyArgs...), "--enabled"),
		append([]string{"quota-alert", "create", "--repo", "npm", "--target", targetID}, policyArgs...),
	)
	api.onETag("POST", "/api/v2/repository-quota-alert-rules", 201, ruleJSON(false, "1"), "1")
	code, stdout, _ := runCLI(t, append(append(append([]string{}, base...), policyArgs...), "--json")...)
	if code != ExitOK || !strings.Contains(stdout, `"enabled": false`) {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
	if body := api.recorded()[0].Body; !strings.Contains(body, `"enabled":false`) || !strings.Contains(body, `"warningForSeconds":600`) {
		t.Fatalf("a new rule sends every value, disabled explicitly: %s", body)
	}
	api.on("POST", "/api/v2/repository-quota-alert-rules", 503, `{"code":"email_disabled","message":"email channel is unavailable"}`)
	code, stdout, _ = runCLI(t, append(append(append([]string{}, base...), policyArgs...), "--enabled", "--yes", "--json")...)
	envelope := decodeEnvelope(t, stdout)
	if code != ExitFailure || envelope.Error.Code != "email_disabled" || strings.Contains(string(envelope.Data), "unknown") {
		t.Fatalf("a closed channel is a definite refusal: exit = %d, stdout = %s", code, stdout)
	}
}

func TestQuotaAlertUpdateKeepsUnchangedFields(t *testing.T) {
	api := newRouteAPI(t, "")
	path := "/api/v2/repository-quota-alert-rules/" + ruleID
	expectUsage(t, api, []string{"quota-alert", "update", ruleID}, []string{"quota-alert", "update", ruleID, "--enabled"})
	api.onETag("GET", path, 200, ruleJSON(true, "4"), "4")
	api.onETag("PUT", path, 200, strings.Replace(ruleJSON(true, "5"), `"warningBasisPoints":8500`, `"warningBasisPoints":8700`, 1), "5")
	code, stdout, _ := runCLI(t, "quota-alert", "update", ruleID, "--warning-bp", "8700", "--json")
	if code != ExitOK {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
	put := api.recorded()[1]
	if put.IfMatch != "4" || !strings.Contains(put.Body, `"enabled":true`) || !strings.Contains(put.Body, `"warningBasisPoints":8700`) || !strings.Contains(put.Body, `"criticalBasisPoints":9500`) {
		t.Fatalf("an update must send the current enabled state and policy: %+v", put)
	}
	api.on("PUT", path, 412, `{"code":"version_conflict","message":"stale"}`)
	code, _, _ = runCLI(t, "quota-alert", "update", ruleID, "--enabled=false", "--if-match", "3")
	if code != ExitConflict || api.count("PUT") != 2 {
		t.Fatalf("exit = %d, puts = %d", code, api.count("PUT"))
	}
	api.onETag("PUT", path, 200, ruleJSON(true, "6"), "6")
	code, stdout, _ = runCLI(t, "quota-alert", "update", ruleID, "--enabled=false", "--json")
	var data unknownOutcome
	_ = json.Unmarshal(decodeEnvelope(t, stdout).Data, &data)
	if code == ExitOK || data.Outcome != "unknown" {
		t.Fatalf("an answer with another enabled state is not confirmed: exit = %d, stdout = %s", code, stdout)
	}
	api.onETag("DELETE", path, 204, "", "7")
	if code, _, _ = runCLI(t, "quota-alert", "delete", ruleID, "--if-match", "6"); code != ExitUsage || api.count("DELETE") != 0 {
		t.Fatalf("a delete needs --yes: exit = %d", code)
	}
	code, _, _ = runCLI(t, "quota-alert", "delete", ruleID, "--yes", "--if-match", "6")
	if code != ExitOK {
		t.Fatalf("delete exit = %d", code)
	}
}

func TestEmailTargetRecipientStaysPrivate(t *testing.T) {
	api := newRouteAPI(t, "")
	dir := t.TempDir()
	public := filepath.Join(dir, "public")
	private := filepath.Join(dir, "private")
	_ = os.WriteFile(public, []byte(recipient+"\n"), 0o644)
	_ = os.WriteFile(private, []byte(recipient+"\n"), 0o600)
	expectUsage(t, api,
		[]string{"email", "target", "create", "ops", "--locale", "en"},
		[]string{"email", "target", "create", "ops", "--locale", "fr", "--recipient-file", private},
		[]string{"email", "target", "create", "ops", "--locale", "en", "--recipient-file", public},
	)
	code, stdout, stderr := runCLIStdin(t, recipient+"\n", "email", "target", "create", "ops", "--locale", "en", "--recipient-stdin", "--dry-run")
	noSecret(t, recipient, stdout, stderr)
	if code != ExitOK || len(api.recorded()) != 0 {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
	api.onETag("POST", "/api/v2/email-targets", 201, targetJSON("ops", false, "1"), "1")
	code, stdout, stderr = runCLI(t, "email", "target", "create", "ops", "--locale", "en", "--recipient-file", private, "--verbose", "--json")
	noSecret(t, recipient, stdout, stderr)
	if code != ExitOK || !strings.Contains(api.recorded()[0].Body, `"recipient":"`+recipient+`"`) || !strings.Contains(api.recorded()[0].Body, `"enabled":false`) {
		t.Fatalf("exit = %d, body = %s", code, api.recorded()[0].Body)
	}
	api.on("POST", "/api/v2/email-targets", 400, `{"code":"invalid_request","message":"`+recipient+` is invalid"}`)
	_, stdout, stderr = runCLI(t, "email", "target", "create", "ops", "--locale", "en", "--recipient-file", private, "--json")
	noSecret(t, recipient, stdout, stderr)
}

func TestEmailTargetUpdateKeepsEnabled(t *testing.T) {
	api := newRouteAPI(t, "")
	path := "/api/v2/email-targets/" + targetID
	api.onETag("GET", path, 200, targetJSON("ops", true, "3"), "3")
	api.onETag("PUT", path, 200, targetJSON("ops-team", true, "4"), "4")
	code, stdout, _ := runCLI(t, "email", "target", "update", targetID, "--name", "ops-team", "--json")
	if code != ExitOK {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
	put := api.recorded()[1]
	if put.IfMatch != "3" || put.Body != `{"enabled":true,"locale":"en","name":"ops-team"}` {
		t.Fatalf("a rename must keep the target enabled and its recipient untouched: %+v", put)
	}
}

func TestEmailPreviewWritesNewFiles(t *testing.T) {
	api := newRouteAPI(t, "")
	dir := t.TempDir()
	existing := filepath.Join(dir, "existing.html")
	_ = os.WriteFile(existing, []byte("keep"), 0o644)
	expectUsage(t, api,
		[]string{"email", "preview", "--scenario", "outage", "--locale", "en"},
		[]string{"email", "preview", "--scenario", "warning", "--locale", "en", "--html-output", existing},
	)
	api.on("POST", "/api/v2/email-notifications:preview", 200, `{"subject":"Quota warning","html":"<p>warning</p>","text":"warning","templateVersion":"t1"}`)
	html, text := filepath.Join(dir, "p.html"), filepath.Join(dir, "p.txt")
	code, stdout, _ := runCLI(t, "email", "preview", "--scenario", "warning", "--locale", "en", "--html-output", html, "--text-output", text)
	gotHTML, _ := os.ReadFile(html)
	gotText, _ := os.ReadFile(text)
	if code != ExitOK || string(gotHTML) != "<p>warning</p>" || string(gotText) != "warning" || !strings.Contains(stdout, "Quota warning") {
		t.Fatalf("exit = %d, html = %q, text = %q", code, gotHTML, gotText)
	}
	same := filepath.Join(dir, "same.out")
	posts := api.count("POST")
	if code, _, _ = runCLI(t, "email", "preview", "--scenario", "warning", "--locale", "en", "--html-output", same, "--text-output", filepath.Join(dir, ".", "same.out")); code != ExitUsage || api.count("POST") != posts {
		t.Fatalf("one path for both parts is a usage error before the request: exit = %d", code)
	}
	if _, err := os.Stat(same); !os.IsNotExist(err) {
		t.Fatalf("a rejected preview writes nothing: %v", err)
	}
}

func TestEmailTestIsConfirmedConditionalAndKeyed(t *testing.T) {
	api := newRouteAPI(t, "")
	expectUsage(t, api,
		[]string{"email", "test", "--target", targetID, "--scenario", "warning"},
		[]string{"email", "test", "--target", targetID, "--scenario", "outage", "--yes"},
		[]string{"email", "test", "--target", targetID, "--scenario", "warning", "--yes", "--idempotency-key", "not-a-uuid"},
	)
	api.onETag("GET", "/api/v2/email-targets/"+targetID, 200, targetJSON("ops", true, "7"), "7")
	var attempts atomic.Int32
	api.onFunc("POST", "/api/v2/email-notifications:test", func(*http.Request) (int, string) {
		if attempts.Add(1) == 1 {
			return 502, `{"code":"bad_gateway","message":"upstream"}`
		}
		return 202, deliveryJSON("pending", "1")
	})
	code, stdout, _ := runCLI(t, "email", "test", "--target", targetID, "--scenario", "warning", "--yes", "--json")
	if code != ExitOK || attempts.Load() != 2 || !strings.Contains(stdout, `"idempotencyKey"`) {
		t.Fatalf("exit = %d, attempts = %d, stdout = %s", code, attempts.Load(), stdout)
	}
	posts := []recordedRequest{}
	for _, request := range api.recorded() {
		if request.Method == "POST" {
			posts = append(posts, request)
		}
	}
	if len(posts) != 2 || posts[0].IfMatch != "7" || posts[0].Body != posts[1].Body {
		t.Fatalf("a retried test resends the same request for the target version: %+v", posts)
	}
	// An unknown outcome names the version it was sent with, and a retry that
	// repeats it is sent against that version without reading a newer one.
	api.on("POST", "/api/v2/email-notifications:test", 502, `{"code":"bad_gateway","message":"upstream"}`)
	code, stdout, _ = runCLI(t, "email", "test", "--target", targetID, "--scenario", "warning", "--yes", "--json")
	if envelope := decodeEnvelope(t, stdout); code != ExitFailure || !strings.Contains(stdout, `"outcome": "unknown"`) || !strings.Contains(envelope.Error.Hint, "--if-match 7") {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
	api.onETag("GET", "/api/v2/email-targets/"+targetID, 200, targetJSON("ops", true, "8"), "8")
	api.on("POST", "/api/v2/email-notifications:test", 202, deliveryJSON("pending", "1"))
	gets := api.count("GET")
	code, _, _ = runCLI(t, "email", "test", "--target", targetID, "--scenario", "warning", "--yes", "--idempotency-key", "8f14e45f-ceea-467a-9575-6a3b1f2c9d10", "--if-match", "7")
	last := api.recorded()[len(api.recorded())-1]
	if code != ExitOK || api.count("GET") != gets || last.IfMatch != "7" {
		t.Fatalf("a retry keeps the first version: exit = %d, gets %d -> %d, if-match = %q", code, gets, api.count("GET"), last.IfMatch)
	}
	if code, _, _ = runCLI(t, "email", "test", "--target", targetID, "--scenario", "warning", "--yes", "--if-match", " "); code != ExitUsage {
		t.Fatalf("an empty --if-match is a usage error: exit = %d", code)
	}
	api.on("POST", "/api/v2/email-notifications:test", 503, `{"code":"email_disabled","message":"email channel is unavailable"}`)
	code, stdout, _ = runCLI(t, "email", "test", "--target", targetID, "--scenario", "warning", "--yes", "--json")
	if code != ExitFailure || decodeEnvelope(t, stdout).Error.Code != "email_disabled" || strings.Contains(stdout, `"outcome"`) {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
}

func TestEmailDeliveryWaitAndReplay(t *testing.T) {
	previous := repoWaitInterval
	repoWaitInterval = 10 * time.Millisecond
	t.Cleanup(func() { repoWaitInterval = previous })
	api := newRouteAPI(t, "")
	path := "/api/v2/email-deliveries/" + deliveryID
	var reads atomic.Int32
	api.onFunc("GET", path, func(*http.Request) (int, string) {
		if reads.Add(1) < 3 {
			return 200, deliveryJSON("delivering", "2")
		}
		return 200, deliveryJSON("accepted", "3")
	})
	code, stdout, _ := runCLI(t, "email", "delivery", "wait", deliveryID, "--json")
	if code != ExitOK || !strings.Contains(stdout, `"state": "accepted"`) {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
	api.on("GET", path, 200, deliveryJSON("retrying", "2"))
	code, stdout, _ = runCLI(t, "email", "delivery", "wait", deliveryID, "--timeout", "100ms", "--json")
	envelope := decodeEnvelope(t, stdout)
	if code != ExitConnection || envelope.Error.Code != "wait_timeout" || !strings.Contains(envelope.Error.Hint, "no cancel") {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}

	if code, _, _ = runCLI(t, "email", "delivery", "replay", deliveryID); code != ExitUsage {
		t.Fatalf("a replay needs --yes: exit = %d", code)
	}
	api.onETag("GET", path, 200, deliveryJSON("dead", "4"), "4")
	api.on("POST", path+":replay", 409, `{"code":"invalid_state","message":"email delivery cannot be replayed"}`)
	code, _, _ = runCLI(t, "email", "delivery", "replay", deliveryID, "--yes")
	if code != ExitConflict || api.count("POST") != 1 {
		t.Fatalf("exit = %d, posts = %d", code, api.count("POST"))
	}
	api.on("GET", "/api/v2/email-deliveries", 200, `[`+deliveryJSON("accepted", "3")+`]`)
	code, stdout, _ = runCLI(t, "email", "delivery", "list", "--limit", "5", "--json")
	if code != ExitOK || !strings.Contains(stdout, `"mode": "bounded"`) || api.recorded()[len(api.recorded())-1].Query != "limit=5" {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
}

func TestAlertCommandsAreRegisteredHonestly(t *testing.T) {
	for name, want := range map[string]struct{ mutating, retry, wait bool }{
		"quota-alert create": {true, false, false}, "quota-alert update": {true, false, false}, "quota-alert delete": {true, false, false},
		"email target create": {true, false, false}, "email target update": {true, false, false},
		"email test": {true, true, false}, "email delivery replay": {true, false, false}, "email delivery wait": {false, true, true},
		"email preview": {false, true, false}, "email capability": {false, true, false},
	} {
		cmd, ok := lookupCommand(name)
		if !ok || cmd.meta.Mutating != want.mutating || cmd.meta.Retry != want.retry || cmd.meta.Wait != want.wait || cmd.meta.Stage != "m2" {
			t.Fatalf("%s meta = %+v", name, cmd.meta)
		}
	}
}
