// Package pypi owns bounded, non-executing Python distribution import parsing.
package pypi

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"net/textproto"
	"net/url"
	"path"
	"sort"
	"strings"

	"github.com/ccsert/artifact-gateway/internal/protocol/identity"
	"golang.org/x/net/html"
)

const (
	ImportFileLimit      = 64 << 20
	ImportMetadataLimit  = 2 << 20
	ImportExpandedLimit  = 256 << 20
	ImportFileCountLimit = 1000
)

var ErrImport = identity.ErrPyPIImport

type Distribution = identity.PyPIDistribution

func NormalizeName(s string) string                   { return identity.NormalizePyPIName(s) }
func ValidName(s string) bool                         { return identity.ValidPyPIName(s) }
func ValidVersion(s string) bool                      { return identity.ValidPyPIVersion(s) }
func ValidPath(s string) bool                         { return identity.ValidPyPIPath(s) }
func ValidDigest(s string) bool                       { return identity.ValidPyPIDigest(s) }
func FilenameIdentity(s string) (Distribution, error) { return identity.PyPIFilenameIdentity(s) }
func validText(s string, limit int) bool {
	return len(s) <= limit && !strings.ContainsAny(s, "\x00\r\n")
}

type File struct {
	Filename       string `json:"filename"`
	SourcePath     string `json:"sourcePath"`
	SHA256         string `json:"sha256"`
	RequiresPython string `json:"requiresPython"`
	Yanked         bool   `json:"yanked"`
	YankReason     string `json:"yankReason"`
}
type Snapshot struct {
	Project string `json:"project"`
	Files   []File `json:"files"`
}

func (s Snapshot) Validate() error {
	if !ValidName(s.Project) || len(s.Files) < 1 || len(s.Files) > ImportFileCountLimit {
		return ErrImport
	}
	seen := map[string]bool{}
	paths := map[string]bool{}
	for _, f := range s.Files {
		d, err := FilenameIdentity(f.Filename)
		if err != nil || d.Project != s.Project || !ValidPath(f.SourcePath) || path.Base(f.SourcePath) != f.Filename || !ValidDigest(f.SHA256) || !validText(f.RequiresPython, 4096) || !validText(f.YankReason, 4096) || !f.Yanked && f.YankReason != "" || seen[f.Filename] || paths[f.SourcePath] {
			return ErrImport
		}
		seen[f.Filename], paths[f.SourcePath] = true, true
	}
	return nil
}
func (s Snapshot) Canonical() ([]byte, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	s.Files = append([]File(nil), s.Files...)
	sort.Slice(s.Files, func(i, j int) bool { return s.Files[i].Filename < s.Files[j].Filename })
	return json.Marshal(s)
}
func DecodeSnapshot(body []byte) (Snapshot, error) {
	var s Snapshot
	if len(body) > ImportMetadataLimit {
		return s, ErrImport
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&s) != nil || decoder.Decode(new(any)) != io.EOF {
		return s, ErrImport
	}
	canonical, err := s.Canonical()
	if err != nil || !bytes.Equal(canonical, body) {
		return s, ErrImport
	}
	return s, nil
}

// ParseSimple freezes file selection semantics. Every link must stay inside the
// configured HTTPS origin/repository; redirects, base elements and duplicates
// cannot redirect source credentials or make two assets share one target name.
func ParseSimple(body []byte, project string, pageURL *url.URL, repositoryPrefix string) (Snapshot, error) {
	s := Snapshot{Project: project}
	if len(body) > ImportMetadataLimit || pageURL == nil || pageURL.Scheme != "https" {
		return s, ErrImport
	}
	tokenizer := html.NewTokenizer(bytes.NewReader(body))
	for {
		tt := tokenizer.Next()
		if tt == html.ErrorToken {
			if tokenizer.Err() != io.EOF {
				return s, ErrImport
			}
			break
		}
		if tt != html.StartTagToken && tt != html.SelfClosingTagToken {
			continue
		}
		if !uniqueAttributes(tokenizer.Raw()) {
			return s, ErrImport
		}
		token := tokenizer.Token()
		if token.Data == "base" {
			return s, ErrImport
		}
		if token.Data != "a" {
			continue
		}
		attrs := map[string]string{}
		for _, a := range token.Attr {
			if _, dup := attrs[a.Key]; dup {
				return s, ErrImport
			}
			attrs[a.Key] = a.Val
		}
		href, ok := attrs["href"]
		if !ok {
			return s, ErrImport
		}
		ref, err := url.Parse(href)
		if err != nil {
			return s, ErrImport
		}
		u := pageURL.ResolveReference(ref)
		if u.Scheme != "https" || u.Host != pageURL.Host || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || !strings.HasPrefix(u.Path, repositoryPrefix) || u.RawPath != "" {
			return s, ErrImport
		}
		alg, hash, ok := strings.Cut(u.Fragment, "=")
		if !ok || alg != "sha256" || !ValidDigest(hash) {
			return s, ErrImport
		}
		source := strings.TrimPrefix(u.Path, repositoryPrefix)
		reason, yanked := attrs["data-yanked"]
		s.Files = append(s.Files, File{Filename: path.Base(source), SourcePath: source, SHA256: hash, RequiresPython: attrs["data-requires-python"], Yanked: yanked, YankReason: reason})
		if len(s.Files) > ImportFileCountLimit {
			return s, ErrImport
		}
	}
	return s, s.Validate()
}

