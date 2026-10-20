package ocigraph

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"testing"
)

func TestSharedGraphDepthUsesLongestPath(t *testing.T) {
	for _, depth := range []int{8, 9, 11} {
		for _, deepestFirst := range []bool{false, true} {
			t.Run(fmt.Sprintf("depth=%d/deepestFirst=%v", depth, deepestFirst), func(t *testing.T) {
				config := []byte(`{}`)
				objects := []Object{{Digest: Digest(config), Size: int64(len(config)), MediaType: "application/vnd.oci.image.config.v1+json"}}
				ref := func(object Object) Descriptor {
					return descriptor(object.MediaType, object.Digest, object.Size)
				}
				add := func(document Document) Object {
					body, err := json.Marshal(document)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := Decode(body, document.MediaType); err != nil {
						t.Fatalf("invalid source fixture: %v", err)
					}
					object := Object{Digest: Digest(body), Size: int64(len(body)), MediaType: document.MediaType, Document: &document}
					objects = append(objects, object)
					return object
				}
				configRef := ref(objects[0])
				image := add(Document{SchemaVersion: 2, MediaType: OCIManifest, Config: &configRef, Layers: []Descriptor{}})
				children := []Descriptor{ref(image)}
				for i := 0; i < depth-2; i++ {
					index := add(Document{SchemaVersion: 2, MediaType: OCIIndex, Manifests: []Descriptor{children[len(children)-1]}})
					children = append(children, ref(index))
				}
				if deepestFirst {
					slices.Reverse(children)
				}
				root := add(Document{SchemaVersion: 2, MediaType: OCIIndex, Manifests: children})
				err := Validate(root.Digest, objects)
				if depth == 8 && err != nil {
					t.Fatalf("complete shared graph at the documented depth must pass: %v", err)
				}
				if depth > 8 && !errors.Is(err, ErrLimit) {
					t.Fatalf("complete shared graph with longest path %d must exceed the documented limit 8: %v", depth, err)
				}
			})
		}
	}
}
