package app

import (
	"bytes"
	"encoding/json"
	"maps"
	"net/http"
	"strings"
	"testing"

	"github.com/ccsert/artifact-gateway/internal/objectstore"
	discovery "github.com/ccsert/artifact-gateway/internal/ocidiscovery"
	"github.com/ccsert/artifact-gateway/internal/ocigraph"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestOCIReferrersDescriptorAnnotationsFromVerifiedManifest(t *testing.T) {
	store := repository.NewMemoryStore()
	objects := objectstore.NewMemoryStore()
	testOCIReferrerAnnotations(t, store, store, objects, objects)
}

func testOCIReferrerAnnotations(t *testing.T, store GatewayStore, persistence discovery.Store, objects, control ObjectStore) {
	t.Helper()
	actual := map[string]string{"org.opencontainers.image.description": "actual manifest annotation", "org.example.sbom.generator": "owned fixture"}
	for _, index := range []bool{false, true} {
		for _, tc := range []struct {
			name       string
			advertised map[string]string
			invalid    bool
		}{
			{"omitted", nil, false},
			{"partial", map[string]string{"org.example.sbom.generator": "owned fixture"}, false},
			{"matching", actual, false},
			{"conflicting", map[string]string{"org.opencontainers.image.description": "unverified replacement"}, true},
			{"forged addition", map[string]string{"org.example.unverified": "not in the manifest"}, true},
		} {
			kind := "manifest"
			if index {
				kind = "index"
			}
			t.Run(kind+"/"+tc.name, func(t *testing.T) {
				f := newDiscoveryArtifactFixture(t, `{"SPDXID":"verified annotations"}`)
				var document map[string]any
				media := ocigraph.OCIManifest
				artifactType := f.refs[0].ArtifactType
				if index {
					media = ocigraph.OCIIndex
					artifactType = "application/example.index"
					document = map[string]any{"schemaVersion": 2, "mediaType": media, "subject": f.subject, "artifactType": artifactType, "manifests": f.refs}
				} else if err := json.Unmarshal(f.objects[f.refs[0].Digest].body, &document); err != nil {
					t.Fatal(err)
				}
				document["annotations"] = actual
				body, err := json.Marshal(document)
				if err != nil {
					t.Fatal(err)
				}
				ref := discovery.Descriptor{Digest: ocigraph.Digest(body), Size: int64(len(body)), MediaType: media, ArtifactType: artifactType, Annotations: tc.advertised}
				f.objects[ref.Digest] = discoveryFixtureObject{body: body, media: media}
				f.refs = []discovery.Descriptor{ref}
				fixture := newProxyRedirectFixture(t, f.serve)
				repo := catalogProxy(t, store, "annotations-"+uuid.NewString()[:8])
				handler := newTestGateway(t, GatewayOptions{Store: store, OCIDiscoveryStore: persistence, Adapter: TestAdapter{}, Authenticator: testAuthenticator(), Storage: ObjectStores{repository.FormatOCI: objects}, Caches: GatewayCaches{OCI: NewDefaultOCICache(control, []string{redirectOrigin, redirectCDN})}, Upstream: fixture.client})
				path := "/v2/" + repo.Name + "/team/app/referrers/" + f.subject.Digest
				response := discoveryRequest(t, handler, path, "admin-secret")
				binding, bindingErr := persistence.GetOCISubjectBinding(t.Context(), "repository:"+repo.ID, discovery.OwnerForActor("alice"), "team/app", f.subject.Digest)
				if tc.invalid {
					if response.Code != http.StatusBadGateway || !strings.Contains(response.Body.String(), "MANIFEST_INVALID") || bindingErr != discovery.ErrNotFound {
						t.Fatalf("unverified annotations accepted: status=%d body=%s binding=%v", response.Code, response.Body.String(), bindingErr)
					}
					return
				}
				refs, err := discovery.DecodeIndex(response.Body.Bytes())
				if response.Code != http.StatusOK || err != nil || len(refs) != 1 || !maps.Equal(refs[0].Annotations, actual) || refs[0].ArtifactType != artifactType {
					t.Fatalf("verified annotations missing: status=%d body=%s err=%v", response.Code, response.Body.String(), err)
				}
				if bindingErr != nil || len(binding.Referrers) != 1 || !maps.Equal(binding.Referrers[0].Annotations, actual) {
					t.Fatalf("verified annotation receipt missing: binding=%+v err=%v", binding, bindingErr)
				}
				original := discoveryRequest(t, handler, "/v2/"+repo.Name+"/team/app/manifests/"+ref.Digest, "admin-secret")
				if original.Code != http.StatusOK || !bytes.Equal(original.Body.Bytes(), body) || original.Header().Get("Docker-Content-Digest") != ref.Digest {
					t.Fatalf("annotation validation changed source bytes: status=%d body=%s", original.Code, original.Body.String())
				}
				again := discoveryRequest(t, handler, path, "admin-secret")
				if again.Code != http.StatusOK || !bytes.Equal(again.Body.Bytes(), response.Body.Bytes()) {
					t.Fatalf("verified annotation receipt drifted: status=%d body=%s", again.Code, again.Body.String())
				}
			})
		}
	}
}
