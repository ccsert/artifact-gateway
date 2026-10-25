package rubygems

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"github.com/ccsert/artifact-gateway/internal/authorization"
	"github.com/google/uuid"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/repository"
	domain "github.com/ccsert/artifact-gateway/internal/rubygemspublication"
)

func policyPath(id string) string {
	return "/api/v2/previews/rubygems/repositories/" + id + "/quarantine-read-policy"
}
func setReadPolicy(t *testing.T, m *Module, id, version string, enabled bool, want int) {
	t.Helper()
	value := "false"
	if enabled {
		value = "true"
	}
	w := request(m.Preview(), "PUT", policyPath(id), []byte(`{"expectedVersion":"`+version+`","enabled":`+value+`}`), "Bearer owned-admin")
	if w.Code != want {
		t.Fatalf("read policy: %d %s", w.Code, w.Body.String())
	}
}

// Shared HTTP contract runs unchanged against memory and real PostgreSQL/RustFS.
func quarantineReadContract(t *testing.T, m *Module, store domain.Store, repo repository.HostedRepository) {
	t.Helper()
	for _, version := range []string{"1.0.0", "1.1.0.pre"} {
		data := gem(t, "isolated", version, "ruby", "owned")
		if w := request(m, "POST", "/rubygems/"+repo.Name+"/api/v1/gems", data, "owned-admin"); w.Code != 201 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	p, err := store.GetRetained(t.Context(), repo.ID, "isolated", "1.0.0", "ruby")
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{"names", "versions", "info/isolated", "specs.4.8.gz", "latest_specs.4.8.gz", "prerelease_specs.4.8.gz", "quick/Marshal.4.8/isolated-1.0.0.gemspec.rz", "gems/isolated-1.0.0.gem"}
	etags := map[string]string{}
	originals := map[string][]byte{}
	for _, path := range paths {
		w := request(m, "GET", "/rubygems/"+repo.Name+"/"+path, nil, "owned-admin")
		if w.Code != 200 {
			t.Fatal(path, w.Code)
		}
		etags[path], originals[path] = w.Header().Get("ETag"), w.Body.Bytes()
	}
	for _, version := range []string{"1.0.0", "1.1.0.pre"} {
		retained, err := store.GetRetained(t.Context(), repo.ID, "isolated", version, "ruby")
		if err != nil {
			t.Fatal(err)
		}
		g := governanceChange(t, m.Preview(), repo.ID, "isolated", version, "ruby", retained.Digest, "1", "quarantine", "Bearer owned-admin", 200)
		if !g.Quarantined || g.Yanked {
			t.Fatal(g)
		}
	}
	// Default policy preserves every representation, including quarantined bytes.
	for _, path := range paths {
		w := request(m, "GET", "/rubygems/"+repo.Name+"/"+path, nil, "owned-admin")
		if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), originals[path]) {
			t.Fatal("default policy", path, w.Code)
		}
	}
	setReadPolicy(t, m, repo.ID, "1", true, 200)
	setReadPolicy(t, m, repo.ID, "1", false, 409)
	setReadPolicy(t, m, repo.ID, "2", true, 200)
	for _, path := range paths {
		asset := strings.HasPrefix(path, "quick/") || strings.HasPrefix(path, "gems/")
		for _, method := range []string{"GET", "HEAD"} {
			r := httptest.NewRequest(method, "/rubygems/"+repo.Name+"/"+path, nil)
			r.Header.Set("Authorization", "owned-admin")
			r.Header.Set("If-None-Match", etags[path])
			r.Header.Set("Range", "bytes=0-31")
			w := httptest.NewRecorder()
			m.ServeHTTP(w, r)
			if asset {
				if w.Code != 404 || w.Header().Get("ETag") != "" {
					t.Fatalf("blocked %s %s: %d", method, path, w.Code)
				}
				continue
			}
			if w.Code != 200 || w.Header().Get("ETag") == etags[path] {
				t.Fatalf("index %s %s %d", method, path, w.Code)
			}
			data := w.Body.Bytes()
			if method == "GET" && strings.HasSuffix(path, ".gz") {
				z, err := gzip.NewReader(bytes.NewReader(data))
				if err != nil {
					t.Fatal(err)
				}
				data, err = io.ReadAll(z)
				_ = z.Close()
				if err != nil {
					t.Fatal(err)
				}
			}
			if bytes.Contains(data, []byte("isolated")) {
				t.Fatal("quarantine leaked index", path)
			}
		}
	}
	if w := request(m, "POST", "/rubygems/"+repo.Name+"/api/v1/gems", originals["gems/isolated-1.0.0.gem"], "owned-admin"); w.Code != 409 {
		t.Fatal("repush", w.Code)
	}
	governanceChange(t, m.Preview(), repo.ID, "isolated", "1.0.0", "ruby", p.Digest, "2", "yank", "Bearer owned-admin", 200)
	g := governanceChange(t, m.Preview(), repo.ID, "isolated", "1.0.0", "ruby", p.Digest, "3", "release", "Bearer owned-admin", 200)
	if !g.Yanked || g.Quarantined {
		t.Fatal(g)
	}
	if w := request(m, "GET", "/rubygems/"+repo.Name+"/gems/isolated-1.0.0.gem", nil, "owned-admin"); w.Code != 404 {
		t.Fatal("release undid yank", w.Code)
	}
	governanceChange(t, m.Preview(), repo.ID, "isolated", "1.0.0", "ruby", p.Digest, "4", "quarantine", "Bearer owned-admin", 200)
	g = governanceChange(t, m.Preview(), repo.ID, "isolated", "1.0.0", "ruby", p.Digest, "5", "restore", "Bearer owned-admin", 200)
	if g.Yanked || !g.Quarantined {
		t.Fatal(g)
	}
	if w := request(m, "GET", "/rubygems/"+repo.Name+"/gems/isolated-1.0.0.gem", nil, "owned-admin"); w.Code != 404 {
		t.Fatal("restore undid quarantine", w.Code)
	}
	governanceChange(t, m.Preview(), repo.ID, "isolated", "1.0.0", "ruby", p.Digest, "6", "release", "Bearer owned-admin", 200)
	w := request(m, "GET", "/rubygems/"+repo.Name+"/gems/isolated-1.0.0.gem", nil, "owned-admin")
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), originals["gems/isolated-1.0.0.gem"]) {
		t.Fatal("release lost original", w.Code)
	}
	setReadPolicy(t, m, repo.ID, "2", false, 200)
	w = request(m, "GET", "/rubygems/"+repo.Name+"/prerelease_specs.4.8.gz", nil, "owned-admin")
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), originals["prerelease_specs.4.8.gz"]) {
		t.Fatal("disabled policy", w.Code)
	}
}

