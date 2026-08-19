package main

import (
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/config"
)

func TestAPTReclaimWorkerRequiresBothFormatAndKindAdmission(t *testing.T) {
	for _, test := range []struct {
		name    string
		formats []string
		kinds   []string
		want    bool
	}{
		{name: "enabled", formats: []string{"apt"}, kinds: []string{"reclaim"}, want: true},
		{name: "format excluded", formats: []string{"raw"}, kinds: []string{"reclaim"}},
		{name: "kind excluded", formats: []string{"apt"}, kinds: []string{"replication"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := config.Config{NodeRoles: []config.NodeRole{config.NodeRoleWorker}, WorkerFormats: test.formats, WorkerKinds: test.kinds}
			if got := aptReclaimWorkerEnabled(cfg); got != test.want {
				t.Fatalf("aptReclaimWorkerEnabled()=%t want=%t", got, test.want)
			}
		})
	}
}

func TestGoReclaimWorkerRequiresBothFormatAndKindAdmission(t *testing.T) {
	for _, test := range []struct {
		name    string
		formats []string
		kinds   []string
		want    bool
	}{
		{name: "enabled", formats: []string{"go"}, kinds: []string{"reclaim"}, want: true},
		{name: "format excluded", formats: []string{"raw"}, kinds: []string{"reclaim"}},
		{name: "kind excluded", formats: []string{"go"}, kinds: []string{"replication"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := config.Config{NodeRoles: []config.NodeRole{config.NodeRoleWorker}, WorkerFormats: test.formats, WorkerKinds: test.kinds}
			if got := goReclaimWorkerEnabled(cfg); got != test.want {
				t.Fatalf("goReclaimWorkerEnabled()=%t want=%t", got, test.want)
			}
		})
	}
}
