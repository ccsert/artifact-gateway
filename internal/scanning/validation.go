package scanning

import (
	"mime"
	"net"
	"net/url"
	"strings"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

const maxAssets = 256

func validateArtifact(artifact Artifact, maxBytes int64) error {
	if !validText(artifact.RepositoryID, 128) || !repository.IsSupportedFormat(artifact.Format) || !validText(artifact.Coordinate, 1024) || !validDigest(artifact.Digest) || len(artifact.Assets) == 0 || len(artifact.Assets) > maxAssets {
		return ErrInvalidArtifact
	}
	seen := make(map[string]struct{}, len(artifact.Assets))
	var total int64
	for _, asset := range artifact.Assets {
		if !validText(asset.Path, 2048) || !validDigest(asset.Digest) || asset.Size < 0 || asset.Size > maxBytes || asset.Open == nil || !validMediaType(asset.MediaType) {
			return ErrInvalidArtifact
		}
		if _, exists := seen[asset.Path]; exists {
			return ErrInvalidArtifact
		}
		seen[asset.Path] = struct{}{}
		if total > maxBytes-asset.Size {
			return ErrInvalidArtifact
		}
		total += asset.Size
	}
	return nil
}

func validMediaType(value string) bool {
	if value == "" {
		return true
	}
	if len(value) > 255 || strings.ContainsRune(value, '\x00') {
		return false
	}
	_, _, err := mime.ParseMediaType(value)
	return err == nil
}

func validReport(report Report) bool {
	if len(report.SBOMs) > 20 || len(report.Licenses) > 100 {
		return false
	}
	for _, sbom := range report.SBOMs {
		if !validText(sbom.MediaType, 255) || !validDigest(sbom.Digest) || sbom.Size < 0 || !validReportURL(sbom.URL) {
			return false
		}
	}
	for _, license := range report.Licenses {
		if !validText(license.SPDXID, 128) || !validText(license.Name, 512) || len(license.Source) > 2048 || strings.ContainsRune(license.Source, '\x00') {
			return false
		}
	}
	if report.Vulnerability == nil {
		return true
	}
	vulnerability := report.Vulnerability
	if !validText(vulnerability.Scanner, 128) {
		return false
	}
	switch vulnerability.Status {
	case "not_scanned", "clean", "affected", "error":
	default:
		return false
	}
	for _, count := range []int{vulnerability.Critical, vulnerability.High, vulnerability.Medium, vulnerability.Low, vulnerability.Unknown} {
		if count < 0 || count > 1_000_000_000 {
			return false
		}
	}
	return true
}

func validReportURL(value string) bool {
	if value == "" {
		return true
	}
	if len(value) > 2048 || strings.ContainsRune(value, '\x00') {
		return false
	}
	parsed, err := url.Parse(value)
	return err == nil && parsed.User == nil && parsed.Host != "" && (parsed.Scheme == "http" || parsed.Scheme == "https")
}

func validateEndpoint(value string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, ErrInvalidConfiguration
	}
	if parsed.Scheme == "https" {
		return parsed, nil
	}
	if parsed.Scheme == "http" && loopbackHost(parsed.Hostname()) {
		return parsed, nil
	}
	return nil, ErrInvalidConfiguration
}

func loopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func validText(value string, maximum int) bool {
	return strings.TrimSpace(value) != "" && len(value) <= maximum && !strings.ContainsRune(value, '\x00') && !strings.ContainsAny(value, "\r\n")
}

func validOptionalText(value string, maximum int) bool {
	return value == "" || validText(value, maximum)
}

func validDigest(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, character := range value[len("sha256:"):] {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}
