package config

import (
	"errors"
	"os"
	"strings"
)

func loadRepositoryPresetsMode() (string, error) {
	mode := strings.TrimSpace(os.Getenv("GATEWAY_REPOSITORY_PRESETS"))
	if mode == "" {
		return "standard", nil
	}
	if mode != "standard" && mode != "none" {
		return "", errors.New("GATEWAY_REPOSITORY_PRESETS must be standard or none")
	}
	return mode, nil
}
