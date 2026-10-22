package helm

import (
	"encoding/hex"
	"encoding/json"
	"net/url"
	"strings"
	"time"
)

const MaxIndexBytes = 1 << 20

type IndexEntry struct {
	Metadata `yaml:",inline"`
	Created  time.Time `yaml:"created"`
	Digest   string    `yaml:"digest"`
	URLs     []string  `yaml:"urls"`
}

// ParseIndex rejects ambiguous YAML and duplicate identities before resolving a
// single canonical HTTP asset URL. Alternative URLs are deliberately not tried:
// a missing or corrupt authoritative asset must not silently change origin.
func ParseIndex(data []byte, indexURL string) ([]IndexEntry, error) {
	var index struct {
		APIVersion string                  `yaml:"apiVersion"`
		Entries    map[string][]IndexEntry `yaml:"entries"`
	}
	base, err := url.Parse(indexURL)
	if err != nil || base == nil || DecodeYAML(data, &index) != nil || index.APIVersion != "v1" || index.Entries == nil {
		return nil, ErrInvalidChart
	}
	out := []IndexEntry{}
	seen := map[string]bool{}
	for name, entries := range index.Entries {
		if !namePattern.MatchString(name) || len(entries) == 0 {
			return nil, ErrInvalidChart
		}
		for _, entry := range entries {
			key := name + "@" + entry.Version
			if len(out) >= MaxCharts || seen[key] || entry.Name != name || !ValidIdentity(name, entry.Version) || (entry.APIVersion != "v1" && entry.APIVersion != "v2") || len(entry.URLs) != 1 {
				return nil, ErrInvalidChart
			}
			seen[key] = true
			digest, e := hex.DecodeString(entry.Digest)
			if e != nil || len(digest) != 32 || entry.Digest != strings.ToLower(entry.Digest) {
				return nil, ErrInvalidChart
			}
			encoded, e := json.Marshal(entry.Metadata)
			if e != nil || len(encoded) > 256<<10 {
				return nil, ErrInvalidChart
			}
			target, e := url.Parse(entry.URLs[0])
			if e != nil || entry.URLs[0] == "" {
				return nil, ErrInvalidChart
			}
			entry.URLs = []string{base.ResolveReference(target).String()}
			out = append(out, entry)
		}
	}
	return out, nil
}
