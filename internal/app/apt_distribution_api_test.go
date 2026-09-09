package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/aptpublication"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestAPTDistributionManagementWorkflow(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	create := func() repository.HostedRepository {
		v, e := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "apt-api-" + uuid.NewString(), Format: repository.FormatAPT, Type: repository.RepositoryTypeHosted})
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	source, target := create(), create()
	deb := aptManagementDebianPackage(t, "Package: widget\nVersion: 1.0-1\nArchitecture: amd64\n")
	sum := sha256.Sum256(deb)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	coordinate := "pool/main/w/widget/widget.deb"
	m := aptpublication.NewManager(store, objects)
	s, _, e := m.CreateSession(ctx, aptpublication.CreateSessionInput{RepositoryID: source.ID, Suite: "testing", Component: "main", ObjectName: "widget.deb", Publisher: "ci", DeclaredDigest: digest, DeclaredSize: int64(len(deb)), IdempotencyKey: "fixture"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = m.UploadPackage(ctx, s.ID, s.ObjectName, bytes.NewReader(deb), int64(len(deb))); e != nil {
		t.Fatal(e)
	}
	publisher := aptpublication.NewPublisher(store, objects, aptManagementSigner{})
	handler := NewGatewayHandler(Dependencies{NativeAPTObjectStore: objects, APTSigner: aptManagementSigner{}}, store, TestAdapter{}, testAuthenticator())
	call := func(method, path, body, key, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		authorize(r, token)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	request := fmt.Sprintf(`{"targetRepositoryId":%q,"coordinate":%q,"digest":%q,"aptTargetSuite":"stable"}`, target.ID, coordinate, digest)
	endpoint := "/api/v2/repositories/" + source.ID
	if w := call("POST", endpoint+"/promotions", request, "staged", "admin-secret"); w.Code != 404 {
		t.Fatalf("staged source accepted: %d %s", w.Code, w.Body.String())
	}
	if _, e = publisher.Publish(ctx, aptpublication.PublishSnapshotInput{ID: uuid.NewString(), RepositoryID: source.ID, Suite: "testing", Sequence: 1, SessionIDs: []string{s.ID}, Actor: "ci", CreatedAt: time.Now().UTC()}); e != nil {
		t.Fatal(e)
	}
	d := aptpublication.Distribution{Store: store, Source: objects, Destination: objects, Publisher: publisher}
	for _, operation := range []string{"promotions", "replications"} {
		for _, body := range []string{strings.Replace(request, `,"aptTargetSuite":"stable"`, "", 1), strings.Replace(request, coordinate, "testing/"+coordinate, 1), strings.Replace(request, `"stable"`, `"../stable"`, 1)} {
			w := call("POST", endpoint+"/"+operation, body, "invalid-"+operation, "admin-secret")
			if w.Code != 400 {
				t.Fatalf("invalid %s: %d %s", operation, w.Code, w.Body.String())
			}
		}
		w := call("POST", endpoint+"/"+operation, request, operation, "admin-secret")
		if w.Code != 202 {
			t.Fatalf("enqueue %s: %d %s", operation, w.Code, w.Body.String())
		}
		if operation == "promotions" {
			e = d.RunPromotionJobs(ctx, 1)
		} else {
			e = d.RunReplicationJobs(ctx, 1)
		}
		if e != nil {
			t.Fatal(e)
		}
		v, e := store.GetVisibleAPTRepositorySnapshot(ctx, target.ID, "stable")
		if e != nil || v.Suite != "stable" {
			t.Fatalf("target: %+v %v", v, e)
		}
		replay := call("POST", endpoint+"/"+operation, request, operation, "admin-secret")
		if replay.Code != 202 {
			t.Fatalf("replay: %d %s", replay.Code, replay.Body.String())
		}
		changed := call("POST", endpoint+"/"+operation, strings.Replace(request, `"stable"`, `"candidate"`, 1), operation, "admin-secret")
		if changed.Code != 409 {
			t.Fatalf("changed suite replay: %d %s", changed.Code, changed.Body.String())
		}
	}
	plans := call("GET", endpoint+"/replications", "", "", "admin-secret")
	if plans.Code != 200 || !strings.Contains(plans.Body.String(), `"aptTargetSuite":"stable"`) || !strings.Contains(plans.Body.String(), `"state":"completed"`) {
		t.Fatalf("plan state: %d %s", plans.Code, plans.Body.String())
	}
}
