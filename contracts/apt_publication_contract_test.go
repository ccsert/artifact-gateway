package contracts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAPTPublicationManagementContract(t *testing.T) {
	spec := loadManagementRuntimeSpec(t)

	create := operation(t, spec, "/repositories/{repositoryId}/apt/publication-sessions", "POST")
	if !hasParameter(create.Parameters, "Idempotency-Key", "header") {
		t.Fatal("APT publication session creation must require Idempotency-Key")
	}
	for _, status := range []string{"201", "400", "401", "403", "404", "409", "500", "507"} {
		requireResponse(t, create, status)
	}
	get := operation(t, spec, "/repositories/{repositoryId}/apt/publication-sessions/{sessionId}", "GET")
	for _, status := range []string{"200", "401", "403", "404", "500"} {
		requireResponse(t, get, status)
	}
	upload := operation(t, spec, "/repositories/{repositoryId}/apt/publication-sessions/{sessionId}/package", "PUT")
	if upload.RequestBody == nil || upload.RequestBody.Value == nil || upload.RequestBody.Value.Content["application/vnd.debian.binary-package"] == nil {
		t.Fatal("APT package upload must use application/vnd.debian.binary-package")
	}
	for _, status := range []string{"200", "400", "401", "403", "404", "409", "415", "422", "500", "507"} {
		requireResponse(t, upload, status)
	}
	publish := operation(t, spec, "/repositories/{repositoryId}/apt/snapshots", "POST")
	if !hasParameter(publish.Parameters, "Idempotency-Key", "header") {
		t.Fatal("APT snapshot publication must require Idempotency-Key")
	}
	for _, status := range []string{"201", "400", "401", "403", "404", "409", "503", "507", "500"} {
		requireResponse(t, publish, status)
	}
	snapshot := spec.Components.Schemas["APTRepositorySnapshot"]
	if snapshot == nil || snapshot.Value == nil {
		t.Fatal("APTRepositorySnapshot schema is missing")
	}
	for _, property := range []string{"id", "repositoryId", "suite", "sequence", "state", "releaseDigest", "inReleaseDigest", "signerIdentity", "keyFingerprint", "signatureAlgorithm", "createdAt", "publishedAt"} {
		if snapshot.Value.Properties[property] == nil {
			t.Fatalf("APTRepositorySnapshot.%s is missing", property)
		}
	}

	session := spec.Components.Schemas["APTPublicationSession"]
	if session == nil || session.Value == nil || session.Value.Properties["state"] == nil || session.Value.Properties["state"].Value == nil {
		t.Fatal("APTPublicationSession.state is missing")
	}
	wantStates := map[any]bool{"open": true, "uploading": true, "staged": true, "aborted": true}
	for _, state := range session.Value.Properties["state"].Value.Enum {
		delete(wantStates, state)
	}
	if len(wantStates) != 0 {
		t.Fatalf("APTPublicationSession states are incomplete: %#v", wantStates)
	}

	problem := spec.Components.Schemas["Problem"]
	codes := make(map[any]bool)
	for _, code := range problem.Value.Properties["code"].Value.Enum {
		codes[code] = true
	}
	for _, code := range []string{"identity_mismatch", "digest_mismatch", "quota_exceeded", "unsupported_media_type", "signer_unavailable"} {
		if !codes[code] {
			t.Fatalf("Problem.code is missing %s", code)
		}
	}
}

func TestAPTPublicationGeneratedConsoleClientAcceptsBinaryFiles(t *testing.T) {
	generated, err := os.ReadFile(filepath.Join("..", "console", "src", "client", "types.gen.ts"))
	if err != nil {
		t.Fatal(err)
	}
	types := string(generated)
	start := strings.Index(types, "export type UploadAptPublicationPackageData")
	if start < 0 {
		t.Fatal("generated APT upload type is missing")
	}
	end := strings.Index(types[start:], "export type UploadAptPublicationPackageErrors")
	if end < 0 || !strings.Contains(types[start:start+end], "body: Blob | File") {
		t.Fatalf("generated APT upload must accept Blob | File: %s", types[start:start+end])
	}
}
