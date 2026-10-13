package agctl

import (
	"context"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
)

type profileRecord struct {
	Name         string `json:"name"`
	Server       string `json:"server,omitempty"`
	TokenStored  bool   `json:"tokenStored"`
	TokenCleared bool   `json:"tokenCleared,omitempty"`
	Insecure     bool   `json:"insecure"`
	Current      bool   `json:"current"`
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func configProfileAddCommand() command {
	var tokenStdin bool
	return command{
		name:        "config profile add",
		synopsis:    "create or update a profile in ~/.config/agctl/config.yaml",
		usage:       "agctl config profile add NAME --server URL [--token-stdin] [--insecure]",
		positionals: 1,
		argsUsage:   "NAME",
		setFlags: func(fs *flag.FlagSet) {
			fs.BoolVar(&tokenStdin, "token-stdin", false, "read the profile token from stdin (bounded, never echoed)")
		},
		meta: commandMeta{
			Resource:     "local",
			Scenarios:    []string{"ci", "admin"},
			Stage:        "m1",
			Endpoints:    []string{},
			Mutating:     true,
			MinimumRole:  "none",
			OutputSchema: "ProfileRecord",
			DryRun:       "unsupported",
		},
		run: func(_ context.Context, env *commandContext) error {
			name := env.args[0]
			if err := validProfileName(name); err != nil {
				return err
			}
			token := ""
			if tokenStdin {
				value, err := readBoundedSecret(env.stdin, secretInputLimit, "token",
					`pipe it in, for example: printf '%s\n' "$TOKEN" | agctl config profile add NAME --server URL --token-stdin`)
				if err != nil {
					return err
				}
				token = value
				env.addKnownSecret(token)
			}
			serverSet := env.flagSet["server"] && env.globals.server != ""
			record := profileRecord{Name: name}
			err := mutateConfigFile(env.configPath, func(config *configFile) error {
				profile := config.Profiles[name]
				previousTarget := ""
				if profile.Server != "" {
					if target, err := normalizeServerTarget(profile.Server); err == nil {
						previousTarget = target
					}
					// An unusable stored server cannot hold a credential
					// bound to it; treat it as a target change.
				}
				if serverSet {
					profile.Server = env.globals.server
				}
				if strings.TrimSpace(profile.Server) == "" {
					return &UsageError{
						Message: fmt.Sprintf("--server is required to store profile %q", name),
						Hint:    "pass --server https://gateway.example.com",
					}
				}
				target, err := normalizeServerTarget(profile.Server)
				if err != nil {
					return err
				}
				// A stored credential belongs to its target: changing the
				// target without providing its credential drops the old one
				// instead of sending it to the new server later.
				if tokenStdin {
					profile.Token = token
				} else if previousTarget != target {
					record.TokenCleared = profile.Token != ""
					profile.Token = ""
				}
				if env.flagSet["insecure"] {
					profile.Insecure = env.globals.insecure
				}
				config.Profiles[name] = profile
				record.Server = profile.Server
				record.TokenStored = profile.Token != ""
				record.Insecure = profile.Insecure
				record.Current = config.CurrentProfile == name
				return nil
			})
			if err != nil {
				return err
			}
			return env.render(record, nil, func(w io.Writer) error {
				details := ""
				if record.TokenCleared {
					details = " (stored token cleared: target changed)"
				}
				_, err := fmt.Fprintf(w, "profile %q stored in %s (server %s, token: %s, insecure: %s)%s\n",
					record.Name, env.configPath, record.Server, yesNo(record.TokenStored), yesNo(record.Insecure), details)
				return err
			})
		},
	}
}

func configProfileListCommand() command {
	return command{
		name:     "config profile list",
		synopsis: "list the local profiles and which one is current",
		usage:    "agctl config profile list [--json]",
		meta: commandMeta{
			Resource:     "local",
			Scenarios:    []string{"ci", "admin"},
			Stage:        "m1",
			Endpoints:    []string{},
			MinimumRole:  "none",
			OutputSchema: "ProfileRecord",
			DryRun:       "unsupported",
		},
		run: func(_ context.Context, env *commandContext) error {
			config, err := storedProfiles(env.configPath)
			if err != nil {
				return err
			}
			names := make([]string, 0, len(config.Profiles))
			for name := range config.Profiles {
				names = append(names, name)
			}
			sort.Strings(names)
			items := make([]profileRecord, 0, len(names))
			for _, name := range names {
				profile := config.Profiles[name]
				items = append(items, profileRecord{
					Name:        name,
					Server:      profile.Server,
					TokenStored: profile.Token != "",
					Insecure:    profile.Insecure,
					Current:     config.CurrentProfile == name,
				})
			}
			payload := newListPayload(items)
			meta := &envelopeMeta{Pagination: &paginationMeta{Mode: "none"}}
			return env.render(payload, meta, func(w io.Writer) error {
				if len(items) == 0 {
					_, err := fmt.Fprintf(w, "no profiles configured in %s\n", env.configPath)
					return err
				}
				nameWidth, serverWidth := len("NAME"), len("SERVER")
				for _, item := range items {
					if len(item.Name) > nameWidth {
						nameWidth = len(item.Name)
					}
					if len(item.Server) > serverWidth {
						serverWidth = len(item.Server)
					}
				}
				if _, err := fmt.Fprintf(w, "%-*s  %-*s  %-5s  %-8s  %s\n", nameWidth, "NAME", serverWidth, "SERVER", "TOKEN", "INSECURE", "CURRENT"); err != nil {
					return err
				}
				for _, item := range items {
					current := ""
					if item.Current {
						current = "*"
					}
					if _, err := fmt.Fprintf(w, "%-*s  %-*s  %-5s  %-8s  %s\n", nameWidth, item.Name, serverWidth, item.Server, yesNo(item.TokenStored), yesNo(item.Insecure), current); err != nil {
						return err
					}
				}
				return nil
			})
		},
	}
}

func configProfileUseCommand() command {
	return command{
		name:        "config profile use",
		synopsis:    "set the current profile for later invocations",
		usage:       "agctl config profile use NAME",
		positionals: 1,
		argsUsage:   "NAME",
		meta: commandMeta{
			Resource:     "local",
			Scenarios:    []string{"ci", "admin"},
			Stage:        "m1",
			Endpoints:    []string{},
			Mutating:     true,
			MinimumRole:  "none",
			OutputSchema: "ProfileRecord",
			DryRun:       "unsupported",
		},
		run: func(_ context.Context, env *commandContext) error {
			name := env.args[0]
			if err := validProfileName(name); err != nil {
				return err
			}
			record := profileRecord{Name: name}
			err := mutateConfigFile(env.configPath, func(config *configFile) error {
				profile, ok := config.Profiles[name]
				if !ok {
					return &UsageError{
						Message: fmt.Sprintf("profile %q does not exist", name),
						Hint:    "run agctl config profile list to see the configured profiles",
					}
				}
				config.CurrentProfile = name
				record.Server = profile.Server
				record.TokenStored = profile.Token != ""
				record.Insecure = profile.Insecure
				record.Current = true
				return nil
			})
			if err != nil {
				return err
			}
			return env.render(record, nil, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "current profile is now %q\n", record.Name)
				return err
			})
		},
	}
}

