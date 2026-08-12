package nuget

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

func TestParsePackageDerivesCanonicalIdentityFromNuspec(t *testing.T) {
	data := testPackage(t, []testPackageEntry{
		{name: "Contoso.Utility.nuspec", body: []byte(`<?xml version="1.0"?>
<package xmlns="http://schemas.microsoft.com/packaging/2013/05/nuspec.xsd">
  <metadata>
    <id>Contoso.Utility</id>
    <version>01.02.003.0-RC.1+build.9</version>
    <authors>Gateway Team</authors>
    <description>fixture</description>
  </metadata>
</package>`)},
		{name: "lib/net8.0/Contoso.Utility.dll", body: []byte("assembly")},
	})

	metadata, err := ParsePackage(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if metadata.ID != "Contoso.Utility" || metadata.DeclaredVersion != "01.02.003.0-RC.1+build.9" {
		t.Fatalf("metadata=%#v", metadata)
	}
	if metadata.NormalizedVersion != "1.2.3-RC.1" {
		t.Fatalf("normalized version=%q", metadata.NormalizedVersion)
	}
	if metadata.CanonicalIdentity != "contoso.utility@1.2.3-rc.1" {
		t.Fatalf("canonical identity=%q", metadata.CanonicalIdentity)
	}
}

func TestNormalizeVersionMatchesNuGetRepositoryIdentityRules(t *testing.T) {
	tests := map[string]string{
		"1":                      "1.0.0",
		"1.0":                    "1.0.0",
		"1.0.0.0":                "1.0.0",
		"1.00.01.2":              "1.0.1.2",
		"2.3.4-BETA.5":           "2.3.4-BETA.5",
		"2.3.4-alpha.1+build.99": "2.3.4-alpha.1",
	}
	for declared, expected := range tests {
		t.Run(declared, func(t *testing.T) {
			actual, err := NormalizeVersion(declared)
			if err != nil {
				t.Fatal(err)
			}
			if actual != expected {
				t.Fatalf("NormalizeVersion(%q)=%q, want %q", declared, actual, expected)
			}
		})
	}
}

func TestNormalizeVersionRejectsNonNuGetVersions(t *testing.T) {
	for _, version := range []string{"", "1.2.3.4.5", "1..2", "v1.2.3", "1.2.3-", "1.2.3-alpha.01", "1.2.3+a..b", "2147483648.0.0", " 1.0.0"} {
		t.Run(version, func(t *testing.T) {
			if _, err := NormalizeVersion(version); err == nil {
				t.Fatalf("invalid version %q was accepted", version)
			}
		})
	}
}

func TestParsePackageRejectsMalformedOrAmbiguousManifest(t *testing.T) {
	valid := func(body string) []byte {
		return testPackage(t, []testPackageEntry{{name: "package.nuspec", body: []byte(body)}})
	}
	tests := []struct {
		name        string
		data        []byte
		errContains string
	}{
		{name: "not zip", data: []byte("not a package"), errContains: "invalid NuGet package archive"},
		{name: "missing manifest", data: testPackage(t, []testPackageEntry{{name: "lib/file.dll", body: []byte("x")}}), errContains: "nuspec manifest is missing"},
		{name: "nested manifest", data: testPackage(t, []testPackageEntry{{name: "metadata/package.nuspec", body: []byte("x")}}), errContains: "nuspec manifest must be at the package root"},
		{name: "root and nested manifest", data: testPackage(t, []testPackageEntry{
			{name: "package.nuspec", body: []byte(`<package><metadata><id>Contoso.Utility</id><version>1.0.0</version></metadata></package>`)},
			{name: "metadata/package.nuspec", body: []byte("x")},
		}), errContains: "nuspec manifest must be at the package root"},
		{name: "duplicate manifest", data: testPackage(t, []testPackageEntry{
			{name: "a.nuspec", body: []byte("<package/>")},
			{name: "b.nuspec", body: []byte("<package/>")},
		}), errContains: "multiple nuspec manifests"},
		{name: "path traversal", data: testPackage(t, []testPackageEntry{
			{name: "package.nuspec", body: []byte("<package/>")},
			{name: "../outside", body: []byte("x")},
		}), errContains: "invalid package entry path"},
		{name: "doctype", data: valid(`<!DOCTYPE package><package><metadata><id>Contoso.Utility</id><version>1.0.0</version></metadata></package>`), errContains: "XML directives are not allowed"},
		{name: "foreign namespace", data: valid(`<evil:package xmlns:evil="urn:evil"><evil:metadata><evil:id>Contoso.Utility</evil:id><evil:version>1.0.0</evil:version></evil:metadata></evil:package>`), errContains: "unsupported nuspec namespace"},
		{name: "mixed metadata namespace", data: valid(`<package xmlns="http://schemas.microsoft.com/packaging/2013/05/nuspec.xsd" xmlns:evil="urn:evil"><evil:metadata><evil:id>Contoso.Utility</evil:id><evil:version>1.0.0</evil:version></evil:metadata></package>`), errContains: "nuspec element namespace does not match package"},
		{name: "mixed identity namespace", data: valid(`<package xmlns="http://schemas.microsoft.com/packaging/2013/05/nuspec.xsd" xmlns:evil="urn:evil"><metadata><evil:id>Contoso.Utility</evil:id><version>1.0.0</version></metadata></package>`), errContains: "nuspec element namespace does not match package"},
		{name: "duplicate metadata", data: valid(`<package><metadata><id>Contoso.Utility</id><version>1.0.0</version></metadata><metadata><id>Other</id><version>2.0.0</version></metadata></package>`), errContains: "exactly one metadata element"},
		{name: "duplicate id", data: valid(`<package><metadata><id>Contoso.Utility</id><id>Other</id><version>1.0.0</version></metadata></package>`), errContains: "exactly one package id"},
		{name: "missing version", data: valid(`<package><metadata><id>Contoso.Utility</id></metadata></package>`), errContains: "exactly one package version"},
		{name: "invalid id", data: valid(`<package><metadata><id>Contoso../Utility</id><version>1.0.0</version></metadata></package>`), errContains: "invalid NuGet package id"},
		{name: "invalid version", data: valid(`<package><metadata><id>Contoso.Utility</id><version>[1.0,2.0)</version></metadata></package>`), errContains: "invalid NuGet package version"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParsePackage(bytes.NewReader(test.data), int64(len(test.data)))
			if err == nil || !strings.Contains(err.Error(), test.errContains) {
				t.Fatalf("error=%v, want substring %q", err, test.errContains)
			}
		})
	}
}

