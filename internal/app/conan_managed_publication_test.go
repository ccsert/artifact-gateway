package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/ccsert/artifact-gateway/internal/repository"
)

type managedConanPublication struct {
	f       conanUploadFixture
	request nativeConanPublishRequest
	bodies  map[string][]byte
	base    string
}

func newManagedConanPublication(t *testing.T, kind string) managedConanPublication {
	t.Helper()
	f := newConanUploadFixture(t)
	m := managedConanPublication{f: f, request: nativeConanPublishRequest{Kind: kind, Reference: "pkg/1.0/_/_", RecipeRevision: f.revision}, base: f.base,
		bodies: map[string][]byte{"conanfile.py": []byte("recipe"), "conanmanifest.txt": f.manifest}}
	if kind == "package" {
		f.publish(t, "build-agent")
		info := []byte("[settings]\nos=Linux\narch=x86_64\n")
		payload := []byte("complete package bytes")
		var archive bytes.Buffer
		gz := gzip.NewWriter(&archive)
		tw := tar.NewWriter(gz)
		if err := tw.WriteHeader(&tar.Header{Name: "payload.txt", Mode: 0o644, Size: int64(len(payload))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(payload); err != nil {
			t.Fatal(err)
		}
		if err := tw.Close(); err != nil {
			t.Fatal(err)
		}
		if err := gz.Close(); err != nil {
			t.Fatal(err)
		}
		entries := fmt.Sprintf("conaninfo.txt: %x\npayload.txt: %x\n", md5.Sum(info), md5.Sum(payload))
		m.request.PackageID = fmt.Sprintf("%x", sha1.Sum(info))
		m.request.PackageRevision = fmt.Sprintf("%x", md5.Sum([]byte(entries)))
		m.base += "/packages/" + m.request.PackageID + "/revisions/" + m.request.PackageRevision
		m.bodies = map[string][]byte{"conaninfo.txt": info, "conan_package.tgz": archive.Bytes(), "conanmanifest.txt": []byte("1700000000\n" + entries)}
	}
	return m
}

func (m managedConanPublication) call(t *testing.T, method, path, token string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, bytes.NewReader(body))
	authorize(r, token)
	w := httptest.NewRecorder()
	m.f.handler.ServeHTTP(w, r)
	return w
}

func (m managedConanPublication) create(t *testing.T, token string, bodies map[string][]byte) string {
	t.Helper()
	request := m.request
	for name, body := range bodies {
		request.Objects = append(request.Objects, repository.MavenDeclaredObject{Name: name, Digest: fmt.Sprintf("sha256:%x", sha256.Sum256(body)), Size: int64(len(body))})
	}
	sort.Slice(request.Objects, func(i, j int) bool { return request.Objects[i].Name < request.Objects[j].Name })
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	w := m.call(t, http.MethodPost, "/api/v2/repositories/"+m.f.repo.ID+"/conan-publish-sessions", token, data)
	if w.Code != http.StatusCreated {
		t.Fatalf("create complete managed declaration=%d %s", w.Code, w.Body.String())
	}
	return requirePublishSessionContractKeys(t, w.Body.Bytes(), len(request.Objects))
}

func (m managedConanPublication) upload(t *testing.T, id, token string, bodies map[string][]byte) {
	t.Helper()
	for name, body := range bodies {
		w := m.call(t, http.MethodPut, "/api/v2/conan-publish-sessions/"+id+"/objects/"+name, token, body)
		if w.Code != http.StatusNoContent {
			t.Fatalf("managed PUT %s=%d %s", name, w.Code, w.Body.String())
		}
	}
}

func (m managedConanPublication) assertInvisible(t *testing.T) {
	t.Helper()
	for name := range m.bodies {
		w := m.f.request(t, http.MethodGet, m.base+"/files/"+name, "reader", nil)
		if w.Code != http.StatusNotFound {
			t.Errorf("incomplete %s became visible: %d %s", name, w.Code, w.Body.String())
		}
	}
	if m.request.Kind == "recipe" {
		revisions, err := m.f.store.ListConanRecipeRevisions(context.Background(), m.f.repo.ID, m.request.Reference)
		if err != nil || len(revisions) != 0 {
			t.Errorf("incomplete recipe metadata=%+v %v", revisions, err)
		}
	} else {
		revisions, err := m.f.store.ListConanPackageRevisions(context.Background(), m.f.repo.ID, m.request.Reference, m.request.RecipeRevision, m.request.PackageID)
		if err != nil || len(revisions) != 0 {
			t.Errorf("incomplete package metadata=%+v %v", revisions, err)
		}
	}
}

func TestConanManagedPublicationCannotBypassManifestAcrossSessions(t *testing.T) {
	for _, kind := range []string{"recipe", "package"} {
		for _, order := range []string{"native-first", "managed-first", "managed-only", "different-actor"} {
			t.Run(kind+"/"+order, func(t *testing.T) {
				m := newManagedConanPublication(t, kind)
				bad := make(map[string][]byte, len(m.bodies))
				for name, body := range m.bodies {
					bad[name] = body
				}
				// Even a fully uploaded, SHA-256-consistent declaration cannot
				// publish a manifest tree belonging to a different revision.
				bad["conanmanifest.txt"] = []byte("1700000000\nmissing.txt: " + strings.Repeat("0", 32) + "\n")
				var nativeID string
				stageNative := func() {
					name := "conanfile.py"
					if kind == "package" {
						name = "conaninfo.txt"
					}
					w := m.f.request(t, http.MethodPut, m.base+"/files/"+name, "build-agent", m.bodies[name])
					if w.Code != http.StatusCreated {
						t.Fatalf("native stage=%d %s", w.Code, w.Body.String())
					}
					nativeID = w.Header().Get("X-Artifact-Gateway-Publish-Session")
				}
				if order == "native-first" || order == "different-actor" {
					stageNative()
				}
				id := m.create(t, "resolver-secret", bad)
				if order == "managed-first" {
					stageNative()
				}
				m.upload(t, id, "resolver-secret", bad)
				actor := "resolver-secret"
				if order == "different-actor" {
					actor = "admin-secret"
				}
				w := m.call(t, http.MethodPost, "/api/v2/conan-publish-sessions/"+id+":commit", actor, nil)
				if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), `"code":"invalid_publication"`) {
					t.Errorf("bad manifest commit=%d %s", w.Code, w.Body.String())
				}
				m.assertInvisible(t)
				for _, sessionID := range []string{id, nativeID} {
					if sessionID == "" {
						continue
					}
					s, err := m.f.store.GetConanPublishSession(context.Background(), sessionID)
					if err != nil || s.State != "open" {
						t.Errorf("failed publication changed session: %+v %v", s, err)
					}
				}
			})
		}
	}
}

