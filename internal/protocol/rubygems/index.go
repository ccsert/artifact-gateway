package rubygems

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"crypto/md5" // RubyGems compact-index protocol checksum; package integrity uses SHA-256.
	"encoding/hex"
	"sort"
	"strings"
)

type IndexEntry struct {
	Metadata Metadata
	Digest   string
}

func reqText(in []Requirement, separator string) string {
	out := make([]string, len(in))
	for i, r := range in {
		out[i] = r.Operator + " " + r.Version
	}
	return strings.Join(out, separator)
}
func Info(entries []IndexEntry) []byte {
	var b strings.Builder
	b.WriteString("---\n")
	for _, e := range sortedEntries(entries) {
		m := e.Metadata
		b.WriteString(m.Version)
		if m.Platform != "ruby" {
			b.WriteString("-" + m.Platform)
		}
		b.WriteByte(' ')
		deps := []string{}
		for _, d := range m.Dependencies {
			if d.Type == "runtime" {
				deps = append(deps, d.Name+":"+reqText(d.Requirements, "&"))
			}
		}
		b.WriteString(strings.Join(deps, ","))
		b.WriteString("|checksum:" + strings.TrimPrefix(e.Digest, "sha256:") + ",ruby:" + reqText(m.RequiredRuby, "&") + ",rubygems:" + reqText(m.RequiredRubyGems, "&") + "\n")
	}
	return []byte(b.String())
}
func Names(entries []IndexEntry) []byte {
	names := entryNames(entries)
	return []byte("---\n" + strings.Join(names, "\n") + "\n")
}
func Versions(entries []IndexEntry) []byte {
	var b strings.Builder
	b.WriteString("created_at: 1970-01-01T00:00:00Z\n---\n")
	for _, name := range entryNames(entries) {
		group := []IndexEntry{}
		versions := []string{}
		for _, e := range sortedEntries(entries) {
			if e.Metadata.Name == name {
				group = append(group, e)
				v := e.Metadata.Version
				if e.Metadata.Platform != "ruby" {
					v += "-" + e.Metadata.Platform
				}
				versions = append(versions, v)
			}
		}
		sum := md5.Sum(Info(group))
		b.WriteString(name + " " + strings.Join(versions, ",") + " " + hex.EncodeToString(sum[:]) + "\n")
	}
	return []byte(b.String())
}
func entryNames(entries []IndexEntry) []string {
	seen := map[string]bool{}
	names := []string{}
	for _, e := range entries {
		if !seen[e.Metadata.Name] {
			names = append(names, e.Metadata.Name)
			seen[e.Metadata.Name] = true
		}
	}
	sort.Strings(names)
	return names
}
func sortedEntries(in []IndexEntry) []IndexEntry {
	out := append([]IndexEntry(nil), in...)
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].Metadata, out[j].Metadata
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		if c := CompareVersion(a.Version, b.Version); c != 0 {
			return c < 0
		}
		return a.Platform < b.Platform
	})
	return out
}

// CompareVersion follows Gem::Version numeric/non-numeric segment precedence.
func CompareVersion(a, b string) int {
	split := func(s string) []string { return canonicalSegments(s) }
	aa, bb := split(a), split(b)
	n := max(len(aa), len(bb))
	for i := 0; i < n; i++ {
		x, y := "0", "0"
		if i < len(aa) {
			x = aa[i]
		}
		if i < len(bb) {
			y = bb[i]
		}
		xn, yn := x[0] >= '0' && x[0] <= '9', y[0] >= '0' && y[0] <= '9'
		if xn != yn {
			if xn {
				return 1
			}
			return -1
		}
		if xn {
			x = strings.TrimLeft(x, "0")
			y = strings.TrimLeft(y, "0")
			if len(x) != len(y) {
				if len(x) > len(y) {
					return 1
				}
				return -1
			}
		}
		if x < y {
			return -1
		}
		if x > y {
			return 1
		}
	}
	return 0
}
func regexpSegments(s string) []string {
	out := []string{}
	start := -1
	digit := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '.' {
			if start >= 0 {
				out = append(out, s[start:i])
				start = -1
			}
			continue
		}
		d := c >= '0' && c <= '9'
		if start < 0 {
			start = i
			digit = d
		} else if d != digit {
			out = append(out, s[start:i])
			start = i
			digit = d
		}
	}
	if start >= 0 {
		out = append(out, s[start:])
	}
	return out
}

type rubySymbol string
type rubyObject struct {
	class  string
	fields []any
}
type rubyUser struct {
	class string
	value any
}
type rubyDump struct {
	class string
	bytes []byte
}

