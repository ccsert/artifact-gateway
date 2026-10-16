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

	adminopenapi "github.com/ccsert/artifact-gateway/internal/admin/openapi"
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

// explicitProfile returns the profile a login must store its token in and its
// stored settings. Only a flag or the environment select it explicitly; until
// the secure secret-output destination lands, an implicit profile is refused
// before any request.
func explicitProfile(env *commandContext) (string, profileConfig, error) {
	name := ""
	switch {
	case env.flagSet["profile"] && env.globals.profile != "":
		name = env.globals.profile
	case os.Getenv("AGCTL_PROFILE") != "":
		name = os.Getenv("AGCTL_PROFILE")
	}
	if name == "" {
		return "", profileConfig{}, &UsageError{
			Message: "auth login needs an explicit profile to store the session token",
			Hint:    "pass --profile NAME (or set AGCTL_PROFILE); a secure --secret-output destination is not available yet",
		}
	}
	config, err := storedProfiles(env.configPath)
	if err != nil {
		return "", profileConfig{}, err
	}
	profile, ok := config.Profiles[name]
	if !ok {
		return "", profileConfig{}, &UsageError{
			Message: fmt.Sprintf("profile %q does not exist", name),
			Hint:    fmt.Sprintf("create it first: agctl config profile add %s --server %s", name, env.settings.Server),
		}
	}
	return name, profile, nil
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
	// Profile names where the token was stored; with --secret-output it is
	// absent and SecretOutput says where the token went.
	Profile            string          `json:"profile,omitempty"`
	Server             string          `json:"server"`
	Username           string          `json:"username"`
	DisplayName        string          `json:"displayName,omitempty"`
	Role               string          `json:"role,omitempty"`
	MustChangePassword bool            `json:"mustChangePassword"`
	SecretOutput       *secretDelivery `json:"secretOutput,omitempty"`
}

