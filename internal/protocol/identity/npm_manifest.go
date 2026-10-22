package identity

import (
	"bytes"
	"encoding/json"
	"errors"
)

var ErrInvalidNPMManifest = errors.New("invalid npm version manifest")

// NPMImportManifest preserves version metadata while removing registry-owned
// transport facts. Source verification and native publication share this exact
// equivalence rule; JSON numbers retain their original precision.
func NPMImportManifest(raw []byte) ([]byte, error) {
	var doc map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&doc); err != nil {
		return nil, err
	}
	if doc == nil {
		return nil, ErrInvalidNPMManifest
	}
	dist, ok := doc["dist"].(map[string]any)
	if !ok {
		return nil, ErrInvalidNPMManifest
	}
	delete(dist, "tarball")
	delete(dist, "integrity")
	delete(dist, "shasum")
	return json.Marshal(doc)
}
