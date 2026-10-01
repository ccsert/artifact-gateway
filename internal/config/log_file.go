package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func configureLogFile(cfg *Config) error {
	cfg.LogFileDirectory = strings.TrimSpace(os.Getenv("GATEWAY_LOG_FILE_DIRECTORY"))
	if cfg.LogFileDirectory != "" && !filepath.IsAbs(cfg.LogFileDirectory) {
		return fmt.Errorf("GATEWAY_LOG_FILE_DIRECTORY must be an absolute path")
	}
	var err error
	if cfg.LogFileMaxBytes, err = positiveInt64Env("GATEWAY_LOG_FILE_MAX_BYTES", 100<<20); err != nil {
		return err
	}
	if cfg.LogFileMaxBytes > 1<<30 {
		return fmt.Errorf("GATEWAY_LOG_FILE_MAX_BYTES must be at most 1073741824")
	}
	if cfg.LogFileMaxBackups, err = positiveIntEnv("GATEWAY_LOG_FILE_MAX_BACKUPS", 10, true); err != nil {
		return err
	}
	if cfg.LogFileMaxBackups > 100 {
		return fmt.Errorf("GATEWAY_LOG_FILE_MAX_BACKUPS must be at most 100")
	}
	cfg.LogFileMaxAge = 168 * time.Hour
	raw := strings.TrimSpace(os.Getenv("GATEWAY_LOG_FILE_MAX_AGE"))
	if raw != "" {
		if seconds, parseErr := strconv.ParseInt(raw, 10, 64); parseErr == nil {
			if seconds <= 0 || seconds > 365*24*60*60 {
				return fmt.Errorf("GATEWAY_LOG_FILE_MAX_AGE must be positive and at most 365 days")
			}
			cfg.LogFileMaxAge = time.Duration(seconds) * time.Second
		} else if cfg.LogFileMaxAge, err = time.ParseDuration(raw); err != nil {
			return fmt.Errorf("GATEWAY_LOG_FILE_MAX_AGE must be a duration")
		}
	}
	if cfg.LogFileMaxAge <= 0 || cfg.LogFileMaxAge > 365*24*time.Hour {
		return fmt.Errorf("GATEWAY_LOG_FILE_MAX_AGE must be positive and at most 365 days")
	}
	return nil
}
