// Package ocifixture supplies a self-contained, independently produced image
// graph. Callers freeze Bytes before handing metadata to a Gateway fixture.
package ocifixture

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ccsert/artifact-gateway/internal/ocigraph"
)

type Fixture struct {
	Name, Reference, Root string
	Bytes                 map[string][]byte
	MediaTypes            map[string]string
	Digests               map[string]string
}

func New(seed string) Fixture { return build(seed, ocigraph.OCIManifest, ocigraph.OCIIndex, false) }

func NewDocker(seed string) Fixture {
	return build(seed, ocigraph.DockerManifest, ocigraph.DockerList, false)
}

func NewNested(seed string) Fixture {
	return build(seed, ocigraph.OCIManifest, ocigraph.OCIIndex, true)
}

func build(seed, imageType, indexType string, nested bool) Fixture {
	configType, layerType := "application/vnd.oci.image.config.v1+json", "application/vnd.oci.image.layer.v1.tar+gzip"
	if imageType == ocigraph.DockerManifest {
		configType, layerType = "application/vnd.docker.container.image.v1+json", "application/vnd.docker.image.rootfs.diff.tar.gzip"
	}
	f := Fixture{Name: "team/widget", Reference: "release", Bytes: map[string][]byte{}, MediaTypes: map[string]string{}, Digests: map[string]string{}}
	var layer bytes.Buffer
	tarWriter := tar.NewWriter(&layer)
	content := []byte("Nexus migration fixture " + seed + "\n")
	if err := tarWriter.WriteHeader(&tar.Header{Name: "fixture.txt", Mode: 0644, Size: int64(len(content))}); err != nil {
		panic(err)
	}
	if _, err := tarWriter.Write(content); err != nil {
		panic(err)
	}
	if err := tarWriter.Close(); err != nil {
		panic(err)
	}
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	if _, err := gz.Write(layer.Bytes()); err != nil {
		panic(err)
	}
	if err := gz.Close(); err != nil {
		panic(err)
	}
	layerDigest := ocigraph.Digest(compressed.Bytes())
	f.add("v2/-/blobs/"+layerDigest, compressed.Bytes(), layerType)
	children := []map[string]any{}
	for _, arch := range []string{"amd64", "arm64"} {
		config := []byte(fmt.Sprintf(`{"architecture":%q,"os":"linux","rootfs":{"type":"layers","diff_ids":[%q]},"config":{"Labels":{"fixture":%q}}}`, arch, ocigraph.Digest(layer.Bytes()), seed))
		configDigest := ocigraph.Digest(config)
		f.add("v2/-/blobs/"+configDigest, config, configType)
		manifest, _ := json.Marshal(map[string]any{"schemaVersion": 2, "mediaType": imageType, "config": map[string]any{"mediaType": configType, "digest": configDigest, "size": len(config)}, "layers": []map[string]any{{"mediaType": layerType, "digest": layerDigest, "size": compressed.Len()}}})
		manifestDigest := ocigraph.Digest(manifest)
		f.add("v2/"+f.Name+"/manifests/"+manifestDigest, manifest, imageType)
		children = append(children, map[string]any{"mediaType": imageType, "digest": manifestDigest, "size": len(manifest), "platform": map[string]string{"architecture": arch, "os": "linux"}})
	}
	root, _ := json.Marshal(map[string]any{"schemaVersion": 2, "mediaType": indexType, "manifests": children})
	if nested {
		nestedChildren := []map[string]any{}
		for _, child := range children {
			innerBytes, _ := json.Marshal(map[string]any{"schemaVersion": 2, "mediaType": indexType, "manifests": []map[string]any{child}})
			inner := ocigraph.Digest(innerBytes)
			f.add("v2/"+f.Name+"/manifests/"+inner, innerBytes, indexType)
			nestedChildren = append(nestedChildren, map[string]any{"mediaType": indexType, "digest": inner, "size": len(innerBytes), "platform": child["platform"]})
		}
		root, _ = json.Marshal(map[string]any{"schemaVersion": 2, "mediaType": indexType, "manifests": nestedChildren})
	}
	f.Root = ocigraph.Digest(root)
	f.add("v2/"+f.Name+"/manifests/"+f.Reference, root, indexType)
	return f
}

func (f *Fixture) add(path string, body []byte, mediaType string) {
	f.Bytes[path] = bytes.Clone(body)
	f.MediaTypes[path] = mediaType
	f.Digests[path] = strings.TrimPrefix(ocigraph.Digest(body), "sha256:")
}
