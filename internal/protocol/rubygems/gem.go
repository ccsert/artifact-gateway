// Package rubygems parses untrusted gem archives as data. It never evaluates
// Ruby, instantiates YAML objects, or deserializes client-supplied Marshal.
package rubygems

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

const MaxGemBytes int64 = 64 << 20
const MaxMetadataBytes int64 = 1 << 20
const MaxExpandedBytes int64 = 256 << 20
const MaxIndexEntries = 4096

var ErrInvalid = errors.New("invalid or unsupported RubyGem archive")
var nameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
var versionRE = regexp.MustCompile(`^[0-9]+(\.[0-9A-Za-z]+)*$`)
var platformRE = regexp.MustCompile(`^[A-Za-z0-9_]+(-[A-Za-z0-9_.]+)*$`)
var operators = map[string]bool{"=": true, "!=": true, ">": true, "<": true, ">=": true, "<=": true, "~>": true}

type Requirement struct {
	Operator string `json:"operator"`
	Version  string `json:"version"`
}
type Dependency struct {
	Name         string        `json:"name"`
	Type         string        `json:"type"`
	Requirements []Requirement `json:"requirements"`
}
type Metadata struct {
	Name             string            `json:"name"`
	Version          string            `json:"version"`
	Platform         string            `json:"platform"`
	Summary          string            `json:"summary"`
	Description      string            `json:"description"`
	Authors          []string          `json:"authors"`
	Licenses         []string          `json:"licenses"`
	Dependencies     []Dependency      `json:"dependencies"`
	RequiredRuby     []Requirement     `json:"requiredRuby"`
	RequiredRubyGems []Requirement     `json:"requiredRubyGems"`
	Extra            map[string]string `json:"metadata"`
	Files            []string          `json:"files"`
}
type Inspection struct {
	Metadata     Metadata
	MetadataJSON []byte
	MetadataYAML []byte
}

