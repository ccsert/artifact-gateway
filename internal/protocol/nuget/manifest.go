package nuget

import (
	"archive/zip"
	"encoding/xml"
	"errors"
	"io"
	"strconv"
	"strings"
)

type Dependency struct {
	ID    string `json:"id"`
	Range string `json:"range,omitempty"`
}

type DependencyGroup struct {
	TargetFramework string       `json:"targetFramework,omitempty"`
	Dependencies    []Dependency `json:"dependencies"`
}

// Inspection retains the original manifest and the metadata needed by NuGet's
// registration resource. Identity always comes from the existing N1 parser.
type Inspection struct {
	Identity         PackageMetadata   `json:"identity"`
	Nuspec           []byte            `json:"-"`
	Authors          string            `json:"authors,omitempty"`
	Description      string            `json:"description,omitempty"`
	DependencyGroups []DependencyGroup `json:"dependencyGroups"`
}

// NormalizeID applies the same bounded, case-insensitive identity as ParsePackage.
func NormalizeID(id string) (string, error) {
	if len(id) > maxPackageIDBytes || !packageIDPattern.MatchString(id) {
		return "", errors.New("invalid NuGet package id")
	}
	return strings.ToLower(id), nil
}

type manifestDependency struct {
	XMLName xml.Name
	ID      string `xml:"id,attr"`
	Version string `xml:"version,attr"`
}

func (d *manifestDependency) UnmarshalXML(decoder *xml.Decoder, start xml.StartElement) error {
	if err := validateManifestAttributes(start, "id", "version"); err != nil {
		return err
	}
	type plain manifestDependency
	return decoder.DecodeElement((*plain)(d), &start)
}

type manifestGroup struct {
	XMLName      xml.Name
	Framework    string               `xml:"targetFramework,attr"`
	Dependencies []manifestDependency `xml:"dependency"`
}

func (g *manifestGroup) UnmarshalXML(decoder *xml.Decoder, start xml.StartElement) error {
	if err := validateManifestAttributes(start, "targetFramework"); err != nil {
		return err
	}
	type plain manifestGroup
	return decoder.DecodeElement((*plain)(g), &start)
}

// encoding/xml matches tagged attributes by local name. Reject namespace aliases
// and duplicate attributes before decoding values used in the dependency graph.
func validateManifestAttributes(start xml.StartElement, graphNames ...string) error {
	seen := make(map[xml.Name]bool, len(start.Attr))
	for _, attr := range start.Attr {
		if seen[attr.Name] {
			return errors.New("duplicate NuGet dependency attribute")
		}
		seen[attr.Name] = true
		if attr.Name.Space == "xmlns" || attr.Name.Space == "" && attr.Name.Local == "xmlns" {
			continue
		}
		for _, name := range graphNames {
			if attr.Name.Local == name && attr.Name.Space != "" {
				return errors.New("namespaced NuGet dependency attribute")
			}
		}
	}
	return nil
}

type manifestDependencies struct {
	XMLName      xml.Name
	Groups       []manifestGroup      `xml:"group"`
	Dependencies []manifestDependency `xml:"dependency"`
}

