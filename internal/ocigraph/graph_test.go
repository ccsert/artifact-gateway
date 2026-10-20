package ocigraph

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func size(n int64) *int64 { return &n }
func descriptor(mediaType, digest string, n int64) Descriptor {
	return Descriptor{MediaType: mediaType, Digest: digest, Size: size(n)}
}

func TestCompleteAllPlatformImageGraph(t *testing.T) {
	config, layer := "sha256:"+strings.Repeat("1", 64), "sha256:"+strings.Repeat("2", 64)
	amd, arm, nested, root := "sha256:"+strings.Repeat("3", 64), "sha256:"+strings.Repeat("4", 64), "sha256:"+strings.Repeat("5", 64), "sha256:"+strings.Repeat("6", 64)
	image := Document{SchemaVersion: 2, MediaType: OCIManifest, Config: func() *Descriptor { d := descriptor("application/vnd.oci.image.config.v1+json", config, 2); return &d }(), Layers: []Descriptor{descriptor("application/vnd.oci.image.layer.v1.tar+gzip", layer, 3)}}
	index := Document{SchemaVersion: 2, MediaType: OCIIndex, Manifests: []Descriptor{descriptor(OCIManifest, amd, 4), descriptor(OCIManifest, arm, 4)}}
	outer := Document{SchemaVersion: 2, MediaType: DockerList, Manifests: []Descriptor{descriptor(OCIIndex, nested, 5)}}
	objects := []Object{{Digest: config, Size: 2, MediaType: "application/vnd.oci.image.config.v1+json"}, {Digest: layer, Size: 3, MediaType: "application/vnd.oci.image.layer.v1.tar+gzip"}, {Digest: amd, Size: 4, MediaType: OCIManifest, Document: &image}, {Digest: arm, Size: 4, MediaType: OCIManifest, Document: &image}, {Digest: nested, Size: 5, MediaType: OCIIndex, Document: &index}, {Digest: root, Size: 6, MediaType: DockerList, Document: &outer}}
	if err := Validate(root, objects); err != nil {
		t.Fatal(err)
	}
	t.Run("missing member", func(t *testing.T) {
		if err := Validate(root, objects[1:]); !errors.Is(err, ErrIncomplete) {
			t.Fatalf("missing child: %v", err)
		}
	})
	t.Run("size mismatch", func(t *testing.T) {
		bad := append([]Object(nil), objects...)
		bad[0].Size = 20
		if err := Validate(root, bad); !errors.Is(err, ErrInvalid) {
			t.Fatalf("size mismatch: %v", err)
		}
	})
	t.Run("extra member", func(t *testing.T) {
		bad := append(append([]Object(nil), objects...), Object{Digest: "sha256:" + strings.Repeat("7", 64), Size: 0})
		if err := Validate(root, bad); !errors.Is(err, ErrInvalid) {
			t.Fatalf("extra member: %v", err)
		}
	})
	t.Run("duplicate member", func(t *testing.T) {
		bad := append(append([]Object(nil), objects...), objects[0])
		if err := Validate(root, bad); !errors.Is(err, ErrInvalid) {
			t.Fatalf("duplicate member: %v", err)
		}
	})
	t.Run("cycle", func(t *testing.T) {
		cyclic := Document{SchemaVersion: 2, MediaType: OCIIndex, Manifests: []Descriptor{descriptor(OCIIndex, root, 6)}}
		if err := Validate(root, []Object{{Digest: root, Size: 6, MediaType: OCIIndex, Document: &cyclic}}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("cycle: %v", err)
		}
	})
}

func TestDecodeRejectsUnsupportedImageSemantics(t *testing.T) {
	config := descriptor("application/vnd.oci.image.config.v1+json", "sha256:"+strings.Repeat("1", 64), 2)
	base := Document{SchemaVersion: 2, MediaType: OCIManifest, Config: &config, Layers: []Descriptor{}}
	body, _ := json.Marshal(base)
	if _, err := Decode(body, OCIManifest); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*Document)
		want   error
	}{
		{"foreign layer", func(d *Document) {
			d.Layers = []Descriptor{descriptor("application/vnd.docker.image.rootfs.foreign.diff.tar.gzip", config.Digest, 2)}
		}, ErrUnsupported},
		{"foreign URL", func(d *Document) {
			copy := *d.Config
			copy.URLs = []string{"https://foreign.example/layer"}
			d.Config = &copy
		}, ErrUnsupported},
		{"subject", func(d *Document) { d.Subject = json.RawMessage(`{"digest":"sha256:abcd"}`) }, ErrUnsupported},
		{"missing size", func(d *Document) { copy := *d.Config; copy.Size = nil; d.Config = &copy }, ErrInvalid},
		{"schema one", func(d *Document) { d.SchemaVersion = 1 }, ErrInvalid},
		{"unknown media", func(d *Document) { d.MediaType = "application/unknown" }, ErrUnsupported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := base
			tc.mutate(&d)
			encoded, _ := json.Marshal(d)
			if _, err := Decode(encoded, d.MediaType); !errors.Is(err, tc.want) {
				t.Fatalf("err=%v want=%v", err, tc.want)
			}
		})
	}
	if _, err := Decode(body, DockerManifest); !errors.Is(err, ErrInvalid) {
		t.Fatalf("body/header media mismatch: %v", err)
	}
}
