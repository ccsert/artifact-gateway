package backupops

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/backupmanifest"
	"github.com/artifact-gateway/artifact-gateway/internal/opsjson"
)

// FrozenSource is the read-only source boundary. ObserveFrozen must fail for
// active writers and return stable process stop identities for the declared
// scope. It does not stop or restart any production service.
type FrozenSource interface {
	ObserveFrozen(context.Context) (string, error)
	Dump(context.Context, io.Writer) error
	Ledger(context.Context) ([]byte, error)
	Metadata(context.Context) ([]byte, error)
	WalkObjects(context.Context, func(string, int64) error) error
	WalkReferences(context.Context, func(string) error) error
	Open(context.Context, string) (io.ReadCloser, int64, error)
}

// Destination exists only after the concrete adapter proves new resource
// ownership and isolation. Data restoration never accepts arbitrary endpoints.
type Destination interface {
	CheckEmpty(context.Context) error
	RestoreDatabase(context.Context, io.Reader) error
	Ledger(context.Context) ([]byte, error)
	Metadata(context.Context) ([]byte, error)
	Put(context.Context, string, io.ReadSeeker, int64, string) error
	Open(context.Context, string) (io.ReadCloser, int64, error)
}

// TransferReport separates byte recovery from source consistency. Operator
// declarations do not become independently verified consistency.
type TransferReport struct {
	SchemaVersion   int                             `json:"schemaVersion"`
	BackupID        string                          `json:"backupId,omitempty"`
	CheckedAt       time.Time                       `json:"checkedAt"`
	Status          string                          `json:"status"`
	Software        string                          `json:"software"`
	Database        string                          `json:"database"`
	GrantMetadata   string                          `json:"grantMetadata"`
	GroupMetadata   string                          `json:"groupMetadata"`
	AuditMetadata   string                          `json:"auditMetadata"`
	Metadata        string                          `json:"metadata"`
	Objects         string                          `json:"objects"`
	VerifiedObjects int64                           `json:"verifiedObjects"`
	VerifiedBytes   int64                           `json:"verifiedBytes"`
	Consistency     string                          `json:"consistency"`
	Readiness       string                          `json:"readiness"`
	Protocol        string                          `json:"protocol"`
	Authorization   string                          `json:"authorization"`
	SchemaSHA256    string                          `json:"schemaSha256,omitempty"`
	TargetProject   string                          `json:"targetProject,omitempty"`
	Gateway         *backupmanifest.GatewayIdentity `json:"gateway,omitempty"`
	Reason          string                          `json:"reason,omitempty"`
}

func report(id string) TransferReport {
	return TransferReport{SchemaVersion: 1, BackupID: id, CheckedAt: time.Now().UTC(), Status: "failed", Software: "unknown", Database: "unknown", Metadata: "unknown", GrantMetadata: "unknown", GroupMetadata: "unknown", AuditMetadata: "unknown", Objects: "unknown", Consistency: "unknown", Readiness: "not_run", Protocol: "not_run", Authorization: "not_run"}
}
func fail(r TransferReport, reason string) (TransferReport, error) {
	r.Reason = reason
	return r, errors.New(reason)
}

