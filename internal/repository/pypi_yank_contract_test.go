package repository

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

type pypiYankContractStore interface {
	NativePyPIStore
	PyPIYankStore
	ListAudits(context.Context, AuditQuery) ([]AuditRecord, error)
}

func TestPyPIYankMemoryContract(t *testing.T) {
	runPyPIYankContract(t, func(t *testing.T) (pypiYankContractStore, HostedRepository) {
		store := NewMemoryStore()
		repo, err := store.CreateHostedRepository(t.Context(), HostedRepository{ID: uuid.NewString(), Name: "yank-" + uuid.NewString()[:8], Format: FormatPyPI})
		if err != nil {
			t.Fatal(err)
		}
		return store, repo
	})
}

func runPyPIYankContract(t *testing.T, factory func(*testing.T) (pypiYankContractStore, HostedRepository)) {
	t.Helper()
	t.Run("file_version_repeat_restore_and_immutable_identity", func(t *testing.T) {
		store, repo := factory(t)
		files := make([]PyPIFile, 0, 2)
		for _, name := range []string{"widget-1.0.tar.gz", "widget-1.0-py3-none-any.whl"} {
			file, err := store.PublishPyPIFile(t.Context(), PyPIFile{RepositoryID: repo.ID, Project: "widget", Version: "1.0", Filename: name, Digest: "sha256:" + strings.Repeat("a", 64), ObjectKey: "native/pypi/" + name, Size: 42})
			if err != nil {
				t.Fatal(err)
			}
			files = append(files, file)
		}
		audit := AuditRecord{Repository: repo.Name, Actor: "writer", Format: "pypi", Operation: "pypi.yank", Outcome: AuditResolved, Status: 200}
		mutation := PyPIYankMutation{RepositoryID: repo.ID, Project: "widget", Version: "1.0", Filename: files[0].Filename, Yanked: true, Reason: "bad ABI", ExpectedRevision: PyPIYankRevision(files), Audit: audit}
		changed, err := store.SetPyPIYank(t.Context(), mutation)
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range changed {
			if file.Yanked != (file.Filename == mutation.Filename) {
				t.Fatalf("file scope=%+v", changed)
			}
		}
		mutation.ExpectedRevision = PyPIYankRevision(changed)
		repeated, err := store.SetPyPIYank(t.Context(), mutation)
		if err != nil || PyPIYankRevision(repeated) != mutation.ExpectedRevision {
			t.Fatalf("repeat=%+v %v", repeated, err)
		}
		mutation.Filename = ""
		changed, err = store.SetPyPIYank(t.Context(), mutation)
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range changed {
			if !file.Yanked || file.YankReason != "bad ABI" || file.Digest != files[0].Digest || file.Size != 42 {
				t.Fatalf("version state=%+v", changed)
			}
		}
		beforeDelete := PyPIYankRevision(changed)
		if _, err = store.TombstonePyPIVersion(t.Context(), repo.ID, "widget", "1.0"); err != nil {
			t.Fatal(err)
		}
		if _, err = store.SetPyPIYank(t.Context(), mutation); !errors.Is(err, ErrNotFound) {
			t.Fatalf("tombstone mutation=%v", err)
		}
		restored, err := store.RestorePyPIVersion(t.Context(), repo.ID, "widget", "1.0")
		if err != nil || PyPIYankRevision(restored) == beforeDelete {
			t.Fatalf("restore=%+v %v", restored, err)
		}
		for _, file := range restored {
			if !file.Yanked || file.YankReason != "bad ABI" || file.Digest != files[0].Digest || file.Size != 42 {
				t.Fatalf("restore erased selection metadata=%+v", restored)
			}
		}
		mutation.ExpectedRevision = beforeDelete
		if _, err = store.SetPyPIYank(t.Context(), mutation); !errors.Is(err, ErrVersionConflict) {
			t.Fatalf("restore ABA accepted=%v", err)
		}
		mutation.ExpectedRevision = PyPIYankRevision(restored)
		mutation.Yanked = false
		mutation.Audit.Operation = "pypi.unyank"
		cleared, err := store.SetPyPIYank(t.Context(), mutation)
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range cleared {
			if file.Yanked || file.YankReason != "" {
				t.Fatalf("unyank=%+v", cleared)
			}
		}
		audits, err := store.ListAudits(t.Context(), AuditQuery{Repository: repo.Name, Operation: "pypi.yank"})
		if err != nil || len(audits) != 3 {
			t.Fatalf("successful audit count=%+v %v", audits, err)
		}
	})
	t.Run("proxy_revalidation_preserves_cached_bytes_and_updates_selection", func(t *testing.T) {
		store, repo := factory(t)
		file := PyPIFile{RepositoryID: repo.ID, Project: "widget", Version: "2.0", Filename: "widget-2.0.tar.gz", Digest: "sha256:" + strings.Repeat("b", 64), SourceURL: "https://upstream.example/widget-2.0.tar.gz", Yanked: true, YankReason: "upstream reason"}
		if err := store.SyncPyPIProxyFiles(t.Context(), repo.ID, "widget", []PyPIFile{file}); err != nil {
			t.Fatal(err)
		}
		file.ObjectKey = "cached/pypi"
		file.Size = 77
		cached, err := store.CachePyPIProxyFile(t.Context(), file)
		if err != nil {
			t.Fatal(err)
		}
		file.Yanked = false
		file.YankReason = ""
		file.ObjectKey = ""
		if err = store.SyncPyPIProxyFiles(t.Context(), repo.ID, "widget", []PyPIFile{file}); err != nil {
			t.Fatal(err)
		}
		current, err := store.GetPyPIFile(t.Context(), repo.ID, file.Filename)
		if err != nil || current.Yanked || current.YankReason != "" || current.ObjectKey != cached.ObjectKey || current.Size != cached.Size || current.Digest != cached.Digest || current.YankRevision <= cached.YankRevision {
			t.Fatalf("revalidation=%+v err=%v", current, err)
		}
	})
	t.Run("failed_revalidation_never_partially_unyanks", func(t *testing.T) {
		store, repo := factory(t)
		files := []PyPIFile{
			{RepositoryID: repo.ID, Project: "widget", Version: "3.0", Filename: "widget-3.0.tar.gz", Digest: "sha256:" + strings.Repeat("a", 64), SourceURL: "https://upstream.example/sdist", Yanked: true, YankReason: "keep warning"},
			{RepositoryID: repo.ID, Project: "widget", Version: "3.0", Filename: "widget-3.0-py3-none-any.whl", Digest: "sha256:" + strings.Repeat("b", 64), SourceURL: "https://upstream.example/wheel", Yanked: true, YankReason: "keep warning"},
		}
		if err := store.SyncPyPIProxyFiles(t.Context(), repo.ID, "widget", files); err != nil {
			t.Fatal(err)
		}
		before, err := store.ListPyPIProjectFiles(t.Context(), repo.ID, "widget")
		if err != nil {
			t.Fatal(err)
		}
		incoming := append([]PyPIFile(nil), files...)
		incoming[0].Yanked = false
		incoming[0].YankReason = ""
		incoming[1].Digest = "sha256:" + strings.Repeat("c", 64)
		if err = store.SyncPyPIProxyFiles(t.Context(), repo.ID, "widget", incoming); !errors.Is(err, ErrUpstreamChanged) {
			t.Fatalf("changed archive=%v", err)
		}
		if err = store.SyncPyPIProxyFiles(t.Context(), repo.ID, "widget", []PyPIFile{incoming[0], files[0]}); !errors.Is(err, ErrUpstreamChanged) {
			t.Fatalf("ambiguous repeated file=%v", err)
		}
		after, err := store.ListPyPIProjectFiles(t.Context(), repo.ID, "widget")
		if err != nil || PyPIYankRevision(before) != PyPIYankRevision(after) {
			t.Fatalf("failed revalidation changed metadata=%+v %v", after, err)
		}
	})
}
