package contracts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublishedOpenAPIBundlesResolveWithoutSourceTree(t *testing.T) {
	for _, name := range []string{"native-hosted-v1.json", "management-runtime-v1.json"} {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("..", "api", "openapi", name))
			if err != nil {
				t.Fatal(err)
			}
			var document any
			if err := json.Unmarshal(data, &document); err != nil {
				t.Fatal(err)
			}
			// Client generators also inspect extension objects. A release bundle
			// must not require the author's unshipped, relative source files.
			var inspect func(any, string)
			inspect = func(value any, location string) {
				switch node := value.(type) {
				case map[string]any:
					for key, child := range node {
						if key == "$ref" {
							ref, ok := child.(string)
							if !ok || !strings.HasPrefix(ref, "#/") {
								t.Errorf("published bundle requires an external source at %s: %v", location, child)
							}
						}
						inspect(child, location+"/"+key)
					}
				case []any:
					for _, child := range node {
						inspect(child, location+"/[]")
					}
				}
			}
			inspect(document, "#")
		})
	}
}