func TestParsePackageBoundsNuspecExpansion(t *testing.T) {
	body := []byte(`<package><metadata><id>Contoso.Utility</id><version>1.0.0</version><description>` + strings.Repeat("x", maxNuspecBytes) + `</description></metadata></package>`)
	data := testPackage(t, []testPackageEntry{{name: "package.nuspec", body: body}})
	if _, err := ParsePackage(bytes.NewReader(data), int64(len(data))); err == nil || !strings.Contains(err.Error(), "nuspec manifest is too large") {
		t.Fatalf("error=%v", err)
	}
}

func FuzzParsePackageDoesNotPanic(f *testing.F) {
	f.Add([]byte("not a package"))
	f.Add(testPackage(f, []testPackageEntry{{
		name: "package.nuspec",
		body: []byte(`<package><metadata><id>Contoso.Utility</id><version>1.0.0</version></metadata></package>`),
	}}))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 2<<20 {
			t.Skip()
		}
		_, _ = ParsePackage(bytes.NewReader(data), int64(len(data)))
	})
}

type testPackageEntry struct {
	name string
	body []byte
}

func testPackage(t testing.TB, entries []testPackageEntry) []byte {
	t.Helper()
	var data bytes.Buffer
	writer := zip.NewWriter(&data)
	for _, entry := range entries {
		file, err := writer.Create(entry.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = file.Write(entry.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}