func TestConanManagedCompletePublicationUsesActualCommitterAndAtomicBoundary(t *testing.T) {
	for _, kind := range []string{"recipe", "package"} {
		t.Run(kind, func(t *testing.T) {
			m := newManagedConanPublication(t, kind)
			id := m.create(t, "resolver-secret", m.bodies)
			m.upload(t, id, "resolver-secret", m.bodies)
			w := m.call(t, http.MethodPost, "/api/v2/conan-publish-sessions/"+id+":commit", "admin-secret", nil)
			if w.Code != http.StatusOK {
				t.Fatalf("valid managed commit=%d %s", w.Code, w.Body.String())
			}
			s, err := m.f.store.GetConanPublishSession(context.Background(), id)
			if err != nil || s.State != "committed" || s.Publisher != "build-agent" {
				t.Fatalf("managed session=%+v %v", s, err)
			}
			for name, expected := range m.bodies {
				w := m.f.request(t, http.MethodGet, m.base+"/files/"+name, "reader", nil)
				if w.Code != http.StatusOK || !bytes.Equal(w.Body.Bytes(), expected) {
					t.Errorf("managed read %s=%d %q", name, w.Code, w.Body.String())
				}
			}
			audits, err := m.f.store.ListAudits(context.Background(), repository.AuditQuery{Repository: m.f.repo.Name, Limit: 100})
			if err != nil {
				t.Fatal(err)
			}
			found := 0
			for _, audit := range audits {
				if audit.Operation == "publish" && audit.Evidence["publicationSessionId"] == id {
					found++
					if audit.Actor != "alice" || audit.Evidence["publicationState"] != "committed" {
						t.Errorf("actual committer audit=%+v", audit)
					}
				}
			}
			if found != 1 {
				t.Errorf("managed transactional publication audits=%d", found)
			}
		})
	}
}
