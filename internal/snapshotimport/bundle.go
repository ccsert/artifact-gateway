// Package snapshotimport validates frozen SNAPSHOT bundles and imports them
// through the native repository/object ports. It never discovers source URLs.
package snapshotimport

import (
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/opsjson"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

type Manifest struct {
	SchemaVersion int          `json:"schemaVersion"`
	SourceID      string       `json:"sourceId"`
	Complete      bool         `json:"complete"`
	Coordinates   []Coordinate `json:"coordinates"`
	Excluded      []Exclusion  `json:"excluded"`
}
type Exclusion struct {
	Coordinate string `json:"coordinate"`
	Reason     string `json:"reason"`
}
type Coordinate struct {
	Coordinate string  `json:"coordinate"`
	Builds     []Build `json:"builds"`
	Metadata   *File   `json:"metadata"`
}
type Build struct {
	Timestamp   string `json:"timestamp"`
	BuildNumber int    `json:"buildNumber"`
	Files       []File `json:"files"`
}
type File struct {
	Path   string `json:"path"`
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
}
type Rejection struct {
	Coordinate string `json:"coordinate"`
	Reason     string `json:"reason"`
}
type Prepared struct {
	Manifest  Manifest
	Digest    string
	Plans     []repository.MavenSnapshotImportPlan
	Root      *os.Root
	validated bool
	generated map[string][]byte
	files     map[string]File
}

func (p *Prepared) Close() error { return p.Root.Close() }

var atom = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
var opaque = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)
var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var suffixPattern = regexp.MustCompile(`^(?:-([A-Za-z0-9][A-Za-z0-9_-]*))?\.([A-Za-z0-9][A-Za-z0-9.-]*)$`)

func base(coordinate string) string {
	p := strings.Split(coordinate, ":")
	return strings.ReplaceAll(p[0], ".", "/") + "/" + p[1] + "/" + p[2] + "/"
}
func validCoordinate(v string) bool {
	p := strings.Split(v, ":")
	if len(p) != 3 || !strings.HasSuffix(p[2], "-SNAPSHOT") {
		return false
	}
	for _, x := range p {
		if !atom.MatchString(x) || strings.Contains(x, "..") {
			return false
		}
	}
	return true
}
func sourcePrefix(c string, b Build) string {
	p := strings.Split(c, ":")
	return p[1] + "-" + strings.TrimSuffix(p[2], "-SNAPSHOT") + "-" + b.Timestamp + "-" + strconv.Itoa(b.BuildNumber)
}
func objectKey(digest string) string {
	return "native/maven/sha256/" + strings.TrimPrefix(digest, "sha256:")
}
func sum(data []byte) string { v := sha256.Sum256(data); return "sha256:" + hex.EncodeToString(v[:]) }
func safeFile(root *os.Root, name string) (*os.File, error) {
	if path.Clean(name) != name || strings.HasPrefix(name, "/") || strings.Contains(name, "\\") || name == "." || strings.HasPrefix(name, "../") {
		return nil, errors.New("invalid bundle path")
	}
	for p := name; p != "."; p = path.Dir(p) {
		info, err := root.Lstat(p)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("bundle path is unavailable or a symlink")
		}
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, errors.New("bundle file must be regular")
	}
	return f, nil
}
func readBounded(root *os.Root, name string, max int64) ([]byte, error) {
	f, err := safeFile(root, name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil || int64(len(b)) > max {
		return nil, errors.New("bundle evidence exceeds limit")
	}
	return b, nil
}
func decodeXML(data []byte, v any) error {
	if err := xmlSingletons(data); err != nil {
		return err
	}
	d := xml.NewDecoder(strings.NewReader(string(data)))
	if err := d.Decode(v); err != nil {
		return err
	}
	if err := d.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("trailing XML")
	}
	return nil
}

