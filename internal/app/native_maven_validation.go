package app

import (
	"context"
	"encoding/xml"
	"errors"
	"io"
	"strings"

	"github.com/ccsert/artifact-gateway/internal/repository"
	"golang.org/x/text/encoding/charmap"
)

type nativeMavenPOM struct {
	GroupID    string `xml:"groupId"`
	ArtifactID string `xml:"artifactId"`
	Version    string `xml:"version"`
	Packaging  string `xml:"packaging"`
	Parent     struct {
		GroupID string `xml:"groupId"`
		Version string `xml:"version"`
	} `xml:"parent"`
}

// validateNativeMavenPOM is shared by native HTTP publication and migration.
// Validation never rewrites the original bytes or resolves external parents.
func validateNativeMavenPOM(ctx context.Context, objects ObjectStore, s repository.MavenPublishSession) error {
	_, err := readNativeMavenPOM(ctx, objects, s)
	return err
}

func readNativeMavenPOM(ctx context.Context, objects ObjectStore, s repository.MavenPublishSession) (nativeMavenPOM, error) {
	parts := strings.Split(s.Coordinate, ":")
	if len(parts) != 3 || s.PomObject != parts[1]+"-"+parts[2]+".pom" {
		return nativeMavenPOM{}, errors.New("staged POM does not match Maven coordinate")
	}
	var pom repository.MavenDeclaredObject
	found := false
	for _, object := range s.Objects {
		if object.Name == s.PomObject {
			pom, found = object, true
			break
		}
	}
	if !found {
		return nativeMavenPOM{}, errors.New("staged Maven coordinate has no POM")
	}
	reader, size, err := objects.Open(ctx, "native/maven/sha256/"+strings.TrimPrefix(pom.Digest, "sha256:"))
	if err != nil {
		return nativeMavenPOM{}, errors.New("staged POM is unavailable")
	}
	defer func() { _ = reader.Close() }()
	if size != pom.Size {
		return nativeMavenPOM{}, errors.New("staged POM size does not match declaration")
	}
	var project nativeMavenPOM
	decoder := xml.NewDecoder(reader)
	// Convert only the validation reader. Published objects and their digests
	// continue to describe the original bytes supplied by the Maven client.
	decoder.CharsetReader = func(charset string, input io.Reader) (io.Reader, error) {
		if !strings.EqualFold(charset, "ISO-8859-1") {
			return nil, errors.New("unsupported POM XML encoding")
		}
		return charmap.ISO8859_1.NewDecoder().Reader(input), nil
	}
	if err := decoder.Decode(&project); err != nil {
		return nativeMavenPOM{}, errors.New("staged POM is invalid XML")
	}
	var extra struct{}
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nativeMavenPOM{}, errors.New("staged POM is invalid XML")
	}
	if project.GroupID == "" {
		project.GroupID = project.Parent.GroupID
	}
	if project.Version == "" || project.Version == "${project.parent.version}" {
		project.Version = project.Parent.Version
	}
	// A matching literal request path cannot make an unresolved expression a
	// valid identity. Do not evaluate user, environment or remote-parent values.
	for _, value := range []string{project.GroupID, project.ArtifactID, project.Version} {
		if strings.Contains(value, "${") {
			return nativeMavenPOM{}, errors.New("staged POM identity contains an unresolved expression")
		}
	}
	if project.GroupID != parts[0] || project.ArtifactID != parts[1] || project.Version != parts[2] {
		return nativeMavenPOM{}, errors.New("staged POM identity does not match Maven coordinate")
	}
	return project, nil
}
