package aptpublication

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/objectstore"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestManagerStagesServerDerivedDebianIdentityWithoutPublishingIt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: "apt-hosted", Name: "apt-hosted", Format: repository.FormatAPT, Type: repository.RepositoryTypeHosted})
	if err != nil {
		t.Fatal(err)
	}
	objects := objectstore.NewMemoryStore()
	manager := NewManager(store, objects)
	deb := testDebianPackage(t, "Package: widget\nVersion: 1:2.0-3\nArchitecture: amd64\n")
	digest := sha256.Sum256(deb)
	declaredDigest := "sha256:" + hex.EncodeToString(digest[:])

	session, replayed, err := manager.CreateSession(ctx, CreateSessionInput{
		ID: "session-one", RepositoryID: repo.ID, Suite: "stable", Component: "main", Publisher: "ci",
		ObjectName: "widget_2.0-3_amd64.deb", DeclaredDigest: declaredDigest, DeclaredSize: int64(len(deb)),
		ExpectedIdentity: "widget@1:2.0-3#amd64", IdempotencyKey: "build-42",
		ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil || replayed {
		t.Fatalf("session=%#v replayed=%t err=%v", session, replayed, err)
	}
	revision, err := manager.UploadPackage(ctx, session.ID, session.ObjectName, bytes.NewReader(deb), int64(len(deb)))
	if err != nil {
		t.Fatal(err)
	}
	if revision.Package != "widget" || revision.Version != "1:2.0-3" || revision.Architecture != "amd64" || revision.CanonicalIdentity != "widget@1:2.0-3#amd64" {
		t.Fatalf("revision=%#v", revision)
	}
	if revision.ObjectKey != "native/apt/sha256/"+strings.TrimPrefix(declaredDigest, "sha256:") {
		t.Fatalf("object key=%q", revision.ObjectKey)
	}
	info, err := objects.Stat(ctx, revision.ObjectKey)
	if err != nil || info.Size != int64(len(deb)) || info.Digest != declaredDigest {
		t.Fatalf("object info=%#v err=%v", info, err)
	}
	if _, err = store.GetAPTAsset(ctx, repo.ID, "pool/main/w/widget/widget_2.0-3_amd64.deb"); err != repository.ErrNotFound {
		t.Fatalf("staged package leaked into protocol reads: %v", err)
	}
}

func TestManagerRejectsClientIdentityMismatchBeforeCreatingObjectIntent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: "apt-hosted", Name: "apt-hosted", Format: repository.FormatAPT, Type: repository.RepositoryTypeHosted})
	if err != nil {
		t.Fatal(err)
	}
	objects := objectstore.NewMemoryStore()
	manager := NewManager(store, objects)
	deb := testDebianPackage(t, "Package: actual\nVersion: 1.0-1\nArchitecture: arm64\n")
	digest := sha256.Sum256(deb)
	session, _, err := manager.CreateSession(ctx, CreateSessionInput{
		ID: "session-one", RepositoryID: repo.ID, Suite: "stable", Component: "main", Publisher: "ci",
		ObjectName: "actual_1.0-1_arm64.deb", DeclaredDigest: "sha256:" + hex.EncodeToString(digest[:]), DeclaredSize: int64(len(deb)),
		ExpectedIdentity: "different@1.0-1#arm64", IdempotencyKey: "build-42", ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = manager.UploadPackage(ctx, session.ID, session.ObjectName, bytes.NewReader(deb), int64(len(deb))); err != ErrIdentityMismatch {
		t.Fatalf("identity mismatch error=%v", err)
	}
	stored, err := store.GetAPTPublicationSession(ctx, session.ID)
	if err != nil || stored.State != repository.APTPublicationSessionOpen || stored.ObjectKey != "" {
		t.Fatalf("session mutated before identity validation: %#v err=%v", stored, err)
	}
	if keys, err := objects.List(ctx, "native/apt/"); err != nil || len(keys) != 0 {
		t.Fatalf("unexpected staged objects=%v err=%v", keys, err)
	}
}

func TestManagerUploadRejectsMissingDependenciesWithoutPanicking(t *testing.T) {
	t.Parallel()
	manager := NewManager(nil, nil)
	if _, err := manager.UploadPackage(context.Background(), "session", "package.deb", strings.NewReader("package"), 7); err != ErrInvalidSessionInput {
		t.Fatalf("upload error=%v", err)
	}
}

func testDebianPackage(t *testing.T, control string) []byte {
	t.Helper()
	controlArchive := testTarGzip(t, "./control", []byte(control))
	dataArchive := testTarGzip(t, "", nil)
	var deb bytes.Buffer
	deb.WriteString("!<arch>\n")
	writeARMember(t, &deb, "debian-binary", []byte("2.0\n"))
	writeARMember(t, &deb, "control.tar.gz", controlArchive)
	writeARMember(t, &deb, "data.tar.gz", dataArchive)
	return deb.Bytes()
}

func testTarGzip(t *testing.T, name string, body []byte) []byte {
	t.Helper()
	var output bytes.Buffer
	gzipWriter := gzip.NewWriter(&output)
	tarWriter := tar.NewWriter(gzipWriter)
	if name != "" {
		if err := tarWriter.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func writeARMember(t *testing.T, output io.Writer, name string, body []byte) {
	t.Helper()
	header := fmt.Sprintf("%-16s%-12d%-6d%-6d%-8o%-10d`\n", name+"/", 0, 0, 0, 0o100644, len(body))
	if len(header) != 60 {
		t.Fatalf("ar header length=%d", len(header))
	}
	if _, err := io.WriteString(output, header); err != nil {
		t.Fatal(err)
	}
	if _, err := output.Write(body); err != nil {
		t.Fatal(err)
	}
	if len(body)%2 != 0 {
		if _, err := io.WriteString(output, "\n"); err != nil {
			t.Fatal(err)
		}
	}
}