func TestRubyGemsQuarantinePolicyFiltersEveryReadIndependently(t *testing.T) {
	f := setup(t)
	quarantineReadContract(t, f.module, f.store, f.repo)
}

type failedRubyReadPolicy struct{ domain.Store }

func (failedRubyReadPolicy) GetReadPolicy(context.Context, string) (repository.RepositoryQuarantineReadPolicy, error) {
	return repository.RepositoryQuarantineReadPolicy{}, errors.New("owned policy failure")
}
func TestRubyGemsReadPolicyFailureDeniesAllReads(t *testing.T) {
	f := setup(t)
	if w := push(f, gem(t, "owned", "1.0.0", "ruby", "fixture")); w.Code != 201 {
		t.Fatal(w.Code)
	}
	m, err := New(options(f.repos, failedRubyReadPolicy{f.store}, f.objects, f.repos))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"names", "versions", "info/owned", "specs.4.8.gz", "latest_specs.4.8.gz", "prerelease_specs.4.8.gz", "quick/Marshal.4.8/owned-1.0.0.gemspec.rz", "gems/owned-1.0.0.gem"} {
		for _, method := range []string{"GET", "HEAD"} {
			w := request(m, method, "/rubygems/"+f.repo.Name+"/"+path, nil, "owned-admin")
			if w.Code != 500 {
				t.Fatal("policy failure leaked", method, path, w.Code)
			}
		}
	}
}

type policyAuthorityGate struct {
	domain.Store
	ready, resume chan struct{}
}

func (g policyAuthorityGate) ReplaceReadPolicy(ctx context.Context, c domain.PolicyChange, check domain.GovernanceCheck) (repository.RepositoryQuarantineReadPolicy, error) {
	close(g.ready)
	select {
	case <-g.resume:
		return g.Store.ReplaceReadPolicy(ctx, c, check)
	case <-ctx.Done():
		return repository.RepositoryQuarantineReadPolicy{}, ctx.Err()
	}
}

