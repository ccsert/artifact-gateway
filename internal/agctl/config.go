package agctl

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	yaml "go.yaml.in/yaml/v3"
)

const defaultProfileName = "default"

type profileConfig struct {
	Server   string `yaml:"server,omitempty"`
	Token    string `yaml:"token,omitempty"`
	Insecure bool   `yaml:"insecure,omitempty"`
}

type configFile struct {
	CurrentProfile string                   `yaml:"current_profile,omitempty"`
	Profiles       map[string]profileConfig `yaml:"profiles,omitempty"`
}

// profileSettings is the resolved connection configuration for one invocation.
type profileSettings struct {
	Name     string
	Server   string
	Token    string
	Insecure bool
}

// defaultConfigPath returns ~/.config/agctl/config.yaml. An unknown home
// directory yields an empty path, which disables the file layer.
func defaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "agctl", "config.yaml")
}

// loadConfigFile reads the multi-profile configuration file. A missing file
// returns (nil, nil). The file must not be readable by other users.
func loadConfigFile(path string) (*configFile, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, &UsageError{Message: fmt.Sprintf("cannot read config file %s: %v", path, err)}
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, &UsageError{
			Message: fmt.Sprintf("config file %s is readable by other users", path),
			Hint:    fmt.Sprintf("chmod 600 %s", path),
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, &UsageError{Message: fmt.Sprintf("cannot read config file %s: %v", path, err)}
	}
	var cfg configFile
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, &UsageError{Message: fmt.Sprintf("config file %s is not valid YAML: %v", path, err)}
	}
	return &cfg, nil
}

func pickString(set bool, flagValue, envValue, fileValue string) string {
	if set {
		return flagValue
	}
	if envValue != "" {
		return envValue
	}
	return fileValue
}

// resolveSettings applies the documented precedence for every setting:
// explicit flag, then environment variable, then the selected config profile.
// Profile selection order is --profile, AGCTL_PROFILE, current_profile, the
// default profile, then a file holding exactly one profile.
func resolveSettings(flags *globalFlags, set map[string]bool, env func(string) string, configPath string) (profileSettings, error) {
	var cfg *configFile
	if configPath != "" {
		loaded, err := loadConfigFile(configPath)
		if err != nil {
			return profileSettings{}, err
		}
		cfg = loaded
	}

	settings := profileSettings{}
	name := ""
	explicit := false
	switch {
	case set["profile"] && flags.profile != "":
		name, explicit = flags.profile, true
	case env("AGCTL_PROFILE") != "":
		name, explicit = env("AGCTL_PROFILE"), true
	}
	if name == "" && cfg != nil {
		switch {
		case cfg.CurrentProfile != "":
			name = cfg.CurrentProfile
		case len(cfg.Profiles) == 1:
			for single := range cfg.Profiles {
				name = single
			}
		default:
			name = defaultProfileName
		}
	}

	var profile profileConfig
	if name != "" {
		found := false
		if cfg != nil {
			profile, found = cfg.Profiles[name]
		}
		switch {
		case found:
			settings.Name = name
		case explicit:
			message := fmt.Sprintf("profile %q not found", name)
			hint := "add the profile to ~/.config/agctl/config.yaml or pass --server and --token"
			if cfg == nil && configPath != "" {
				message = fmt.Sprintf("profile %q requested but %s does not exist", name, configPath)
			}
			return profileSettings{}, &UsageError{Message: message, Hint: hint}
		case cfg != nil && cfg.CurrentProfile == name:
			return profileSettings{}, &UsageError{
				Message: fmt.Sprintf("config file %s names current_profile %q but defines no such profile", configPath, name),
			}
		}
	}

	settings.Server = pickString(set["server"], flags.server, env("AGCTL_SERVER"), profile.Server)
	settings.Token = pickString(set["token"], flags.token, env("AGCTL_TOKEN"), profile.Token)
	if set["insecure"] {
		settings.Insecure = flags.insecure
	} else {
		settings.Insecure = profile.Insecure
	}
	return settings, nil
}

// validProfileName rejects names that cannot round-trip as configuration keys.
func validProfileName(name string) error {
	if name == "" || strings.TrimSpace(name) != name {
		return &UsageError{Message: "profile names must be non-empty without surrounding whitespace"}
	}
	for _, r := range name {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return &UsageError{
				Message: fmt.Sprintf("profile name %q must not contain whitespace or control characters", name),
			}
		}
	}
	return nil
}

// mutateConfigFile loads the configuration file (a missing file starts an
// empty document), applies mutate, and rewrites the file atomically.
func mutateConfigFile(path string, mutate func(*configFile) error) error {
	if path == "" {
		return &UsageError{
			Message: "the home directory is unknown, so no configuration file can be stored",
			Hint:    "set --token or AGCTL_TOKEN for a file-less invocation",
		}
	}
	cfg, err := loadConfigFile(path)
	if err != nil {
		return err
	}
	if cfg == nil {
		cfg = &configFile{}
	}
	if cfg.Profiles == nil {
		cfg.Profiles = map[string]profileConfig{}
	}
	if err := mutate(cfg); err != nil {
		return err
	}
	return writeConfigFile(path, cfg)
}

// writeConfigFile replaces the configuration file atomically. The written
// file is always mode 0600, and an existing lax file is refused rather than
// rewritten.
func writeConfigFile(path string, cfg *configFile) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return &UsageError{Message: fmt.Sprintf("cannot create %s: %v", directory, err)}
	}
	if info, err := os.Stat(path); err == nil && info.Mode().Perm()&0o077 != 0 {
		return &UsageError{
			Message: fmt.Sprintf("config file %s is readable by other users", path),
			Hint:    fmt.Sprintf("chmod 600 %s", path),
		}
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return &UsageError{Message: fmt.Sprintf("cannot encode the configuration: %v", err)}
	}
	temporary, err := os.CreateTemp(directory, ".config-*.yaml")
	if err != nil {
		return &UsageError{Message: fmt.Sprintf("cannot write %s: %v", path, err)}
	}
	temporaryName := temporary.Name()
	defer func() { _ = os.Remove(temporaryName) }()
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return &UsageError{Message: fmt.Sprintf("cannot write %s: %v", path, err)}
	}
	if err := temporary.Close(); err != nil {
		return &UsageError{Message: fmt.Sprintf("cannot write %s: %v", path, err)}
	}
	if err := os.Rename(temporaryName, path); err != nil {
		return &UsageError{Message: fmt.Sprintf("cannot replace %s: %v", path, err)}
	}
	return nil
}

// storedProfiles loads the configuration file for read-modify-write commands.
// A missing file yields an empty document.
func storedProfiles(path string) (*configFile, error) {
	cfg, err := loadConfigFile(path)
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		cfg = &configFile{}
	}
	if cfg.Profiles == nil {
		cfg.Profiles = map[string]profileConfig{}
	}
	return cfg, nil
}
