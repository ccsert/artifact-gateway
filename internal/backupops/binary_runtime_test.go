package backupops

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ccsert/artifact-gateway/internal/backupmanifest"
)

func TestBinaryRuntimePinsBelongToTrustedIndex(t *testing.T) {
	body, err := os.ReadFile("testdata/binary-runtime-index.json")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != "afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab" {
		t.Fatal("original trusted runtime index bytes changed")
	}
	var index struct {
		MediaType string `json:"mediaType"`
		Manifests []struct {
			Digest   string                            `json:"digest"`
			Platform struct{ OS, Architecture string } `json:"platform"`
		} `json:"manifests"`
	}
	if json.Unmarshal(body, &index) != nil || index.MediaType != "application/vnd.oci.image.index.v1+json" {
		t.Fatal("trusted runtime index is invalid")
	}
	want := map[string]string{
		"linux/amd64": "sha256:52dcfbabb7457ea47c82f6e13af8c8a4a1d9f7b0145142b3ecab20f2b888411d",
		"linux/arm64": "sha256:06c3c14b7fa252d9f4285242d2d665ce3b51f74b02d3c529464f220f2912aecb",
	}
	for platform, digest := range want {
		matches := 0
		for _, descriptor := range index.Manifests {
			if descriptor.Platform.OS+"/"+descriptor.Platform.Architecture == platform && descriptor.Digest == digest {
				matches++
			}
		}
		if matches != 1 {
			t.Fatalf("platform %s lacks one exact trusted child descriptor", platform)
		}
	}
}

func TestStartGatewayUsesVerifiedBinaryRuntimePlatform(t *testing.T) {
	for _, tc := range []struct{ platform, image string }{
		{"linux/amd64", "gcr.io/distroless/static-debian12:nonroot@sha256:52dcfbabb7457ea47c82f6e13af8c8a4a1d9f7b0145142b3ecab20f2b888411d"},
		{"linux/arm64", "gcr.io/distroless/static-debian12:nonroot@sha256:06c3c14b7fa252d9f4285242d2d665ce3b51f74b02d3c529464f220f2912aecb"},
	} {
		t.Run(tc.platform, func(t *testing.T) {
			dir := t.TempDir()
			binary := []byte("verified synthetic executable")
			if err := os.WriteFile(filepath.Join(dir, "gateway"), binary, 0500); err != nil {
				t.Fatal(err)
			}
			log := filepath.Join(dir, "create-arguments")
			// Stop at the public Docker boundary after recording creation. The
			// real PG/RustFS fixtures exercise startup, reads and owned cleanup.
			script := "#!/bin/sh\nif [ \"$3\" = create ]; then\nprintf '%s\\n' \"$@\" > '" + strings.ReplaceAll(log, "'", "'\"'\"'") + "'\nprintf '%s\\n' '" + strings.Repeat("c", 64) + "'\nexit 0\nfi\nexit 1\n"
			if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			sum := sha256.Sum256(binary)
			target := &OwnedTarget{checked: true, spec: TargetSpec{Project: "ag-restore-synthetic", Docker: Docker{Context: "synthetic"}, Release: Release{Directory: dir, Identity: backupmanifest.GatewayIdentity{Artifact: &backupmanifest.SoftwareArtifact{Kind: "binary", Platform: tc.platform, SHA256: "sha256:" + hex.EncodeToString(sum[:])}}}}}
			defer func() { _ = os.RemoveAll(target.directory) }()
			if _, err := target.StartGateway(context.Background()); err == nil || err.Error() != "restored Gateway startup failed" {
				t.Fatalf("did not reach the controlled Docker start boundary: %v", err)
			}
			data, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			args := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
			if args[len(args)-1] != tc.image {
				t.Fatalf("verified %s binary selected runtime %q, want %q", tc.platform, args[len(args)-1], tc.image)
			}
			platform := ""
			for i, arg := range args[:len(args)-1] {
				if arg == "--platform" {
					platform = args[i+1]
				}
			}
			if platform != tc.platform {
				t.Fatalf("Docker platform=%q want verified %q", platform, tc.platform)
			}
		})
	}
}

func TestNewTargetRejectsUnsupportedBinaryPlatformBeforeDocker(t *testing.T) {
	for _, platform := range []string{"", "darwin/arm64", "linux/s390x", "linux/arm64/v8"} {
		t.Run(platform, func(t *testing.T) {
			dir := t.TempDir()
			marker := filepath.Join(dir, "docker-called")
			script := "#!/bin/sh\nprintf called > '" + strings.ReplaceAll(marker, "'", "'\"'\"'") + "'\nexit 1\n"
			if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			spec := TargetSpec{Project: "ag-restore-unsupported", Docker: Docker{Context: "synthetic"}, PostgresPassword: "fixture-password", AccessKey: "fixture-access", SecretKey: "fixture-secret", RPCSecret: strings.Repeat("r", 32), AdminToken: "fixture-admin", ResolverToken: "fixture-resolver", Release: Release{Identity: backupmanifest.GatewayIdentity{Artifact: &backupmanifest.SoftwareArtifact{Kind: "binary", Platform: platform}}}}
			if _, err := NewTarget(context.Background(), spec); err == nil || err.Error() != "unsupported binary runtime platform" {
				t.Fatalf("unsupported platform outcome=%v", err)
			}
			target := &OwnedTarget{checked: true, spec: spec}
			if _, err := target.StartGateway(context.Background()); err == nil || err.Error() != "unsupported binary runtime platform" || target.directory != "" {
				t.Fatalf("unsupported direct startup staged or accepted a runtime: directory=%q err=%v", target.directory, err)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("unsupported platform contacted Docker before rejection: %v", err)
			}
		})
	}
}
