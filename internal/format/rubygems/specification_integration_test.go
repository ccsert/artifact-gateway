//go:build integration

package rubygems

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	protocol "github.com/ccsert/artifact-gateway/internal/protocol/rubygems"
	gemfixture "github.com/ccsert/artifact-gateway/internal/testsupport/rubygemsfixture"
	"github.com/google/uuid"
)

func TestRubyGemsFixedOfficialSpecificationDateAndRequirePaths(t *testing.T) {
	image := os.Getenv("RUBYGEMS_TEST_IMAGE")
	if image == "" {
		t.Skip("fixed official Ruby image required")
	}
	root := t.TempDir()
	type example struct {
		Name     string `json:"name"`
		Tail     string `json:"tail"`
		Accepted bool   `json:"accepted"`
	}
	cases := []example{
		{"missing-date", "", true},
		{"null-date", "date:\n", true},
		{"iso-date", "date: 2026-10-23\n", true},
		{"quoted-date", "date: '2026-10-23'\n", true},
		{"official-timestamp", "date: 2026-10-23 00:00:00.000000000 Z\n", true},
		{"iso-timestamp", "date: 2026-10-23T00:00:00Z\n", true},
		{"bad-date", "date: definitely-not-a-date\n", false},
		{"empty-date", "date: ''\n", false},
		{"invalid-month", "date: '2026-13-01'\n", false},
		{"quoted-iso-timestamp", "date: '2026-10-23T00:00:00Z'\n", false},
		{"scalar-require-path", "require_paths: lib\n", false},
		{"flat-require-paths", "require_paths: [lib, extra]\n", true},
		{"nested-require-paths", "require_paths: [[lib], [extra]]\n", true},
		{"tail-version-array", "extra_tail: !ruby/object:Gem::Version\n  version: [bad]\n", false},
		{"tail-spec-invalid-date", "extra_tail: !ruby/object:Gem::Specification\n  date: definitely-not-a-date\n", false},
		{"tail-valid-version", "extra_tail: !ruby/object:Gem::Version\n  version: '1.2.0'\n", true},
		{"tail-invalid-instance-variable", "extra_tail: !ruby/object:Gem::Requirement\n  invalid-key: value\n", false},
	}
	for _, c := range cases {
		yaml := append(gemfixture.YAML("owned", "1.0.0", "ruby", "fixture", gemfixture.Dependency("leaf", "runtime")), c.Tail...)
		ownedFile(t, root, c.Name+".gem", string(gemfixture.Build(t, yaml, "lib/owned.rb")))
	}
	container := os.Getenv("RUBYGEMS_OWNED_PROJECT") + "-spec-" + uuid.NewString()
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	// Gem::Package#spec loads the archive metadata with the fixed official
	// parser. It neither evaluates a gemspec nor executes any payload file.
	probe := `require "json"; require "rubygems/package"; require "bundler"
abort "unexpected official toolchain" unless [RUBY_VERSION, Gem::VERSION, Bundler::VERSION] == ["3.4.4", "3.6.7", "2.6.7"]
puts JSON.generate(Dir["/fixtures/*.gem"].sort.map do |path|
  begin
    spec = Gem::Package.new(path).spec
    {name: File.basename(path, ".gem"), accepted: true, identity: [spec.name, spec.version.to_s, spec.platform.to_s], runtimeDependencies: spec.runtime_dependencies.map { |dep| [dep.name, dep.requirement.to_s] }, date: spec.date.utc.strftime("%Y-%m-%d"), requirePaths: spec.raw_require_paths}
  rescue StandardError => error
    {name: File.basename(path, ".gem"), accepted: false, errorClass: error.class.name, error: error.message}
  end
end)`
	cmd := exec.CommandContext(ctx, "docker", "run", "--rm", "--name", container, "--label", "com.artifact-gateway.rubygems-e2e.run="+os.Getenv("RUBYGEMS_OWNED_PROJECT"), "--network", "none", "--user", strconv.Itoa(os.Getuid())+":"+strconv.Itoa(os.Getgid()), "--mount", "type=bind,src="+root+",dst=/fixtures,readonly", image, "ruby", "-e", probe)
	output, err := cmd.Output()
	if ctx.Err() != nil {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		_ = exec.CommandContext(cleanup, "docker", "rm", "-f", container).Run()
		stop()
	}
	if err != nil {
		if failed, ok := err.(*exec.ExitError); ok {
			t.Fatalf("fixed official metadata probe: %v %s", err, failed.Stderr)
		}
		t.Fatal(err)
	}
	if evidence := os.Getenv("RUBYGEMS_OWNED_EVIDENCE_DIR"); evidence != "" {
		if err := os.WriteFile(filepath.Join(evidence, "official-specification-validation.json"), output, 0600); err != nil {
			t.Fatal(err)
		}
	}
	var official []struct {
		Name                string     `json:"name"`
		Accepted            bool       `json:"accepted"`
		Identity            []string   `json:"identity"`
		RuntimeDependencies [][]string `json:"runtimeDependencies"`
	}
	if err := json.Unmarshal(output, &official); err != nil || len(official) != len(cases) {
		t.Fatalf("fixed official probe output: %v %s", err, output)
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			var found bool
			for _, result := range official {
				if result.Name != c.Name {
					continue
				}
				found = true
				if result.Accepted != c.Accepted || result.Accepted && (len(result.Identity) != 3 || result.Identity[0] != "owned" || result.Identity[1] != "1.0.0" || result.Identity[2] != "ruby" || len(result.RuntimeDependencies) != 1 || result.RuntimeDependencies[0][0] != "leaf") {
					t.Fatalf("fixed official spec/dependency interpretation: %+v", result)
				}
			}
			if !found {
				t.Fatal("missing official case")
			}
			data, err := os.ReadFile(filepath.Join(root, c.Name+".gem"))
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := protocol.Inspect(bytes.NewReader(data))
			if (err == nil) != c.Accepted || err == nil && (parsed.Metadata.Name != "owned" || len(parsed.Metadata.Dependencies) != 1 || parsed.Metadata.Dependencies[0].Name != "leaf") {
				t.Fatalf("preview differs from supported fixed official specification: accepted=%v error=%v", err == nil, err)
			}
		})
	}
}
