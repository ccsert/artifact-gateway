package npm

import (
	"encoding/base64"
	"encoding/hex"
	"testing"
)

func TestImportIntegrityUsesStrongestSRIAndChecksShasum(t *testing.T) {
	hashers := ImportHashers()
	sums := map[string][]byte{}
	for algorithm, h := range hashers {
		_, _ = h.Write([]byte("complete npm bytes"))
		sums[algorithm] = h.Sum(nil)
	}
	h1 := hex.EncodeToString(sums["sha1"])
	strong := "sha512-" + base64.StdEncoding.EncodeToString(sums["sha512"])
	for _, v := range []struct {
		sri, sha string
		want     bool
	}{{strong, h1, true}, {strong, "0000000000000000000000000000000000000000", false}, {"", h1, true}, {"", "", false}, {"sha512-AAAA", h1, false}, {"unknown-YQ==", h1, false}, {"sha1-AAAAAAAAAAAAAAAAAAAAAAAAAAA= " + strong, h1, true}} {
		if got := ImportIntegrity(v.sri, v.sha, sums); got != v.want {
			t.Fatalf("integrity acceptance=%v want=%v", got, v.want)
		}
	}
}
func TestImportPackumentRejectsMissingTagTargetAndVersionIdentity(t *testing.T) {
	for _, raw := range []string{`{"name":"widget","dist-tags":{"latest":"2.0.0"},"versions":{"1.0.0":{"name":"widget","version":"1.0.0","dist":{"tarball":"https://example.com/a.tgz","shasum":"a"}}}}`, `{"name":"widget","dist-tags":{},"versions":{"1.0.0":{"name":"other","version":"1.0.0","dist":{"tarball":"https://example.com/a.tgz","shasum":"a"}}}}`} {
		if _, err := DecodeImportPackument([]byte(raw), "widget"); err == nil {
			t.Fatal("incomplete/ambiguous packument accepted")
		}
	}
}