func ValidName(s string) bool {
	return nameRE.MatchString(s) && strings.IndexFunc(s, func(r rune) bool { return r > 127 }) < 0 && strings.IndexFunc(s, func(r rune) bool { return r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' }) >= 0
}
func ValidVersion(s string) bool { return len(s) <= 128 && versionRE.MatchString(s) }
func ValidPlatform(s string) bool {
	return len(s) > 0 && len(s) <= 128 && platformRE.MatchString(s) && canonicalPlatform(s) == s
}
func (m Metadata) FullName() string {
	s := m.Name + "-" + m.Version
	if m.Platform != "ruby" {
		s += "-" + m.Platform
	}
	return s
}
func (m Metadata) Prerelease() bool {
	return strings.IndexFunc(m.Version, func(r rune) bool { return r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' }) >= 0
}
func SafePath(s string) bool {
	return s != "" && len(s) <= 4096 && s == path.Clean(s) && !strings.HasPrefix(s, "/") && s != ".." && !strings.HasPrefix(s, "../") && !strings.ContainsAny(s, "\\\x00\r\n:") && utf8.ValidString(s)
}

func readBounded(r io.Reader, limit int64) ([]byte, error) {
	data, e := io.ReadAll(io.LimitReader(r, limit+1))
	if e != nil || int64(len(data)) > limit {
		return nil, ErrInvalid
	}
	return data, nil
}
func gunzip(data []byte, limit int64) ([]byte, error) {
	r := bytes.NewReader(data)
	z, e := gzip.NewReader(r)
	if e != nil {
		return nil, ErrInvalid
	}
	z.Multistream(false)
	out, e := readBounded(z, limit)
	closeErr := z.Close()
	if e != nil || closeErr != nil || r.Len() != 0 {
		return nil, ErrInvalid
	}
	return out, nil
}
func Inspect(r io.Reader) (Inspection, error) {
	var empty Inspection
	data, e := readBounded(r, MaxGemBytes)
	if e != nil {
		return empty, e
	}
	outerReader := bytes.NewReader(data)
	tr := tar.NewReader(outerReader)
	parts := map[string][]byte{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return empty, ErrInvalid
		}
		if h.Format != tar.FormatUSTAR || len(h.PAXRecords) != 0 || h.Typeflag != tar.TypeReg || h.Size < 1 || h.Size > MaxGemBytes || len(parts) >= 3 || parts[h.Name] != nil {
			return empty, ErrInvalid
		}
		if h.Name != "metadata.gz" && h.Name != "data.tar.gz" && h.Name != "checksums.yaml.gz" {
			return empty, ErrInvalid
		}
		b, err := readBounded(tr, MaxGemBytes)
		if err != nil || int64(len(b)) != h.Size {
			return empty, ErrInvalid
		}
		parts[h.Name] = b
	}
	if len(parts) != 3 {
		return empty, ErrInvalid
	}
	// The outer tar must consume its complete 512-byte closure. Extra archives
	// and non-zero data following the end marker are rejected.
	if len(data)%512 != 0 || !zeroTail(data[len(data)-outerReader.Len():]) {
		return empty, ErrInvalid
	}
	checks, e := gunzip(parts["checksums.yaml.gz"], 64<<10)
	if e != nil {
		return empty, e
	}
	n, e := parseYAML(checks)
	if e != nil {
		return empty, e
	}
	sums, e := mapping(n)
	if e != nil || len(sums) != 2 {
		return empty, ErrInvalid
	}
	for _, algorithm := range []string{"SHA256", "SHA512"} {
		entries, err := mapping(sums[algorithm])
		if err != nil || len(entries) != 2 {
			return empty, ErrInvalid
		}
		for _, name := range []string{"metadata.gz", "data.tar.gz"} {
			want := scalar(entries[name])
			var actual string
			if algorithm == "SHA256" {
				s := sha256.Sum256(parts[name])
				actual = hex.EncodeToString(s[:])
			} else {
				s := sha512.Sum512(parts[name])
				actual = hex.EncodeToString(s[:])
			}
			if want != actual {
				return empty, ErrInvalid
			}
		}
	}
	y, e := gunzip(parts["metadata.gz"], MaxMetadataBytes)
	if e != nil {
		return empty, e
	}
	meta, e := ParseMetadata(y)
	if e != nil {
		return empty, e
	}
	payload, e := gunzip(parts["data.tar.gz"], MaxExpandedBytes)
	if e != nil {
		return empty, e
	}
	payloadReader := bytes.NewReader(payload)
	pt := tar.NewReader(payloadReader)
	seen := map[string]bool{}
	var total int64
	for count := 0; ; count++ {
		h, err := pt.Next()
		if err == io.EOF {
			break
		}
		if err != nil || h.Format != tar.FormatUSTAR || len(h.PAXRecords) != 0 || count >= 10000 || !SafePath(h.Name) || seen[h.Name] || h.Typeflag != tar.TypeReg || h.Size < 0 || h.Size > MaxExpandedBytes-total {
			return empty, ErrInvalid
		}
		seen[h.Name] = true
		total += h.Size
		if _, err = io.Copy(io.Discard, pt); err != nil {
			return empty, ErrInvalid
		}
	}
	if len(seen) == 0 || len(payload)%512 != 0 || !zeroTail(payload[len(payload)-payloadReader.Len():]) {
		return empty, ErrInvalid
	}
	if len(meta.Files) != len(seen) {
		return empty, ErrInvalid
	}
	for _, name := range meta.Files {
		if !seen[name] {
			return empty, ErrInvalid
		}
	}
	b, _ := json.Marshal(meta)
	return Inspection{meta, b, y}, nil
}

func zeroTail(tail []byte) bool {
	for _, b := range tail {
		if b != 0 {
			return false
		}
	}
	return true
}

func parseYAML(data []byte) (*yaml.Node, error) {
	if len(data) == 0 || int64(len(data)) > MaxMetadataBytes || !utf8.Valid(data) {
		return nil, ErrInvalid
	}
	var doc yaml.Node
	dec := yaml.NewDecoder(bytes.NewReader(data))
	if dec.Decode(&doc) != nil || len(doc.Content) != 1 {
		return nil, ErrInvalid
	}
	var extra yaml.Node
	if dec.Decode(&extra) != io.EOF {
		return nil, ErrInvalid
	}
	count := 0
	var walk func(*yaml.Node, int) bool
	walk = func(n *yaml.Node, depth int) bool {
		count++
		if depth > 32 || count > 30000 || n.Kind == yaml.AliasNode || n.Anchor != "" || len(n.Value) > 65536 {
			return false
		}
		switch n.Tag {
		case "!!map", "!!seq", "!!str", "!!int", "!!float", "!!null", "!!bool", "!!timestamp", "!ruby/object:Gem::Specification", "!ruby/object:Gem::Version", "!ruby/object:Gem::Dependency", "!ruby/object:Gem::Requirement", "!ruby/object:Gem::Platform":
		default:
			return false
		}
		if n.Kind == yaml.MappingNode {
			if len(n.Content)%2 != 0 {
				return false
			}
			keys := map[string]bool{}
			for i := 0; i < len(n.Content); i += 2 {
				k := n.Content[i]
				if !isRubyString(k) || keys[k.Value] || k.Value == "<<" {
					return false
				}
				keys[k.Value] = true
			}
		}
		for _, c := range n.Content {
			if !walk(c, depth+1) {
				return false
			}
		}
		return validRubyYAMLObject(n)
	}
	if !walk(doc.Content[0], 0) {
		return nil, ErrInvalid
	}
	return doc.Content[0], nil
}
func mapping(n *yaml.Node) (map[string]*yaml.Node, error) {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil, ErrInvalid
	}
	m := map[string]*yaml.Node{}
	for i := 0; i < len(n.Content); i += 2 {
		m[n.Content[i].Value] = n.Content[i+1]
	}
	return m, nil
}
func scalar(n *yaml.Node) string {
	if n == nil || n.Kind != yaml.ScalarNode || n.Tag == "!!null" {
		return ""
	}
	return n.Value
}

func stringScalar(n *yaml.Node) string {
	if !isRubyString(n) {
		return ""
	}
	return n.Value
}

func isRubyString(n *yaml.Node) bool {
	if n == nil || n.Kind != yaml.ScalarNode || n.Tag != "!!str" {
		return false
	}
	if n.Style != 0 {
		return true
	}
	if strings.HasPrefix(n.Value, ":") {
		return false
	}
	switch strings.ToLower(n.Value) {
	case "yes", "no", "on", "off":
		return false
	}
	return true
}
func stringList(n *yaml.Node) ([]string, error) {
	out := []string{}
	if n == nil || n.Tag == "!!null" {
		return out, nil
	}
	if n.Kind != yaml.SequenceNode || len(n.Content) > 10000 {
		return nil, ErrInvalid
	}
	for _, v := range n.Content {
		if !isRubyString(v) {
			return nil, ErrInvalid
		}
		out = append(out, v.Value)
	}
	return out, nil
}
func version(n *yaml.Node) string {
	m, e := mapping(n)
	if e != nil || n.Tag != "!ruby/object:Gem::Version" || len(m) != 1 {
		return ""
	}
	return stringScalar(m["version"])
}
func requirements(n *yaml.Node) ([]Requirement, error) {
	if n == nil {
		return []Requirement{{">=", "0"}}, nil
	}
	m, e := mapping(n)
	if e != nil || n.Tag != "!ruby/object:Gem::Requirement" || len(m) != 1 {
		return nil, ErrInvalid
	}
	seq := m["requirements"]
	if seq == nil || seq.Kind != yaml.SequenceNode || len(seq.Content) == 0 || len(seq.Content) > 32 {
		return nil, ErrInvalid
	}
	out := []Requirement{}
	for _, entry := range seq.Content {
		if entry.Kind != yaml.SequenceNode || len(entry.Content) != 2 {
			return nil, ErrInvalid
		}
		r := Requirement{stringScalar(entry.Content[0]), version(entry.Content[1])}
		if !operators[r.Operator] || !ValidVersion(r.Version) {
			return nil, ErrInvalid
		}
		out = append(out, r)
	}
	return out, nil
}
func ParseMetadata(y []byte) (Metadata, error) {
	var out Metadata
	n, e := parseYAML(y)
	if e != nil || n.Tag != "!ruby/object:Gem::Specification" {
		return out, ErrInvalid
	}
	m, e := mapping(n)
	if e != nil {
		return out, e
	}
	out.Name, out.Version = stringScalar(m["name"]), version(m["version"])
	out.Platform, _ = specificationPlatform(m["platform"])
	specificationVersion := m["specification_version"]
	if !ValidName(out.Name) || !ValidVersion(out.Version) || !ValidPlatform(out.Platform) || specificationVersion == nil || specificationVersion.Tag != "!!int" || scalar(specificationVersion) != "4" {
		return out, ErrInvalid
	}
	out.Summary, out.Description = stringScalar(m["summary"]), stringScalar(m["description"])
	if out.Summary == "" {
		return out, ErrInvalid
	}
	if out.Authors, e = stringList(m["authors"]); e != nil {
		return out, e
	}
	if out.Licenses, e = stringList(m["licenses"]); e != nil {
		return out, e
	}
	if out.RequiredRuby, e = requirements(m["required_ruby_version"]); e != nil {
		return out, e
	}
	if out.RequiredRubyGems, e = requirements(m["required_rubygems_version"]); e != nil {
		return out, e
	}
	out.Dependencies = []Dependency{}
	if deps := m["dependencies"]; deps != nil {
		if deps.Kind != yaml.SequenceNode || len(deps.Content) > 256 {
			return out, ErrInvalid
		}
		seen := map[string]bool{}
		for _, dep := range deps.Content {
			d, err := mapping(dep)
			if err != nil || dep.Tag != "!ruby/object:Gem::Dependency" {
				return out, ErrInvalid
			}
			typeNode := d["type"]
			if typeNode == nil || typeNode.Kind != yaml.ScalarNode || typeNode.Tag != "!!str" || typeNode.Style != 0 {
				return out, ErrInvalid
			}
			typeValue := typeNode.Value
			if typeValue != ":runtime" && typeValue != ":development" {
				return out, ErrInvalid
			}
			v := Dependency{Name: stringScalar(d["name"]), Type: strings.TrimPrefix(typeValue, ":")}
			if !ValidName(v.Name) || seen[v.Name] || (v.Type != "runtime" && v.Type != "development") {
				return out, ErrInvalid
			}
			seen[v.Name] = true
			if d["requirement"] == nil {
				return out, ErrInvalid
			}
			v.Requirements, err = requirements(d["requirement"])
			if err != nil {
				return out, err
			}
			if legacy := d["version_requirements"]; legacy != nil {
				other, err := requirements(legacy)
				if err != nil {
					return out, err
				}
				a, _ := json.Marshal(other)
				b, _ := json.Marshal(v.Requirements)
				if !bytes.Equal(a, b) {
					return out, ErrInvalid
				}
			}
			out.Dependencies = append(out.Dependencies, v)
		}
	}
	sort.Slice(out.Dependencies, func(i, j int) bool { return out.Dependencies[i].Name < out.Dependencies[j].Name })
	out.Extra = map[string]string{}
	if extra := m["metadata"]; extra != nil {
		items, e := mapping(extra)
		if e != nil || len(items) > 128 {
			return out, ErrInvalid
		}
		for k, v := range items {
			if len(k) > 128 || !isRubyString(v) || len(v.Value) > 1024 {
				return out, ErrInvalid
			}
			out.Extra[k] = v.Value
		}
	}
	for _, field := range []string{"files", "extensions", "extra_rdoc_files", "test_files"} {
		values, e := stringList(m[field])
		if e != nil {
			return out, e
		}
		for _, s := range values {
			if !SafePath(s) {
				return out, ErrInvalid
			}
		}
		if field == "files" {
			out.Files = values
			seen := map[string]bool{}
			for _, s := range values {
				if seen[s] {
					return out, ErrInvalid
				}
				seen[s] = true
			}
		}
	}
	return out, nil
}
