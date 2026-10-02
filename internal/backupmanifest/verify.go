// Package backupmanifest verifies local backup bytes and declared writer
// intervals. It cannot establish a consistent source snapshot or restore it.
package backupmanifest

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"math"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/opsjson"
)

type Status string

const (
	Unknown          Status = "unknown"
	Invalid          Status = "invalid"
	Verified         Status = "verified"
	maxMetadataBytes int64  = 64 << 20
	maxLineBytes            = 64 << 10
)

type Manifest struct {
	SchemaVersion int             `json:"schemaVersion"`
	BackupID      string          `json:"backupId"`
	State         string          `json:"state"`
	StartedAt     time.Time       `json:"startedAt"`
	CompletedAt   time.Time       `json:"completedAt"`
	Gateway       GatewayIdentity `json:"gateway"`
	Database      DatabaseExport  `json:"database"`
	Schema        SchemaExport    `json:"schema"`
	Objects       ObjectExport    `json:"objects"`
	Writers       WriterEvidence  `json:"writers"`
	Metadata      *File           `json:"metadata,omitempty"`
}

type GatewayIdentity struct {
	Version     string            `json:"version"`
	Revision    string            `json:"revision"`
	ImageDigest string            `json:"imageDigest,omitempty"`
	Artifact    *SoftwareArtifact `json:"artifact,omitempty"`
}

// SoftwareArtifact identifies the actual deployment artifact. Version 1 keeps
// its original imageDigest meaning; version 2 never treats a binary as an image.
type SoftwareArtifact struct {
	Kind     string `json:"kind"`
	SHA256   string `json:"sha256"`
	Platform string `json:"platform"`
}

type File struct {
	Path   string `json:"path"`
	Size   *int64 `json:"size"`
	SHA256 string `json:"sha256"`
}

type DatabaseExport struct {
	Format string `json:"format"`
	File   File   `json:"file"`
}
type SchemaExport struct {
	Format string `json:"format"`
	File   File   `json:"file"`
}
type ObjectExport struct {
	Format    string `json:"format"`
	Inventory File   `json:"inventory"`
	Count     *int64 `json:"count"`
	Bytes     *int64 `json:"bytes"`
}
type Object struct {
	Key  string `json:"key"`
	File File   `json:"file"`
}

// These are operator declarations, not authenticated process/fencing evidence.
type WriterEvidence struct {
	ScopeID                   string   `json:"scopeId"`
	InventoryDeclaredComplete bool     `json:"inventoryDeclaredComplete"`
	Writers                   []Writer `json:"writers"`
}
type Writer struct {
	ID               string    `json:"id"`
	StopConfirmed    bool      `json:"stopConfirmed"`
	StoppedAt        time.Time `json:"stoppedAt"`
	ConfirmedThrough time.Time `json:"confirmedThrough"`
}

type Report struct {
	SchemaVersion       int       `json:"schemaVersion"`
	BackupID            string    `json:"backupId"`
	CheckedAt           time.Time `json:"checkedAt"`
	ManifestSHA256      string    `json:"manifestSha256,omitempty"`
	Status              Status    `json:"status"`
	Integrity           Status    `json:"integrity"`
	Consistency         Status    `json:"consistency"`
	WriterEvidence      string    `json:"writerEvidence"`
	VerifiedObjects     int64     `json:"verifiedObjects"`
	VerifiedObjectBytes int64     `json:"verifiedObjectBytes"`
	VerifiedMigrations  int64     `json:"verifiedMigrations"`
	Reasons             []string  `json:"reasons"`
}

