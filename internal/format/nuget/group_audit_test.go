package nuget

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
)

type nuGetGroupResponse struct {
	status int
	body   []byte
	header http.Header
	err    error
}

func groupAuditFixture(t *testing.T) (fixture, repository.HostedGroup, *httptest.Server, []byte) {
	t.Helper()
	body := packageFixture(t, "Acme.Audited", "1.0.0", "")
	f, _ := proxyFixture(t, newTestFeed(t))
	hostedPackage(t, f, f.repo, body)
	group := testNuGetGroup(t, f, f.repo)
	server := httptest.NewServer(f.module)
	t.Cleanup(server.Close)
	return f, group, server, body
}

func groupAuditURL(server *httptest.Server, group repository.HostedGroup, file string) string {
	return server.URL + "/nuget/" + group.Name + "/v3/flat/acme.audited/1.0.0/" + file
}

func readGroupResponse(ctx context.Context, client *http.Client, method, target string, headers map[string]string) nuGetGroupResponse {
	req, err := http.NewRequestWithContext(ctx, method, target, nil)
	if err != nil {
		return nuGetGroupResponse{err: err}
	}
	req.Header.Set("Authorization", "Bearer nuget-test-admin")
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	response, err := client.Do(req)
	if err != nil {
		return nuGetGroupResponse{err: err}
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	return nuGetGroupResponse{status: response.StatusCode, body: body, header: response.Header, err: err}
}

func assertGroupAudit(t *testing.T, f fixture, group repository.HostedGroup, response nuGetGroupResponse, status int, outcome repository.AuditOutcome, count int) {
	t.Helper()
	if response.err != nil || response.status != status {
		t.Fatalf("Group response=%d body=%s err=%v", response.status, response.body, response.err)
	}
	audits, err := f.store.ListAudits(t.Context(), repository.AuditQuery{Repository: group.Name, Operation: "nuget.group.resolve", Limit: 100})
	if err != nil || len(audits) != count {
		t.Fatalf("Group audits=%+v err=%v", audits, err)
	}
	audit := audits[0]
	if audit.Status != status || audit.Outcome != outcome || audit.Bytes != int64(len(response.body)) || audit.Evidence["repositoryId"] != f.repo.ID {
		t.Fatalf("Group audit differs from HTTP response: audit=%+v status=%d bytes=%d", audit, status, len(response.body))
	}
}

type waitingNuGetGroupRead struct {
	repository.NativeNuGetStore
	attempted chan struct{}
	once      sync.Once
}

func (s *waitingNuGetGroupRead) LockNuGetPublication(ctx context.Context, repo, id, version string) (context.Context, func(), error) {
	s.once.Do(func() { close(s.attempted) })
	return s.NativeNuGetStore.LockNuGetPublication(ctx, repo, id, version)
}

func TestNuGetGroupAuditRechecksGovernanceAfterWaitingForReadLock(t *testing.T) {
	for _, action := range []string{"delete", "quarantine"} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			t.Run(action+"/"+method, func(t *testing.T) {
				f, group, server, body := groupAuditFixture(t)
				target := groupAuditURL(server, group, "acme.audited.1.0.0.nupkg")
				initial := readGroupResponse(t.Context(), server.Client(), http.MethodGet, target, nil)
				if initial.err != nil || initial.status != 200 || !bytes.Equal(initial.body, body) {
					t.Fatalf("initial authority=%d %v", initial.status, initial.err)
				}
				if action == "quarantine" {
					setReadPolicy(t, f.module, f.repo, "1", true)
				}
				p, err := f.store.GetNuGetPublication(t.Context(), f.repo.ID, "acme.audited", "1.0.0")
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				ctx, unlock, err := f.store.LockNuGetPublication(ctx, f.repo.ID, p.PackageID, p.Version)
				if err != nil {
					t.Fatal(err)
				}
				defer unlock()
				waiting := &waitingNuGetGroupRead{NativeNuGetStore: f.store, attempted: make(chan struct{})}
				f.module.store = waiting
				completed := make(chan nuGetGroupResponse, 1)
				go func() { completed <- readGroupResponse(ctx, server.Client(), method, target, nil) }()
				select {
				case <-waiting.attempted:
				case <-ctx.Done():
					t.Fatal("Group did not reach the package read lock")
				}
				// The HTTP request already selected and verified its pinned owner.
				// Apply governance while holding the same lock the read now awaits.
				_, err = f.store.TransitionNuGetGovernance(ctx, repository.NuGetGovernanceChange{RepositoryID: f.repo.ID, PackageID: p.PackageID, Version: p.Version, ExpectedDigest: p.Digest, ExpectedVersion: p.Governance.Version, Action: action, Actor: "operator", Reason: "Owned Group read-barrier regression"})
				if err != nil {
					t.Fatal(err)
				}
				unlock()
				select {
				case response := <-completed:
					assertGroupAudit(t, f, group, response, http.StatusNotFound, repository.AuditNotFound, 2)
				case <-ctx.Done():
					t.Fatal("Group read did not finish after releasing the lock")
				}
			})
		}
	}
}

