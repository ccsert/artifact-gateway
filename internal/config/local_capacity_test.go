package config

import (
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/localcapacity"
)

func TestLoadLocalCapacityIsExplicitAndDoesNotReadDirectories(t *testing.T) {
	setCompleteConfiguration(t)
	t.Setenv("GATEWAY_LOCAL_CAPACITY_MOUNTS", "")
	cfg, err := Load()
	if err != nil || len(cfg.LocalCapacityMounts) != 0 {
		t.Fatalf("default capacity selection=%v err=%v", cfg.LocalCapacityMounts, err)
	}
	// These synthetic paths need not exist: startup validates syntax only.
	t.Setenv("GATEWAY_LOCAL_CAPACITY_MOUNTS", `{"temporary":"/synthetic/temporary","logs":"/synthetic/logs"}`)
	cfg, err = Load()
	if err != nil || len(cfg.LocalCapacityMounts) != 2 || cfg.LocalCapacityMounts[localcapacity.Temporary] != "/synthetic/temporary" || cfg.LocalCapacityMounts[localcapacity.Logs] != "/synthetic/logs" {
		t.Fatalf("explicit capacity selection=%v err=%v", cfg.LocalCapacityMounts, err)
	}
}

func TestLoadRejectsInvalidLocalCapacityWithoutEchoingInput(t *testing.T) {
	for _, input := range []string{
		`null`, `[]`, `{"temporary":null}`, `{"temporary":123}`,
		`{"temporary":""}`, `{"temporary":"private-secret"}`,
		`{"temporary":"/private-secret/../other"}`,
		`{"unexpected-private-secret":"/private-secret"}`,
		`{"temporary":"/private-secret","temporary":"/other"}`,
		`{"temporary":"/private-secret"} {}`,
		`{"temporary":"/private-secret\u0000"}`,
		`{"temporary":"/` + strings.Repeat("x", 4096) + `"}`,
		strings.Repeat(" ", 16385),
	} {
		t.Run("invalid selection", func(t *testing.T) {
			setCompleteConfiguration(t)
			t.Setenv("GATEWAY_LOCAL_CAPACITY_MOUNTS", input)
			_, err := Load()
			if err == nil || err.Error() != "GATEWAY_LOCAL_CAPACITY_MOUNTS is invalid" {
				t.Fatalf("unsafe or missing configuration error=%v", err)
			}
		})
	}
}
