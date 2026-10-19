package contracts

import (
	"go/build"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// Integration-tagged persistence tests must run in the same required CI command
// as the existing database lifecycle suites, rather than only on manual invocations.
func TestRequiredIntegrationCommandCoversIntegrationTaggedPackages(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("..", "compose.integration.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var compose struct {
		Services map[string]struct {
			Command []string `yaml:"command"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(content, &compose); err != nil {
		t.Fatal(err)
	}
	command := compose.Services["test"].Command
	if len(command) < 2 || command[0] != "go" || command[1] != "test" || !slices.Contains(command, "-tags=integration") {
		t.Fatalf("required integration service must execute Go integration tests: %v", command)
	}
	baseline := build.Default
	baseline.GOOS = "linux"
	baseline.GOARCH = "amd64"
	integration := baseline
	integration.BuildTags = []string{"integration"}
	required := map[string]bool{}
	err = filepath.WalkDir(filepath.Join("..", "internal"), func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		dir := filepath.Dir(path)
		enabled, err := integration.MatchFile(dir, entry.Name())
		if err != nil {
			return err
		}
		ordinary, err := baseline.MatchFile(dir, entry.Name())
		if err != nil {
			return err
		}
		if enabled && !ordinary {
			relative, err := filepath.Rel("..", dir)
			if err != nil {
				return err
			}
			required["./"+filepath.ToSlash(relative)] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(required) == 0 {
		t.Fatal("integration-tagged packages were not discovered")
	}
	for pkg := range required {
		included := false
		for _, argument := range command[2:] {
			if argument == pkg || strings.HasSuffix(argument, "/...") && strings.HasPrefix(pkg+"/", strings.TrimSuffix(argument, "...")) {
				included = true
				break
			}
		}
		if !included {
			t.Errorf("required integration service omits %s; include it in compose.integration.yml test.command", pkg)
		}
	}
}
