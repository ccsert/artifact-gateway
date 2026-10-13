package agctl

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
)

const (
	secretInputLimit = 4 << 10
	secretPairLimit  = 8 << 10
	repositorySample = 20
)

// readBoundedSecret reads one private value from a bounded stdin and trims
// only the trailing line ending. Empty or oversized input fails before any
// request leaves the client.
func readBoundedSecret(reader io.Reader, limit int, label, hint string) (string, error) {
	if reader == nil {
		return "", &UsageError{Message: fmt.Sprintf("stdin is unavailable to read the %s", label), Hint: hint}
	}
	data, err := io.ReadAll(io.LimitReader(reader, int64(limit)+1))
	if err != nil {
		return "", &UsageError{Message: fmt.Sprintf("cannot read the %s from stdin", label), Hint: hint}
	}
	if len(data) > limit {
		return "", &UsageError{Message: fmt.Sprintf("the %s on stdin exceeds %d bytes", label, limit), Hint: hint}
	}
	value := strings.TrimRight(string(data), "\r\n")
	if value == "" {
		return "", &UsageError{Message: fmt.Sprintf("no %s provided on stdin", label), Hint: hint}
	}
	return value, nil
}

// readPasswordPair reads the current password and the new password, one per
// line, from a bounded stdin.
func readPasswordPair(reader io.Reader, limit int) (string, string, error) {
	hint := `pipe both lines in, for example: printf '%s\n%s\n' "$CURRENT" "$NEW" | agctl auth change-password --password-stdin`
	if reader == nil {
		return "", "", &UsageError{Message: "stdin is unavailable to read the passwords", Hint: hint}
	}
	data, err := io.ReadAll(io.LimitReader(reader, int64(limit)+1))
	if err != nil {
		return "", "", &UsageError{Message: "cannot read the passwords from stdin", Hint: hint}
	}
	if len(data) > limit {
		return "", "", &UsageError{Message: fmt.Sprintf("the passwords on stdin exceed %d bytes", limit), Hint: hint}
	}
	lines := strings.Split(strings.TrimRight(string(data), "\r\n"), "\n")
	if len(lines) != 2 {
		return "", "", &UsageError{
			Message: "stdin must contain exactly two lines: the current password, then the new password",
			Hint:    hint,
		}
	}
	current := strings.TrimRight(lines[0], "\r")
	updated := strings.TrimRight(lines[1], "\r")
	if strings.TrimSpace(current) == "" || strings.TrimSpace(updated) == "" {
		return "", "", &UsageError{Message: "both password lines must be non-empty", Hint: hint}
	}
	return current, updated, nil
}

// explicitProfile returns the profile a login must store its token in. Only a
// flag or the environment select it explicitly; until the secure secret-output
// destination lands, an implicit profile is refused before any request.
func explicitProfile(env *commandContext) (string, error) {
	name := ""
	switch {
	case env.flagSet["profile"] && env.globals.profile != "":
		name = env.globals.profile
	case os.Getenv("AGCTL_PROFILE") != "":
		name = os.Getenv("AGCTL_PROFILE")
	}
	if name == "" {
		return "", &UsageError{
			Message: "auth login needs an explicit profile to store the session token",
			Hint:    "pass --profile NAME (or set AGCTL_PROFILE); a secure --secret-output destination is not available yet",
		}
	}
	config, err := storedProfiles(env.configPath)
	if err != nil {
		return "", err
	}
	if _, ok := config.Profiles[name]; !ok {
		return "", &UsageError{
			Message: fmt.Sprintf("profile %q does not exist", name),
			Hint:    fmt.Sprintf("create it first: agctl config profile add %s --server %s", name, env.settings.Server),
		}
	}
	return name, nil
}

