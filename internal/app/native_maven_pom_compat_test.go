package app

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestNativeMavenPublishesOriginalLatin1POM(t *testing.T) {
	pom, err := os.ReadFile("testdata/maven-pom-compat/commons-parent-48.pom")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(pom)
	if got := hex.EncodeToString(sum[:]); got != "1e1f7de9370a7b7901f128f1dacd1422be74e3f47f9558b0f79e04c0637ca0b4" {
		t.Fatalf("original Central POM SHA-256=%s", got)
	}
	for _, strict := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "coordinate-commit"}[strict], func(t *testing.T) {
			assertMavenPOMPublication(t, strict, "org.apache.commons:commons-parent:48", pom)
		})
	}
}

func TestNativeMavenPublishesOriginalParentVersionPOM(t *testing.T) {
	pom, err := os.ReadFile("testdata/maven-pom-compat/flyway-database-postgresql-11.7.2.pom")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(pom)
	if got := hex.EncodeToString(sum[:]); got != "1daf89be859c8f8a66661d88aff79da76382aa104e50b9071f37bc74d78d8b29" {
		t.Fatalf("original Central POM SHA-256=%s", got)
	}
	for _, strict := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "coordinate-commit"}[strict], func(t *testing.T) {
			assertMavenPOMPublication(t, strict, "org.flywaydb:flyway-database-postgresql:11.7.2", pom)
		})
	}
}

func TestNativeMavenRejectsUnresolvedIdentityEvenWhenPathMatches(t *testing.T) {
	pom := []byte(`<project><groupId>org.example</groupId><artifactId>widget</artifactId><version>${revision}</version></project>`)
	for _, strict := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "coordinate-commit"}[strict], func(t *testing.T) {
			assertMavenPOMRejected(t, strict, "org.example:widget:${revision}", pom)
		})
	}
}

func TestNativeMavenPOMCompatibilityAndRefusalBoundaries(t *testing.T) {
	for _, fixture := range []struct {
		file, coordinate, sha256 string
	}{
		{"commons-parent-39.pom", "org.apache.commons:commons-parent:39", "87cd27e1a02a5c3eb6d85059ce98696bb1b44c2b8b650f0567c86df60fa61da7"},
		{"commons-parent-64.pom", "org.apache.commons:commons-parent:64", "6f19638994e8357b4ed734696f992057efaafa1235673998133299798e2ccddb"},
		{"objenesis-3.3.pom", "org.objenesis:objenesis:3.3", "ba0c40da2669a048b6e24ef7066a471f0fbcbfcc509e6a3e856ca4ddfa614ad3"},
	} {
		t.Run(fixture.file, func(t *testing.T) {
			pom, err := os.ReadFile("testdata/maven-pom-compat/" + fixture.file)
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(pom)
			if hex.EncodeToString(sum[:]) != fixture.sha256 {
				t.Fatal("original Central POM bytes changed")
			}
			for _, strict := range []bool{false, true} {
				assertMavenPOMPublication(t, strict, fixture.coordinate, pom)
			}
		})
	}
	for name, pom := range map[string][]byte{
		"real-latin1-with-inherited-identity": []byte("<?xml version=\"1.0\" encoding=\"iso-8859-1\"?><project><parent><groupId>org.example</groupId><version>1.0.0</version></parent><artifactId>widget</artifactId><description>Caf\xe9</description></project>"),
		"utf8-explicit-identity":              []byte(`<?xml version="1.0" encoding="UTF-8"?><project><groupId>org.example</groupId><artifactId>widget</artifactId><version>1.0.0</version><description>Café</description></project>`),
	} {
		t.Run(name, func(t *testing.T) {
			for _, strict := range []bool{false, true} {
				assertMavenPOMPublication(t, strict, "org.example:widget:1.0.0", pom)
			}
		})
	}
	identity := `<groupId>org.example</groupId><artifactId>widget</artifactId><version>1.0.0</version>`
	for name, pom := range map[string][]byte{
		"unknown-encoding":          []byte(`<?xml version="1.0" encoding="invented"?><project>` + identity + `</project>`),
		"wrong-encoding":            []byte("<?xml version=\"1.0\" encoding=\"UTF-8\"?><project>" + identity + "<description>Caf\xe9</description></project>"),
		"invalid-xml":               []byte(`<project>` + identity + `<description></project>`),
		"trailing-root":             []byte(`<project>` + identity + `</project><extra/>`),
		"different-version":         []byte(`<project><groupId>org.example</groupId><artifactId>widget</artifactId><version>2.0.0</version></project>`),
		"unresolved-version":        []byte(`<project><groupId>org.example</groupId><artifactId>widget</artifactId><version>${revision}</version></project>`),
		"environment-version":       []byte(`<project><groupId>org.example</groupId><artifactId>widget</artifactId><version>${env.POM_VERSION}</version></project>`),
		"unresolved-parent-version": []byte(`<project><parent><groupId>org.example</groupId><version>${revision}</version></parent><artifactId>widget</artifactId><version>${project.parent.version}</version></project>`),
		"missing-parent-version":    []byte(`<project><groupId>org.example</groupId><artifactId>widget</artifactId><version>${project.parent.version}</version></project>`),
	} {
		t.Run(name, func(t *testing.T) {
			for _, strict := range []bool{false, true} {
				assertMavenPOMRejected(t, strict, "org.example:widget:1.0.0", pom)
			}
		})
	}
	flyway, err := os.ReadFile("testdata/maven-pom-compat/flyway-database-postgresql-11.7.2.pom")
	if err != nil {
		t.Fatal(err)
	}
	for _, strict := range []bool{false, true} {
		assertMavenPOMRejected(t, strict, "org.flywaydb:flyway-database-postgresql:11.7.3", flyway)
	}
}

