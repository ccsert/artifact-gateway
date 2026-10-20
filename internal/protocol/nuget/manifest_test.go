package nuget

import (
	"bytes"
	"testing"
)

func TestInspectPackageRetainsManifestAndDependencyIdentity(t *testing.T) {
	nuspec := []byte(`<package xmlns="http://schemas.microsoft.com/packaging/2013/05/nuspec.xsd"><metadata><id>Acme.App</id><version>1.0</version><authors>Acme</authors><description>Example</description><dependencies><group targetFramework="net10.0"><dependency id="Acme.Core" version="[1.0.0,2.0.0)" /></group></dependencies></metadata></package>`)
	archive := testPackage(t, []testPackageEntry{{name: "Acme.App.nuspec", body: nuspec}})
	got, err := InspectPackage(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.CanonicalIdentity != "acme.app@1.0.0" || !bytes.Equal(got.Nuspec, nuspec) || got.Authors != "Acme" || got.Description != "Example" {
		t.Fatalf("inspection=%+v", got)
	}
	if len(got.DependencyGroups) != 1 || got.DependencyGroups[0].TargetFramework != "net10.0" || len(got.DependencyGroups[0].Dependencies) != 1 || got.DependencyGroups[0].Dependencies[0].ID != "Acme.Core" || got.DependencyGroups[0].Dependencies[0].Range != "[1.0.0,2.0.0)" {
		t.Fatalf("dependencies=%+v", got.DependencyGroups)
	}
}

func TestInspectPackageAcceptsOfficialUTF8BOMWithoutChangingBytes(t *testing.T) {
	body := append([]byte{0xef, 0xbb, 0xbf}, []byte(`<?xml version="1.0" encoding="utf-8"?><package xmlns="http://schemas.microsoft.com/packaging/2012/06/nuspec.xsd"><metadata><id>Hosted.Leaf</id><version>1.0.0</version><dependencies><group targetFramework="net10.0"/></dependencies></metadata></package>`)...)
	archive := testPackage(t, []testPackageEntry{{name: "Hosted.Leaf.nuspec", body: body}})
	got, err := InspectPackage(bytes.NewReader(archive), int64(len(archive)))
	if err != nil || got.Identity.CanonicalIdentity != "hosted.leaf@1.0.0" || !bytes.Equal(got.Nuspec, body) {
		t.Fatalf("official UTF-8 manifest=%+v %v", got, err)
	}
	for _, bad := range [][]byte{append([]byte{0xef, 0xbb, 0xbf}, body...), append(body, 0xef, 0xbb, 0xbf)} {
		archive = testPackage(t, []testPackageEntry{{name: "Hosted.Leaf.nuspec", body: bad}})
		if _, err := InspectPackage(bytes.NewReader(archive), int64(len(archive))); err == nil {
			t.Fatal("multiple or trailing BOM accepted")
		}
	}
}

func TestInspectPackageRejectsAmbiguousDependencyMetadata(t *testing.T) {
	for _, dependency := range []string{
		`<dependencies><dependency id="../Other" version="1.0" /></dependencies>`,
		`<dependencies><dependency id="Other" version="*" /></dependencies>`,
		`<dependencies><dependency id="Other" version="[2.0,1.0)" /></dependencies>`,
		`<dependencies><group><dependency id="Other" version="1.0"/><dependency id="OTHER" version="1.0"/></group></dependencies>`,
		`<dependencies/><dependencies/>`,
		`<dependencies><group targetFramework="net10.0"/><group targetFramework="net10.0"/></dependencies>`,
		`<dependencies><dependency id="Other" version="1.0"/><group targetFramework="net10.0"/></dependencies>`,
		`<dependencies xmlns="urn:foreign"><dependency id="Other" version="1.0"/></dependencies>`,
	} {
		t.Run(dependency, func(t *testing.T) {
			body := []byte(`<package><metadata><id>Example</id><version>1</version>` + dependency + `</metadata></package>`)
			archive := testPackage(t, []testPackageEntry{{name: "Example.nuspec", body: body}})
			if _, err := InspectPackage(bytes.NewReader(archive), int64(len(archive))); err == nil {
				t.Fatal("ambiguous dependency metadata accepted")
			}
		})
	}
}

func TestInspectPackageRejectsAmbiguousDependencyAttributes(t *testing.T) {
	for _, dependency := range []string{
		`<dependency id="Expected" id="Other" version="[1.0.0]"/>`,
		`<dependency id="Expected" version="[1.0.0]" version="[2.0.0]"/>`,
		`<dependency xmlns:x="urn:foreign" id="Expected" x:id="Other" version="[1.0.0]"/>`,
		`<dependency xmlns:x="urn:foreign" x:id="Other" id="Expected" version="[1.0.0]"/>`,
		`<dependency xmlns:x="urn:foreign" id="Expected" version="[1.0.0]" x:version="[2.0.0]"/>`,
		`<dependency xmlns:x="urn:foreign" id="Expected" x:version="[2.0.0]" version="[1.0.0]"/>`,
		`<group targetFramework="net10.0" targetFramework="net9.0"><dependency id="Expected" version="[1.0.0]"/></group>`,
		`<group xmlns:x="urn:foreign" targetFramework="net10.0" x:targetFramework="net9.0"><dependency id="Expected" version="[1.0.0]"/></group>`,
		`<group xmlns:x="urn:foreign" x:targetFramework="net9.0" targetFramework="net10.0"><dependency id="Expected" version="[1.0.0]"/></group>`,
	} {
		t.Run(dependency, func(t *testing.T) {
			body := []byte(`<package><metadata><id>Example</id><version>1</version><dependencies>` + dependency + `</dependencies></metadata></package>`)
			archive := testPackage(t, []testPackageEntry{{name: "Example.nuspec", body: body}})
			if got, err := InspectPackage(bytes.NewReader(archive), int64(len(archive))); err == nil {
				t.Fatalf("ambiguous attributes accepted: %+v", got.DependencyGroups)
			}
		})
	}
}

func TestInspectPackageRetainsSupportedDependencyAttributesAndOriginalBytes(t *testing.T) {
	body := []byte(`<package xmlns="http://schemas.microsoft.com/packaging/2013/05/nuspec.xsd"><metadata><id>Example</id><version>1</version><dependencies><group xmlns:targetFramework="urn:extension" targetFramework="net10.0"><dependency xmlns:id="urn:extension" xmlns:version="urn:extension" id="Expected" version="[1.0.0]" include="Runtime,Compile" exclude="Build,Analyzers"/></group></dependencies></metadata></package>`)
	archive := testPackage(t, []testPackageEntry{{name: "Example.nuspec", body: body}})
	got, err := InspectPackage(bytes.NewReader(archive), int64(len(archive)))
	if err != nil || !bytes.Equal(got.Nuspec, body) || len(got.DependencyGroups) != 1 || got.DependencyGroups[0].TargetFramework != "net10.0" || len(got.DependencyGroups[0].Dependencies) != 1 || got.DependencyGroups[0].Dependencies[0] != (Dependency{ID: "Expected", Range: "[1.0.0]"}) {
		t.Fatalf("supported attributes=%+v %v", got, err)
	}
}

func TestCompareNuGetVersions(t *testing.T) {
	for _, pair := range [][2]string{{"1.0-alpha.2", "1.0-alpha.10"}, {"1.0-alpha", "1.0"}, {"1.0.0", "1.0.0.1"}, {"1.9", "1.10"}, {"1.0-1", "1.0-a"}} {
		if CompareVersions(pair[0], pair[1]) >= 0 || CompareVersions(pair[1], pair[0]) <= 0 {
			t.Fatalf("version order=%v", pair)
		}
	}
	if CompareVersions("01.0.0-RC.1+build", "1.0.0-rc.1") != 0 {
		t.Fatal("equivalent versions have different precedence")
	}
}
