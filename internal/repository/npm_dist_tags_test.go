package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func publishNPMDistTagFixture(t *testing.T, store NativeNPMStore, repositoryID, packageName, version string, tags map[string]string) NPMVersion {
	t.Helper()
	sum := sha256.Sum256([]byte(repositoryID + packageName + version))
	digest := "sha256:" + hex.EncodeToString(sum[:])
	published, err := store.PublishNPMVersion(context.Background(), NPMVersion{
		RepositoryID: repositoryID, PackageName: packageName, Version: version,
		Digest: digest, Integrity: "sha512-YQ==", Shasum: strings.Repeat("b", 40),
		TarballName: "widget-" + version + ".tgz", ObjectKey: "native/npm/sha256/" + strings.TrimPrefix(digest, "sha256:"), Size: 8,
		Manifest: json.RawMessage(`{"name":"` + packageName + `","version":"` + version + `"}`), Publisher: "release-bot",
	}, tags)
	if err != nil {
		t.Fatal(err)
	}
	return published
}

func npmDistTagMutation(repositoryID, packageName, tag, version string) NPMDistTagMutation {
	return NPMDistTagMutation{
		RepositoryID: repositoryID, PackageName: packageName, Tag: tag, Version: version,
		Audit: AuditRecord{Repository: repositoryID, GroupName: repositoryID, Actor: "release-bot", Outcome: AuditResolved, OccurredAt: time.Now().UTC(), Format: "npm", Resource: packageName, Operation: "npm.dist-tag.set"},
	}
}

// exerciseNPMDistTagContract is shared by the Memory test and the PostgreSQL
// integration test so both stores keep one dist-tag contract.
func exerciseNPMDistTagContract(t *testing.T, store interface {
	NativeNPMStore
	NPMRestoreStore
}, repositoryID string) {
	t.Helper()
	ctx := context.Background()
	const name = "@scope/tagged"
	first := publishNPMDistTagFixture(t, store, repositoryID, name, "1.0.0", map[string]string{"latest": "1.0.0"})
	publishNPMDistTagFixture(t, store, repositoryID, name, "1.1.0", map[string]string{"next": "1.1.0"})

	change, err := store.SetNPMDistTag(ctx, npmDistTagMutation(repositoryID, name, "beta", "1.1.0"))
	if err != nil || !change.Changed || change.Previous != "" || change.DistTags["beta"] != "1.1.0" || change.DistTags["latest"] != "1.0.0" {
		t.Fatalf("add beta=%#v err=%v", change, err)
	}
	replay, err := store.SetNPMDistTag(ctx, npmDistTagMutation(repositoryID, name, "beta", "1.1.0"))
	if err != nil || replay.Changed || replay.Previous != "1.1.0" || replay.DistTags["beta"] != "1.1.0" {
		t.Fatalf("replayed beta=%#v err=%v", replay, err)
	}
	moved, err := store.SetNPMDistTag(ctx, npmDistTagMutation(repositoryID, name, "beta", "1.0.0"))
	if err != nil || !moved.Changed || moved.Previous != "1.1.0" || moved.DistTags["beta"] != "1.0.0" {
		t.Fatalf("move beta=%#v err=%v", moved, err)
	}
	for _, version := range []string{"9.9.9", ""} {
		if _, err = store.SetNPMDistTag(ctx, npmDistTagMutation(repositoryID, name, "beta", version)); !errors.Is(err, ErrNPMDistTagTargetUnavailable) {
			t.Fatalf("unknown target %q err=%v", version, err)
		}
	}
	if _, err = store.SetNPMDistTag(ctx, npmDistTagMutation(repositoryID, "@scope/missing", "beta", "1.0.0")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing package err=%v", err)
	}

	// A tombstone drops every tag naming the version and the tag cannot be
	// pointed back at it until it is restored.
	if _, err = store.TombstoneNPMVersion(ctx, repositoryID, name, first.Version); err != nil {
		t.Fatal(err)
	}
	if _, err = store.SetNPMDistTag(ctx, npmDistTagMutation(repositoryID, name, "beta", first.Version)); !errors.Is(err, ErrNPMDistTagTargetUnavailable) {
		t.Fatalf("tombstoned target err=%v", err)
	}
	pkg, err := store.GetNPMPackage(ctx, repositoryID, name)
	if err != nil || pkg.DistTags["beta"] != "" || pkg.DistTags["latest"] != "" || pkg.DistTags["next"] != "1.1.0" {
		t.Fatalf("tags after tombstone=%#v err=%v", pkg.DistTags, err)
	}
	if _, err = store.RestoreNPMVersion(ctx, NPMRestoreRequest{RepositoryID: repositoryID, PackageName: name, Version: first.Version}); err != nil {
		t.Fatal(err)
	}
	if change, err = store.SetNPMDistTag(ctx, npmDistTagMutation(repositoryID, name, "beta", first.Version)); err != nil || change.DistTags["beta"] != first.Version {
		t.Fatalf("restored target=%#v err=%v", change, err)
	}

	removed, err := store.DeleteNPMDistTag(ctx, npmDistTagMutation(repositoryID, name, "beta", ""))
	if err != nil || !removed.Changed || removed.Previous != first.Version || removed.DistTags["beta"] != "" || removed.DistTags["next"] != "1.1.0" {
		t.Fatalf("remove beta=%#v err=%v", removed, err)
	}
	if _, err = store.DeleteNPMDistTag(ctx, npmDistTagMutation(repositoryID, name, "beta", "")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second remove err=%v", err)
	}
	pkg, err = store.GetNPMPackage(ctx, repositoryID, name)
	if err != nil || pkg.DistTags["beta"] != "" || pkg.DistTags["next"] != "1.1.0" || len(pkg.Versions) != 2 {
		t.Fatalf("final package=%#v err=%v", pkg, err)
	}
	for _, version := range pkg.Versions {
		if version.Version == first.Version && (version.Digest != first.Digest || version.ObjectKey != first.ObjectKey || version.Size != first.Size) {
			t.Fatalf("tag changes rewrote version identity: %#v want %#v", version, first)
		}
	}
}

