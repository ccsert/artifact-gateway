package main

import (
	"slices"
	"testing"

	"github.com/ccsert/artifact-gateway/internal/config"
)

func TestMigrationWorkerOnlyAdmitsSelectedNativeFormats(t *testing.T) {
	for _, test := range []struct{ formats, want []string }{
		{nil, []string{}},
		{[]string{"maven", "raw", "oci", "conan", "npm"}, []string{"maven", "raw", "oci", "conan", "npm"}},
		{[]string{"raw"}, []string{"raw"}},
		{[]string{"maven"}, []string{"maven"}},
		{[]string{"oci", "npm"}, []string{"oci", "npm"}},
		{[]string{"npm", "helm", "nuget", "cargo", "pypi", "go"}, []string{"npm"}},
	} {
		if got := migrationWorkerFormats(config.Config{WorkerFormats: test.formats}); !slices.Equal(got, test.want) {
			t.Fatalf("formats %v admitted %v, want %v", test.formats, got, test.want)
		}
	}
}
