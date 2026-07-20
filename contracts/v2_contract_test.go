package contracts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This test is documentation-facing. Format implementations add their client
// fixtures beside it while this guards the decisions they share.
func TestV2ContractHasRequiredDecisions(t *testing.T) {
	document, err := os.ReadFile(filepath.Join("..", "docs", "v2-contract.md"))
	if err != nil {
		t.Fatalf("read V2 contract: %v", err)
	}

	for _, clause := range []string{
		"CONTRACT: anonymous-default-deny",
		"CONTRACT: audit-fields",
		"CONTRACT: raw-path-normalization",
		"CONTRACT: raw-proxy-allowlist",
		"CONTRACT: raw-cache",
		"CONTRACT: raw-checksum",
		"CONTRACT: conan2-only",
		"CONTRACT: conan-coordinate",
		"CONTRACT: conan2-read-endpoints",
		"CONTRACT: conan-cache",
		"CONTRACT: conan-proxy-allowlist",
		"CONTRACT: fixtures-and-upgrade",
	} {
		if !strings.Contains(string(document), clause) {
			t.Errorf("V2 contract is missing %q", clause)
		}
	}
}

func TestV2ContractScenarioSkeleton(t *testing.T) {
	document, err := os.ReadFile(filepath.Join("..", "docs", "v2-contract.md"))
	if err != nil {
		t.Fatalf("read V2 contract: %v", err)
	}

	// Concrete Raw/Conan adapters replace these document assertions with real
	// client fixtures while retaining the scenario names and expectations.
	for _, scenario := range []struct {
		name     string
		expected []string
	}{
		{"anonymous default deny", []string{"Every read begins denied", "effective anonymous policy is an AND", "`access_denied`"}},
		{"hosted precedes proxy", []string{"Hosted members are considered", "before Proxy members", "first successful Hosted response wins"}},
		{"raw canonical range and directory", []string{"returns `404` for a path ending in `/`", "RFC 7233", "multipart ranges are rejected with `416`"}},
		{"raw checksum and cache", []string{"CONTRACT: raw-checksum", "malformed sidecar returns `502`", "is not cached"}},
		{"conan2 client and coordinates", []string{"Conan 2.x client", "name/version@user/channel#rrev:package_id#prev", "CONTRACT: conan2-read-endpoints", "metadata checksums before caching"}},
		{"proxy restrictions", []string{"MUST use HTTPS", "never contacted", "separate from OCI and Maven"}},
		{"schema migration and bounded metrics", []string{"Migrations are additive, transactional, forward-only", "bounded labels only", "never a destructive down migration"}},
		{"fixtures and upgrade", []string{"Raw fixture MUST cover", "Conan fixture MUST use a Conan 2.x client", "Upgrade tests MUST apply"}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			for _, expected := range scenario.expected {
				if !strings.Contains(string(document), expected) {
					t.Errorf("contract scenario missing %q", expected)
				}
			}
		})
	}
}
