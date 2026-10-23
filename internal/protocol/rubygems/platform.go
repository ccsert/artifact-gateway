package rubygems

import (
	"regexp"
	"strings"
)

// Canonical string platforms follow the pinned Gem::Specification#platform=
// and Gem::Platform parser. Aliases and host-dependent "current" are rejected
// rather than creating a filename different from the original gem's full_name.
type platformPattern struct {
	re           *regexp.Regexp
	os           string
	versionGroup int
}

var numericSuffix = regexp.MustCompile(`\d+(\.\d+)?$`)
var numericVersion = regexp.MustCompile(`^\d+(\.\d+)?$`)
var legacyCPU = regexp.MustCompile(`i\d86`)
var platformPatterns = []platformPattern{
	{regexp.MustCompile(`aix(\d+)?`), "aix", 1},
	{regexp.MustCompile(`cygwin`), "cygwin", 0},
	{regexp.MustCompile(`darwin(\d+)?`), "darwin", 1},
	{regexp.MustCompile(`^macruby$`), "macruby", 0},
	{regexp.MustCompile(`freebsd(\d+)?`), "freebsd", 1},
	{regexp.MustCompile(`^(java|jruby)$`), "java", 0},
	{regexp.MustCompile(`^java([\d.]*)`), "java", 1},
	{regexp.MustCompile(`^dalvik(\d+)?$`), "dalvik", 1},
	{regexp.MustCompile(`^dotnet$`), "dotnet", 0},
	{regexp.MustCompile(`^dotnet([\d.]*)`), "dotnet", 1},
	{regexp.MustCompile(`linux-?(\w+)?`), "linux", 1},
	{regexp.MustCompile(`mingw32`), "mingw32", 0},
	{regexp.MustCompile(`mingw-?(\w+)?`), "mingw", 1},
	{regexp.MustCompile(`(mswin\d+)(_([0-9]+))?`), "", 3},
	{regexp.MustCompile(`netbsdelf`), "netbsdelf", 0},
	{regexp.MustCompile(`openbsd(\d+\.\d+)?`), "openbsd", 1},
	{regexp.MustCompile(`solaris(\d+\.\d+)?`), "solaris", 1},
	{regexp.MustCompile(`wasi`), "wasi", 0},
	{regexp.MustCompile(`^(\w+_platform)(\d+)?`), "", 2},
}

func canonicalPlatform(value string) string {
	switch value {
	case "ruby", "":
		return "ruby"
	case "current":
		return ""
	case "mswin32":
		value = "x86-mswin32"
	case "i586-linux":
		value = "x86-linux"
	case "powerpc-darwin":
		value = "ppc-darwin"
	}
	parts := strings.Split(value, "-")
	if len(parts) > 2 && !numericSuffix.MatchString(parts[len(parts)-1]) {
		parts[len(parts)-2] += "-" + parts[len(parts)-1]
		parts = parts[:len(parts)-1]
	}
	cpu := parts[0]
	parts = parts[1:]
	if legacyCPU.MatchString(cpu) {
		cpu = "x86"
	}
	if len(parts) == 2 && numericVersion.MatchString(parts[1]) {
		return cpu + "-" + parts[0] + "-" + parts[1]
	}
	os := ""
	if len(parts) == 0 {
		os = cpu
		cpu = ""
	} else {
		os = parts[0]
	}
	version := ""
	normalized := "unknown"
	for _, pattern := range platformPatterns {
		match := pattern.re.FindStringSubmatch(os)
		if match == nil {
			continue
		}
		normalized = pattern.os
		if normalized == "" {
			normalized = match[1]
		}
		if pattern.versionGroup > 0 {
			version = match[pattern.versionGroup]
		}
		if strings.HasPrefix(normalized, "mswin") && strings.HasSuffix(normalized, "32") && cpu == "" {
			cpu = "x86"
		}
		break
	}
	result := []string{}
	for _, s := range []string{cpu, normalized, version} {
		if s != "" {
			result = append(result, s)
		}
	}
	return strings.Join(result, "-")
}
