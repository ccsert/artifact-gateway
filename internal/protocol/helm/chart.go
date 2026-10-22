// Package helm inspects traditional Helm chart archives without extracting files.
package helm

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"path"
	"regexp"
	"strings"

	"github.com/Masterminds/semver/v3"
	"go.yaml.in/yaml/v3"
	"golang.org/x/crypto/openpgp/clearsign"
	"golang.org/x/crypto/openpgp/packet"
)

const (
	MaxChartBytes      int64 = 64 << 20
	MaxExpandedBytes   int64 = 256 << 20
	MaxProvenanceBytes int64 = 1 << 20
	MaxCharts                = 4096
	maxEntries               = 10000
)

var ErrInvalidChart = errors.New("invalid Helm chart or provenance")
var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// Metadata is Helm's Chart.yaml metadata. Original Chart.yaml and archives are
// preserved separately. No dependency URL is fetched while inspecting metadata.
type Metadata struct {
	APIVersion   string            `yaml:"apiVersion" json:"apiVersion"`
	Name         string            `yaml:"name" json:"name"`
	Version      string            `yaml:"version" json:"version"`
	KubeVersion  string            `yaml:"kubeVersion,omitempty" json:"kubeVersion,omitempty"`
	Description  string            `yaml:"description,omitempty" json:"description,omitempty"`
	Type         string            `yaml:"type,omitempty" json:"type,omitempty"`
	Keywords     []string          `yaml:"keywords,omitempty" json:"keywords,omitempty"`
	Home         string            `yaml:"home,omitempty" json:"home,omitempty"`
	Sources      []string          `yaml:"sources,omitempty" json:"sources,omitempty"`
	Maintainers  []Maintainer      `yaml:"maintainers,omitempty" json:"maintainers,omitempty"`
	Icon         string            `yaml:"icon,omitempty" json:"icon,omitempty"`
	AppVersion   string            `yaml:"appVersion,omitempty" json:"appVersion,omitempty"`
	Deprecated   bool              `yaml:"deprecated,omitempty" json:"deprecated,omitempty"`
	Annotations  map[string]string `yaml:"annotations,omitempty" json:"annotations,omitempty"`
	Dependencies []Dependency      `yaml:"dependencies,omitempty" json:"dependencies,omitempty"`
}
type Maintainer struct {
	Name  string `yaml:"name" json:"name"`
	Email string `yaml:"email,omitempty" json:"email,omitempty"`
	URL   string `yaml:"url,omitempty" json:"url,omitempty"`
}
type Dependency struct {
	Name         string   `yaml:"name" json:"name"`
	Version      string   `yaml:"version" json:"version"`
	Repository   string   `yaml:"repository,omitempty" json:"repository,omitempty"`
	Condition    string   `yaml:"condition,omitempty" json:"condition,omitempty"`
	Tags         []string `yaml:"tags,omitempty" json:"tags,omitempty"`
	Enabled      bool     `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	ImportValues []any    `yaml:"import-values,omitempty" json:"import-values,omitempty"`
	Alias        string   `yaml:"alias,omitempty" json:"alias,omitempty"`
}
type Inspection struct {
	Metadata     Metadata
	MetadataJSON []byte
	ChartYAML    []byte
}

func ValidIdentity(name, version string) bool {
	if !namePattern.MatchString(name) || len(version) > 256 {
		return false
	}
	_, err := semver.StrictNewVersion(version)
	return err == nil
}
func Filename(name, version string) string { return name + "-" + version + ".tgz" }

// DecodeYAML rejects aliases, duplicate or non-string mapping keys, excessive
// nesting, extra documents and unbounded node counts before decoding values.
func DecodeYAML(data []byte, target any) error {
	if len(data) == 0 || len(data) > 1<<20 {
		return ErrInvalidChart
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var root yaml.Node
	if decoder.Decode(&root) != nil || len(root.Content) != 1 {
		return ErrInvalidChart
	}
	if decoder.Decode(&yaml.Node{}) != io.EOF {
		return ErrInvalidChart
	}
	count := 0
	var walk func(*yaml.Node, int) bool
	walk = func(n *yaml.Node, depth int) bool {
		count++
		if depth > 64 || count > 100000 || n.Kind == yaml.AliasNode || n.Anchor != "" {
			return false
		}
		if n.Kind == yaml.MappingNode {
			keys := map[string]bool{}
			for i := 0; i < len(n.Content); i += 2 {
				key := n.Content[i]
				if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || keys[key.Value] {
					return false
				}
				keys[key.Value] = true
			}
		}
		for _, child := range n.Content {
			if !walk(child, depth+1) {
				return false
			}
		}
		return true
	}
	if !walk(&root, 0) || root.Decode(target) != nil {
		return ErrInvalidChart
	}
	return nil
}
func metadata(data []byte) (Metadata, []byte, error) {
	var m Metadata
	if DecodeYAML(data, &m) != nil || !ValidIdentity(m.Name, m.Version) || (m.APIVersion != "v1" && m.APIVersion != "v2") || (m.Type != "" && m.Type != "application" && m.Type != "library") {
		return m, nil, ErrInvalidChart
	}
	for _, dep := range m.Dependencies {
		if !namePattern.MatchString(dep.Name) || (dep.Alias != "" && !namePattern.MatchString(dep.Alias)) {
			return m, nil, ErrInvalidChart
		}
		if _, err := semver.NewConstraint(dep.Version); err != nil {
			return m, nil, ErrInvalidChart
		}
	}
	encoded, err := json.Marshal(m)
	if err != nil || len(encoded) > 256<<10 {
		return m, nil, ErrInvalidChart
	}
	return m, encoded, nil
}

// Inspect reads exactly one gzip member and a bounded safe tar. Links, devices,
// duplicate paths, traversal, extra gzip members and nonzero trailing tar data
// are rejected; no filesystem path is ever created from an archive entry.
type archiveBudget struct {
	expanded int64
	entries  int
}
type boundedArchive struct {
	reader io.Reader
	budget *archiveBudget
	size   int64
	tail   [1024]byte
}

func (r *boundedArchive) Read(p []byte) (int, error) {
	remaining := MaxExpandedBytes - r.budget.expanded
	if remaining < 0 {
		return 0, ErrInvalidChart
	}
	if int64(len(p)) > remaining+1 {
		p = p[:remaining+1]
	}
	n, err := r.reader.Read(p)
	r.budget.expanded += int64(n)
	r.size += int64(n)
	if n >= len(r.tail) {
		copy(r.tail[:], p[n-len(r.tail):n])
	} else if n > 0 {
		copy(r.tail[:], r.tail[n:])
		copy(r.tail[len(r.tail)-n:], p[:n])
	}
	if r.budget.expanded > MaxExpandedBytes {
		return n, ErrInvalidChart
	}
	return n, err
}
func Inspect(reader io.Reader) (Inspection, error) { return inspect(reader, &archiveBudget{}, 0) }
func inspect(reader io.Reader, budget *archiveBudget, depth int) (Inspection, error) {
	var out Inspection
	if depth > 8 {
		return out, ErrInvalidChart
	}
	buffered := bufio.NewReader(reader)
	gz, err := gzip.NewReader(buffered)
	if err != nil {
		return out, ErrInvalidChart
	}
	defer func() { _ = gz.Close() }()
	gz.Multistream(false)
	limited := &boundedArchive{reader: gz, budget: budget}
	archive := tar.NewReader(limited)
	seen := map[string]bool{}
	root := ""
	for {
		h, e := archive.Next()
		if e == io.EOF {
			break
		}
		if e != nil || budget.entries >= maxEntries || h.Size < 0 || h.Size > MaxExpandedBytes {
			return out, ErrInvalidChart
		}
		budget.entries++
		entry := strings.TrimSuffix(h.Name, "/")
		if entry == "" || len(entry) > 4096 || strings.ContainsAny(entry, "\\\x00\r\n") || strings.HasPrefix(entry, "/") || path.Clean(entry) != entry || strings.Count(entry, "/") > 64 || seen[entry] {
			return out, ErrInvalidChart
		}
		for _, segment := range strings.Split(entry, "/") {
			if segment == "." || segment == ".." || segment == "" {
				return out, ErrInvalidChart
			}
		}
		seen[entry] = true
		first := strings.SplitN(entry, "/", 2)[0]
		if root == "" {
			root = first
		}
		if first != root {
			return out, ErrInvalidChart
		}
		if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA && h.Typeflag != tar.TypeDir {
			return out, ErrInvalidChart
		}
		if h.Typeflag != tar.TypeDir && !strings.Contains(entry, "/") {
			return out, ErrInvalidChart
		}
		if h.Typeflag == tar.TypeDir {
			if h.Size != 0 {
				return out, ErrInvalidChart
			}
			continue
		}
		if entry == root+"/Chart.yaml" {
			if h.Size < 1 || h.Size > 1<<20 || len(out.ChartYAML) != 0 {
				return out, ErrInvalidChart
			}
			out.ChartYAML, err = io.ReadAll(archive)
			if err != nil || int64(len(out.ChartYAML)) != h.Size {
				return out, ErrInvalidChart
			}
		} else if strings.HasPrefix(entry, root+"/charts/") && strings.HasSuffix(entry, ".tgz") {
			if h.Size > MaxChartBytes {
				return out, ErrInvalidChart
			}
			packaged, e := io.ReadAll(archive)
			if e != nil || int64(len(packaged)) != h.Size {
				return out, ErrInvalidChart
			}
			dependency, e := inspect(bytes.NewReader(packaged), budget, depth+1)
			if e != nil || path.Base(entry) != Filename(dependency.Metadata.Name, dependency.Metadata.Version) {
				return out, ErrInvalidChart
			}
		} else if _, err = io.Copy(io.Discard, archive); err != nil {
			return out, ErrInvalidChart
		}
	}
	// tar.Reader stops at end markers. Only zero padding may follow them, and
	// reading gzip through EOF verifies its checksum and uncompressed size.
	tail := make([]byte, 32<<10)
	for {
		n, e := limited.Read(tail)
		for _, b := range tail[:n] {
			if b != 0 {
				return out, ErrInvalidChart
			}
		}
		if e == io.EOF {
			break
		}
		if e != nil {
			return out, ErrInvalidChart
		}
	}
	if limited.size < 1024 || limited.size%512 != 0 || !bytes.Equal(limited.tail[:], make([]byte, 1024)) {
		return out, ErrInvalidChart
	}
	if _, err = buffered.ReadByte(); err != io.EOF {
		return out, ErrInvalidChart
	}
	out.Metadata, out.MetadataJSON, err = metadata(out.ChartYAML)
	if err != nil || out.Metadata.Name != root {
		return out, ErrInvalidChart
	}
	return out, nil
}

// InspectProvenance validates the original clearsigned document's structure,
// metadata and exact chart digest. Cryptographic publisher trust is deliberately
// consumer-owned: callers must run helm verify with an explicit public keyring.
func InspectProvenance(data []byte, chart Inspection, digest string) error {
	if len(data) == 0 || int64(len(data)) > MaxProvenanceBytes || !bytes.HasPrefix(data, []byte("-----BEGIN PGP SIGNED MESSAGE-----")) {
		return ErrInvalidChart
	}
	block, rest := clearsign.Decode(data)
	if block == nil || len(bytes.TrimSpace(rest)) != 0 || block.ArmoredSignature == nil {
		return ErrInvalidChart
	}
	sig, err := packet.Read(block.ArmoredSignature.Body)
	if err != nil {
		return ErrInvalidChart
	}
	if _, ok := sig.(*packet.Signature); !ok {
		return ErrInvalidChart
	}
	if _, err = packet.Read(block.ArmoredSignature.Body); err != io.EOF {
		return ErrInvalidChart
	}
	parts := bytes.Split(bytes.ReplaceAll(block.Plaintext, []byte("\r\n"), []byte("\n")), []byte("\n...\n"))
	if len(parts) != 2 {
		return ErrInvalidChart
	}
	_, encoded, err := metadata(parts[0])
	if err != nil || !bytes.Equal(encoded, chart.MetadataJSON) {
		return ErrInvalidChart
	}
	var sums struct {
		Files map[string]string `yaml:"files"`
	}
	if DecodeYAML(parts[1], &sums) != nil || len(sums.Files) != 1 || sums.Files[Filename(chart.Metadata.Name, chart.Metadata.Version)] != digest {
		return ErrInvalidChart
	}
	return nil
}