func InspectPackage(reader io.ReaderAt, size int64) (Inspection, error) {
	identity, err := ParsePackage(reader, size)
	if err != nil {
		return Inspection{}, err
	}
	archive, err := zip.NewReader(reader, size)
	if err != nil {
		return Inspection{}, err
	}
	var body []byte
	for _, entry := range archive.File {
		if !strings.Contains(entry.Name, "/") && strings.HasSuffix(strings.ToLower(entry.Name), ".nuspec") {
			r, openErr := entry.Open()
			if openErr != nil {
				return Inspection{}, openErr
			}
			body, err = io.ReadAll(io.LimitReader(r, maxNuspecBytes+1))
			closeErr := r.Close()
			if err != nil || closeErr != nil || len(body) > maxNuspecBytes {
				return Inspection{}, errors.New("read NuGet manifest")
			}
			break
		}
	}
	var document struct {
		XMLName  xml.Name
		Metadata struct {
			Authors      []string               `xml:"authors"`
			Descriptions []string               `xml:"description"`
			Dependencies []manifestDependencies `xml:"dependencies"`
		} `xml:"metadata"`
	}
	if xml.Unmarshal(body, &document) != nil || len(document.Metadata.Dependencies) > 1 || len(document.Metadata.Authors) > 1 || len(document.Metadata.Descriptions) > 1 {
		return Inspection{}, errors.New("ambiguous NuGet metadata")
	}
	result := Inspection{Identity: identity, Nuspec: body, DependencyGroups: []DependencyGroup{}}
	if len(document.Metadata.Authors) == 1 {
		result.Authors = document.Metadata.Authors[0]
	}
	if len(document.Metadata.Descriptions) == 1 {
		result.Description = document.Metadata.Descriptions[0]
	}
	if len(document.Metadata.Dependencies) == 0 {
		return result, nil
	}
	deps := document.Metadata.Dependencies[0]
	if deps.XMLName.Space != document.XMLName.Space || len(deps.Groups) > 256 || len(deps.Groups) > 0 && len(deps.Dependencies) > 0 {
		return Inspection{}, errors.New("unsupported NuGet dependency grouping")
	}
	groups := deps.Groups
	if len(groups) == 0 {
		groups = []manifestGroup{{XMLName: document.XMLName, Dependencies: deps.Dependencies}}
	}
	seenGroups := map[string]bool{}
	dependencyCount := 0
	for _, group := range groups {
		if group.XMLName.Space != document.XMLName.Space || len(group.Framework) > 256 || strings.ContainsAny(group.Framework, "\x00\r\n") || seenGroups[strings.ToLower(group.Framework)] {
			return Inspection{}, errors.New("invalid NuGet dependency group")
		}
		seenGroups[strings.ToLower(group.Framework)] = true
		out := DependencyGroup{TargetFramework: group.Framework, Dependencies: []Dependency{}}
		seen := map[string]bool{}
		for _, dep := range group.Dependencies {
			id, idErr := NormalizeID(dep.ID)
			dependencyCount++
			if idErr != nil || dep.XMLName.Space != document.XMLName.Space || seen[id] || dependencyCount > 4096 || !validDependencyRange(dep.Version) {
				return Inspection{}, errors.New("invalid NuGet dependency")
			}
			seen[id] = true
			out.Dependencies = append(out.Dependencies, Dependency{ID: dep.ID, Range: dep.Version})
		}
		result.DependencyGroups = append(result.DependencyGroups, out)
	}
	return result, nil
}

func validDependencyRange(value string) bool {
	if len(value) > 512 || strings.TrimSpace(value) != value {
		return false
	}
	if value == "" {
		return true
	}
	if value[0] != '[' && value[0] != '(' {
		_, err := NormalizeVersion(value)
		return err == nil
	}
	if len(value) < 3 || value[len(value)-1] != ']' && value[len(value)-1] != ')' {
		return false
	}
	inner := value[1 : len(value)-1]
	low, high, hasComma := strings.Cut(inner, ",")
	if !hasComma {
		_, err := NormalizeVersion(inner)
		return value[0] == '[' && value[len(value)-1] == ']' && err == nil
	}
	low, high = strings.TrimSpace(low), strings.TrimSpace(high)
	if low == "" && value[0] != '(' || high == "" && value[len(value)-1] != ')' {
		return false
	}
	for _, bound := range []string{low, high} {
		if bound != "" {
			if _, err := NormalizeVersion(bound); err != nil {
				return false
			}
		}
	}
	return low == "" || high == "" || CompareVersions(low, high) < 0 || CompareVersions(low, high) == 0 && value[0] == '[' && value[len(value)-1] == ']'
}

// CompareVersions orders validated NuGet versions by their normalized numeric
// and prerelease precedence, including NuGet's optional fourth numeric part.
func CompareVersions(left, right string) int {
	a, aerr := NormalizeVersion(left)
	b, berr := NormalizeVersion(right)
	if aerr != nil || berr != nil {
		return strings.Compare(strings.ToLower(left), strings.ToLower(right))
	}
	acore, apre, _ := strings.Cut(a, "-")
	bcore, bpre, _ := strings.Cut(b, "-")
	ap, bp := strings.Split(acore, "."), strings.Split(bcore, ".")
	for i := 0; i < 4; i++ {
		var av, bv int64
		if i < len(ap) {
			av, _ = strconv.ParseInt(ap[i], 10, 64)
		}
		if i < len(bp) {
			bv, _ = strconv.ParseInt(bp[i], 10, 64)
		}
		if av < bv {
			return -1
		}
		if av > bv {
			return 1
		}
	}
	if apre == "" && bpre != "" {
		return 1
	}
	if apre != "" && bpre == "" {
		return -1
	}
	al, bl := strings.Split(strings.ToLower(apre), "."), strings.Split(strings.ToLower(bpre), ".")
	for i := 0; i < len(al) && i < len(bl); i++ {
		x, y := al[i], bl[i]
		if x == y {
			continue
		}
		xn, yn := numericLabel(x), numericLabel(y)
		if xn != yn {
			if xn {
				return -1
			}
			return 1
		}
		if xn && len(x) != len(y) {
			if len(x) < len(y) {
				return -1
			}
			return 1
		}
		return strings.Compare(x, y)
	}
	if len(al) < len(bl) {
		return -1
	}
	if len(al) > len(bl) {
		return 1
	}
	return 0
}