// Prepare verifies every listed source byte and semantic identity before a
// target can be opened. Rejections are whole-GAV; no source asset is repaired.
func Prepare(ctx context.Context, directory, expected string) (*Prepared, []Rejection, error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, nil, errors.New("bundle unavailable")
	}
	p := &Prepared{Root: root, generated: map[string][]byte{}, files: map[string]File{}}
	fail := func(reason string) (*Prepared, []Rejection, error) {
		_ = root.Close()
		return nil, nil, errors.New(reason)
	}
	data, err := readBounded(root, "manifest.json", 8<<20)
	if err != nil || !digestPattern.MatchString(expected) || sum(data) != expected {
		return fail("manifest_identity_mismatch")
	}
	if opsjson.Decode(data, &p.Manifest) != nil || !requiredManifestFields(data) || p.Manifest.SchemaVersion != 1 || !p.Manifest.Complete || !opaque.MatchString(p.Manifest.SourceID) || len(p.Manifest.Coordinates) == 0 || len(p.Manifest.Coordinates) > 10000 {
		return fail("invalid_manifest")
	}
	p.Digest = expected
	seen := map[string]bool{}
	for _, e := range p.Manifest.Excluded {
		if !validCoordinate(e.Coordinate) || !opaque.MatchString(e.Reason) || seen[e.Coordinate] {
			return fail("invalid_exclusion")
		}
		seen[e.Coordinate] = true
	}
	rejected := []Rejection{}
	for _, c := range p.Manifest.Coordinates {
		if !validCoordinate(c.Coordinate) || seen[c.Coordinate] {
			return fail("coordinate_identity_conflict")
		}
		seen[c.Coordinate] = true
		plan, e := p.prepareCoordinate(ctx, c)
		if e != nil {
			rejected = append(rejected, Rejection{Coordinate: c.Coordinate, Reason: e.Error()})
			continue
		}
		p.Plans = append(p.Plans, plan)
	}
	if len(rejected) > 0 {
		return p, rejected, errors.New("bundle_rejected")
	}
	p.validated = true
	sort.Slice(p.Plans, func(i, j int) bool { return p.Plans[i].Coordinate < p.Plans[j].Coordinate })
	return p, rejected, nil
}
func (p *Prepared) prepareCoordinate(ctx context.Context, c Coordinate) (repository.MavenSnapshotImportPlan, error) {
	bad := func(r string) (repository.MavenSnapshotImportPlan, error) {
		return repository.MavenSnapshotImportPlan{}, errors.New(r)
	}
	plan := repository.MavenSnapshotImportPlan{Coordinate: c.Coordinate, SourceID: p.Manifest.SourceID, ManifestDigest: p.Digest, Aliases: map[string]string{}}
	if len(c.Builds) == 0 || len(c.Builds) > 10000 {
		return bad("invalid_builds")
	}
	identities := map[string]bool{}
	paths := map[string]bool{}
	for _, b := range c.Builds {
		t, err := time.Parse("20060102.150405", b.Timestamp)
		if err != nil || t.Format("20060102.150405") != b.Timestamp || b.BuildNumber <= 0 || b.BuildNumber > 2147483647 || len(b.Files) == 0 {
			return bad("invalid_build_identity")
		}
		identity := b.Timestamp + "-" + strconv.Itoa(b.BuildNumber)
		if identities[identity] {
			return bad("duplicate_build_identity")
		}
		identities[identity] = true
		prefix := sourcePrefix(c.Coordinate, b)
		pomPath := base(c.Coordinate) + prefix + ".pom"
		pomFound := false
		mainExt := "jar"
		mainFiles := map[string]bool{}
		pomDigest := ""
		for _, f := range b.Files {
			if paths[f.Path] || path.Dir(f.Path)+"/" != base(c.Coordinate) || !strings.HasPrefix(path.Base(f.Path), prefix) {
				return bad("path_identity_mismatch")
			}
			suffix := strings.TrimPrefix(path.Base(f.Path), prefix)
			pair := suffixPattern.FindStringSubmatch(suffix)
			if pair == nil {
				return bad("path_identity_mismatch")
			}
			for _, ext := range []string{"md5", "sha1", "sha256", "sha512"} {
				if strings.HasSuffix(f.Path, "."+ext) {
					return bad("source_checksum_not_primary")
				}
			}
			paths[f.Path] = true
			if pair[1] == "" {
				mainFiles[pair[2]] = true
			}
			checksums, err := p.verifyFile(ctx, f)
			if err != nil {
				return bad("source_bytes_mismatch")
			}
			if f.Path == pomPath {
				pom, err := readBounded(p.Root, f.Path, 16<<20)
				if err != nil || int64(len(pom)) != f.Size || sum(pom) != f.Digest {
					return bad("invalid_pom")
				}
				mainExt, err = validatePOM(pom, c.Coordinate)
				if err != nil {
					return bad("pom_identity_or_packaging_mismatch")
				}
				pomFound = true
				pomDigest = f.Digest
			}
			p.files[f.Path] = f
			plan.Assets = append(plan.Assets, p.assets(f, checksums)...)
		}
		if !pomFound || !mainFiles[mainExt] {
			return bad("incomplete_build")
		}
		plan.Artifacts = append(plan.Artifacts, repository.MavenArtifact{Coordinate: c.Coordinate, Digest: pomDigest, SourceTimestamp: b.Timestamp, SourceBuildNumber: b.BuildNumber})
	}
	if c.Metadata != nil {
		f := *c.Metadata
		if f.Path != base(c.Coordinate)+"maven-metadata.xml" || f.Size > 4<<20 {
			return bad("metadata_path_mismatch")
		}
		checksums, err := p.verifyFile(ctx, f)
		if err != nil {
			return bad("metadata_bytes_mismatch")
		}
		data, err := readBounded(p.Root, f.Path, 4<<20)
		if err != nil || int64(len(data)) != f.Size || sum(data) != f.Digest {
			return bad("metadata_unavailable")
		}
		aliases, err := validateMetadata(data, c.Coordinate, paths, identities)
		if err != nil {
			if err.Error() == "unsupported_metadata_without_snapshot_versions" {
				return bad(err.Error())
			}
			return bad("metadata_identity_or_reference_mismatch")
		}
		plan.Aliases = aliases
		p.files[f.Path] = f
		all := p.assets(f, checksums)
		plan.Assets = append(plan.Assets, all...)
		m := all[0]
		plan.Metadata = &m
		keys := make([]string, 0, len(aliases))
		for canonical := range aliases {
			keys = append(keys, canonical)
		}
		for _, canonical := range keys {
			target := aliases[canonical]
			for _, ext := range []string{"md5", "sha1", "sha256", "sha512"} {
				plan.Aliases[canonical+"."+ext] = target + "." + ext
			}
		}
	}
	sort.Slice(plan.Assets, func(i, j int) bool { return plan.Assets[i].Path < plan.Assets[j].Path })
	sort.Slice(plan.Artifacts, func(i, j int) bool {
		a, b := plan.Artifacts[i], plan.Artifacts[j]
		if a.SourceTimestamp != b.SourceTimestamp {
			return a.SourceTimestamp < b.SourceTimestamp
		}
		return a.SourceBuildNumber < b.SourceBuildNumber
	})
	return plan, nil
}
func (p *Prepared) verifyFile(ctx context.Context, f File) (map[string]string, error) {
	if !digestPattern.MatchString(f.Digest) || f.Size < 0 || f.Size > 1<<40 {
		return nil, errors.New("invalid_file")
	}
	r, err := safeFile(p.Root, f.Path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	hashes := map[string]interface {
		io.Writer
		Sum([]byte) []byte
	}{"md5": md5.New(), "sha1": sha1.New(), "sha256": sha256.New(), "sha512": sha512.New()}
	writers := []io.Writer{}
	for _, h := range hashes {
		writers = append(writers, h)
	}
	n, err := io.Copy(io.MultiWriter(writers...), &contextReader{ctx: ctx, r: io.LimitReader(r, f.Size+1)})
	if err != nil || n != f.Size || "sha256:"+hex.EncodeToString(hashes["sha256"].Sum(nil)) != f.Digest {
		return nil, errors.New("source_bytes_mismatch")
	}
	out := map[string]string{}
	for ext, h := range hashes {
		out[ext] = hex.EncodeToString(h.Sum(nil))
	}
	return out, nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r *contextReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(b)
}
func (p *Prepared) assets(f File, checksums map[string]string) []repository.MavenAsset {
	out := []repository.MavenAsset{{Path: f.Path, ObjectKey: objectKey(f.Digest), Digest: f.Digest, Size: f.Size}}
	for _, ext := range []string{"md5", "sha1", "sha256", "sha512"} {
		data := []byte(checksums[ext])
		d := sum(data)
		p.generated[f.Path+"."+ext] = data
		out = append(out, repository.MavenAsset{Path: f.Path + "." + ext, ObjectKey: objectKey(d), Digest: d, Size: int64(len(data))})
	}
	return out
}
func validatePOM(data []byte, coordinate string) (string, error) {
	// XML field names are explicit because Go field names are not Maven tags.
	var pom struct {
		XMLName   xml.Name `xml:"project"`
		Group     string   `xml:"groupId"`
		Artifact  string   `xml:"artifactId"`
		Version   string   `xml:"version"`
		Packaging string   `xml:"packaging"`
		Parent    struct {
			Group   string `xml:"groupId"`
			Version string `xml:"version"`
		} `xml:"parent"`
	}
	if err := decodeXML(data, &pom); err != nil {
		return "", err
	}
	if pom.Group == "" {
		pom.Group = pom.Parent.Group
	}
	if pom.Version == "" {
		pom.Version = pom.Parent.Version
	}
	if pom.Group+":"+pom.Artifact+":"+pom.Version != coordinate {
		return "", errors.New("POM identity mismatch")
	}
	ext := pom.Packaging
	if ext == "" {
		ext = "jar"
	}
	switch ext {
	case "pom", "jar", "war", "ear", "rar", "zip":
		return ext, nil
	case "maven-plugin", "maven-archetype", "ejb", "bundle":
		return "jar", nil
	default:
		return "", errors.New("unsupported POM packaging")
	}
}
func validateMetadata(data []byte, c string, paths, builds map[string]bool) (map[string]string, error) {
	var m struct {
		XMLName  xml.Name `xml:"metadata"`
		Group    string   `xml:"groupId"`
		Artifact string   `xml:"artifactId"`
		Version  string   `xml:"version"`
		Snapshot struct {
			Timestamp string `xml:"timestamp"`
			Build     int    `xml:"buildNumber"`
			Local     string `xml:"localCopy"`
		} `xml:"versioning>snapshot"`
		Versions []struct {
			Extension  string `xml:"extension"`
			Classifier string `xml:"classifier"`
			Value      string `xml:"value"`
			Updated    string `xml:"updated"`
		} `xml:"versioning>snapshotVersions>snapshotVersion"`
	}
	if err := decodeXML(data, &m); err != nil {
		return nil, err
	}
	if len(m.Versions) == 0 {
		return nil, errors.New("unsupported_metadata_without_snapshot_versions")
	}
	if m.Group+":"+m.Artifact+":"+m.Version != c || m.Snapshot.Local == "true" || !builds[m.Snapshot.Timestamp+"-"+strconv.Itoa(m.Snapshot.Build)] || len(m.Versions) == 0 {
		return nil, errors.New("metadata identity mismatch")
	}
	out := map[string]string{}
	for _, v := range m.Versions {
		suffix := ""
		if v.Classifier != "" {
			suffix = "-" + v.Classifier
		}
		suffix += "." + v.Extension
		if suffixPattern.FindStringSubmatch(suffix) == nil {
			return nil, errors.New("invalid metadata pair")
		}
		canonical := base(c) + m.Artifact + "-" + m.Version + suffix
		target := base(c) + m.Artifact + "-" + v.Value + suffix
		if !strings.HasPrefix(v.Value, strings.TrimSuffix(m.Version, "-SNAPSHOT")+"-") || !builds[strings.TrimPrefix(v.Value, strings.TrimSuffix(m.Version, "-SNAPSHOT")+"-")] || !paths[target] || out[canonical] != "" {
			return nil, errors.New("metadata current reference missing or duplicated")
		}
		if _, err := time.Parse("20060102150405", v.Updated); err != nil {
			return nil, errors.New("invalid metadata update")
		}
		out[canonical] = target
	}
	return out, nil
}

func (p *Prepared) Reader(ctx context.Context, a repository.MavenAsset) (io.ReadCloser, error) {
	if b, ok := p.generated[a.Path]; ok {
		return io.NopCloser(strings.NewReader(string(b))), nil
	}
	f, ok := p.files[a.Path]
	if !ok || f.Digest != a.Digest || f.Size != a.Size {
		return nil, errors.New("unknown source file")
	}
	// Freeze and hash exactly the bytes that will be uploaded. Reopening the
	// mutable source after hashing would let a changed stream poison its CAS key.
	source, err := safeFile(p.Root, f.Path)
	if err != nil {
		return nil, errors.New("source_unavailable")
	}
	defer func() { _ = source.Close() }()
	spool, err := os.CreateTemp("", "artifact-gateway-snapshot-import-*")
	if err != nil {
		return nil, errors.New("source_spool_unavailable")
	}
	// Supported operator platforms (Linux/macOS) keep this descriptor alive
	// after unlink, so no path can be modified or left behind on interruption.
	if err = os.Remove(spool.Name()); err != nil {
		_ = spool.Close()
		_ = os.Remove(spool.Name())
		return nil, errors.New("anonymous_source_spool_unavailable")
	}
	fail := func() (io.ReadCloser, error) { _ = spool.Close(); return nil, errors.New("source_bytes_mismatch") }
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(spool, h), &contextReader{ctx: ctx, r: io.LimitReader(source, f.Size+1)})
	if err != nil || n != f.Size || "sha256:"+hex.EncodeToString(h.Sum(nil)) != f.Digest {
		return fail()
	}
	if _, err = spool.Seek(0, io.SeekStart); err != nil {
		return fail()
	}
	return spool, nil
}
func (p *Prepared) References(repo, target, actor string, binding ...string) ([]repository.MavenSnapshotImportPlan, error) {
	if !p.validated {
		return nil, errors.New("bundle_not_validated")
	}
	if _, err := uuid.Parse(repo); err != nil || !opaque.MatchString(target) || !opaque.MatchString(actor) {
		return nil, errors.New("invalid target identity")
	}
	plans := append([]repository.MavenSnapshotImportPlan(nil), p.Plans...)
	for i := range plans {
		plans[i].RepositoryID = repo
		plans[i].TargetID = target
		if len(binding) == 1 {
			plans[i].TargetBinding = binding[0]
		}
		plans[i].Actor = actor
		plans[i].Assets = append([]repository.MavenAsset(nil), plans[i].Assets...)
		plans[i].Artifacts = append([]repository.MavenArtifact(nil), plans[i].Artifacts...)
		for j := range plans[i].Assets {
			plans[i].Assets[j].RepositoryID = repo
		}
		for j := range plans[i].Artifacts {
			plans[i].Artifacts[j].RepositoryID = repo
		}
		if plans[i].Metadata != nil {
			m := *plans[i].Metadata
			m.RepositoryID = repo
			plans[i].Metadata = &m
		}
	}
	return plans, nil
}