func TestMemoryNPMDistTagContract(t *testing.T) {
	store := NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), HostedRepository{ID: "npm-tags", Name: "npm-tags", Format: FormatNPM})
	if err != nil {
		t.Fatal(err)
	}
	exerciseNPMDistTagContract(t, store, repo.ID)
	var evidence []map[string]string
	for _, audit := range store.Audits {
		if audit.Operation == "npm.dist-tag.set" && audit.Resource == "@scope/tagged" {
			evidence = append(evidence, audit.Evidence)
		}
	}
	// add, replay, move, restored add, and delete (the delete fixture reuses
	// the set operation name); refused mutations are not store audits.
	if len(evidence) != 5 || evidence[0]["changed"] != "true" || evidence[1]["changed"] != "false" || evidence[2]["previous"] != "1.1.0" || evidence[2]["version"] != "1.0.0" || evidence[4]["version"] != "" || evidence[4]["previous"] != "1.0.0" {
		t.Fatalf("dist-tag audit evidence=%#v", evidence)
	}
}

func TestMemoryNPMDistTagRejectsProxyPackages(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	if _, err := store.SyncNPMProxyPackage(ctx, NPMPackage{
		RepositoryID: "npm-proxy", Name: "widget", SourceEndpoint: "https://registry.example.test",
		DistTags: map[string]string{"latest": "1.0.0"},
		Versions: []NPMVersion{{Version: "1.0.0", Integrity: "sha512-YQ==", Shasum: strings.Repeat("b", 40), UpstreamTarball: "https://registry.example.test/widget-1.0.0.tgz"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetNPMDistTag(ctx, npmDistTagMutation("npm-proxy", "widget", "beta", "1.0.0")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("proxy set err=%v", err)
	}
	if _, err := store.DeleteNPMDistTag(ctx, npmDistTagMutation("npm-proxy", "widget", "latest", "")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("proxy delete err=%v", err)
	}
	if len(store.Audits) != 0 {
		t.Fatalf("refused proxy mutation was audited: %#v", store.Audits)
	}
}