type profileRemoveResult struct {
	Removed        string `json:"removed"`
	ClearedCurrent bool   `json:"clearedCurrent,omitempty"`
}

func configProfileRemoveCommand() command {
	return command{
		name:        "config profile remove",
		synopsis:    "delete a profile from the local configuration file",
		usage:       "agctl config profile remove NAME",
		positionals: 1,
		argsUsage:   "NAME",
		meta: commandMeta{
			Resource:     "local",
			Scenarios:    []string{"ci", "admin"},
			Stage:        "m1",
			Endpoints:    []string{},
			Mutating:     true,
			MinimumRole:  "none",
			OutputSchema: "ProfileRemoveResult",
			DryRun:       "unsupported",
		},
		run: func(_ context.Context, env *commandContext) error {
			name := env.args[0]
			if err := validProfileName(name); err != nil {
				return err
			}
			result := profileRemoveResult{Removed: name}
			err := mutateConfigFile(env.configPath, func(config *configFile) error {
				if _, ok := config.Profiles[name]; !ok {
					return &UsageError{
						Message: fmt.Sprintf("profile %q does not exist", name),
						Hint:    "run agctl config profile list to see the configured profiles",
					}
				}
				delete(config.Profiles, name)
				if config.CurrentProfile == name {
					config.CurrentProfile = ""
					result.ClearedCurrent = true
				}
				return nil
			})
			if err != nil {
				return err
			}
			return env.render(result, nil, func(w io.Writer) error {
				if _, err := fmt.Fprintf(w, "profile %q removed\n", result.Removed); err != nil {
					return err
				}
				if result.ClearedCurrent {
					_, err := fmt.Fprintln(w, "it was the current profile; current_profile is now unset")
					return err
				}
				return nil
			})
		},
	}
}