func TestNativeMavenPOMCompatibilityRetainsUploadGuardrails(t *testing.T) {
	pom, err := os.ReadFile("testdata/maven-pom-compat/objenesis-3.3.pom")
	if err != nil {
		t.Fatal(err)
	}
	for _, strict := range []bool{false, true} {
		store := repository.NewMemoryStore()
		_, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
			ID: uuid.NewString(), Name: "deploys", Format: repository.FormatMaven, MavenStrictPublication: strict,
		})
		if err != nil {
			t.Fatal(err)
		}
		handler := newNativeMavenHandler(store, NewMemoryOCIObjectStore(), testAuthenticator())
		const path = "/repository/maven/deploys/org/objenesis/objenesis/3.3/objenesis-3.3.pom"
		for _, fixture := range []struct {
			name, actor, password string
			body                  io.Reader
			status                int
		}{
			{"bad-credential", "maven", "wrong-fixture-secret", bytes.NewReader(pom), http.StatusUnauthorized},
			{"read-only-principal", "reader", "resolver-secret", bytes.NewReader(pom), http.StatusForbidden},
			{"oversize-upload", "maven", "resolver-secret", io.LimitReader(mavenFillReader{'x'}, 129<<20), http.StatusRequestEntityTooLarge},
		} {
			t.Run(fixture.name+map[bool]string{false: "/direct", true: "/strict"}[strict], func(t *testing.T) {
				request := httptest.NewRequest(http.MethodPut, path, fixture.body)
				request.SetBasicAuth(fixture.actor, fixture.password)
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				if response.Code != fixture.status {
					t.Fatalf("guardrail=%d %s, want %d", response.Code, response.Body.String(), fixture.status)
				}
			})
		}
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.SetBasicAuth("maven", "resolver-secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("rejected upload became visible: %d", response.Code)
		}
	}
}

