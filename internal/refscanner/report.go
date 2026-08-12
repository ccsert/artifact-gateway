package refscanner

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

const (
	maximumLicenses        = 100
	maximumDetailedFinding = 200
	maximumGatewayReport   = 480 << 10
)

type scanResponse struct {
	SchemaVersion string             `json:"schemaVersion"`
	SBOMs         []scanSBOM         `json:"sboms"`
	Licenses      []scanLicense      `json:"licenses"`
	Vulnerability *scanVulnerability `json:"vulnerability,omitempty"`
}

type scanSBOM struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	URL       string `json:"url,omitempty"`
	Size      int64  `json:"size"`
}

type scanLicense struct {
	SPDXID string `json:"spdxId"`
	Name   string `json:"name"`
	Source string `json:"source,omitempty"`
}

type scanVulnerability struct {
	Status   string        `json:"status"`
	Critical int           `json:"critical"`
	High     int           `json:"high"`
	Medium   int           `json:"medium"`
	Low      int           `json:"low"`
	Unknown  int           `json:"unknown"`
	Findings []scanFinding `json:"findings,omitempty"`
}

type scanFinding struct {
	ID           string   `json:"id"`
	Source       string   `json:"source,omitempty"`
	Severity     string   `json:"severity"`
	Component    string   `json:"component"`
	Version      string   `json:"version,omitempty"`
	FixedVersion string   `json:"fixedVersion,omitempty"`
	Location     string   `json:"location,omitempty"`
	Title        string   `json:"title,omitempty"`
	Description  string   `json:"description,omitempty"`
	URL          string   `json:"url,omitempty"`
	CVSSScore    *float64 `json:"cvssScore,omitempty"`
	CVSSVector   string   `json:"cvssVector,omitempty"`
}

type trivyReport struct {
	SchemaVersion int           `json:"SchemaVersion"`
	Results       []trivyResult `json:"Results"`
}

type trivyResult struct {
	Target          string               `json:"Target"`
	Packages        []trivyPackage       `json:"Packages"`
	Licenses        []trivyLicense       `json:"Licenses"`
	Vulnerabilities []trivyVulnerability `json:"Vulnerabilities"`
}

type trivyIdentifier struct {
	PURL string `json:"PURL"`
}

type trivyPackage struct {
	Name       string          `json:"Name"`
	Version    string          `json:"Version"`
	Identifier trivyIdentifier `json:"Identifier"`
	Licenses   []string        `json:"Licenses"`
}

type trivyLicense struct {
	Name     string `json:"Name"`
	FilePath string `json:"FilePath"`
}

type trivyDataSource struct {
	ID string `json:"ID"`
}

type trivyCVSS struct {
	V2Vector string  `json:"V2Vector"`
	V3Vector string  `json:"V3Vector"`
	V2Score  float64 `json:"V2Score"`
	V3Score  float64 `json:"V3Score"`
}

type trivyVulnerability struct {
	VulnerabilityID  string               `json:"VulnerabilityID"`
	PkgID            string               `json:"PkgID"`
	PkgName          string               `json:"PkgName"`
	PkgPath          string               `json:"PkgPath"`
	PkgIdentifier    trivyIdentifier      `json:"PkgIdentifier"`
	InstalledVersion string               `json:"InstalledVersion"`
	FixedVersion     string               `json:"FixedVersion"`
	Severity         string               `json:"Severity"`
	SeveritySource   string               `json:"SeveritySource"`
	PrimaryURL       string               `json:"PrimaryURL"`
	Title            string               `json:"Title"`
	Description      string               `json:"Description"`
	DataSource       trivyDataSource      `json:"DataSource"`
	CVSS             map[string]trivyCVSS `json:"CVSS"`
}

func mapTrivyReport(output EngineOutput, includeFindings bool) (scanResponse, error) {
	var native trivyReport
	if len(output.Report) == 0 || json.Unmarshal(output.Report, &native) != nil || native.SchemaVersion < 1 {
		return scanResponse{}, errors.New("trivy report is invalid")
	}
	response := scanResponse{
		SchemaVersion: reportSchemaVersion,
		SBOMs:         make([]scanSBOM, 0, 1),
	}
	licenses, err := collectLicenses(native.Results)
	if err != nil {
		return scanResponse{}, err
	}
	response.Licenses = licenses
	if len(output.SBOM) > 0 {
		digest := sha256.Sum256(output.SBOM)
		response.SBOMs = append(response.SBOMs, scanSBOM{
			MediaType: "application/vnd.cyclonedx+json",
			Digest:    "sha256:" + hex.EncodeToString(digest[:]),
			Size:      int64(len(output.SBOM)),
		})
	}
	response.Vulnerability = collectVulnerabilities(native.Results, includeFindings)
	if includeFindings && response.Vulnerability.Findings != nil {
		encoded, err := json.Marshal(response)
		if err != nil {
			return scanResponse{}, errors.New("reference scanner report is invalid")
		}
		if len(encoded) > maximumGatewayReport {
			response.Vulnerability.Findings = nil
		}
	}
	return response, nil
}

