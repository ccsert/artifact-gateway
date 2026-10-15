package agctl

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

const (
	memberA = "00000000-0000-4000-8000-00000000000a"
	memberB = "00000000-0000-4000-8000-00000000000b"
	memberC = "00000000-0000-4000-8000-00000000000c"
)

type fakeMember struct {
	RepositoryID string `json:"repositoryId"`
	Position     int    `json:"position"`
}

type fakeGroup struct {
	ID            string       `json:"id"`
	Name          string       `json:"name"`
	Format        string       `json:"format"`
	AnonymousRead bool         `json:"anonymousRead"`
	Members       []fakeMember `json:"members"`
	Version       string       `json:"version"`
}

// fakeGroupAPI stands in for the Group endpoints with the server semantics
// the commands rely on: cursor pages, idempotent create replay, If-Match
// replacement, synchronous deletion, and the member replacement that answers
// with the whole Group.
type fakeGroupAPI struct {
	server *httptest.Server

	mu          sync.Mutex
	groups      []*fakeGroup
	requests    []recordedRequest
	idempotency map[string]string
	// failWrites answers the next N writes with 503 after applying them.
	failWrites int
	// writeBody, when set, replaces the body of the next successful write.
	writeBody *string
	// membersAsList answers member replacement with the documented MemberList.
	membersAsList bool
	// readBody overrides a Group read to exercise incomplete read-back responses.
	readBody *string
}

func newFakeGroupAPI(t *testing.T) *fakeGroupAPI {
	t.Helper()
	isolatedCLI(t)
	api := &fakeGroupAPI{idempotency: map[string]string{}}
	api.server = httptest.NewServer(http.HandlerFunc(api.handle))
	t.Cleanup(api.server.Close)
	t.Setenv("AGCTL_SERVER", api.server.URL)
	t.Setenv("AGCTL_TOKEN", repoTestToken)
	return api
}

func (api *fakeGroupAPI) addLocked(name, format string, members ...string) *fakeGroup {
	group := &fakeGroup{
		ID:      fmt.Sprintf("00000000-0000-4000-9000-%012d", len(api.groups)+1),
		Name:    name,
		Format:  format,
		Version: "1",
	}
	for index, member := range members {
		group.Members = append(group.Members, fakeMember{RepositoryID: member, Position: index})
	}
	api.groups = append(api.groups, group)
	return group
}

func (api *fakeGroupAPI) add(name, format string, members ...string) *fakeGroup {
	api.mu.Lock()
	defer api.mu.Unlock()
	return api.addLocked(name, format, members...)
}

func (api *fakeGroupAPI) snapshot(id string) fakeGroup {
	api.mu.Lock()
	defer api.mu.Unlock()
	for _, group := range api.groups {
		if group.ID == id {
			copied := *group
			copied.Members = append([]fakeMember(nil), group.Members...)
			return copied
		}
	}
	return fakeGroup{}
}

func (api *fakeGroupAPI) count() int {
	api.mu.Lock()
	defer api.mu.Unlock()
	return len(api.groups)
}

func (api *fakeGroupAPI) writes() []recordedRequest {
	api.mu.Lock()
	defer api.mu.Unlock()
	var writes []recordedRequest
	for _, request := range api.requests {
		if request.Method != http.MethodGet {
			writes = append(writes, request)
		}
	}
	return writes
}

