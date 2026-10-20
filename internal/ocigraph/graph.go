// Package ocigraph validates complete, immutable OCI image dependency graphs.
package ocigraph

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
)

const (
	OCIManifest      = "application/vnd.oci.image.manifest.v1+json"
	OCIIndex         = "application/vnd.oci.image.index.v1+json"
	DockerManifest   = "application/vnd.docker.distribution.manifest.v2+json"
	DockerList       = "application/vnd.docker.distribution.manifest.list.v2+json"
	MaxDepth         = 8
	MaxObjects       = 1024
	MaxManifestBytes = 16 << 20
)

var ErrUnsupported = errors.New("oci_graph_unsupported")
var ErrInvalid = errors.New("oci_graph_invalid")
var ErrIncomplete = errors.New("oci_graph_incomplete")
var ErrLimit = errors.New("oci_graph_limit_exceeded")

var imageName = regexp.MustCompile(`^[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*(?:/[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*)*$`)
var tag = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)

func ValidName(value string) bool      { return len(value) <= 255 && imageName.MatchString(value) }
func ValidReference(value string) bool { return ValidDigest(value) || tag.MatchString(value) }

type Descriptor struct {
	MediaType string   `json:"mediaType"`
	Digest    string   `json:"digest"`
	Size      *int64   `json:"size"`
	URLs      []string `json:"urls,omitempty"`
	Data      string   `json:"data,omitempty"`
}

type Document struct {
	SchemaVersion int             `json:"schemaVersion"`
	MediaType     string          `json:"mediaType"`
	Config        *Descriptor     `json:"config"`
	Layers        []Descriptor    `json:"layers"`
	Manifests     []Descriptor    `json:"manifests"`
	Subject       json.RawMessage `json:"subject"`
	ArtifactType  string          `json:"artifactType"`
}

func Digest(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func ValidDigest(value string) bool {
	return len(value) == 71 && strings.HasPrefix(value, "sha256:") && strings.Trim(value[7:], "0123456789abcdef") == ""
}

func IsManifest(mediaType string) bool {
	return mediaType == OCIManifest || mediaType == DockerManifest
}

func IsIndex(mediaType string) bool { return mediaType == OCIIndex || mediaType == DockerList }

// Decode preserves raw bytes at the caller. It admits image manifests/indexes;
// artifact/referrer, foreign and embedded descriptors have separate semantics.
func Decode(body []byte, mediaType string) (Document, error) {
	var document Document
	if len(body) == 0 || len(body) > MaxManifestBytes {
		return document, ErrLimit
	}
	if json.Unmarshal(body, &document) != nil || document.SchemaVersion != 2 {
		return document, ErrInvalid
	}
	mediaType = strings.TrimSpace(strings.Split(mediaType, ";")[0])
	if mediaType == "" {
		mediaType = document.MediaType
	}
	if document.MediaType != "" && document.MediaType != mediaType {
		return document, ErrInvalid
	}
	document.MediaType = mediaType
	if !IsManifest(mediaType) && !IsIndex(mediaType) {
		return document, ErrUnsupported
	}
	if document.ArtifactType != "" || len(document.Subject) > 0 && string(document.Subject) != "null" {
		return document, ErrUnsupported
	}
	if IsIndex(mediaType) {
		if document.Config != nil || len(document.Layers) != 0 || len(document.Manifests) == 0 {
			return document, ErrInvalid
		}
	} else if document.Config == nil || document.Layers == nil || len(document.Manifests) != 0 {
		return document, ErrInvalid
	}
	for _, descriptor := range document.Dependencies() {
		if !ValidDigest(descriptor.Digest) || descriptor.Size == nil || *descriptor.Size < 0 {
			return document, ErrInvalid
		}
		if len(descriptor.URLs) > 0 || descriptor.Data != "" {
			return document, ErrUnsupported
		}
	}
	if IsIndex(mediaType) {
		for _, descriptor := range document.Manifests {
			if !IsManifest(descriptor.MediaType) && !IsIndex(descriptor.MediaType) {
				return document, ErrUnsupported
			}
		}
	} else {
		if document.Config.MediaType != "application/vnd.oci.image.config.v1+json" && document.Config.MediaType != "application/vnd.docker.container.image.v1+json" {
			return document, ErrUnsupported
		}
		for _, descriptor := range document.Layers {
			switch descriptor.MediaType {
			case "application/vnd.oci.image.layer.v1.tar", "application/vnd.oci.image.layer.v1.tar+gzip", "application/vnd.oci.image.layer.v1.tar+zstd", "application/vnd.docker.image.rootfs.diff.tar.gzip":
			default:
				return document, ErrUnsupported
			}
		}
	}
	return document, nil
}

func (d Document) Dependencies() []Descriptor {
	if IsIndex(d.MediaType) {
		return d.Manifests
	}
	out := make([]Descriptor, 0, len(d.Layers)+1)
	if d.Config != nil {
		out = append(out, *d.Config)
	}
	return append(out, d.Layers...)
}

type Object struct {
	Digest    string
	Size      int64
	MediaType string
	Document  *Document
}

// Validate checks exact membership: no missing, extra, contradictory, or cyclic
// member can be committed as a complete reference. Shared members remain valid.
func Validate(root string, objects []Object) error {
	if !ValidDigest(root) || len(objects) == 0 {
		return ErrInvalid
	}
	if len(objects) > MaxObjects {
		return ErrLimit
	}
	byDigest := make(map[string]Object, len(objects))
	for _, object := range objects {
		if !ValidDigest(object.Digest) || object.Size < 0 {
			return ErrInvalid
		}
		if _, found := byDigest[object.Digest]; found {
			return ErrInvalid
		}
		byDigest[object.Digest] = object
	}
	visited, active := map[string]bool{}, map[string]bool{}
	var visit func(string, int) error
	visit = func(digest string, depth int) error {
		if depth > MaxDepth {
			return ErrLimit
		}
		if active[digest] {
			return ErrInvalid
		}
		if visited[digest] {
			return nil
		}
		object, found := byDigest[digest]
		if !found {
			return ErrIncomplete
		}
		visited[digest], active[digest] = true, true
		defer delete(active, digest)
		if object.Document == nil {
			if digest == root || IsIndex(object.MediaType) || IsManifest(object.MediaType) {
				return ErrInvalid
			}
			return nil
		}
		if object.Document.MediaType != object.MediaType {
			return ErrInvalid
		}
		for _, descriptor := range object.Document.Dependencies() {
			child, found := byDigest[descriptor.Digest]
			if !found {
				return ErrIncomplete
			}
			if descriptor.Size == nil || child.Size != *descriptor.Size || child.MediaType != descriptor.MediaType {
				return ErrInvalid
			}
			if err := visit(descriptor.Digest, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if err := visit(root, 0); err != nil {
		return err
	}
	if len(visited) != len(objects) {
		return ErrInvalid
	}
	return nil
}
