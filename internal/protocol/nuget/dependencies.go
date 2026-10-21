package nuget

import (
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
)

// CanonicalDependencyGroups compares wire registrations with the original
// nuspec. Common NuGet long/short framework names and normalized version ranges
// have one representation. Unknown framework shapes fail explicitly.
func CanonicalDependencyGroups(groups []DependencyGroup) ([]byte, error) {
	invalid := errors.New("unsupported NuGet dependency graph")
	if len(groups) > 256 {
		return nil, invalid
	}
	out := []DependencyGroup{}
	seen := map[string]bool{}
	count := 0
	for _, group := range groups {
		framework, err := canonicalFramework(group.TargetFramework)
		if err != nil || seen[framework] {
			return nil, invalid
		}
		seen[framework] = true
		g := DependencyGroup{TargetFramework: framework, Dependencies: []Dependency{}}
		ids := map[string]bool{}
		for _, dependency := range group.Dependencies {
			id, err := NormalizeID(dependency.ID)
			count++
			if err != nil || ids[id] || count > 4096 || !validDependencyRange(dependency.Range) {
				return nil, invalid
			}
			ids[id] = true
			rangeValue := strings.TrimSpace(dependency.Range)
			if rangeValue == "" {
				rangeValue = "(,)"
			} else if rangeValue[0] != '[' && rangeValue[0] != '(' {
				v, _ := NormalizeVersion(rangeValue)
				rangeValue = "[" + strings.ToLower(v) + ",)"
			} else {
				inner := rangeValue[1 : len(rangeValue)-1]
				bounds := strings.Split(inner, ",")
				for i, b := range bounds {
					b = strings.TrimSpace(b)
					if b != "" {
						v, err := NormalizeVersion(b)
						if err != nil {
							return nil, invalid
						}
						bounds[i] = strings.ToLower(v)
					} else {
						bounds[i] = ""
					}
				}
				rangeValue = rangeValue[:1] + strings.Join(bounds, ",") + rangeValue[len(rangeValue)-1:]
			}
			g.Dependencies = append(g.Dependencies, Dependency{ID: id, Range: rangeValue})
		}
		sort.Slice(g.Dependencies, func(i, j int) bool { return g.Dependencies[i].ID < g.Dependencies[j].ID })
		out = append(out, g)
	}
	// An empty unqualified group and an omitted dependency graph both contain
	// no dependency information. Framework-specific empty groups remain distinct.
	if len(out) == 1 && out[0].TargetFramework == "" && len(out[0].Dependencies) == 0 {
		out = []DependencyGroup{}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TargetFramework < out[j].TargetFramework })
	return json.Marshal(out)
}
func canonicalFramework(raw string) (string, error) {
	value := strings.ToLower(raw)
	if value == "" {
		return "", nil
	}
	if len(value) > 256 || strings.TrimSpace(value) != value {
		return "", errors.New("unsupported NuGet framework")
	}
	for _, prefix := range []string{".netstandard,version=v", ".netcoreapp,version=v", ".netframework,version=v"} {
		if strings.HasPrefix(value, prefix) {
			version := strings.TrimPrefix(value, prefix)
			parts := strings.Split(version, ".")
			if len(parts) < 2 || len(parts) > 3 {
				return "", errors.New("unsupported NuGet framework")
			}
			for _, p := range parts {
				n, err := strconv.Atoi(p)
				if err != nil || n < 0 || n > 1000 || strconv.Itoa(n) != p {
					return "", errors.New("unsupported NuGet framework")
				}
			}
			if len(parts) == 3 && parts[2] == "0" {
				parts = parts[:2]
				version = strings.Join(parts, ".")
			}
			switch prefix {
			case ".netstandard,version=v":
				return canonicalFramework("netstandard" + version)
			case ".netcoreapp,version=v":
				major, _ := strconv.Atoi(parts[0])
				if major >= 5 {
					return canonicalFramework("net" + version)
				}
				return canonicalFramework("netcoreapp" + version)
			default:
				return canonicalFramework("net" + strings.Join(parts, ""))
			}
		}
	}
	if strings.HasPrefix(value, ".netstandard") {
		value = strings.TrimPrefix(value, ".")
	}
	if strings.HasPrefix(value, ".netcoreapp") {
		value = strings.TrimPrefix(value, ".")
	}
	for _, prefix := range []string{"netstandard", "netcoreapp", "net"} {
		if !strings.HasPrefix(value, prefix) {
			continue
		}
		version := strings.TrimPrefix(value, prefix)
		if prefix == "net" && !strings.Contains(version, ".") {
			if len(version) < 2 || len(version) > 3 || version[0] < '1' || version[0] > '4' {
				break
			}
			for _, r := range version {
				if r < '0' || r > '9' {
					return "", errors.New("unsupported NuGet framework")
				}
			}
			return value, nil
		}
		parts := strings.Split(version, ".")
		if len(parts) != 2 {
			break
		}
		for _, p := range parts {
			n, err := strconv.Atoi(p)
			if err != nil || n < 0 || n > 1000 || strconv.Itoa(n) != p {
				return "", errors.New("unsupported NuGet framework")
			}
		}
		major, _ := strconv.Atoi(parts[0])
		if prefix == "net" && major < 5 || prefix == "netstandard" && major < 1 || prefix == "netcoreapp" && (major < 1 || major > 3) {
			break
		}
		return value, nil
	}
	return "", errors.New("unsupported NuGet framework")
}