var identity = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:+-]{0,127}$`)
var revision = regexp.MustCompile(`^[a-f0-9]{40}$`)
var hash = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
var migrationName = regexp.MustCompile(`^[0-9]{6}_[A-Za-z0-9_]+\.sql$`)

// Verify reads only a private local directory. Even verified bytes and complete
// writer declarations leave overall status and consistency unknown.
func Verify(ctx context.Context, directory string, m Manifest) Report {
	r := Report{SchemaVersion: 1, CheckedAt: time.Now().UTC(), Status: Unknown, Integrity: Unknown, Consistency: Unknown, WriterEvidence: "unknown", Reasons: []string{"consistency_not_independently_verified"}}
	if identity.MatchString(m.BackupID) {
		r.BackupID = m.BackupID
	}
	if ctx.Err() != nil {
		return reason(r, Unknown, "cancelled")
	}
	if status, why := validate(m, r.CheckedAt); why != "" {
		return reason(r, status, why)
	}
	r.WriterEvidence, r.Reasons = writerDeclarations(m, r.CheckedAt, r.Reasons)
	root, err := os.OpenRoot(directory)
	if err != nil {
		return reason(r, Invalid, "bundle_unavailable")
	}
	defer func() { _ = root.Close() }()
	info, err := root.Stat(".")
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return reason(r, Invalid, "bundle_permissions_not_private")
	}
	if m.Metadata != nil {
		if m.SchemaVersion != 2 {
			return reason(r, Invalid, "metadata_profile_invalid")
		}
		if why := verifyFile(ctx, root, *m.Metadata, maxMetadataBytes, nil); why != "" {
			return fileFailure(r, ctx, why)
		}
	}
	if why := verifyFile(ctx, root, m.Database.File, 0, nil); why != "" {
		return fileFailure(r, ctx, why)
	}
	var migrations int64
	parseSchema := func(reader io.Reader) string {
		scanner := lines(reader)
		if !scanner.Scan() || scanner.Text() != "filename\tsha256" {
			return "migration_ledger_invalid"
		}
		previous := ""
		for scanner.Scan() {
			columns := strings.Split(scanner.Text(), "\t")
			if len(columns) != 2 || !migrationName.MatchString(columns[0]) || columns[0] <= previous || !hash.MatchString("sha256:"+columns[1]) {
				return "migration_ledger_invalid"
			}
			previous = columns[0]
			migrations++
		}
		if scanner.Err() != nil || migrations == 0 {
			return "migration_ledger_invalid"
		}
		return ""
	}
	if why := verifyFile(ctx, root, m.Schema.File, maxMetadataBytes, parseSchema); why != "" {
		return fileFailure(r, ctx, why)
	}
	r.VerifiedMigrations = migrations
	parseInventory := func(reader io.Reader) string {
		scanner := lines(reader)
		previous := ""
		for scanner.Scan() {
			var object Object
			if opsjson.Decode(scanner.Bytes(), &object) != nil || !validKey(object.Key) || object.Key <= previous || !strings.HasPrefix(object.File.Path, "objects/") {
				return "object_inventory_invalid"
			}
			previous = object.Key
			if why := verifyFile(ctx, root, object.File, 0, nil); why != "" {
				return why
			}
			if r.VerifiedObjects == math.MaxInt64 || *object.File.Size > math.MaxInt64-r.VerifiedObjectBytes {
				return "object_totals_overflow"
			}
			r.VerifiedObjects++
			r.VerifiedObjectBytes += *object.File.Size
		}
		if scanner.Err() != nil {
			return "object_inventory_invalid"
		}
		if r.VerifiedObjects != *m.Objects.Count || r.VerifiedObjectBytes != *m.Objects.Bytes {
			return "object_totals_mismatch"
		}
		return ""
	}
	if why := verifyFile(ctx, root, m.Objects.Inventory, maxMetadataBytes, parseInventory); why != "" {
		return fileFailure(r, ctx, why)
	}
	r.Integrity = Verified
	if r.WriterEvidence == "invalid" {
		r.Status = Invalid
	}
	sort.Strings(r.Reasons)
	return r
}

func validate(m Manifest, now time.Time) (Status, string) {
	if m.SchemaVersion != 1 && m.SchemaVersion != 2 {
		return Unknown, "manifest_version_unsupported"
	}
	if m.State != "complete" {
		return Unknown, "backup_set_incomplete"
	}
	if !identity.MatchString(m.BackupID) || !ValidSoftwareIdentity(m.SchemaVersion, m.Gateway) {
		return Unknown, "software_or_backup_identity_unknown"
	}
	if m.StartedAt.IsZero() || !m.CompletedAt.After(m.StartedAt) || m.CompletedAt.After(now) {
		return Unknown, "backup_interval_unknown"
	}
	if m.Database.Format != "pg-custom-v1" || m.Schema.Format != "gateway-migrations-tsv-v1" || m.Objects.Format != "s3-bytes-v1" {
		return Unknown, "export_or_schema_format_unsupported"
	}
	if (m.Objects.Count != nil && *m.Objects.Count < 0) || (m.Objects.Bytes != nil && *m.Objects.Bytes < 0) {
		return Invalid, "artifact_quantities_invalid"
	}
	if m.Objects.Count == nil || m.Objects.Bytes == nil || m.Database.File.Size == nil || m.Schema.File.Size == nil || m.Objects.Inventory.Size == nil {
		return Unknown, "artifact_quantities_unknown"
	}
	return Unknown, ""
}

// ValidSoftwareIdentity checks syntax only. It does not approve executable
// software or compare a release with the database migration ledger.
func ValidSoftwareIdentity(version int, g GatewayIdentity) bool {
	if !identity.MatchString(g.Version) || !revision.MatchString(g.Revision) {
		return false
	}
	if version == 1 {
		return g.Artifact == nil && hash.MatchString(g.ImageDigest)
	}
	if version != 2 || g.ImageDigest != "" || g.Artifact == nil {
		return false
	}
	a := g.Artifact
	return (a.Kind == "binary" || a.Kind == "oci-image") && hash.MatchString(a.SHA256) && (a.Platform == "linux/amd64" || a.Platform == "linux/arm64")
}

func writerDeclarations(m Manifest, now time.Time, reasons []string) (string, []string) {
	if !identity.MatchString(m.Writers.ScopeID) || !m.Writers.InventoryDeclaredComplete || len(m.Writers.Writers) == 0 {
		return "unknown", append(reasons, "writer_inventory_unconfirmed")
	}
	seen := make(map[string]bool)
	state := "declared"
	for _, writer := range m.Writers.Writers {
		if !identity.MatchString(writer.ID) || seen[writer.ID] {
			return "invalid", append(reasons, "writer_identity_invalid")
		}
		seen[writer.ID] = true
		if !writer.StopConfirmed {
			state = "unknown"
			continue
		}
		if writer.StoppedAt.IsZero() || writer.StoppedAt.After(m.StartedAt) || writer.ConfirmedThrough.Before(m.CompletedAt) || writer.ConfirmedThrough.After(now) {
			return "invalid", append(reasons, "writer_interval_not_covered")
		}
	}
	if state == "unknown" {
		reasons = append(reasons, "writer_stop_unconfirmed")
	}
	return state, reasons
}

func verifyFile(ctx context.Context, root *os.Root, file File, limit int64, parse func(io.Reader) string) string {
	if !validPath(file.Path) || file.Size == nil || *file.Size < 0 || !hash.MatchString(file.SHA256) {
		return "artifact_reference_invalid"
	}
	if limit > 0 && *file.Size > limit {
		return "metadata_limit_exceeded"
	}
	info, err := root.Lstat(file.Path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return "artifact_unavailable_or_not_private"
	}
	f, err := root.Open(file.Path)
	if err != nil {
		return "artifact_unavailable_or_not_private"
	}
	defer func() { _ = f.Close() }()
	info, err = f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() != *file.Size {
		return "artifact_size_or_permissions_mismatch"
	}
	sum := sha256.New()
	reader := io.TeeReader(contextReader{ctx, io.LimitReader(f, *file.Size)}, sum)
	if parse != nil {
		if why := parse(reader); why != "" {
			return why
		}
	}
	if _, err := io.Copy(io.Discard, reader); err != nil {
		return "artifact_read_failed"
	}
	var extra [1]byte
	n, err := f.Read(extra[:])
	if n != 0 || err != io.EOF {
		return "artifact_size_changed"
	}
	if "sha256:"+hex.EncodeToString(sum.Sum(nil)) != file.SHA256 {
		return "artifact_digest_mismatch"
	}
	return ""
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
func lines(reader io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), maxLineBytes)
	return scanner
}
func validKey(key string) bool {
	return key != "" && len(key) <= 4096 && !strings.ContainsAny(key, "\x00\r\n")
}
func validPath(name string) bool {
	return name != "" && len(name) <= 4096 && name != "." && name != ".." && path.Clean(name) == name && !strings.HasPrefix(name, "/") && !strings.HasPrefix(name, "../") && !strings.ContainsAny(name, "\\:\x00\r\n")
}
func reason(r Report, status Status, why string) Report {
	r.Status = status
	if status == Invalid {
		r.Integrity = Invalid
	}
	r.Reasons = append(r.Reasons, why)
	sort.Strings(r.Reasons)
	return r
}
func fileFailure(r Report, ctx context.Context, why string) Report {
	if ctx.Err() != nil {
		return reason(r, Unknown, "cancelled")
	}
	return reason(r, Invalid, why)
}
