package ocidiscovery

import (
	"encoding/json"

	"github.com/ccsert/artifact-gateway/internal/ocigraph"
)

type Requirement struct {
	Descriptor
	Resource string
}

type wireDescriptor struct {
	MediaType string   `json:"mediaType"`
	Digest    string   `json:"digest"`
	Size      *int64   `json:"size"`
	URLs      []string `json:"urls"`
	Data      string   `json:"data"`
}

func descriptorRequirement(d wireDescriptor, resource string) (Requirement, error) {
	if !ocigraph.ValidDigest(d.Digest) || d.Size == nil || *d.Size < 0 || d.MediaType == "" || len(d.MediaType) > 512 || len(d.URLs) != 0 || d.Data != "" {
		return Requirement{}, ErrInvalid
	}
	return Requirement{Descriptor: Descriptor{Digest: d.Digest, MediaType: d.MediaType, Size: *d.Size}, Resource: resource}, nil
}

// DecodeArtifact checks relationships while the caller checks the original
// bytes and closure. It does not extend image-only migration acceptance, fetch
// foreign URLs, execute signature tools, or establish signer trust.
func DecodeArtifact(body []byte, expected Descriptor, subject *Descriptor) ([]Requirement, string, error) {
	var document struct {
		SchemaVersion int               `json:"schemaVersion"`
		MediaType     string            `json:"mediaType"`
		ArtifactType  string            `json:"artifactType"`
		Subject       *wireDescriptor   `json:"subject"`
		Config        *wireDescriptor   `json:"config"`
		Layers        *[]wireDescriptor `json:"layers"`
		Manifests     *[]wireDescriptor `json:"manifests"`
	}
	if len(body) == 0 || len(body) > ocigraph.MaxManifestBytes || int64(len(body)) != expected.Size || ocigraph.Digest(body) != expected.Digest || json.Unmarshal(body, &document) != nil || document.SchemaVersion != 2 {
		return nil, "", ErrInvalid
	}
	if document.MediaType != "" && document.MediaType != expected.MediaType {
		return nil, "", ErrInvalid
	}
	if !ocigraph.IsManifest(expected.MediaType) && !ocigraph.IsIndex(expected.MediaType) {
		return nil, "", ErrInvalid
	}
	if subject != nil {
		if document.Subject == nil {
			return nil, "", ErrInvalid
		}
		reference, err := descriptorRequirement(*document.Subject, "manifests")
		if err != nil || reference.Digest != subject.Digest || reference.Size != subject.Size || reference.MediaType != subject.MediaType {
			return nil, "", ErrInvalid
		}
	}
	artifactType := document.ArtifactType
	if len(artifactType) > 512 {
		return nil, "", ErrInvalid
	}
	dependencies := []Requirement{}
	if ocigraph.IsIndex(expected.MediaType) {
		if document.Config != nil || document.Layers != nil || document.Manifests == nil {
			return nil, "", ErrInvalid
		}
		for _, child := range *document.Manifests {
			if !ocigraph.IsManifest(child.MediaType) && !ocigraph.IsIndex(child.MediaType) {
				return nil, "", ErrInvalid
			}
			r, err := descriptorRequirement(child, "manifests")
			if err != nil {
				return nil, "", err
			}
			dependencies = append(dependencies, r)
		}
	} else {
		if document.Config == nil || document.Layers == nil || document.Manifests != nil {
			return nil, "", ErrInvalid
		}
		config, err := descriptorRequirement(*document.Config, "blobs")
		if err != nil {
			return nil, "", err
		}
		dependencies = append(dependencies, config)
		if artifactType == "" {
			artifactType = config.MediaType
		}
		for _, child := range *document.Layers {
			r, err := descriptorRequirement(child, "blobs")
			if err != nil {
				return nil, "", err
			}
			dependencies = append(dependencies, r)
		}
	}
	if len(dependencies) > ocigraph.MaxObjects {
		return nil, "", ErrLimit
	}
	if expected.ArtifactType != "" && expected.ArtifactType != artifactType {
		return nil, "", ErrInvalid
	}
	return dependencies, artifactType, nil
}
