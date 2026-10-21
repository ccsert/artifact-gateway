package nuget

import (
	"bytes"
	"testing"
)

func TestNuGetDependencyGraphCanonicalization(t *testing.T) {
	for _, pair := range [][2]string{{".NETCoreApp,Version=v10.0", "net10.0"}, {".NETStandard,Version=v2.0", "netstandard2.0"}, {".NETFramework,Version=v4.7.2", "net472"}, {".NETFramework,Version=v1.0", "net10"}, {".NETCoreApp,Version=v10.0.0", "net10.0"}} {
		t.Run(pair[1], func(t *testing.T) {
			left, err := CanonicalDependencyGroups([]DependencyGroup{{TargetFramework: pair[0], Dependencies: []Dependency{{ID: "ACME.First", Range: "1.0"}, {ID: "acme.second", Range: "[2.0, 3.0)"}}}})
			if err != nil {
				t.Fatal(err)
			}
			right, err := CanonicalDependencyGroups([]DependencyGroup{{TargetFramework: pair[1], Dependencies: []Dependency{{ID: "Acme.Second", Range: "[2.0.0,3.0.0)"}, {ID: "acme.first", Range: "[1.0.0,)"}}}})
			if err != nil || !bytes.Equal(left, right) {
				t.Fatalf("equivalent graph changed: %s != %s (%v)", left, right, err)
			}
		})
	}
	for _, framework := range []string{"net", "network", "net1000", "net10.0-unrecognized", "portable-net45", ".NETCoreApp,Version=v10.0,Profile=Other"} {
		if _, err := CanonicalDependencyGroups([]DependencyGroup{{TargetFramework: framework}}); err == nil {
			t.Errorf("unknown framework accepted: %q", framework)
		}
	}
	for _, graph := range [][]DependencyGroup{
		{{TargetFramework: "net10.0"}, {TargetFramework: ".NETCoreApp,Version=v10.0"}},
		{{Dependencies: []Dependency{{ID: "ACME", Range: "[1.0.0]"}, {ID: "acme", Range: "[2.0.0]"}}}},
		{{Dependencies: []Dependency{{ID: "Acme", Range: "1.*"}}}},
	} {
		if _, err := CanonicalDependencyGroups(graph); err == nil {
			t.Errorf("ambiguous dependency graph accepted: %+v", graph)
		}
	}
}
