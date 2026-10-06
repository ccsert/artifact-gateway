package snapshotimport

import (
	"encoding/xml"
	"errors"
	"regexp"
	"strings"
)

// POMMainExtension applies the same bounded identity and packaging rules used
// by the controlled historical importer to a new deployment's POM.
func POMMainExtension(data []byte, coordinate string) (string, error) {
	return validatePOM(data, coordinate)
}

func DeploymentReceipt(data []byte, coordinate string) (string, int, error) {
	var m struct {
		XMLName  xml.Name `xml:"metadata"`
		Group    string   `xml:"groupId"`
		Artifact string   `xml:"artifactId"`
		Version  string   `xml:"version"`
		Snapshot struct {
			Timestamp string `xml:"timestamp"`
			Build     int    `xml:"buildNumber"`
		} `xml:"versioning>snapshot"`
	}
	if err := decodeXML(data, &m); err != nil {
		return "", 0, err
	}
	if m.Group+":"+m.Artifact+":"+m.Version != coordinate {
		return "", 0, errors.New("metadata identity mismatch")
	}
	return m.Snapshot.Timestamp, m.Snapshot.Build, nil
}

var deploymentPath = regexp.MustCompile(`^([0-9]{8}\.[0-9]{6})-([1-9][0-9]*)(?:-|\.)`)

// DeploymentMetadata validates a client completion receipt against only known
// paths. Its aliases are proof of uploaded pairs, never a new routing policy.
func DeploymentMetadata(data []byte, coordinate string, allowed map[string]string) (string, int, map[string]string, error) {
	var m struct {
		Snapshot struct {
			Timestamp string `xml:"timestamp"`
			Build     int    `xml:"buildNumber"`
		} `xml:"versioning>snapshot"`
	}
	if err := decodeXML(data, &m); err != nil {
		return "", 0, nil, err
	}
	parts := strings.Split(coordinate, ":")
	prefix := base(coordinate) + parts[1] + "-" + strings.TrimSuffix(parts[2], "-SNAPSHOT") + "-"
	paths, builds := map[string]bool{}, map[string]bool{}
	for _, target := range allowed {
		paths[target] = true
		match := deploymentPath.FindStringSubmatch(strings.TrimPrefix(target, prefix))
		if len(match) > 0 {
			builds[match[1]+"-"+match[2]] = true
		}
	}
	aliases, err := validateMetadata(data, coordinate, paths, builds)
	if err != nil {
		return "", 0, nil, err
	}
	for canonical, target := range aliases {
		if allowed[canonical] != target {
			return "", 0, nil, errors.New("metadata pair does not match deployment or current receipt")
		}
	}
	return m.Snapshot.Timestamp, m.Snapshot.Build, aliases, nil
}
