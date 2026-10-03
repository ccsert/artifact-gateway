//go:build linux || darwin

package localcapacity_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/localcapacity"
)

func TestNativeObservationReadsOnlyOwnedTemporaryFilesystemMetadata(t *testing.T) {
	root := t.TempDir()
	logs := filepath.Join(root, "logs")
	if err := os.Mkdir(logs, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "synthetic-marker")
	contents := []byte("owned synthetic content; capacity observation must preserve it")
	if err := os.WriteFile(marker, contents, 0600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(marker)
	if err != nil {
		t.Fatal(err)
	}
	o := observer(t, map[localcapacity.Alias]string{localcapacity.Temporary: root, localcapacity.Logs: logs}, localcapacity.Options{})
	s := o.Snapshot(context.Background())
	for _, m := range s.Mounts[:2] {
		if m.Status != localcapacity.Available || m.TotalBytes == nil || *m.TotalBytes <= 0 || m.AvailableBytes == nil || *m.AvailableBytes < 0 || *m.AvailableBytes > *m.TotalBytes || m.SampleAt == nil || m.SampleAt.After(s.CheckedAt) || len(m.SharedWith) != 1 {
			t.Fatalf("owned temporary filesystem observation=%#v", m)
		}
	}
	if s.Mounts[2].Status != localcapacity.NotConfigured {
		t.Fatalf("unconfigured backup location was probed: %#v", s.Mounts[2])
	}
	after, err := os.Stat(marker)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(marker)
	if err != nil || !bytes.Equal(actual, contents) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) || before.Mode() != after.Mode() {
		t.Fatal("owned synthetic marker changed during observation")
	}
}

func TestNativeObservationRejectsFileAndFinalSymlinkWithoutReadingContents(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "synthetic-file")
	if err := os.WriteFile(file, []byte("owned synthetic bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "synthetic-link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	o := observer(t, map[localcapacity.Alias]string{localcapacity.Temporary: file, localcapacity.Logs: link, localcapacity.Backups: root}, localcapacity.Options{})
	s := o.Snapshot(context.Background())
	for _, m := range s.Mounts[:2] {
		if m.Status != localcapacity.Unknown || m.Reason != localcapacity.ReasonReadFailed || m.TotalBytes != nil || m.AvailableBytes != nil {
			t.Fatalf("file or final symlink accepted: %#v", m)
		}
	}
	if s.Mounts[2].Status != localcapacity.Available {
		t.Fatalf("invalid locations suppressed independent directory: %#v", s.Mounts[2])
	}
}

func TestNativeObservationRequiresDirectoryReadPermission(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission fixture must run without root privilege")
	}
	root := t.TempDir()
	unreadable := filepath.Join(root, "synthetic-unreadable")
	if err := os.Mkdir(unreadable, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(unreadable, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(unreadable, 0700); err != nil {
			t.Error(err)
		}
	})
	o := observer(t, map[localcapacity.Alias]string{localcapacity.Temporary: unreadable, localcapacity.Logs: root}, localcapacity.Options{})
	s := o.Snapshot(context.Background())
	if s.Mounts[0].Status != localcapacity.Unknown || s.Mounts[0].Reason != localcapacity.ReasonReadFailed || s.Mounts[0].AvailableBytes != nil || s.Mounts[0].TotalBytes != nil {
		t.Fatal("directory read permission was bypassed")
	}
	if s.Mounts[1].Status != localcapacity.Available {
		t.Fatal("denial erased the independent directory")
	}
}