func TestNuGetGroupAuditMatchesRangeConditionalAndHeadResponses(t *testing.T) {
	f, group, server, body := groupAuditFixture(t)
	target := groupAuditURL(server, group, "acme.audited.1.0.0.nupkg")
	initial := readGroupResponse(t.Context(), server.Client(), http.MethodGet, target, nil)
	assertGroupAudit(t, f, group, initial, http.StatusOK, repository.AuditResolved, 1)
	if !bytes.Equal(initial.body, body) {
		t.Fatal("Group changed original package bytes")
	}
	for i, entry := range []struct {
		method, file string
		headers      map[string]string
		status       int
		outcome      repository.AuditOutcome
	}{
		{http.MethodGet, "acme.audited.1.0.0.nupkg", map[string]string{"Range": "bytes=2-7"}, http.StatusPartialContent, repository.AuditResolved},
		{http.MethodGet, "acme.audited.1.0.0.nupkg", map[string]string{"If-None-Match": initial.header.Get("ETag")}, http.StatusNotModified, repository.AuditResolved},
		{http.MethodHead, "acme.audited.1.0.0.nupkg", nil, http.StatusOK, repository.AuditResolved},
		{http.MethodHead, "acme.audited.1.0.0.nupkg", map[string]string{"If-None-Match": initial.header.Get("ETag")}, http.StatusNotModified, repository.AuditResolved},
		{http.MethodGet, "acme.audited.nuspec", nil, http.StatusOK, repository.AuditResolved},
		{http.MethodHead, "acme.audited.nuspec", nil, http.StatusOK, repository.AuditResolved},
		{http.MethodGet, "acme.audited.1.0.0.nupkg.sha512", nil, http.StatusOK, repository.AuditResolved},
		{http.MethodGet, "acme.audited.1.0.0.nupkg", map[string]string{"Range": "bytes=9-1"}, http.StatusRequestedRangeNotSatisfiable, repository.AuditProxyDenied},
	} {
		response := readGroupResponse(t.Context(), server.Client(), entry.method, groupAuditURL(server, group, entry.file), entry.headers)
		assertGroupAudit(t, f, group, response, entry.status, entry.outcome, i+2)
		if entry.status == http.StatusPartialContent && !bytes.Equal(response.body, body[2:8]) {
			t.Fatalf("Group range returned %q", response.body)
		}
	}
}

type unavailableNuGetGroupObjects struct {
	objectstore.Store
	opens atomic.Int32
}

func (s *unavailableNuGetGroupObjects) Open(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	if s.opens.Add(1) > 1 {
		return nil, 0, errors.New("owned Group object failure")
	}
	return s.Store.Open(ctx, key)
}

func (*unavailableNuGetGroupObjects) OpenRange(context.Context, string, int64, int64) (io.ReadCloser, int64, error) {
	return nil, 0, errors.New("owned Group object range failure")
}

func TestNuGetGroupAuditMatchesStorageFailures(t *testing.T) {
	for _, entry := range []struct {
		name, method string
		headers      map[string]string
	}{{"get", http.MethodGet, nil}, {"head", http.MethodHead, nil}, {"range", http.MethodGet, map[string]string{"Range": "bytes=2-7"}}} {
		t.Run(entry.name, func(t *testing.T) {
			f, group, server, _ := groupAuditFixture(t)
			f.module.objects = &unavailableNuGetGroupObjects{Store: f.objects}
			response := readGroupResponse(t.Context(), server.Client(), entry.method, groupAuditURL(server, group, "acme.audited.1.0.0.nupkg"), entry.headers)
			assertGroupAudit(t, f, group, response, http.StatusInternalServerError, repository.AuditStorageError, 1)
		})
	}
}

type unavailableNuGetGroupPolicy struct {
	repository.NativeNuGetStore
	reads atomic.Int32
}

func (s *unavailableNuGetGroupPolicy) GetRepositoryQuarantineReadPolicy(ctx context.Context, id string) (repository.RepositoryQuarantineReadPolicy, error) {
	if s.reads.Add(1) > 1 {
		return repository.RepositoryQuarantineReadPolicy{}, errors.New("owned Group policy failure")
	}
	return s.NativeNuGetStore.GetRepositoryQuarantineReadPolicy(ctx, id)
}

func TestNuGetGroupAuditMatchesPolicyFailureAtReadBarrier(t *testing.T) {
	f, group, server, _ := groupAuditFixture(t)
	changeGovernance(t, f.module, f.repo, "acme.audited", "quarantine")
	f.module.store = &unavailableNuGetGroupPolicy{NativeNuGetStore: f.store}
	response := readGroupResponse(t.Context(), server.Client(), http.MethodGet, groupAuditURL(server, group, "acme.audited.1.0.0.nupkg"), nil)
	assertGroupAudit(t, f, group, response, http.StatusServiceUnavailable, repository.AuditStorageError, 1)
}