// clearStoredToken removes token from every profile that stores it and
// reports the profile it was removed from, if any.
func clearStoredToken(env *commandContext, token string) (string, error) {
	if token == "" || env.configPath == "" {
		return "", nil
	}
	existing, err := loadConfigFile(env.configPath)
	if err != nil || existing == nil {
		return "", err
	}
	cleared := ""
	err = mutateConfigFile(env.configPath, func(config *configFile) error {
		for name, profile := range config.Profiles {
			if profile.Token == token {
				profile.Token = ""
				config.Profiles[name] = profile
				cleared = name
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return cleared, nil
}

type authLoginResult struct {
	Profile            string `json:"profile"`
	Username           string `json:"username"`
	DisplayName        string `json:"displayName,omitempty"`
	Role               string `json:"role,omitempty"`
	MustChangePassword bool   `json:"mustChangePassword"`
}

func authLoginCommand() command {
	var username string
	var passwordStdin bool
	return command{
		name:     "auth login",
		synopsis: "authenticate with a username and password and store the session token in a profile",
		usage:    "agctl auth login --username NAME --profile NAME --password-stdin",
		setFlags: func(fs *flag.FlagSet) {
			fs.StringVar(&username, "username", "", "local username to authenticate")
			fs.BoolVar(&passwordStdin, "password-stdin", false, "read the password from stdin (bounded, never echoed)")
		},
		meta: commandMeta{
			Resource:     "auth",
			Scenarios:    []string{"ci", "admin"},
			Stage:        "m1",
			Endpoints:    []string{"POST /auth/login"},
			Mutating:     true,
			MinimumRole:  "anonymous",
			InputSchema:  "LoginRequest",
			OutputSchema: "AuthLoginResult",
			DryRun:       "unsupported",
		},
		run: func(ctx context.Context, env *commandContext) error {
			profile, err := explicitProfile(env)
			if err != nil {
				return err
			}
			if strings.TrimSpace(username) == "" {
				return &UsageError{Message: "auth login requires --username", Hint: "pass --username NAME"}
			}
			if !passwordStdin {
				return &UsageError{
					Message: "auth login requires --password-stdin",
					Hint:    `pipe the password in: printf '%s\n' "$AGCTL_LOGIN_PASSWORD" | agctl auth login --username NAME --profile NAME --password-stdin`,
				}
			}
			password, err := readBoundedSecret(env.stdin, secretInputLimit, "password",
				`pipe it in, for example: printf '%s\n' "$AGCTL_LOGIN_PASSWORD" | agctl auth login --username NAME --profile NAME --password-stdin`)
			if err != nil {
				return err
			}
			env.addKnownSecret(password)

			client, err := env.client()
			if err != nil {
				return err
			}
			var login struct {
				Token              string `json:"token"`
				Username           string `json:"username"`
				DisplayName        string `json:"displayName"`
				Role               string `json:"role"`
				MustChangePassword bool   `json:"mustChangePassword"`
			}
			if err := client.postJSON(ctx, "/auth/login", map[string]string{
				"username": strings.TrimSpace(username),
				"password": password,
			}, &login); err != nil {
				return err
			}
			if login.Token == "" {
				return &APIError{Code: "invalid_response", Message: "the server returned no session token"}
			}
			env.addKnownSecret(login.Token)

			if err := mutateConfigFile(env.configPath, func(config *configFile) error {
				profileConfig, ok := config.Profiles[profile]
				if !ok {
					return &UsageError{Message: fmt.Sprintf("profile %q does not exist", profile)}
				}
				profileConfig.Token = login.Token
				config.Profiles[profile] = profileConfig
				return nil
			}); err != nil {
				return err
			}
			result := authLoginResult{
				Profile:            profile,
				Username:           login.Username,
				DisplayName:        login.DisplayName,
				Role:               login.Role,
				MustChangePassword: login.MustChangePassword,
			}
			return env.render(result, nil, func(w io.Writer) error {
				if _, err := fmt.Fprintf(w, "authenticated as %s; session token stored in profile %q\n", result.Username, result.Profile); err != nil {
					return err
				}
				if result.MustChangePassword {
					_, err := fmt.Fprintf(w, "password change required: run `agctl auth change-password --profile %s` and sign in again\n", result.Profile)
					return err
				}
				return nil
			})
		},
	}
}

type authLogoutResult struct {
	ServerRevoked  bool   `json:"serverRevoked"`
	AlreadyInvalid bool   `json:"alreadyInvalid,omitempty"`
	ProfileCleared string `json:"profileCleared,omitempty"`
	Note           string `json:"note"`
}

const authLogoutNote = "logout revokes the caller's local user session and removes a stored session token; service account tokens and API keys are not revoked"

func authLogoutCommand() command {
	var localOnly bool
	return command{
		name:     "auth logout",
		synopsis: "revoke the caller's local user session and remove a stored session token",
		usage:    "agctl auth logout [--local-only]",
		setFlags: func(fs *flag.FlagSet) {
			fs.BoolVar(&localOnly, "local-only", false, "only forget local credentials; do not call the server")
		},
		meta: commandMeta{
			Resource:     "auth",
			Scenarios:    []string{"ci", "admin"},
			Stage:        "m1",
			Endpoints:    []string{"POST /auth/logout"},
			Mutating:     true,
			MinimumRole:  "authenticated",
			OutputSchema: "AuthLogoutResult",
			DryRun:       "unsupported",
		},
		run: func(ctx context.Context, env *commandContext) error {
			token := env.settings.Token
			if token == "" {
				return &CredentialsError{
					Message: "no credentials configured",
					Hint:    "logout needs the credential to forget: pass --token, set AGCTL_TOKEN, or use a profile",
				}
			}
			result := authLogoutResult{Note: authLogoutNote}
			if !localOnly {
				client, err := env.client()
				if err != nil {
					return err
				}
				err = client.postJSON(ctx, "/auth/logout", nil, nil)
				var api *APIError
				switch {
				case err == nil:
					result.ServerRevoked = true
				case errors.As(err, &api) && api.HTTPStatus == http.StatusUnauthorized:
					result.AlreadyInvalid = true
				default:
					return err
				}
			}
			cleared, err := clearStoredToken(env, token)
			if err != nil {
				return err
			}
			result.ProfileCleared = cleared
			return env.render(result, nil, func(w io.Writer) error {
				lines := []string{}
				switch {
				case result.ServerRevoked:
					lines = append(lines, "the server revoked the session for this credential")
				case result.AlreadyInvalid:
					lines = append(lines, "the server reported the credential as already invalid")
				default:
					lines = append(lines, "the server session was not revoked (--local-only)")
				}
				if result.ProfileCleared != "" {
					lines = append(lines, fmt.Sprintf("stored token removed from profile %q", result.ProfileCleared))
				} else {
					lines = append(lines, "no stored profile token matched; tokens from --token or AGCTL_TOKEN are not removed")
				}
				lines = append(lines, result.Note)
				for _, line := range lines {
					if _, err := fmt.Fprintln(w, line); err != nil {
						return err
					}
				}
				return nil
			})
		},
	}
}

type whoamiRepositories struct {
	Count     int      `json:"count"`
	Names     []string `json:"names"`
	Truncated bool     `json:"truncated"`
}

type whoamiResult struct {
	Actor         string             `json:"actor"`
	Kind          string             `json:"kind"`
	Role          string             `json:"role,omitempty"`
	Administrator bool               `json:"administrator"`
	OIDC          bool               `json:"oidc"`
	Repositories  whoamiRepositories `json:"repositories"`
}

func authWhoamiCommand() command {
	return command{
		name:     "auth whoami",
		synopsis: "show the caller identity and the repositories it can read",
		usage:    "agctl auth whoami [--json]",
		meta: commandMeta{
			Resource:  "auth",
			Scenarios: []string{"ci", "admin"},
			Stage:     "m1",
			Endpoints: []string{
				"GET " + managementPrefix + "/identity",
				"GET " + managementPrefix + "/repositories",
			},
			MinimumRole:  "authenticated",
			OutputSchema: "WhoamiResult",
			DryRun:       "unsupported",
			Retry:        true,
		},
		run: func(ctx context.Context, env *commandContext) error {
			if env.settings.Token == "" {
				return &CredentialsError{
					Message: "no credentials configured",
					Hint:    "pass --token, set AGCTL_TOKEN, or store a session token with agctl auth login",
				}
			}
			client, err := env.client()
			if err != nil {
				return err
			}
			var identity adminopenapi.CurrentIdentity
			if err := client.getJSON(ctx, managementPrefix+"/identity", nil, &identity); err != nil {
				return err
			}
			var page struct {
				Items []struct {
					Name string `json:"name"`
				} `json:"items"`
				NextPageToken string `json:"nextPageToken"`
			}
			if err := client.getJSON(ctx, managementPrefix+"/repositories", url.Values{"pageSize": {"200"}}, &page); err != nil {
				return err
			}
			result := whoamiResult{
				Actor:         identity.Actor,
				Kind:          string(identity.Kind),
				Administrator: identity.Administrator,
				OIDC:          identity.Oidc != nil,
			}
			if identity.Role != nil {
				result.Role = string(*identity.Role)
			}
			result.Repositories.Count = len(page.Items)
			result.Repositories.Truncated = page.NextPageToken != ""
			result.Repositories.Names = []string{}
			for _, item := range page.Items {
				if len(result.Repositories.Names) == repositorySample {
					result.Repositories.Truncated = true
					break
				}
				result.Repositories.Names = append(result.Repositories.Names, item.Name)
			}
			return env.render(result, nil, func(w io.Writer) error {
				details := []string{}
				if result.Kind != "" {
					details = append(details, "kind="+result.Kind)
				}
				if result.Role != "" {
					details = append(details, "role="+result.Role)
				}
				details = append(details, fmt.Sprintf("administrator=%t", result.Administrator))
				if result.OIDC {
					details = append(details, "oidc=true")
				}
				if _, err := fmt.Fprintf(w, "identity: %s (%s)\n", result.Actor, strings.Join(details, ", ")); err != nil {
					return err
				}
				count := fmt.Sprintf("%d visible", result.Repositories.Count)
				if result.Repositories.Truncated {
					count = fmt.Sprintf("at least %d visible", result.Repositories.Count)
				}
				_, err := fmt.Fprintf(w, "repositories: %s: %s\n", count, strings.Join(result.Repositories.Names, ", "))
				return err
			})
		},
	}
}

type authChangePasswordResult struct {
	ProfileCleared  string `json:"profileCleared,omitempty"`
	ReLoginRequired bool   `json:"reLoginRequired"`
	Note            string `json:"note"`
}

func authChangePasswordCommand() command {
	var passwordStdin bool
	return command{
		name:     "auth change-password",
		synopsis: "change the caller's own password using a local user session",
		usage:    "agctl auth change-password --password-stdin",
		setFlags: func(fs *flag.FlagSet) {
			fs.BoolVar(&passwordStdin, "password-stdin", false, "read the current and new passwords from stdin, one per line (bounded, never echoed)")
		},
		meta: commandMeta{
			Resource:     "auth",
			Scenarios:    []string{"admin"},
			Stage:        "m1",
			Endpoints:    []string{"POST /auth/change-password"},
			Mutating:     true,
			MinimumRole:  "authenticated",
			InputSchema:  "ChangePasswordInput",
			OutputSchema: "AuthChangePasswordResult",
			DryRun:       "unsupported",
		},
		run: func(ctx context.Context, env *commandContext) error {
			if env.settings.Token == "" {
				return &CredentialsError{
					Message: "no credentials configured",
					Hint:    "change-password needs a local user session; sign in first with agctl auth login",
				}
			}
			if !passwordStdin {
				return &UsageError{
					Message: "auth change-password requires --password-stdin",
					Hint:    `pipe both lines in: printf '%s\n%s\n' "$CURRENT" "$NEW" | agctl auth change-password --password-stdin`,
				}
			}
			current, updated, err := readPasswordPair(env.stdin, secretPairLimit)
			if err != nil {
				return err
			}
			env.addKnownSecret(current)
			env.addKnownSecret(updated)

			client, err := env.client()
			if err != nil {
				return err
			}
			if err := client.postJSON(ctx, "/auth/change-password", map[string]string{
				"currentPassword": current,
				"newPassword":     updated,
			}, nil); err != nil {
				return err
			}
			cleared, err := clearStoredToken(env, env.settings.Token)
			if err != nil {
				return err
			}
			result := authChangePasswordResult{
				ProfileCleared:  cleared,
				ReLoginRequired: true,
				Note:            "the previous session token is no longer valid; run `agctl auth login` again",
			}
			return env.render(result, nil, func(w io.Writer) error {
				if _, err := fmt.Fprintln(w, "password changed"); err != nil {
					return err
				}
				if result.ProfileCleared != "" {
					if _, err := fmt.Fprintf(w, "stored token removed from profile %q\n", result.ProfileCleared); err != nil {
						return err
					}
				}
				_, err := fmt.Fprintln(w, result.Note)
				return err
			})
		},
	}
}
