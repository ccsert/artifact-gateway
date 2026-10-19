package contracts

import (
	"crypto/md5"
	"crypto/sha256"
	"fmt"
	"testing"
)

func TestConanPublishRecipeAcceptsEmptyPackageFields(t *testing.T) {
	schema := loadManagementRuntimeSpec(t).Components.Schemas["CreateConanPublishSession"].Value
	recipe := []byte("from conan import ConanFile\n")
	entries := fmt.Sprintf("conanfile.py: %x\n", md5.Sum(recipe))
	manifest := []byte("1700000000\n" + entries)
	body := map[string]any{
		"kind": "recipe", "reference": "pkg/1.0/user/stable",
		"recipeRevision": fmt.Sprintf("%x", md5.Sum([]byte(entries))),
		"packageId":      "", "packageRevision": "",
		"objects": []any{
			map[string]any{"name": "conanfile.py", "digest": fmt.Sprintf("sha256:%x", sha256.Sum256(recipe)), "size": len(recipe)},
			map[string]any{"name": "conanmanifest.txt", "digest": fmt.Sprintf("sha256:%x", sha256.Sum256(manifest)), "size": len(manifest)},
		},
	}
	if err := schema.VisitJSON(body); err != nil {
		t.Fatalf("recipe fields emitted by the Go request must satisfy the contract: %v", err)
	}
	for _, field := range []string{"packageId", "packageRevision"} {
		body[field] = "invalid/value"
		if err := schema.VisitJSON(body); err == nil {
			t.Fatalf("%s must remain one path segment", field)
		}
		body[field] = ""
	}
}
