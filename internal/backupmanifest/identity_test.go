package backupmanifest

import (
	"context"
	"strings"
	"testing"
)

func TestVersionedSoftwareIdentity(t *testing.T) {
	for _, kind := range []string{"binary", "oci-image"} {
		t.Run(kind, func(t *testing.T) {
			dir, m := fixture(t)
			m.SchemaVersion = 2
			m.Gateway.ImageDigest = ""
			m.Gateway.Artifact = &SoftwareArtifact{Kind: kind, SHA256: "sha256:" + strings.Repeat("b", 64), Platform: "linux/amd64"}
			r := Verify(context.Background(), dir, m)
			if r.Integrity != Verified || r.Consistency != Unknown {
				t.Fatalf("v2 identity: %+v", r)
			}
			m.Gateway.ImageDigest = "sha256:" + strings.Repeat("c", 64)
			if Verify(context.Background(), dir, m).Integrity == Verified {
				t.Fatal("ambiguous identities accepted")
			}
		})
	}
	dir, m := fixture(t)
	m.Gateway.Artifact = &SoftwareArtifact{Kind: "binary", SHA256: m.Gateway.ImageDigest, Platform: "linux/amd64"}
	if Verify(context.Background(), dir, m).Integrity == Verified {
		t.Fatal("v1 binary reinterpreted as image")
	}
	for _, kind := range []string{"", "archive", "Binary"} {
		m.SchemaVersion = 2
		m.Gateway.ImageDigest = ""
		m.Gateway.Artifact.Kind = kind
		if Verify(context.Background(), dir, m).Integrity == Verified {
			t.Fatal("unsupported kind accepted")
		}
	}
}