func collectLicenses(results []trivyResult) ([]scanLicense, error) {
	values := make(map[string]scanLicense)
	for _, result := range results {
		for _, value := range result.Packages {
			source := boundedLine(firstNonempty(value.Identifier.PURL, value.Name+"@"+value.Version, result.Target), 2048)
			for _, license := range value.Licenses {
				addLicense(values, license, source)
			}
		}
		for _, value := range result.Licenses {
			addLicense(values, value.Name, boundedLine(firstNonempty(value.FilePath, result.Target), 2048))
		}
	}
	if len(values) > maximumLicenses {
		return nil, errors.New("trivy report exceeds the unique-license limit")
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	licenses := make([]scanLicense, 0, len(keys))
	for _, key := range keys {
		licenses = append(licenses, values[key])
	}
	return licenses, nil
}

func addLicense(values map[string]scanLicense, name, source string) {
	name = boundedLine(name, 128)
	if name == "" {
		return
	}
	value := scanLicense{SPDXID: name, Name: boundedLine(name, 512), Source: source}
	key := value.SPDXID
	existing, exists := values[key]
	if !exists || value.Source < existing.Source {
		values[key] = value
	}
}

func collectVulnerabilities(results []trivyResult, includeFindings bool) *scanVulnerability {
	value := &scanVulnerability{Status: "clean"}
	seen := make(map[string]struct{})
	findings := make([]scanFinding, 0)
	for _, result := range results {
		for _, vulnerability := range result.Vulnerabilities {
			finding := mapVulnerability(result.Target, vulnerability)
			key := finding.ID + "\x00" + finding.Source + "\x00" + finding.Component + "\x00" + finding.Version + "\x00" + finding.Location
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			switch finding.Severity {
			case "critical":
				value.Critical++
			case "high":
				value.High++
			case "medium":
				value.Medium++
			case "low":
				value.Low++
			default:
				value.Unknown++
			}
			if includeFindings && len(findings) <= maximumDetailedFinding {
				findings = append(findings, finding)
			}
		}
	}
	if len(seen) > 0 {
		value.Status = "affected"
	}
	if includeFindings && len(findings) <= maximumDetailedFinding {
		value.Findings = findings
	}
	return value
}

func mapVulnerability(target string, value trivyVulnerability) scanFinding {
	severity := strings.ToLower(strings.TrimSpace(value.Severity))
	switch severity {
	case "critical", "high", "medium", "low", "unknown":
	default:
		severity = "unknown"
	}
	score, vector := selectCVSS(value.CVSS, value.SeveritySource)
	return scanFinding{
		ID:           boundedLine(firstNonempty(value.VulnerabilityID, "unknown-vulnerability"), 128),
		Source:       boundedLine(firstNonempty(value.SeveritySource, value.DataSource.ID), 128),
		Severity:     severity,
		Component:    boundedLine(firstNonempty(value.PkgIdentifier.PURL, value.PkgID, value.PkgName, "unknown-component"), 512),
		Version:      boundedLine(value.InstalledVersion, 256),
		FixedVersion: boundedLine(value.FixedVersion, 256),
		Location:     boundedLine(firstNonempty(value.PkgPath, target), 2048),
		Title:        boundedLine(value.Title, 512),
		Description:  boundedDescription(value.Description, 4096),
		URL:          safeURL(value.PrimaryURL),
		CVSSScore:    score,
		CVSSVector:   boundedLine(vector, 256),
	}
}

func selectCVSS(values map[string]trivyCVSS, preferred string) (*float64, string) {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if preferred != "" {
		for index, key := range keys {
			if strings.EqualFold(key, preferred) {
				keys[0], keys[index] = keys[index], keys[0]
				break
			}
		}
	}
	var bestScore float64
	var bestVector string
	found := false
	for _, key := range keys {
		value := values[key]
		score, vector := value.V3Score, value.V3Vector
		if vector == "" && score == 0 {
			score, vector = value.V2Score, value.V2Vector
		}
		if score < 0 || score > 10 {
			continue
		}
		if !found || score > bestScore {
			bestScore, bestVector, found = score, vector, true
		}
	}
	if !found {
		return nil, ""
	}
	return &bestScore, bestVector
}

func safeURL(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 2048 {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.User != nil || parsed.Host == "" || parsed.Scheme != "http" && parsed.Scheme != "https" {
		return ""
	}
	return value
}

func boundedDescription(value string, maximum int) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\x00", ""))
	if len(value) > maximum {
		value = value[:maximum]
	}
	return value
}

func firstNonempty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func rawVersionString(value json.RawMessage) string {
	if len(value) == 0 || string(value) == "null" {
		return ""
	}
	var text string
	if json.Unmarshal(value, &text) == nil {
		return boundedLine(text, 256)
	}
	var number json.Number
	if json.Unmarshal(value, &number) == nil {
		return boundedLine(number.String(), 256)
	}
	return boundedLine(strconv.Quote(string(value)), 256)
}
