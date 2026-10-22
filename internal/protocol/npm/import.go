package npm

import (
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash"
	"strings"

	"github.com/ccsert/artifact-gateway/internal/protocol/identity"
)

const ImportPackumentLimit = 64 << 20
const ImportVersionLimit = 1000

var ErrImportPackument = errors.New("invalid npm import packument")

type ImportPackument struct {
	Name     string                     `json:"name"`
	DistTags map[string]string          `json:"dist-tags"`
	Versions map[string]json.RawMessage `json:"versions"`
}

type ImportVersion struct {
	Name       string `json:"name"`
	Version    string `json:"version"`
	Deprecated string `json:"deprecated"`
	Dist       struct {
		Tarball   string `json:"tarball"`
		Integrity string `json:"integrity"`
		Shasum    string `json:"shasum"`
	} `json:"dist"`
}

// DecodeImportPackument admits complete package closures, including every tag
// target. There is no synthesized latest tag or filename-derived version.
func DecodeImportPackument(body []byte, name string) (ImportPackument, error) {
	var p ImportPackument
	if len(body) == 0 || len(body) > ImportPackumentLimit || json.Unmarshal(body, &p) != nil || p.Name != name || !ValidPackageName(name) || len(p.Versions) < 1 || len(p.Versions) > ImportVersionLimit || p.DistTags == nil {
		return p, ErrImportPackument
	}
	for version, raw := range p.Versions {
		var v ImportVersion
		if !ValidVersion(version) || json.Unmarshal(raw, &v) != nil || v.Name != name || v.Version != version || v.Dist.Tarball == "" || (v.Dist.Integrity == "" && v.Dist.Shasum == "") {
			return p, ErrImportPackument
		}
	}
	for tag, version := range p.DistTags {
		if !ValidDistTag(tag) || p.Versions[version] == nil {
			return p, ErrImportPackument
		}
	}
	return p, nil
}

// CanonicalImportManifest compares protocol metadata independently of registry
// URLs and integrity encodings. Those fields are authoritative byte facts.
func CanonicalImportManifest(raw []byte) ([]byte, error) {
	canonical, err := identity.NPMImportManifest(raw)
	if err != nil {
		return nil, ErrImportPackument
	}
	return canonical, nil
}

// ImportIntegrity verifies shasum and the strongest supported SRI algorithm.
// Alternative hashes at the same strength follow SRI matching semantics.
func ImportIntegrity(integrity, shasum string, sums map[string][]byte) bool {
	if shasum != "" && (len(shasum) != 40 || !strings.EqualFold(shasum, hex.EncodeToString(sums["sha1"]))) {
		return false
	}
	if integrity == "" {
		return shasum != ""
	}
	strength := map[string]int{"sha1": 1, "sha256": 2, "sha384": 3, "sha512": 4}
	strongest, matched := 0, false
	for _, token := range strings.Fields(integrity) {
		algorithm, encoded, ok := strings.Cut(token, "-")
		rank := strength[algorithm]
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if !ok || rank == 0 || err != nil || len(decoded) != len(sums[algorithm]) {
			return false
		}
		if rank > strongest {
			strongest, matched = rank, false
		}
		if rank == strongest && base64.StdEncoding.EncodeToString(decoded) == base64.StdEncoding.EncodeToString(sums[algorithm]) {
			matched = true
		}
	}
	return strongest > 0 && matched
}

func ImportHashers() map[string]hash.Hash {
	return map[string]hash.Hash{"sha1": sha1.New(), "sha256": sha256.New(), "sha384": sha512.New384(), "sha512": sha512.New()}
}
