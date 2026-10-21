package ocidiscovery

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ccsert/artifact-gateway/internal/ocigraph"
)

func TestReferrerManifestSubjectAndDescriptors(t *testing.T) {
	subject := Descriptor{Digest: "sha256:" + strings.Repeat("a", 64), MediaType: ocigraph.OCIManifest, Size: 42}
	for _, tc := range []struct {
		name, subject, config, layers, artifact string
		bad                                     bool
	}{
		{"sbom", fmt.Sprintf(`{"digest":%q,"size":42,"mediaType":%q}`, subject.Digest, subject.MediaType), `{"digest":"sha256:` + strings.Repeat("b", 64) + `","size":2,"mediaType":"application/vnd.oci.empty.v1+json"}`, `[]`, "application/example.sbom", false},
		{"wrong subject", `{"digest":"sha256:` + strings.Repeat("f", 64) + `","size":42,"mediaType":"application/vnd.oci.image.manifest.v1+json"}`, `{}`, `[]`, "", true},
		{"missing subject", `null`, `{}`, `[]`, "", true},
		{"missing size", fmt.Sprintf(`{"digest":%q,"mediaType":%q}`, subject.Digest, subject.MediaType), `{}`, `[]`, "", true},
		{"foreign layer", fmt.Sprintf(`{"digest":%q,"size":42,"mediaType":%q}`, subject.Digest, subject.MediaType), `{"digest":"sha256:` + strings.Repeat("b", 64) + `","size":2,"mediaType":"application/vnd.oci.empty.v1+json"}`, `[{"digest":"sha256:` + strings.Repeat("c", 64) + `","size":1,"mediaType":"application/example.sbom","urls":["https://private.invalid/payload"]}]`, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{"schemaVersion":2,"mediaType":%q,"subject":%s,"artifactType":%q,"config":%s,"layers":%s}`, ocigraph.OCIManifest, tc.subject, tc.artifact, tc.config, tc.layers))
			d := Descriptor{Digest: ocigraph.Digest(body), Size: int64(len(body)), MediaType: ocigraph.OCIManifest}
			children, artifact, err := DecodeArtifact(body, d, &subject)
			if (err != nil) != tc.bad {
				t.Fatalf("children=%+v metadata=%+v err=%v", children, artifact, err)
			}
			if !tc.bad && (len(children) != 1 || artifact.ArtifactType != tc.artifact) {
				t.Fatalf("metadata=%+v children=%+v", artifact, children)
			}
		})
	}
}
