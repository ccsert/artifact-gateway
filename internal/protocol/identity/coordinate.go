// Package identity owns the protocol-specific construction of canonical
// immutable artifact coordinates used by management operations.
package identity

import "regexp"

var sha256DigestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

func Maven(coordinate string) string { return coordinate }

func OCI(name string) string { return name }

func Raw(path string) string { return path }

func NPMVersion(packageName, version string) string { return packageName + "@" + version }

func PyPIVersion(project, version string) string { return project + "@" + version }

func GoVersion(module, version string) string { return module + "@" + version }

func APTVersion(packageName, version, architecture string) string {
	return packageName + "@" + version + "#" + architecture
}

func ConanRecipe(reference, recipeRevision string) string {
	return reference + "#" + recipeRevision
}

func ConanPackage(reference, recipeRevision, packageID, packageRevision string) string {
	return ConanRecipe(reference, recipeRevision) + "/" + packageID + "#" + packageRevision
}

func IsSHA256Digest(digest string) bool { return sha256DigestPattern.MatchString(digest) }

// PostgreSQL expression helpers keep the same protocol separators authoritative
// when persistence must filter and order canonical coordinates before paging.
// Callers pass only trusted column expressions, never request input.
func PostgreSQLNPMVersion(packageName, version string) string {
	return packageName + ` || '@' || ` + version
}

func PostgreSQLPyPIVersion(project, version string) string {
	return project + ` || '@' || ` + version
}

func PostgreSQLGoVersion(module, version string) string {
	return module + ` || '@' || ` + version
}

func PostgreSQLConanRecipe(reference, recipeRevision string) string {
	return reference + ` || '#' || ` + recipeRevision
}

func PostgreSQLConanPackage(reference, recipeRevision, packageID, packageRevision string) string {
	return PostgreSQLConanRecipe(reference, recipeRevision) + ` || '/' || ` + packageID + ` || '#' || ` + packageRevision
}