// xmlSingletons rejects ambiguous repeated identity fields while permitting
// collections (dependencies and snapshotVersion entries) in their own frames.
func xmlSingletons(data []byte) error {
	singleton := map[string]bool{}
	for _, root := range []string{"project", "metadata"} {
		for _, field := range []string{"groupId", "artifactId", "version", "packaging"} {
			singleton[root+"/"+field] = true
		}
	}
	for _, field := range []string{"groupId", "version"} {
		singleton["project/parent/"+field] = true
	}
	singleton["project/parent"] = true
	singleton["metadata/versioning"] = true
	for _, field := range []string{"snapshot", "snapshotVersions"} {
		singleton["metadata/versioning/"+field] = true
	}
	for _, field := range []string{"timestamp", "buildNumber", "localCopy"} {
		singleton["metadata/versioning/snapshot/"+field] = true
	}
	for _, field := range []string{"extension", "classifier", "value", "updated"} {
		singleton["metadata/versioning/snapshotVersions/snapshotVersion/"+field] = true
	}
	decoder := xml.NewDecoder(strings.NewReader(string(data)))
	names := []string{}
	frames := []map[string]bool{{}}
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		switch e := token.(type) {
		case xml.StartElement:
			key := strings.Join(append(append([]string(nil), names...), e.Name.Local), "/")
			frame := frames[len(frames)-1]
			if singleton[key] && frame[e.Name.Local] {
				return errors.New("duplicate XML identity field")
			}
			frame[e.Name.Local] = true
			names = append(names, e.Name.Local)
			frames = append(frames, map[string]bool{})
		case xml.EndElement:
			if len(names) == 0 {
				return errors.New("invalid XML")
			}
			names = names[:len(names)-1]
			frames = frames[:len(frames)-1]
		}
	}
}