func quarantineAuthorityContract(t *testing.T, m *Module, store domain.Store, repo repository.HostedRepository, keys repository.APIKeyStore, grants repository.RepositoryGrantStore) {
	t.Helper()
	for _, action := range []string{"quarantine", "release", "policy", "policy-noop"} {
		for _, revocation := range []string{"credential", "grant"} {
			t.Run(action+"/"+revocation, func(t *testing.T) {
				name := "authority_" + strings.ReplaceAll(uuid.NewString(), "-", "")
				if w := request(m, "POST", "/rubygems/"+repo.Name+"/api/v1/gems", gem(t, name, "1.0.0", "ruby", "authority"), "owned-admin"); w.Code != 201 {
					t.Fatal(w.Code)
				}
				p, err := store.GetRetained(t.Context(), repo.ID, name, "1.0.0", "ruby")
				if err != nil {
					t.Fatal(err)
				}
				if action == "release" {
					governanceChange(t, m.Preview(), repo.ID, name, "1.0.0", "ruby", p.Digest, "1", "quarantine", "Bearer owned-admin", 200)
					p, err = store.GetRetained(t.Context(), repo.ID, name, "1.0.0", "ruby")
					if err != nil {
						t.Fatal(err)
					}
				}
				token := uuid.NewString()
				key, err := keys.CreateAPIKey(t.Context(), repository.APIKey{ID: uuid.NewString(), Name: name, SecretHash: authorization.HashAPIKey(token), Roles: []string{"member"}})
				if err != nil {
					t.Fatal(err)
				}
				prior, err := grants.GetRepositoryGrants(t.Context(), repo.ID)
				if err != nil {
					t.Fatal(err)
				}
				set, err := grants.ReplaceRepositoryGrants(t.Context(), repo.ID, []repository.RepositoryGrant{{Principal: "api-key:" + key.ID, Scopes: []string{"repositories:admin"}}}, prior.Version)
				if err != nil {
					t.Fatal(err)
				}
				ready, resume := make(chan struct{}), make(chan struct{})
				current := *m
				current.auth.APIKeys = keys
				current.access.Legacy = current.auth
				current.access.Grants = grants
				if strings.HasPrefix(action, "policy") {
					current.store = policyAuthorityGate{store, ready, resume}
				} else {
					current.store = governanceLockGate{store, ready, resume}
				}
				result := make(chan int, 1)
				ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
				defer cancel()
				policy, err := store.GetReadPolicy(ctx, repo.ID)
				if err != nil {
					t.Fatal(err)
				}
				go func() {
					path := governancePath(repo.ID, name, "1.0.0", "ruby")
					method := "POST"
					body, _ := json.Marshal(map[string]string{"expectedDigest": p.Digest, "expectedGovernanceVersion": p.Governance.Version, "action": action, "reason": "Owned current authority"})
					if strings.HasPrefix(action, "policy") {
						path, method = policyPath(repo.ID), "PUT"
						enabled := !policy.Enabled
						if action == "policy-noop" {
							enabled = policy.Enabled
						}
						body, _ = json.Marshal(map[string]any{"expectedVersion": policy.Version, "enabled": enabled})
					}
					r := httptest.NewRequest(method, path, bytes.NewReader(body)).WithContext(ctx)
					r.Header.Set("Authorization", "Bearer "+token)
					w := httptest.NewRecorder()
					current.Preview().ServeHTTP(w, r)
					result <- w.Code
				}()
				select {
				case <-ready:
				case code := <-result:
					t.Fatalf("authority boundary not reached: %d", code)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				if revocation == "credential" {
					_, err = keys.RevokeAPIKey(ctx, key.ID)
				} else {
					_, err = grants.ReplaceRepositoryGrants(ctx, repo.ID, nil, set.Version)
				}
				close(resume)
				if err != nil {
					t.Fatal(err)
				}
				select {
				case code := <-result:
					if code != 401 && code != 403 {
						t.Fatalf("revoked %s passed %s: %d", revocation, action, code)
					}
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				got, err := store.GetRetained(ctx, repo.ID, name, "1.0.0", "ruby")
				if err != nil || got.Governance != p.Governance {
					t.Fatal("revocation changed governance", got, err)
				}
				after, err := store.GetReadPolicy(ctx, repo.ID)
				if err != nil || after != policy {
					t.Fatal("revocation changed policy", after, err)
				}
			})
		}
	}
}
func TestRubyGemsQuarantineAndPolicyRequireCurrentDurableAuthority(t *testing.T) {
	f := setup(t)
	quarantineAuthorityContract(t, f.module, f.store, f.repo, f.repos, f.repos)
}