func assertMavenPOMRejected(t *testing.T, strict bool, coordinate string, pom []byte) {
	t.Helper()
	store := repository.NewMemoryStore()
	_, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: uuid.NewString(), Name: "deploys", Format: repository.FormatMaven, MavenStrictPublication: strict,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := newNativeMavenHandler(store, NewMemoryOCIObjectStore(), testAuthenticator())
	parts := strings.Split(coordinate, ":")
	name := parts[1] + "-" + parts[2] + ".pom"
	path := "/repository/maven/deploys/" + strings.ReplaceAll(parts[0], ".", "/") + "/" + parts[1] + "/" + parts[2] + "/" + name
	request := httptest.NewRequest(http.MethodPut, path, bytes.NewReader(pom))
	request.SetBasicAuth("maven", "resolver-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if strict {
		if response.Code != http.StatusCreated {
			t.Fatalf("stage POM=%d %s", response.Code, response.Body.String())
		}
		body, err := json.Marshal(map[string]any{"expectedAssetNames": []string{name}})
		if err != nil {
			t.Fatal(err)
		}
		request = httptest.NewRequest(http.MethodPost, "/repository/maven/deploys/coordinates/"+coordinate+":commit", bytes.NewReader(body))
		request.SetBasicAuth("maven", "resolver-secret")
		request.Header.Set("Idempotency-Key", "pom-reject-commit")
		response = httptest.NewRecorder()
		handler.ServeHTTP(response, request)
	}
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid POM publication=%d %s", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodGet, path, nil)
	request.SetBasicAuth("maven", "resolver-secret")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("invalid POM became visible: %d", response.Code)
	}
}

// Exercise the same HTTP publication and read routes used by Maven clients.
func assertMavenPOMPublication(t *testing.T, strict bool, coordinate string, pom []byte) {
	t.Helper()
	store := repository.NewMemoryStore()
	_, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: uuid.NewString(), Name: "deploys", Format: repository.FormatMaven, MavenStrictPublication: strict,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := newNativeMavenHandler(store, NewMemoryOCIObjectStore(), testAuthenticator())
	parts := strings.Split(coordinate, ":")
	name := parts[1] + "-" + parts[2] + ".pom"
	path := "/repository/maven/deploys/" + strings.ReplaceAll(parts[0], ".", "/") + "/" + parts[1] + "/" + parts[2] + "/" + name
	request := httptest.NewRequest(http.MethodPut, path, bytes.NewReader(pom))
	request.SetBasicAuth("maven", "resolver-secret")
	upload := httptest.NewRecorder()
	handler.ServeHTTP(upload, request)
	if upload.Code != http.StatusCreated {
		t.Fatalf("PUT original POM=%d %s", upload.Code, upload.Body.String())
	}
	get := func(suffix string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, path+suffix, nil)
		request.SetBasicAuth("maven", "resolver-secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	if strict {
		if response := get(""); response.Code != http.StatusNotFound {
			t.Fatalf("POM visible before commit: %d", response.Code)
		}
		body, err := json.Marshal(map[string]any{"expectedAssetNames": []string{name}})
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPost, "/repository/maven/deploys/coordinates/"+coordinate+":commit", bytes.NewReader(body))
		request.SetBasicAuth("maven", "resolver-secret")
		request.Header.Set("Idempotency-Key", "pom-compat-commit")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("commit original POM=%d %s", response.Code, response.Body.String())
		}
	}
	if response := get(""); response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), pom) {
		t.Fatalf("original POM read=%d, exact bytes preserved=%t", response.Code, bytes.Equal(response.Body.Bytes(), pom))
	}
	md5sum, sha1sum, sha256sum := md5.Sum(pom), sha1.Sum(pom), sha256.Sum256(pom)
	// Release publication exposes these three sidecars; SHA-512 is currently
	// reserved for SNAPSHOT publication and is not added by this compatibility fix.
	for suffix, expected := range map[string]string{
		".md5": hex.EncodeToString(md5sum[:]), ".sha1": hex.EncodeToString(sha1sum[:]),
		".sha256": hex.EncodeToString(sha256sum[:]),
	} {
		if response := get(suffix); response.Code != http.StatusOK || response.Body.String() != expected+"\n" {
			t.Fatalf("original POM %s read=%d %q", suffix, response.Code, response.Body.String())
		}
	}
}