// Required evidence cannot be silently inferred from omitted JSON fields.
func requiredManifestFields(data []byte) bool {
	var root map[string]json.RawMessage
	if json.Unmarshal(data, &root) != nil {
		return false
	}
	has := func(m map[string]json.RawMessage, keys ...string) bool {
		for _, k := range keys {
			if _, ok := m[k]; !ok {
				return false
			}
		}
		return true
	}
	if !has(root, "schemaVersion", "sourceId", "complete", "coordinates", "excluded") {
		return false
	}
	var coords []map[string]json.RawMessage
	if json.Unmarshal(root["coordinates"], &coords) != nil {
		return false
	}
	file := func(raw json.RawMessage) bool {
		var f map[string]json.RawMessage
		return json.Unmarshal(raw, &f) == nil && has(f, "path", "digest", "size")
	}
	for _, c := range coords {
		if !has(c, "coordinate", "builds", "metadata") {
			return false
		}
		if string(c["metadata"]) != "null" && !file(c["metadata"]) {
			return false
		}
		var builds []map[string]json.RawMessage
		if json.Unmarshal(c["builds"], &builds) != nil {
			return false
		}
		for _, b := range builds {
			if !has(b, "timestamp", "buildNumber", "files") {
				return false
			}
			var files []json.RawMessage
			if json.Unmarshal(b["files"], &files) != nil {
				return false
			}
			for _, f := range files {
				if !file(f) {
					return false
				}
			}
		}
	}
	return true
}

// Reverify checks the frozen inventory and all primary bytes before target
// mutation, including source files already present in the target object store.
func (p *Prepared) Reverify(ctx context.Context) error {
	if p == nil || !p.validated {
		return errors.New("source_not_verified")
	}
	b, err := readBounded(p.Root, "manifest.json", 8<<20)
	if err != nil || sum(b) != p.Digest {
		return errors.New("source_drift")
	}
	for _, f := range p.files {
		r, err := safeFile(p.Root, f.Path)
		if err != nil {
			return errors.New("source_drift")
		}
		h := sha256.New()
		n, e := io.Copy(h, &contextReader{ctx: ctx, r: io.LimitReader(r, f.Size+1)})
		closeErr := r.Close()
		if e != nil || closeErr != nil || n != f.Size || "sha256:"+hex.EncodeToString(h.Sum(nil)) != f.Digest {
			return errors.New("source_drift")
		}
	}
	return nil
}