func authLoginCommand() command {
	var username, secretOutput string
	var passwordStdin bool
	return command{
		name:     "auth login",
		synopsis: "authenticate with a username and password and store the session token in a profile or a private file",
		usage:    "agctl auth login --username NAME (--profile NAME | --secret-output FILE) --password-stdin",
		setFlags: func(fs *flag.FlagSet) {
			fs.StringVar(&username, "username", "", "local username to authenticate")
			fs.BoolVar(&passwordStdin, "password-stdin", false, "read the password from stdin (bounded, never echoed)")
			fs.StringVar(&secretOutput, "secret-output", "", "write the session token to this new private file instead of a profile (mode 0600; never overwritten)")
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
			if env.flagSet["secret-output"] {
				if env.flagSet["profile"] {
					return &UsageError{Message: "choose one of --profile or --secret-output for the session token", Hint: "--profile stores it in the config file; --secret-output writes it to a new private file"}
				}
				return loginToFile(ctx, env, username, passwordStdin, secretOutput)
			}
			profile, stored, err := explicitProfile(env)
			if err != nil {
				return err
			}
			// A session token must never land in a profile that targets a
			// different server: later invocations would send it elsewhere.
			target, err := normalizeServerTarget(env.settings.Server)
			if err != nil {
				return err
			}
			if stored.Server != "" {
				bound, err := normalizeServerTarget(stored.Server)
				if err != nil || bound != target {
					return &UsageError{
						Message: fmt.Sprintf("profile %q targets %s but this login targets %s", profile, redactServerURL(stored.Server, stored.Token), redactServerURL(env.settings.Server, env.settings.Token)),
						Hint:    "update the profile first, or log in to the profile's own target",
					}
				}
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
				// The profile may have been edited while the login request was
				// in flight; refuse instead of binding the token to a target
				// that changed under it.
				if profileConfig.Server != "" {
					storedTarget, err := normalizeServerTarget(profileConfig.Server)
					if err != nil || storedTarget != target {
						return &UsageError{
							Message: fmt.Sprintf("profile %q changed target while login was in flight; the session token was not stored", profile),
							Hint:    fmt.Sprintf("re-run agctl auth login for the current target of profile %s", profile),
						}
					}
				}
				profileConfig.Token = login.Token
				if profileConfig.Server == "" {
					profileConfig.Server = env.settings.Server
				}
				config.Profiles[profile] = profileConfig
				return nil
			}); err != nil {
				return err
			}
			result := authLoginResult{
				Profile:            profile,
				Server:             redactServerURL(env.settings.Server, env.settings.Token, login.Token, password),
				Username:           login.Username,
				DisplayName:        login.DisplayName,
				Role:               login.Role,
				MustChangePassword: login.MustChangePassword,
			}
			return env.render(result, nil, func(w io.Writer) error {
				if _, err := fmt.Fprintf(w, "authenticated as %s against %s; session token stored in profile %q\n", result.Username, result.Server, result.Profile); err != nil {
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

// readLoginInput validates the username and reads the password; it runs
// before any request.
func readLoginInput(env *commandContext, username string, passwordStdin bool) (string, error) {
	if strings.TrimSpace(username) == "" {
		return "", &UsageError{Message: "auth login requires --username", Hint: "pass --username NAME"}
	}
	hint := `pipe it in, for example: printf '%s\n' "$AGCTL_LOGIN_PASSWORD" | agctl auth login --username NAME --secret-output FILE --password-stdin`
	if !passwordStdin {
		return "", &UsageError{Message: "auth login requires --password-stdin", Hint: hint}
	}
	password, err := readBoundedSecret(env.stdin, secretInputLimit, "password", hint)
	if err != nil {
		return "", err
	}
	env.addKnownSecret(password)
	return password, nil
}

// loginToFile signs in and writes the session token to a new private file,
// with the same rules as every one-time secret: the destination is checked
// before the request, and the token never reaches any output.
func loginToFile(ctx context.Context, env *commandContext, username string, passwordStdin bool, output string) error {
	password, err := readLoginInput(env, username, passwordStdin)
	if err != nil {
		return err
	}
	destination, err := prepareSecretDestination(output)
	if err != nil {
		return err
	}
	client, err := env.client()
	if err != nil {
		destination.discard()
		return err
	}
	var login struct {
		Token              string `json:"token"`
		Username           string `json:"username"`
		DisplayName        string `json:"displayName"`
		Role               string `json:"role"`
		MustChangePassword bool   `json:"mustChangePassword"`
	}
	if err := client.postJSON(ctx, "/auth/login", map[string]string{"username": strings.TrimSpace(username), "password": password}, &login); err != nil {
		destination.discard()
		return err
	}
	if login.Token == "" || login.Username == "" {
		destination.discard()
		return &APIError{Code: "invalid_response", HTTPStatus: http.StatusOK, Message: "the server returned no session token"}
	}
	env.addKnownSecret(login.Token)
	result := authLoginResult{
		Server:             redactServerURL(env.settings.Server, env.settings.Token, login.Token, password),
		Username:           login.Username,
		DisplayName:        login.DisplayName,
		Role:               login.Role,
		MustChangePassword: login.MustChangePassword,
		SecretOutput:       &secretDelivery{Path: destination.path, Mode: "0600", Delivered: true},
	}
	if err := destination.deliver(login.Token); err != nil {
		result.SecretOutput.Delivered = false
		return &resultError{
			err:  &deliveryError{message: err.Error()},
			data: result,
			hint: "the session was created but its token was not saved; an administrator can end it with agctl user sessions revoke, or it expires on its own",
		}
	}
	return env.render(result, nil, func(w io.Writer) error {
		if _, err := fmt.Fprintf(w, "authenticated as %s against %s; session token written to %s (mode 0600) and not shown\n", result.Username, result.Server, destination.path); err != nil {
			return err
		}
		if result.MustChangePassword {
			_, err := fmt.Fprintln(w, "password change required: run `agctl auth change-password` with this token and sign in again")
			return err
		}
		return nil
	})
}

type authLogoutResult struct {
	ServerLogoutRequested bool `json:"serverLogoutRequested"`
	AlreadyInvalid        bool `json:"alreadyInvalid,omitempty"`
	// CredentialInvalidated is null when the state could not be confirmed
	// (--local-only or an unreachable probe after the logout request).
	CredentialInvalidated *bool  `json:"credentialInvalidated"`
	ProfileCleared        string `json:"profileCleared,omitempty"`
	Note                  string `json:"note"`
}

const authLogoutNote = "the Gateway revokes a local user session; service account tokens, API keys, and static administrator tokens are revoked from their own management surfaces, never by this command"

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
					result.ServerLogoutRequested = true
					// The endpoint answers 204 for every credential kind but
					// only revokes local user sessions, so the state is probed
					// instead of assumed: a still-valid credential must not be
					// reported as revoked.
					var session struct {
						Authenticated bool `json:"authenticated"`
					}
					if probeErr := client.getJSON(ctx, "/auth/session", nil, &session); probeErr == nil {
						invalidated := !session.Authenticated
						result.CredentialInvalidated = &invalidated
					}
				case errors.As(err, &api) && api.HTTPStatus == http.StatusUnauthorized:
					result.AlreadyInvalid = true
					invalidated := true
					result.CredentialInvalidated = &invalidated
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
				case result.ServerLogoutRequested:
					lines = append(lines, "the server accepted the logout request")
				case result.AlreadyInvalid:
					lines = append(lines, "the server reported the credential as already invalid")
				default:
					lines = append(lines, "the server session was not contacted (--local-only)")
				}
				switch {
				case result.CredentialInvalidated == nil:
					if !localOnly {
						lines = append(lines, "the credential state could not be confirmed")
					}
				case !*result.CredentialInvalidated:
					lines = append(lines, "this credential still authenticates — it is a machine credential or static token; revoke it from its own management surface")
				case result.AlreadyInvalid:
					// covered by the server line above
				default:
					lines = append(lines, "the credential no longer authenticates")
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
