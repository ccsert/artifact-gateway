package config

import (
	"testing"
	"time"
)

func TestLoadFileLogDefaultsAndOverrides(t *testing.T) {
	setCompleteConfiguration(t)
	for _, name := range []string{"GATEWAY_LOG_FILE_DIRECTORY", "GATEWAY_LOG_FILE_MAX_BYTES", "GATEWAY_LOG_FILE_MAX_BACKUPS", "GATEWAY_LOG_FILE_MAX_AGE"} {
		t.Setenv(name, "")
	}
	cfg, err := Load()
	if err != nil || cfg.LogFileDirectory != "" || cfg.LogFileMaxBytes != 100<<20 || cfg.LogFileMaxBackups != 10 || cfg.LogFileMaxAge != 168*time.Hour {
		t.Fatalf("defaults = %q %d %d %s, %v", cfg.LogFileDirectory, cfg.LogFileMaxBytes, cfg.LogFileMaxBackups, cfg.LogFileMaxAge, err)
	}
	t.Setenv("GATEWAY_LOG_FILE_DIRECTORY", t.TempDir())
	t.Setenv("GATEWAY_LOG_FILE_MAX_BYTES", "1024")
	t.Setenv("GATEWAY_LOG_FILE_MAX_BACKUPS", "0")
	t.Setenv("GATEWAY_LOG_FILE_MAX_AGE", "2h")
	cfg, err = Load()
	if err != nil || cfg.LogFileDirectory == "" || cfg.LogFileMaxBytes != 1024 || cfg.LogFileMaxBackups != 0 || cfg.LogFileMaxAge != 2*time.Hour {
		t.Fatalf("overrides = %+v, %v", cfg, err)
	}
}

func TestLoadRejectsInvalidFileLogConfiguration(t *testing.T) {
	for name, values := range map[string][]string{
		"GATEWAY_LOG_FILE_DIRECTORY":   {"relative/path"},
		"GATEWAY_LOG_FILE_MAX_BYTES":   {"0", "-1", "1073741825", "many"},
		"GATEWAY_LOG_FILE_MAX_BACKUPS": {"-1", "101", "many"},
		"GATEWAY_LOG_FILE_MAX_AGE":     {"0", "-1h", "8761h", "many", "9223372036854775807"},
	} {
		for _, value := range values {
			t.Run(name+"/"+value, func(t *testing.T) {
				setCompleteConfiguration(t)
				t.Setenv(name, value)
				if _, err := Load(); err == nil {
					t.Fatalf("accepted %s=%q", name, value)
				}
			})
		}
	}
}