// marshal only serializes server-owned primitives and fixed RubyGems classes.
// It has no reader, class lookup, eval, or client-controlled class names.
func marshal(v any) []byte {
	b := &bytes.Buffer{}
	b.Write([]byte{4, 8})
	writeRuby(b, v)
	return b.Bytes()
}
func rubyLong(b *bytes.Buffer, n int) {
	if n == 0 {
		b.WriteByte(0)
		return
	}
	if n > 0 && n < 123 {
		b.WriteByte(byte(n + 5))
		return
	}
	if n < 0 && n > -124 {
		b.WriteByte(byte(n - 5))
		return
	}
	count := 0
	value := n
	for value != 0 && value != -1 {
		count++
		value >>= 8
	}
	if n < 0 {
		b.WriteByte(byte(-count))
	} else {
		b.WriteByte(byte(count))
	}
	for i := 0; i < count; i++ {
		b.WriteByte(byte(n >> (8 * i)))
	}
}
func rubyRaw(b *bytes.Buffer, s string) { rubyLong(b, len(s)); b.WriteString(s) }
func writeRuby(b *bytes.Buffer, v any) {
	switch x := v.(type) {
	case nil:
		b.WriteByte('0')
	case bool:
		if x {
			b.WriteByte('T')
		} else {
			b.WriteByte('F')
		}
	case int:
		b.WriteByte('i')
		rubyLong(b, x)
	case string:
		b.WriteByte('I')
		b.WriteByte('"')
		rubyRaw(b, x)
		rubyLong(b, 1)
		writeRuby(b, rubySymbol("E"))
		b.WriteByte('T')
	case rubySymbol:
		b.WriteByte(':')
		rubyRaw(b, string(x))
	case []any:
		b.WriteByte('[')
		rubyLong(b, len(x))
		for _, v := range x {
			writeRuby(b, v)
		}
	case map[string]string:
		b.WriteByte('{')
		rubyLong(b, len(x))
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			writeRuby(b, k)
			writeRuby(b, x[k])
		}
	case rubyObject:
		b.WriteByte('o')
		writeRuby(b, rubySymbol(x.class))
		rubyLong(b, len(x.fields)/2)
		for _, f := range x.fields {
			writeRuby(b, f)
		}
	case rubyUser:
		b.WriteByte('U')
		writeRuby(b, rubySymbol(x.class))
		writeRuby(b, x.value)
	case rubyDump:
		b.WriteByte('u')
		writeRuby(b, rubySymbol(x.class))
		rubyLong(b, len(x.bytes))
		b.Write(x.bytes)
	default:
		panic("unsupported server RubyGems value")
	}
}
func rubyVersion(s string) any { return rubyUser{"Gem::Version", []any{s}} }
func rubyRequirement(in []Requirement) any {
	r := []any{}
	for _, v := range in {
		r = append(r, []any{v.Operator, rubyVersion(v.Version)})
	}
	return rubyUser{"Gem::Requirement", []any{r}}
}
func rubyStrings(in []string) []any {
	out := []any{}
	for _, s := range in {
		out = append(out, s)
	}
	return out
}
func Specification(m Metadata) []byte {
	deps := []any{}
	for _, d := range m.Dependencies {
		deps = append(deps, rubyObject{"Gem::Dependency", []any{rubySymbol("@name"), d.Name, rubySymbol("@requirement"), rubyRequirement(d.Requirements), rubySymbol("@type"), rubySymbol(d.Type), rubySymbol("@prerelease"), false}})
	}
	fields := []any{"3.6.7", 4, m.Name, rubyVersion(m.Version), "1970-01-01", m.Summary, rubyRequirement(m.RequiredRuby), rubyRequirement(m.RequiredRubyGems), m.Platform, deps, "", nil, rubyStrings(m.Authors), m.Description, nil, true, m.Platform, rubyStrings(m.Licenses), m.Extra}
	return marshal(rubyDump{"Gem::Specification", marshal(fields)})
}
func QuickSpecification(m Metadata) []byte {
	var b bytes.Buffer
	z := zlib.NewWriter(&b)
	_, _ = z.Write(Specification(m))
	_ = z.Close()
	return b.Bytes()
}
func LegacySpecs(entries []IndexEntry, kind string) []byte {
	items := []any{}
	latest := map[string]IndexEntry{}
	for _, e := range sortedEntries(entries) {
		m := e.Metadata
		if kind == "prerelease" && !m.Prerelease() || kind != "prerelease" && m.Prerelease() {
			continue
		}
		if kind == "latest" {
			key := m.Name + "\x00" + m.Platform
			latest[key] = e
			continue
		}
		items = append(items, []any{m.Name, rubyVersion(m.Version), m.Platform})
	}
	if kind == "latest" {
		list := []IndexEntry{}
		for _, e := range latest {
			list = append(list, e)
		}
		for _, e := range sortedEntries(list) {
			items = append(items, []any{e.Metadata.Name, rubyVersion(e.Metadata.Version), e.Metadata.Platform})
		}
	}
	var b bytes.Buffer
	z := gzip.NewWriter(&b)
	_, _ = z.Write(marshal(items))
	_ = z.Close()
	return b.Bytes()
}
func RequirementsText(in []Requirement) string { return reqText(in, ", ") }
func CanonicalVersion(v string) string {
	return strings.Join(canonicalSegments(v), ".")
}

func canonicalSegments(v string) []string {
	s := regexpSegments(v)
	for i, x := range s {
		if x[0] >= '0' && x[0] <= '9' {
			x = strings.TrimLeft(x, "0")
			if x == "" {
				x = "0"
			}
			s[i] = x
		}
	}
	for len(s) > 0 && s[len(s)-1] == "0" {
		s = s[:len(s)-1]
	}
	firstLetter := len(s)
	for i, x := range s {
		if x[0] < '0' || x[0] > '9' {
			firstLetter = i
			break
		}
	}
	cut := firstLetter
	for cut > 0 && s[cut-1] == "0" {
		cut--
	}
	if cut < firstLetter {
		s = append(append([]string(nil), s[:cut]...), s[firstLetter:]...)
	}
	if len(s) == 0 {
		return []string{"0"}
	}
	return s
}