func readMetadata(data []byte, d Distribution) (Distribution, error) {
	h, err := textproto.NewReader(bufio.NewReader(bytes.NewReader(append(append([]byte(nil), data...), '\n')))).ReadMIMEHeader()
	if err != nil || len(h.Values("Name")) != 1 || len(h.Values("Version")) != 1 || len(h.Values("Requires-Python")) > 1 || NormalizeName(h.Get("Name")) != d.Project || h.Get("Version") != d.Version {
		return d, ErrImport
	}
	d.RequiresPython = h.Get("Requires-Python")
	if !validText(d.RequiresPython, 4096) {
		return d, ErrImport
	}
	return d, nil
}

// Inspect reads archives without extracting or executing their content. It
// consumes every compressed member/trailer and rejects duplicate/unsafe paths,
// ambiguous metadata, excess expansion and malformed ZIP/gzip checksums.
func Inspect(reader io.ReaderAt, size int64, filename string) (Distribution, error) {
	d, err := FilenameIdentity(filename)
	if err != nil || size <= 0 || size > ImportFileLimit {
		return d, ErrImport
	}
	var metadata []byte
	seen := map[string]bool{}
	var expanded int64
	accept := func(name string, r io.Reader, n int64, isMetadata bool) error {
		if !ValidPath(strings.TrimSuffix(name, "/")) || seen[name] || n < 0 || n > ImportExpandedLimit-expanded {
			return ErrImport
		}
		seen[name] = true
		if isMetadata {
			if metadata != nil || n > ImportMetadataLimit {
				return ErrImport
			}
			metadata, err = io.ReadAll(io.LimitReader(r, ImportMetadataLimit+1))
			if err != nil || int64(len(metadata)) != n {
				return ErrImport
			}
		} else {
			written, e := io.Copy(io.Discard, io.LimitReader(r, n+1))
			if e != nil || written != n {
				return ErrImport
			}
		}
		expanded += n
		return nil
	}
	if d.FileType == "bdist_wheel" {
		z, e := zip.NewReader(reader, size)
		if e != nil || len(z.File) > 10000 {
			return d, ErrImport
		}
		for _, f := range z.File {
			if !f.Mode().IsRegular() && !f.FileInfo().IsDir() {
				return d, ErrImport
			}
			if f.UncompressedSize64 > ImportExpandedLimit {
				return d, ErrImport
			}
			parts := strings.Split(f.Name, "/")
			isMetadata := strings.HasSuffix(f.Name, ".dist-info/METADATA")
			if isMetadata {
				if len(parts) != 2 {
					return d, ErrImport
				}
				prefix := strings.TrimSuffix(parts[0], ".dist-info")
				n, v, ok := strings.Cut(prefix, "-")
				if !ok || NormalizeName(n) != d.Project || v != d.Version {
					return d, ErrImport
				}
			}
			r, e := f.Open()
			if e != nil {
				return d, ErrImport
			}
			e = accept(f.Name, r, int64(f.UncompressedSize64), isMetadata)
			closeErr := r.Close()
			if e != nil || closeErr != nil {
				return d, ErrImport
			}
		}
	} else {
		gz, e := gzip.NewReader(io.NewSectionReader(reader, 0, size))
		if e != nil {
			return d, ErrImport
		}
		defer func() { _ = gz.Close() }()
		bounded := &io.LimitedReader{R: gz, N: ImportExpandedLimit + 1}
		tr := tar.NewReader(bounded)
		count := 0
		for {
			h, e := tr.Next()
			if errors.Is(e, io.EOF) {
				break
			}
			if e != nil {
				return d, ErrImport
			}
			count++
			if count > 10000 || (h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeDir) {
				return d, ErrImport
			}
			parts := strings.Split(h.Name, "/")
			isMetadata := strings.HasSuffix(h.Name, "/PKG-INFO")
			if isMetadata && (len(parts) != 2 || parts[0] != strings.TrimSuffix(filename, ".tar.gz")) {
				return d, ErrImport
			}
			if e = accept(h.Name, tr, h.Size, isMetadata); e != nil {
				return d, e
			}
		}
		if _, e = io.Copy(io.Discard, bounded); e != nil || bounded.N <= 0 {
			return d, ErrImport
		}
	}
	if metadata == nil {
		return d, ErrImport
	}
	return readMetadata(metadata, d)
}

// x/net/html follows HTML's first-attribute-wins rule. Imports reject duplicate
// attributes instead of silently choosing one security/selection fact.
func uniqueAttributes(raw []byte) bool {
	i := 1
	space := func(c byte) bool { return c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '\f' }
	for i < len(raw) && !space(raw[i]) && raw[i] != '>' && raw[i] != '/' {
		i++
	}
	seen := map[string]bool{}
	for i < len(raw) {
		for i < len(raw) && space(raw[i]) {
			i++
		}
		if i == len(raw) || raw[i] == '>' || raw[i] == '/' {
			return true
		}
		start := i
		for i < len(raw) && !space(raw[i]) && raw[i] != '=' && raw[i] != '>' && raw[i] != '/' {
			i++
		}
		if start == i {
			return false
		}
		key := strings.ToLower(string(raw[start:i]))
		if seen[key] {
			return false
		}
		seen[key] = true
		for i < len(raw) && space(raw[i]) {
			i++
		}
		if i == len(raw) || raw[i] != '=' {
			continue
		}
		i++
		for i < len(raw) && space(raw[i]) {
			i++
		}
		if i == len(raw) {
			return false
		}
		if raw[i] == '\'' || raw[i] == '"' {
			quote := raw[i]
			i++
			for i < len(raw) && raw[i] != quote {
				i++
			}
			if i == len(raw) {
				return false
			}
			i++
		} else {
			for i < len(raw) && !space(raw[i]) && raw[i] != '>' {
				i++
			}
		}
	}
	return true
}