func (api *fakeGroupAPI) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	api.mu.Lock()
	defer api.mu.Unlock()
	api.requests = append(api.requests, recordedRequest{
		Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery,
		IfMatch: r.Header.Get("If-Match"), IdempotencyKey: r.Header.Get("Idempotency-Key"), Body: string(body),
	})
	if r.Header.Get("Authorization") != "Bearer "+repoTestToken {
		writeProblem(w, http.StatusUnauthorized, "access_denied", "authentication required")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v2/groups")
	if path == "" {
		switch r.Method {
		case http.MethodGet:
			api.list(w, r)
		case http.MethodPost:
			api.create(w, r, body)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return
	}
	id, sub, _ := strings.Cut(strings.TrimPrefix(path, "/"), "/")
	var group *fakeGroup
	index := -1
	for position, candidate := range api.groups {
		if candidate.ID == id {
			group, index = candidate, position
		}
	}
	if group == nil {
		writeProblem(w, http.StatusNotFound, "not_found", "group not found")
		return
	}
	switch {
	case sub == "" && r.Method == http.MethodGet:
		if api.readBody != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, *api.readBody)
			return
		}
		writeJSON(w, http.StatusOK, group)
	case sub == "members" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, group.Members)
	case sub == "resolution" && r.Method == http.MethodGet:
		members := []map[string]any{}
		for _, member := range group.Members {
			members = append(members, map[string]any{
				"repositoryId": member.RepositoryID, "repositoryName": "repo-" + member.RepositoryID[len(member.RepositoryID)-1:],
				"type": "hosted", "configuredPosition": member.Position, "resolutionOrder": member.Position,
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"groupId": group.ID, "groupVersion": group.Version, "format": group.Format,
			"strategy": "hosted-first", "members": members, "excludedMemberCount": 1,
		})
	case sub == "" && r.Method == http.MethodPut:
		if r.Header.Get("If-Match") != group.Version {
			writeProblem(w, http.StatusPreconditionFailed, "version_conflict", "If-Match does not match current version")
			return
		}
		var replacement fakeGroup
		_ = json.Unmarshal(body, &replacement)
		group.Name, group.AnonymousRead, group.Members = replacement.Name, replacement.AnonymousRead, replacement.Members
		group.Version = bump(group.Version)
		api.answerWrite(w, http.StatusOK, group)
	case sub == "members" && r.Method == http.MethodPut:
		if r.Header.Get("If-Match") != group.Version {
			writeProblem(w, http.StatusPreconditionFailed, "version_conflict", "If-Match does not match current version")
			return
		}
		var members []fakeMember
		_ = json.Unmarshal(body, &members)
		group.Members = members
		group.Version = bump(group.Version)
		if api.membersAsList {
			api.answerWrite(w, http.StatusOK, group.Members)
			return
		}
		api.answerWrite(w, http.StatusOK, group)
	case sub == "" && r.Method == http.MethodDelete:
		api.groups = append(api.groups[:index], api.groups[index+1:]...)
		if api.failWrites > 0 {
			api.failWrites--
			writeProblem(w, http.StatusServiceUnavailable, "unavailable", "try again")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// answerWrite applies the configured failure or body override to a write
// that the fake has already applied.
func (api *fakeGroupAPI) answerWrite(w http.ResponseWriter, status int, value any) {
	if api.failWrites > 0 {
		api.failWrites--
		writeProblem(w, http.StatusServiceUnavailable, "unavailable", "try again")
		return
	}
	if api.writeBody != nil {
		body := *api.writeBody
		api.writeBody = nil
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
		return
	}
	writeJSON(w, status, value)
}

func (api *fakeGroupAPI) list(w http.ResponseWriter, r *http.Request) {
	size, _ := strconv.Atoi(r.URL.Query().Get("pageSize"))
	start := 0
	if token := r.URL.Query().Get("pageToken"); token != "" {
		start, _ = strconv.Atoi(strings.TrimPrefix(token, "cursor-"))
	}
	end := start + size
	if end > len(api.groups) {
		end = len(api.groups)
	}
	page := map[string]any{"items": api.groups[start:end]}
	if end < len(api.groups) {
		page["nextPageToken"] = fmt.Sprintf("cursor-%d", end)
	}
	writeJSON(w, http.StatusOK, page)
}

func (api *fakeGroupAPI) create(w http.ResponseWriter, r *http.Request, body []byte) {
	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		writeProblem(w, http.StatusBadRequest, "invalid_request", "Idempotency-Key is required")
		return
	}
	var request fakeGroup
	_ = json.Unmarshal(body, &request)
	var group *fakeGroup
	if previous, ok := api.idempotency[key]; ok {
		if previous != string(body) {
			writeProblem(w, http.StatusConflict, "idempotency_conflict", "Idempotency-Key was already used with a different request")
			return
		}
		for _, candidate := range api.groups {
			if candidate.Name == request.Name {
				group = candidate
			}
		}
	} else {
		for _, candidate := range api.groups {
			if candidate.Name == request.Name {
				writeProblem(w, http.StatusConflict, "version_conflict", "group name already exists")
				return
			}
		}
		api.idempotency[key] = string(body)
		group = api.addLocked(request.Name, request.Format)
		group.AnonymousRead = request.AnonymousRead
		group.Members = request.Members
	}
	api.answerWrite(w, http.StatusCreated, group)
}

func TestGroupListReadsOnePage(t *testing.T) {
	api := newFakeGroupAPI(t)
	api.add("npm-all", "npm", memberA, memberB)
	api.add("raw-all", "raw", memberC)
	code, stdout, stderr := runCLI(t, "group", "list", "--page-size", "1", "--json")
	if code != ExitOK {
		t.Fatalf("exit = %d, stderr = %s", code, stderr)
	}
	assertGolden(t, "group_list_first_page", stdout)

	code, stdout, stderr = runCLI(t, "group", "list", "--page-size", "1")
	if code != ExitOK || !strings.Contains(stdout, "npm-all") || !strings.Contains(stderr, "more groups: agctl group list --cursor cursor-1") {
		t.Fatalf("exit = %d, stdout = %s, stderr = %s", code, stdout, stderr)
	}
}

func TestGroupShowAndMembersList(t *testing.T) {
	api := newFakeGroupAPI(t)
	group := api.add("npm-all", "npm", memberA, memberB)
	code, stdout, _ := runCLI(t, "group", "show", group.ID)
	if code != ExitOK || !strings.Contains(stdout, "npm-all") || !strings.Contains(stdout, memberB) {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
	code, stdout, _ = runCLI(t, "group", "members", "list", group.ID, "--json")
	if code != ExitOK {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
	assertGolden(t, "group_members_list", stdout)
	if code, _, _ := runCLI(t, "group", "show", "00000000-0000-4000-9000-999999999999"); code != ExitNotFound {
		t.Fatalf("missing Group exit = %d, want %d", code, ExitNotFound)
	}
}

func TestGroupResolutionNamesExclusions(t *testing.T) {
	api := newFakeGroupAPI(t)
	group := api.add("npm-all", "npm", memberA, memberB)
	code, stdout, _ := runCLI(t, "group", "resolution", group.ID)
	if code != ExitOK || !strings.Contains(stdout, "repo-a") || !strings.Contains(stdout, "1 members excluded") {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
}

func TestGroupCreateValidatesBeforeRequest(t *testing.T) {
	api := newFakeGroupAPI(t)
	for _, args := range [][]string{
		{"group", "create", "Bad_Name", "--format", "npm", "--member", memberA},
		{"group", "create", "npm-all", "--member", memberA},
		{"group", "create", "npm-all", "--format", "rpm", "--member", memberA},
		{"group", "create", "npm-all", "--format", "npm"},
		{"group", "create", "npm-all", "--format", "npm", "--member", "npm-hosted"},
		{"group", "create", "npm-all", "--format", "npm", "--member", memberA, "--member", memberA},
		{"group", "create", "npm-all", "--format", "npm", "--member", memberA, "--idempotency-key", " "},
	} {
		if code, _, _ := runCLI(t, args...); code != ExitUsage {
			t.Fatalf("%v exit = %d, want %d", args, code, ExitUsage)
		}
	}
	if len(api.requests) != 0 {
		t.Fatalf("requests = %+v", api.requests)
	}
}

func TestGroupCreateOrdersMembersAndReusesKey(t *testing.T) {
	api := newFakeGroupAPI(t)
	api.failWrites = 1
	code, stdout, stderr := runCLI(t, "group", "create", "npm-all", "--format", "npm", "--member", memberB, "--member", memberA, "--json")
	if code != ExitOK {
		t.Fatalf("exit = %d, stdout = %s, stderr = %s", code, stdout, stderr)
	}
	writes := api.writes()
	if len(writes) != 2 || writes[0].IdempotencyKey == "" || writes[0].IdempotencyKey != writes[1].IdempotencyKey || writes[0].Body != writes[1].Body {
		t.Fatalf("a retried create must resend the same key and body: %+v", writes)
	}
	want := `{"format":"npm","members":[{"position":0,"repositoryId":"` + memberB + `"},{"position":1,"repositoryId":"` + memberA + `"}],"name":"npm-all"}`
	if writes[0].Body != want {
		t.Fatalf("body = %s, want %s", writes[0].Body, want)
	}
	if api.count() != 1 {
		t.Fatalf("groups = %d, want exactly one", api.count())
	}
}

func TestGroupCreateUnknownOutcomeKeepsKey(t *testing.T) {
	for name, body := range map[string]string{
		"unavailable": "",
		"empty":       " ",
		"null":        "null",
		"no id":       `{"name":"npm-all","format":"npm","members":[{"repositoryId":"` + memberA + `","position":0}],"version":"1"}`,
		"no members":  `{"id":"00000000-0000-4000-9000-000000000001","name":"npm-all","format":"npm","members":[],"version":"1"}`,
		"unreadable":  `{"id":`,
	} {
		t.Run(name, func(t *testing.T) {
			api := newFakeGroupAPI(t)
			if name == "unavailable" {
				api.failWrites = 10
			} else {
				api.writeBody = &body
			}
			code, stdout, _ := runCLI(t, "group", "create", "npm-all", "--format", "npm", "--member", memberA, "--idempotency-key", "agctl-fixed-key", "--json")
			envelope := decodeEnvelope(t, stdout)
			var data unknownOutcome
			_ = json.Unmarshal(envelope.Data, &data)
			if code == ExitOK || data.Outcome != "unknown" || data.IdempotencyKey != "agctl-fixed-key" || data.ReadBack != "agctl group list" {
				t.Fatalf("exit = %d, stdout = %s", code, stdout)
			}
			if name == "unavailable" {
				assertGolden(t, "group_create_unknown_outcome", stdout)
			}
			// Re-running with the reported key replays the original creation.
			api.failWrites = 0
			code, _, _ = runCLI(t, "group", "create", "npm-all", "--format", "npm", "--member", memberA, "--idempotency-key", "agctl-fixed-key")
			if code != ExitOK || api.count() != 1 {
				t.Fatalf("replay exit = %d, groups = %d", code, api.count())
			}
		})
	}
}

func TestGroupCreateDryRunSubmitsNothing(t *testing.T) {
	api := newFakeGroupAPI(t)
	code, stdout, _ := runCLI(t, "group", "create", "npm-all", "--format", "npm", "--member", memberA, "--member", memberB,
		"--anonymous-read", "--idempotency-key", "agctl-fixed-key", "--dry-run", "--json")
	if code != ExitOK {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
	assertGolden(t, "group_create_dry_run", stdout)
	if len(api.requests) != 0 {
		t.Fatalf("requests = %+v", api.requests)
	}
}

func TestGroupUpdateReplacesWholeGroupConditionally(t *testing.T) {
	api := newFakeGroupAPI(t)
	group := api.add("npm-all", "npm", memberA, memberB)
	code, stdout, stderr := runCLI(t, "group", "update", group.ID, "--name", "npm-public", "--anonymous-read", "--json")
	if code != ExitOK {
		t.Fatalf("exit = %d, stdout = %s, stderr = %s", code, stdout, stderr)
	}
	writes := api.writes()
	if len(writes) != 1 || writes[0].IfMatch != "1" || !strings.Contains(writes[0].Body, `"name":"npm-public"`) || !strings.Contains(writes[0].Body, memberB) {
		t.Fatalf("the replacement must keep the members and be conditional: %+v", writes)
	}

	// A stale --if-match fails with 412, is not retried, and overwrites nothing.
	code, stdout, _ = runCLI(t, "group", "update", group.ID, "--name", "npm-stale", "--if-match", "1", "--json")
	envelope := decodeEnvelope(t, stdout)
	if code != ExitConflict || !strings.Contains(envelope.Error.Hint, "was not updated") {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
	if current := api.snapshot(group.ID); current.Name != "npm-public" || current.Version != "2" || len(api.writes()) != 2 {
		t.Fatalf("a 412 must not overwrite: %+v, writes = %d", current, len(api.writes()))
	}
}

func TestGroupUpdateIncompleteResponseIsUnknown(t *testing.T) {
	api := newFakeGroupAPI(t)
	group := api.add("npm-all", "npm", memberA)
	body := `{}`
	api.writeBody = &body
	code, stdout, _ := runCLI(t, "group", "update", group.ID, "--anonymous-read", "--json")
	envelope := decodeEnvelope(t, stdout)
	var data unknownOutcome
	_ = json.Unmarshal(envelope.Data, &data)
	if code == ExitOK || data.Outcome != "unknown" || data.ReadBack != "agctl group show "+group.ID || envelope.Error.Code != "invalid_response" {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
	if len(api.writes()) != 1 {
		t.Fatalf("an unknown update must not be resent: %+v", api.writes())
	}
}

func TestGroupUpdateRequiresAChange(t *testing.T) {
	api := newFakeGroupAPI(t)
	group := api.add("npm-all", "npm", memberA)
	for _, args := range [][]string{
		{"group", "update", group.ID},
		{"group", "update", group.ID, "--name", "Bad_Name"},
		{"group", "update", group.ID, "--anonymous-read", "--if-match", ""},
	} {
		if code, _, _ := runCLI(t, args...); code != ExitUsage {
			t.Fatalf("%v exit = %d, want %d", args, code, ExitUsage)
		}
	}
	if len(api.requests) != 0 {
		t.Fatalf("requests = %+v", api.requests)
	}
}

func TestGroupUpdateDryRunReadsButDoesNotWrite(t *testing.T) {
	api := newFakeGroupAPI(t)
	group := api.add("npm-all", "npm", memberA)
	code, stdout, _ := runCLI(t, "group", "update", group.ID, "--name", "npm-public", "--dry-run", "--json")
	if code != ExitOK || len(api.writes()) != 0 {
		t.Fatalf("exit = %d, writes = %+v, stdout = %s", code, api.writes(), stdout)
	}
	assertGolden(t, "group_update_dry_run", stdout)
}

func TestGroupMembersSetIsConditional(t *testing.T) {
	api := newFakeGroupAPI(t)
	group := api.add("npm-all", "npm", memberA, memberB)
	code, stdout, stderr := runCLI(t, "group", "members", "set", group.ID, "--member", memberC, "--member", memberA, "--json")
	if code != ExitOK {
		t.Fatalf("exit = %d, stdout = %s, stderr = %s", code, stdout, stderr)
	}
	writes := api.writes()
	want := `[{"position":0,"repositoryId":"` + memberC + `"},{"position":1,"repositoryId":"` + memberA + `"}]`
	if len(writes) != 1 || writes[0].IfMatch != "1" || writes[0].Body != want {
		t.Fatalf("writes = %+v", writes)
	}
	if !strings.Contains(stdout, `"version": "2"`) {
		t.Fatalf("stdout = %s", stdout)
	}

	code, stdout, _ = runCLI(t, "group", "members", "set", group.ID, "--member", memberB, "--if-match", "1", "--json")
	if code != ExitConflict {
		t.Fatalf("stale exit = %d, stdout = %s", code, stdout)
	}
	if current := api.snapshot(group.ID); len(current.Members) != 2 || current.Members[0].RepositoryID != memberC {
		t.Fatalf("a 412 must not overwrite: %+v", current)
	}
}

func TestGroupMembersSetAcceptsDocumentedMemberList(t *testing.T) {
	api := newFakeGroupAPI(t)
	group := api.add("npm-all", "npm", memberA)
	api.membersAsList = true
	code, stdout, stderr := runCLI(t, "group", "members", "set", group.ID, "--member", memberB, "--json")
	if code != ExitOK || !strings.Contains(stdout, `"version": "2"`) || !strings.Contains(stdout, memberB) {
		t.Fatalf("exit = %d, stdout = %s, stderr = %s", code, stdout, stderr)
	}
}

func TestGroupMembersSetIncompleteReadBackPreservesAppliedMembers(t *testing.T) {
	for name, body := range map[string]string{"empty": "", "null": "null", "empty object": "{}", "partial group": `{"name":"npm-all"}`} {
		t.Run(name, func(t *testing.T) {
			api := newFakeGroupAPI(t)
			group := api.add("npm-all", "npm", memberA)
			api.membersAsList = true
			api.readBody = &body
			code, stdout, _ := runCLI(t, "group", "members", "set", group.ID, "--member", memberB, "--if-match", "1", "--json")
			envelope := decodeEnvelope(t, stdout)
			if code != ExitFailure || envelope.OK || envelope.Error == nil || envelope.Error.Code != "invalid_response" || envelope.Error.HTTPStatus == nil || *envelope.Error.HTTPStatus != http.StatusOK {
				t.Fatalf("an incomplete read-back must not report success: exit = %d, stdout = %s", code, stdout)
			}
			var data struct {
				Items []fakeMember `json:"items"`
			}
			if err := json.Unmarshal(envelope.Data, &data); err != nil || len(data.Items) != 1 || data.Items[0].RepositoryID != memberB || data.Items[0].Position != 0 {
				t.Fatalf("the confirmed member replacement must be retained: stdout = %s, err = %v", stdout, err)
			}
			if !strings.Contains(envelope.Error.Hint, "the members were replaced") || !strings.Contains(envelope.Error.Hint, "agctl group show "+group.ID) {
				t.Fatalf("the read-back recovery hint must be retained: stdout = %s", stdout)
			}
			if len(api.writes()) != 1 || api.snapshot(group.ID).Members[0].RepositoryID != memberB {
				t.Fatalf("the applied replacement must not be resent: writes = %+v", api.writes())
			}
		})
	}
}

func TestGroupMembersSetIncompleteResponseIsUnknown(t *testing.T) {
	for name, body := range map[string]string{"empty": "", "null": "null", "empty list": "[]", "partial group": `{"name":"npm-all"}`} {
		t.Run(name, func(t *testing.T) {
			api := newFakeGroupAPI(t)
			group := api.add("npm-all", "npm", memberA)
			api.writeBody = &body
			code, stdout, _ := runCLI(t, "group", "members", "set", group.ID, "--member", memberB, "--json")
			envelope := decodeEnvelope(t, stdout)
			var data unknownOutcome
			_ = json.Unmarshal(envelope.Data, &data)
			if code == ExitOK || data.Outcome != "unknown" || data.ReadBack != "agctl group members list "+group.ID {
				t.Fatalf("exit = %d, stdout = %s", code, stdout)
			}
		})
	}
}

func TestGroupMembersSetDryRunAndValidation(t *testing.T) {
	api := newFakeGroupAPI(t)
	group := api.add("npm-all", "npm", memberA)
	if code, _, _ := runCLI(t, "group", "members", "set", group.ID); code != ExitUsage {
		t.Fatalf("no members exit = %d", code)
	}
	if len(api.requests) != 0 {
		t.Fatalf("requests = %+v", api.requests)
	}
	code, stdout, _ := runCLI(t, "group", "members", "set", group.ID, "--member", memberB, "--if-match", "1", "--dry-run", "--json")
	if code != ExitOK || len(api.requests) != 0 {
		t.Fatalf("a dry run with --if-match needs no request: exit = %d, requests = %+v", code, api.requests)
	}
	assertGolden(t, "group_members_set_dry_run", stdout)
}

func TestGroupDeleteIsConfirmedAndNotRetried(t *testing.T) {
	api := newFakeGroupAPI(t)
	group := api.add("npm-all", "npm", memberA)
	if code, _, _ := runCLI(t, "group", "delete", group.ID); code != ExitUsage || len(api.requests) != 0 {
		t.Fatalf("unconfirmed delete exit = %d, requests = %+v", code, api.requests)
	}
	code, stdout, _ := runCLI(t, "group", "delete", group.ID, "--dry-run", "--json")
	if code != ExitOK || len(api.writes()) != 0 || !strings.Contains(stdout, `"method": "DELETE"`) {
		t.Fatalf("dry run exit = %d, writes = %+v, stdout = %s", code, api.writes(), stdout)
	}

	api.failWrites = 1
	code, stdout, _ = runCLI(t, "group", "delete", group.ID, "--yes", "--json")
	envelope := decodeEnvelope(t, stdout)
	var data unknownOutcome
	_ = json.Unmarshal(envelope.Data, &data)
	if code == ExitOK || data.Outcome != "unknown" || len(api.writes()) != 1 {
		t.Fatalf("a failed delete must not be resent: exit = %d, writes = %d, stdout = %s", code, len(api.writes()), stdout)
	}
	if code, _, _ := runCLI(t, "group", "show", group.ID); code != ExitNotFound {
		t.Fatalf("read-back exit = %d, want %d", code, ExitNotFound)
	}

	second := api.add("raw-all", "raw", memberC)
	code, stdout, _ = runCLI(t, "group", "delete", second.ID, "--yes", "--json")
	if code != ExitOK || !strings.Contains(stdout, `"deleted": true`) {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
}

func TestGroupCommandsAreRegisteredHonestly(t *testing.T) {
	want := map[string]struct {
		mutating bool
		dryRun   string
		retry    bool
	}{
		"group list":         {false, "unsupported", true},
		"group show":         {false, "unsupported", true},
		"group create":       {true, "client-plan", true},
		"group update":       {true, "client-plan", false},
		"group delete":       {true, "client-plan", false},
		"group members list": {false, "unsupported", true},
		"group members set":  {true, "client-plan", false},
		"group resolution":   {false, "unsupported", true},
	}
	for name, expected := range want {
		cmd, ok := lookupCommand(name)
		if !ok {
			t.Fatalf("%s is not registered", name)
		}
		if cmd.meta.Mutating != expected.mutating || cmd.meta.DryRun != expected.dryRun || cmd.meta.Retry != expected.retry || cmd.meta.Wait || len(cmd.meta.Endpoints) == 0 {
			t.Fatalf("%s meta = %+v", name, cmd.meta)
		}
	}
}