// Export captures real PostgreSQL/S3 bytes into a new private bundle, checking
// the observed frozen scope again before atomically publishing the manifest.
func Export(ctx context.Context, source FrozenSource, directory string, release Release, writers backupmanifest.WriterEvidence, id string) (r TransferReport, resultErr error) {
	r = report(id)
	m := backupmanifest.Manifest{SchemaVersion: 2, BackupID: id, State: "complete", Gateway: release.Identity, Writers: writers, StartedAt: time.Now().UTC()}
	if !backupmanifest.ValidSoftwareIdentity(2, m.Gateway) || !writers.InventoryDeclaredComplete || len(writers.Writers) == 0 {
		return fail(r, "source_scope_or_identity_unconfirmed")
	}
	frozen, err := source.ObserveFrozen(ctx)
	if err != nil || frozen == "" {
		return fail(r, "source_not_frozen")
	}
	ledger, err := source.Ledger(ctx)
	if err != nil {
		return fail(r, "source_schema_unavailable")
	}
	if VerifyRelease(m, release, strings.NewReader(string(ledger))) != nil {
		return fail(r, "release_schema_mismatch")
	}
	r.Software = "verified"
	r.Gateway = &m.Gateway
	metadata, err := source.Metadata(ctx)
	if err != nil || len(metadata) > 64<<20 {
		return fail(r, "metadata_unavailable")
	}
	if err = os.Mkdir(directory, 0700); err != nil {
		return fail(r, "new_private_bundle_required")
	}
	stage, failedKey, objectFile := "bundle_initialization", "", ""
	defer func() {
		if resultErr == nil {
			return
		}
		receipt := struct {
			BackupID string    `json:"backupId"`
			FailedAt time.Time `json:"failedAt"`
			Stage    string    `json:"stage"`
			Key      string    `json:"key,omitempty"`
			File     string    `json:"file,omitempty"`
			Reason   string    `json:"reason"`
		}{id, time.Now().UTC(), stage, failedKey, objectFile, r.Reason}
		data, e := json.Marshal(receipt)
		if e == nil {
			_, _ = writeFile(directory, "failure.json", bytes.NewReader(data))
		}
	}()
	if err = os.Mkdir(filepath.Join(directory, "objects"), 0700); err != nil {
		return fail(r, "bundle_write_failed")
	}
	incomplete := filepath.Join(directory, "INCOMPLETE")
	if err = os.WriteFile(incomplete, []byte("incomplete\n"), 0600); err != nil {
		return fail(r, "bundle_write_failed")
	}
	schemaFile, err := writeFile(directory, "migrations.tsv", strings.NewReader(string(ledger)))
	if err != nil {
		return fail(r, "bundle_write_failed")
	}
	metaFile, err := writeFile(directory, "metadata.json", strings.NewReader(string(metadata)))
	if err != nil {
		return fail(r, "bundle_write_failed")
	}
	dump, err := os.OpenFile(filepath.Join(directory, "database.dump"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fail(r, "dump_write_failed")
	}
	stage = "database_export"
	dumpErr := source.Dump(ctx, dump)
	syncErr := dump.Sync()
	closeErr := dump.Close()
	if dumpErr != nil || syncErr != nil || closeErr != nil {
		return fail(r, "database_export_failed")
	}
	dumpFile, err := describeFile(directory, "database.dump")
	if err != nil {
		return fail(r, "dump_write_failed")
	}
	r.Database = "exported"
	index, err := os.OpenFile(filepath.Join(directory, "objects.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return fail(r, "inventory_write_failed")
	}
	previous := ""
	encoder := json.NewEncoder(index)
	walkErr := source.WalkObjects(ctx, func(key string, expectedSize int64) error {
		if key == "" || key <= previous || expectedSize < 0 || expectedSize == math.MaxInt64 || len(key) > 32<<10 {
			return errors.New("invalid object inventory")
		}
		previous = key
		stage, failedKey, objectFile = "object_export", key, fmt.Sprintf("objects/%012d.data", r.VerifiedObjects)
		body, size, err := source.Open(ctx, key)
		if err != nil {
			return err
		}
		fileName := fmt.Sprintf("objects/%012d.data", r.VerifiedObjects)
		info, copyErr := writeFile(directory, fileName, io.LimitReader(body, expectedSize+1))
		closeErr := body.Close()
		if copyErr != nil || closeErr != nil || size != expectedSize || *info.Size != expectedSize {
			return errors.New("object export failed")
		}
		if r.VerifiedBytes > math.MaxInt64-expectedSize {
			return errors.New("object byte overflow")
		}
		if err = encoder.Encode(backupmanifest.Object{Key: key, File: info}); err != nil {
			return err
		}
		r.VerifiedObjects++
		r.VerifiedBytes += expectedSize
		return nil
	})
	syncErr = index.Sync()
	closeErr = index.Close()
	if walkErr != nil || syncErr != nil || closeErr != nil {
		return fail(r, "object_export_failed")
	}
	indexFile, err := describeFile(directory, "objects.jsonl")
	if err != nil {
		return fail(r, "inventory_write_failed")
	}
	stage, failedKey, objectFile = "source_object_reference_recheck", "", ""
	if verifySourceObjects(ctx, source, directory, func(key string) { failedKey = key }) != nil {
		return fail(r, "source_objects_or_references_changed")
	}
	stage, failedKey = "source_freeze_recheck", ""
	observed, err := source.ObserveFrozen(ctx)
	if err != nil || observed != frozen {
		return fail(r, "source_freeze_changed")
	}
	stage = "source_schema_recheck"
	afterLedger, err := source.Ledger(ctx)
	if err != nil || string(afterLedger) != string(ledger) {
		return fail(r, "source_schema_changed")
	}
	stage = "source_metadata_recheck"
	afterMetadata, err := source.Metadata(ctx)
	if err != nil || string(afterMetadata) != string(metadata) {
		return fail(r, "source_metadata_changed")
	}
	stage = "bundle_publication"
	m.CompletedAt = time.Now().UTC()
	m.Metadata = &metaFile
	m.Database = backupmanifest.DatabaseExport{Format: "pg-custom-v1", File: dumpFile}
	m.Schema = backupmanifest.SchemaExport{Format: "gateway-migrations-tsv-v1", File: schemaFile}
	m.Objects = backupmanifest.ObjectExport{Format: "s3-bytes-v1", Inventory: indexFile, Count: &r.VerifiedObjects, Bytes: &r.VerifiedBytes}
	for i := range m.Writers.Writers {
		m.Writers.Writers[i].StopConfirmed = true
		m.Writers.Writers[i].StoppedAt = m.StartedAt
		m.Writers.Writers[i].ConfirmedThrough = m.CompletedAt
	}
	if verified := backupmanifest.Verify(ctx, directory, m); verified.Integrity != backupmanifest.Verified || verified.WriterEvidence != "declared" {
		return fail(r, "bundle_verification_failed")
	}
	data, err := json.Marshal(m)
	if err != nil {
		return fail(r, "manifest_write_failed")
	}
	if _, err = writeFile(directory, "manifest.pending", strings.NewReader(string(data)+"\n")); err != nil {
		return fail(r, "manifest_write_failed")
	}
	if err = os.Rename(filepath.Join(directory, "manifest.pending"), filepath.Join(directory, "manifest.json")); err != nil {
		return fail(r, "manifest_write_failed")
	}
	if err = syncDirectory(directory); err != nil {
		return fail(r, "manifest_write_failed")
	}
	if err = os.Remove(incomplete); err != nil {
		return fail(r, "manifest_write_failed")
	}
	if err = syncDirectory(directory); err != nil {
		return fail(r, "manifest_write_failed")
	}
	r.Consistency = "operator_confirmed_scope"
	r.SchemaSHA256 = m.Schema.File.SHA256
	r.Status = "exported"
	r.Metadata = "exported"
	r.Objects = "verified"
	return r, nil
}

// Restore validates all input and the explicitly approved release before any
// target writes. Caller provides a destination created by the isolation adapter.
func Restore(ctx context.Context, directory string, release Release, target Destination) (TransferReport, error) {
	m, err := LoadManifest(directory)
	r := report(m.BackupID)
	if err != nil {
		return fail(r, "bundle_input_invalid")
	}
	check := backupmanifest.Verify(ctx, directory, m)
	if check.Integrity != backupmanifest.Verified || check.WriterEvidence != "declared" || m.Metadata == nil {
		return fail(r, "bundle_input_invalid")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return fail(r, "bundle_input_invalid")
	}
	defer func() { _ = root.Close() }()
	ledger, err := readBounded(root, m.Schema.File.Path, 64<<20)
	if err != nil || VerifyRelease(m, release, strings.NewReader(string(ledger))) != nil {
		return fail(r, "release_schema_mismatch")
	}
	r.Software = "verified"
	r.Gateway = &m.Gateway
	metadata, err := readBounded(root, m.Metadata.Path, 64<<20)
	if err != nil {
		return fail(r, "metadata_input_invalid")
	}
	if target.CheckEmpty(ctx) != nil {
		return fail(r, "target_not_owned_empty_isolated")
	}
	dump, err := checkedFile(root, m.Database.File)
	if err != nil {
		return fail(r, "database_input_invalid")
	}
	err = target.RestoreDatabase(ctx, dump)
	closeErr := dump.Close()
	if err != nil || closeErr != nil {
		return fail(r, "database_restore_failed")
	}
	r.Database = "restored"
	restoredLedger, err := target.Ledger(ctx)
	if err != nil || string(restoredLedger) != string(ledger) {
		return fail(r, "restored_schema_mismatch")
	}
	restoredMetadata, err := target.Metadata(ctx)
	if err != nil || string(restoredMetadata) != string(metadata) {
		return fail(r, "restored_metadata_mismatch")
	}
	r.Metadata = "verified"
	recordMetadataEvidence(restoredMetadata, &r)
	index, err := root.Open(m.Objects.Inventory.Path)
	if err != nil {
		return fail(r, "inventory_input_invalid")
	}
	defer func() { _ = index.Close() }()
	scanner := bufio.NewScanner(io.LimitReader(index, 64<<20+1))
	scanner.Buffer(make([]byte, 4096), 64<<10)
	for scanner.Scan() {
		var object backupmanifest.Object
		if opsjson.Decode(scanner.Bytes(), &object) != nil {
			return fail(r, "inventory_input_invalid")
		}
		file, err := checkedFile(root, object.File)
		if err != nil {
			return fail(r, "object_input_invalid")
		}
		putErr := target.Put(ctx, object.Key, file, *object.File.Size, object.File.SHA256)
		closeErr := file.Close()
		if putErr != nil || closeErr != nil {
			return fail(r, "object_restore_failed")
		}
		body, size, err := target.Open(ctx, object.Key)
		if err != nil {
			return fail(r, "restored_object_unavailable")
		}
		sum, count, readErr := hashReader(io.LimitReader(body, *object.File.Size+1))
		closeErr = body.Close()
		if readErr != nil || closeErr != nil || count != *object.File.Size || size != count || sum != object.File.SHA256 {
			return fail(r, "restored_object_mismatch")
		}
		r.VerifiedObjects++
		r.VerifiedBytes += count
	}
	if scanner.Err() != nil || r.VerifiedObjects != *m.Objects.Count || r.VerifiedBytes != *m.Objects.Bytes {
		return fail(r, "restored_inventory_mismatch")
	}
	r.SchemaSHA256 = m.Schema.File.SHA256
	r.Consistency = "operator_declared_scope"
	r.Status = "restored"
	r.Objects = "verified"
	return r, nil
}

func LoadManifest(directory string) (backupmanifest.Manifest, error) {
	var m backupmanifest.Manifest
	root, err := os.OpenRoot(directory)
	if err != nil {
		return m, err
	}
	defer func() { _ = root.Close() }()
	info, err := root.Stat(".")
	if err != nil || info.Mode().Perm()&0077 != 0 {
		return m, errors.New("private bundle required")
	}
	if _, err = root.Lstat("INCOMPLETE"); err == nil || !errors.Is(err, os.ErrNotExist) {
		return m, errors.New("incomplete bundle")
	}
	data, err := readBounded(root, "manifest.json", 8<<20)
	if err != nil {
		return m, err
	}
	if opsjson.Decode(data, &m) != nil {
		return m, errors.New("invalid manifest")
	}
	return m, nil
}

func writeFile(directory, name string, r io.Reader) (backupmanifest.File, error) {
	f, err := os.OpenFile(filepath.Join(directory, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return backupmanifest.File{}, err
	}
	_, copyErr := io.Copy(f, r)
	syncErr := f.Sync()
	closeErr := f.Close()
	if copyErr != nil {
		return backupmanifest.File{}, copyErr
	}
	if syncErr != nil {
		return backupmanifest.File{}, syncErr
	}
	if closeErr != nil {
		return backupmanifest.File{}, closeErr
	}
	return describeFile(directory, name)
}
func describeFile(directory, name string) (backupmanifest.File, error) {
	f, err := os.Open(filepath.Join(directory, name))
	if err != nil {
		return backupmanifest.File{}, err
	}
	defer func() { _ = f.Close() }()
	sum, size, err := hashReader(f)
	return backupmanifest.File{Path: name, Size: &size, SHA256: sum}, err
}
func checkedFile(root *os.Root, info backupmanifest.File) (*os.File, error) {
	if info.Size == nil || *info.Size < 0 || *info.Size == math.MaxInt64 {
		return nil, errors.New("invalid size")
	}
	stat, err := root.Lstat(info.Path)
	if err != nil || !stat.Mode().IsRegular() || stat.Mode().Perm()&0077 != 0 {
		return nil, errors.New("invalid file")
	}
	f, err := root.Open(info.Path)
	if err != nil {
		return nil, err
	}
	sum, n, err := hashReader(io.LimitReader(f, *info.Size+1))
	if err != nil || n != *info.Size || sum != info.SHA256 {
		_ = f.Close()
		return nil, errors.New("file changed")
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}
func readBounded(root *os.Root, name string, limit int64) ([]byte, error) {
	info, err := root.Lstat(name)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > limit {
		return nil, errors.New("invalid private input")
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, errors.New("input too large")
	}
	return data, nil
}
func syncDirectory(directory string) error {
	f, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return f.Sync()
}

// These categories describe exact pre-start table fingerprints. Protocol
// authorization checks remain separate and never follow from metadata alone.
func recordMetadataEvidence(data []byte, r *TransferReport) {
	var entries []struct {
		Table string `json:"table"`
	}
	if json.Unmarshal(data, &entries) != nil {
		return
	}
	tables := map[string]bool{}
	for _, entry := range entries {
		tables[entry.Table] = true
	}
	if tables["repository_grants"] && tables["repository_grant_sets"] {
		r.GrantMetadata = "verified"
	}
	if tables["hosted_groups"] && tables["hosted_group_members"] {
		r.GroupMetadata = "verified"
	}
	if tables["resolver_audit_log"] {
		r.AuditMetadata = "verified"
	}
}
