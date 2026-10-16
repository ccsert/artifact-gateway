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
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

const (
	usersPath           = managementPrefix + "/users"
	minPasswordRunes    = 8
	maxPasswordBytes    = 72
	passwordPlaceholder = "<read from --password-stdin or --password-file; never shown>"
)

func userPath(id string) string {
	return usersPath + "/" + url.PathEscape(id)
}

// passwordInput reads a password from bounded stdin or a private file.
// Exactly one source is required; the password is registered for redaction
// and never appears in output, plans, or errors.
type passwordInput struct {
	stdin bool
	file  string
}

func (p *passwordInput) register(fs *flag.FlagSet) {
	fs.BoolVar(&p.stdin, "password-stdin", false, "read the password from stdin (bounded, never echoed)")
	fs.StringVar(&p.file, "password-file", "", "read the password from this file (first line)")
}

func (p passwordInput) read(env *commandContext) (string, error) {
	hint := `pipe it in, for example: printf '%s\n' "$NEW_PASSWORD" | agctl ... --password-stdin, or pass --password-file FILE`
	if p.stdin == (p.file != "") {
		return "", &UsageError{Message: "pass exactly one of --password-stdin or --password-file", Hint: hint}
	}
	var password string
	if p.stdin {
		value, err := readBoundedSecret(env.stdin, secretInputLimit, "password", hint)
		if err != nil {
			return "", err
		}
		password = value
	} else {
		info, err := os.Stat(p.file)
		if err != nil || !info.Mode().IsRegular() || info.Size() > secretInputLimit {
			return "", &UsageError{Message: fmt.Sprintf("--password-file %s is not a readable file of at most %d bytes", p.file, secretInputLimit), Hint: hint}
		}
		file, err := os.Open(p.file)
		if err != nil {
			return "", &UsageError{Message: fmt.Sprintf("cannot read --password-file %s", p.file), Hint: hint}
		}
		defer func() { _ = file.Close() }()
		info, err = file.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || info.Size() > secretInputLimit {
			return "", &UsageError{Message: "--password-file must be a private regular file of at most 4096 bytes", Hint: "use chmod 600 on the password file, or pass --password-stdin"}
		}
		data, err := io.ReadAll(io.LimitReader(file, int64(secretInputLimit)+1))
		if err != nil || len(data) > secretInputLimit {
			return "", &UsageError{Message: "cannot read a password file of at most 4096 bytes", Hint: hint}
		}
		password, _, _ = strings.Cut(string(data), "\n")
		password = strings.TrimRight(password, "\r")
	}
	env.addKnownSecret(password)
	if utf8.RuneCountInString(password) < minPasswordRunes || len(password) > maxPasswordBytes {
		return "", &UsageError{Message: "the password must have at least 8 characters and at most 72 bytes", Hint: "choose another password"}
	}
	return password, nil
}

func renderUsers(w io.Writer, users []adminopenapi.User) error {
	if len(users) == 0 {
		return emptyLine(w, "users")
	}
	table := [][]string{{"NAME", "ROLE", "STATE", "LAST LOGIN", "VERSION", "ID"}}
	for _, user := range users {
		table = append(table, []string{user.Name, string(user.Role), string(user.State), formatTime(user.LastLoginAt), user.Version, user.Id.String()})
	}
	return writeColumns(w, table)
}

func renderUser(w io.Writer, user adminopenapi.User) error {
	locked := ""
	if user.LockedUntil != nil && user.LockedUntil.After(time.Now()) {
		locked = "until " + formatTime(user.LockedUntil)
	}
	return keyValues(w, [][2]string{
		{"id", user.Id.String()},
		{"principal", "user:" + user.Name},
		{"name", user.Name},
		{"display name", user.DisplayName},
		{"email", user.Email},
		{"description", user.Description},
		{"role", string(user.Role)},
		{"state", string(user.State)},
		{"local password", yesNo(user.LocalPasswordEnabled)},
		{"must change password", yesNo(user.MustChangePassword)},
		{"locked", locked},
		{"last login", formatTime(user.LastLoginAt)},
		{"version", user.Version},
	})
}

func completeUser(user adminopenapi.User) bool {
	return user.Id != (openapi_types.UUID{}) && user.Name != "" && user.Role != "" && user.State != "" && user.Version != ""
}

func validUserRole(role string) bool { return role == "none" || role == "member" || role == "admin" }

func readUser(ctx context.Context, client *apiClient, id string) (adminopenapi.User, error) {
	var user adminopenapi.User
	err := client.getJSON(ctx, userPath(id), nil, &user)
	return user, err
}

// sendUserWrite sends a write that answers with the user and requires a
// complete user before reporting success.
func sendUserWrite(ctx context.Context, client *apiClient, method, path string, options requestOptions) (adminopenapi.User, error) {
	var user adminopenapi.User
	response, err := client.sendJSON(ctx, method, path, options, &user)
	if err == nil && !completeUser(user) {
		err = incompleteResponse(response, client, "user")
	}
	return user, err
}

func userConflictHint(err error, ifMatch, id string) {
	var api *APIError
	if errors.As(err, &api) && api.HTTPStatus == http.StatusPreconditionFailed {
		api.Hint = fmt.Sprintf("the user changed since version %s and was not updated; run agctl user show %s and retry", ifMatch, id)
	}
}

func userListCommand() command {
	var search, role, state string
	var limit, offset int
	return command{
		name:     "user list",
		synopsis: "list local users",
		usage:    "agctl user list [--search TEXT] [--role none|member|admin] [--state active|disabled] [--limit N] [--offset N] [--json]",
		setFlags: func(fs *flag.FlagSet) {
			fs.StringVar(&search, "search", "", "match name, display name, or email")
			fs.StringVar(&role, "role", "", "only this global role")
			fs.StringVar(&state, "state", "", "only active or disabled users")
			fs.IntVar(&limit, "limit", defaultPageSize, "users per window (1-200)")
			fs.IntVar(&offset, "offset", 0, "users to skip")
		},
		meta: commandMeta{
			Resource: "user", Scenarios: []string{"admin"}, Stage: "m1",
			Endpoints: []string{"GET " + usersPath}, MinimumRole: "platform admin",
			OutputSchema: "UserList", Pagination: true, DryRun: "unsupported", Retry: true,
		},
		run: func(ctx context.Context, env *commandContext) error {
			if role != "" && !validUserRole(role) {
				return &UsageError{Message: fmt.Sprintf("invalid --role %q", role), Hint: "use none, member, or admin"}
			}
			if state != "" && state != "active" && state != "disabled" {
				return &UsageError{Message: fmt.Sprintf("invalid --state %q", state), Hint: "use active or disabled"}
			}
			if limit < 1 || limit > maxPageSize || offset < 0 {
				return &UsageError{Message: "--limit must be 1-200 and --offset must not be negative", Hint: "page with --offset"}
			}
			query := url.Values{"limit": {strconv.Itoa(limit)}, "offset": {strconv.Itoa(offset)}}
			for name, value := range map[string]string{"search": search, "role": role, "state": state} {
				if value != "" {
					query.Set(name, value)
				}
			}
			client, err := env.client()
			if err != nil {
				return err
			}
			var page adminopenapi.UserList
			if err := client.getJSON(ctx, usersPath, query, &page); err != nil {
				return err
			}
			total, start := page.Total, page.Offset
			complete := page.Offset+len(page.Items) >= page.Total
			meta := &envelopeMeta{Pagination: &paginationMeta{Mode: "offset", Limit: page.Limit, Offset: &start, Total: &total, Complete: &complete}}
			return env.render(newListPayload(page.Items), meta, func(w io.Writer) error {
				if err := renderUsers(w, page.Items); err != nil {
					return err
				}
				if !complete {
					_, _ = fmt.Fprintf(env.stderr, "users %d-%d of %d; continue with --offset %d\n", start+1, start+len(page.Items), total, start+len(page.Items))
				}
				return nil
			})
		},
	}
}

func userShowCommand() command {
	return command{
		name:        "user show",
		synopsis:    "show one user",
		usage:       "agctl user show ID [--json]",
		positionals: 1,
		argsUsage:   "ID",
		meta: commandMeta{
			Resource: "user", Scenarios: []string{"admin"}, Stage: "m1",
			Endpoints: []string{"GET " + usersPath + "/{userId}"}, MinimumRole: "platform admin",
			OutputSchema: "User", DryRun: "unsupported", Retry: true,
		},
		run: documentCommand[adminopenapi.User]{
			request: func(env *commandContext) (string, url.Values, error) { return userPath(env.args[0]), nil, nil },
			render:  renderUser,
		}.run,
	}
}

type userProfileFlags struct {
	displayName, email, description string
}

func (f *userProfileFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&f.displayName, "display-name", "", "display name (at most 128 characters)")
	fs.StringVar(&f.email, "email", "", "email address (at most 254 characters)")
	fs.StringVar(&f.description, "description", "", "description (at most 512 characters)")
}

// values returns the profile fields that were set, after local validation.
func (f userProfileFlags) values(env *commandContext) (displayName, email, description *string, err error) {
	limits := []struct {
		name  string
		value string
		max   int
		out   **string
	}{
		{"display-name", f.displayName, 128, &displayName},
		{"email", f.email, 254, &email},
		{"description", f.description, 512, &description},
	}
	for _, field := range limits {
		if !env.flagSet[field.name] {
			continue
		}
		trimmed := strings.TrimSpace(field.value)
		if utf8.RuneCountInString(trimmed) > field.max {
			return nil, nil, nil, &UsageError{Message: fmt.Sprintf("--%s must be at most %d characters", field.name, field.max), Hint: "shorten it"}
		}
		if field.name == "email" && trimmed != "" && (!strings.Contains(trimmed, "@") || strings.ContainsAny(trimmed, " \t\r\n")) {
			return nil, nil, nil, &UsageError{Message: fmt.Sprintf("--email %q is not an email address", trimmed), Hint: "for example --email alice@example.com"}
		}
		*field.out = &trimmed
	}
	return displayName, email, description, nil
}

func userCreateCommand() command {
	var role string
	var mustChange bool
	var profile userProfileFlags
	var password passwordInput
	return command{
		name:        "user create",
		synopsis:    "create a local user with an initial password",
		usage:       "agctl user create NAME --role none|member|admin (--password-stdin | --password-file FILE) [--must-change-password] [--display-name TEXT] [--email ADDRESS] [--description TEXT] [--dry-run] [--json]",
		positionals: 1,
		argsUsage:   "NAME",
		setFlags: func(fs *flag.FlagSet) {
			fs.StringVar(&role, "role", "", "global role: none (pending), member, or admin; member grants no repository access by itself")
			fs.BoolVar(&mustChange, "must-change-password", false, "require a password change at first sign-in")
			profile.register(fs)
			password.register(fs)
		},
		meta: commandMeta{
			Resource: "user", Scenarios: []string{"admin"}, Stage: "m1",
			Endpoints: []string{"POST " + usersPath}, Mutating: true, MinimumRole: "platform admin",
			InputSchema: "CreateUser", OutputSchema: "User", DryRun: "client-plan",
		},
		run: func(ctx context.Context, env *commandContext) error {
			name := strings.TrimSpace(env.args[0])
			if name == "" || utf8.RuneCountInString(name) > 128 {
				return &UsageError{Message: "the user name must be 1-128 characters", Hint: "for example agctl user create alice --role member --password-stdin"}
			}
			if !validUserRole(role) {
				return &UsageError{Message: "--role must be none, member, or admin", Hint: "none leaves the account pending; member grants no repository access by itself"}
			}
			displayName, email, description, err := profile.values(env)
			if err != nil {
				return err
			}
			secret, err := password.read(env)
			if err != nil {
				return err
			}
			body := adminopenapi.CreateUser{Name: name, Password: secret, Role: adminopenapi.CreateUserRole(role), DisplayName: displayName, Description: description}
			if email != nil {
				value := openapi_types.Email(*email)
				body.Email = &value
			}
			if env.flagSet["must-change-password"] {
				body.MustChangePassword = &mustChange
			}
			if env.globals.dryRun {
				shown := body
				shown.Password = passwordPlaceholder
				plan := writePlan{Mode: "client-plan", Method: http.MethodPost, Path: usersPath, Headers: map[string]string{}, Body: shown,
					Verified:    []string{"name, role, profile limits, and password length"},
					NotVerified: []string{"caller permission (platform admin)", "name uniqueness"}}
				return env.render(plan, nil, func(w io.Writer) error { return renderPlan(w, plan) })
			}
			client, err := env.client()
			if err != nil {
				return err
			}
			encoded, err := encodeBody(body)
			if err != nil {
				return err
			}
			// Not retried: there is no idempotency key, and a repeat after a lost
			// response answers 409 for the user it created.
			user, err := sendUserWrite(ctx, client, http.MethodPost, usersPath, requestOptions{body: encoded})
			if err != nil {
				if outcomeUnknown(err) {
					return unknownOutcomeError(err, unknownOutcome{Operation: "user create", Name: name, ReadBack: "agctl user list --search " + name},
						fmt.Sprintf("the user may have been created; look for %q with agctl user list --search before creating it again", name))
				}
				return err
			}
			return env.render(user, nil, func(w io.Writer) error { return renderUser(w, user) })
		},
	}
}

func userUpdateCommand() command {
	var role, state, ifMatch string
	var profile userProfileFlags
	return command{
		name:        "user update",
		synopsis:    "change a user's profile, role, or state (conditional on its version)",
		usage:       "agctl user update ID [--role none|member|admin] [--state active|disabled] [--display-name TEXT] [--email ADDRESS] [--description TEXT] [--if-match VERSION] [--dry-run] [--json]",
		positionals: 1,
		argsUsage:   "ID",
		setFlags: func(fs *flag.FlagSet) {
			fs.StringVar(&role, "role", "", "new global role")
			fs.StringVar(&state, "state", "", "active or disabled; disabling ends the user's sessions")
			fs.StringVar(&ifMatch, "if-match", "", "apply only if the user is still at this version (default: the version read just before)")
			profile.register(fs)
		},
		meta: commandMeta{
			Resource: "user", Scenarios: []string{"admin"}, Stage: "m1",
			Endpoints: []string{"PATCH " + usersPath + "/{userId}", "GET " + usersPath + "/{userId}"}, Mutating: true,
			MinimumRole: "platform admin", InputSchema: "UpdateUser", OutputSchema: "User", DryRun: "client-plan",
		},
		run: func(ctx context.Context, env *commandContext) error {
			id := env.args[0]
			displayName, email, description, err := profile.values(env)
			if err != nil {
				return err
			}
			body := adminopenapi.UpdateUser{DisplayName: displayName, Description: description}
			if email != nil {
				value := openapi_types.Email(*email)
				body.Email = &value
			}
			if env.flagSet["role"] {
				if !validUserRole(role) {
					return &UsageError{Message: "--role must be none, member, or admin", Hint: "member grants no repository access by itself"}
				}
				value := adminopenapi.UpdateUserRole(role)
				body.Role = &value
			}
			if env.flagSet["state"] {
				if state != "active" && state != "disabled" {
					return &UsageError{Message: "--state must be active or disabled", Hint: "disabling ends the user's sessions"}
				}
				value := adminopenapi.UpdateUserState(state)
				body.State = &value
			}
			if body.Role == nil && body.State == nil && displayName == nil && email == nil && description == nil {
				return &UsageError{Message: "nothing to update", Hint: "pass --role, --state, --display-name, --email, or --description"}
			}
			if env.flagSet["if-match"] && strings.TrimSpace(ifMatch) == "" {
				return &UsageError{Message: "--if-match cannot be empty", Hint: "pass the version from agctl user show"}
			}
			client, err := env.client()
			if err != nil {
				return err
			}
			var current *adminopenapi.User
			if ifMatch == "" {
				user, err := readUser(ctx, client, id)
				if err != nil {
					return err
				}
				current = &user
				ifMatch = user.Version
			}
			if env.globals.dryRun {
				plan := writePlan{Mode: "client-plan", Method: http.MethodPatch, Path: userPath(id), Headers: map[string]string{"If-Match": ifMatch}, Body: body,
					Verified:    []string{"at least one change is given, within the field limits"},
					NotVerified: []string{"caller permission (platform admin)", "that the last active administrator keeps the admin role", "that the version still matches"}}
				if current != nil {
					plan.Current = current
				}
				return env.render(plan, nil, func(w io.Writer) error { return renderPlan(w, plan) })
			}
			encoded, err := encodeBody(body)
			if err != nil {
				return err
			}
			// Conditional and therefore not retried.
			user, err := sendUserWrite(ctx, client, http.MethodPatch, userPath(id), requestOptions{body: encoded, header: http.Header{"If-Match": {ifMatch}}})
			if err != nil {
				userConflictHint(err, ifMatch, id)
				if outcomeUnknown(err) {
					return unknownOutcomeError(err, unknownOutcome{Operation: "user update", ID: id, ReadBack: "agctl user show " + id},
						fmt.Sprintf("the update may have been applied; run agctl user show %s and compare its version with %s", id, ifMatch))
				}
				return err
			}
			return env.render(user, nil, func(w io.Writer) error { return renderUser(w, user) })
		},
	}
}

func userDeleteCommand() command {
	return command{
		name:        "user delete",
		synopsis:    "delete a local user",
		usage:       "agctl user delete ID --yes [--dry-run] [--json]",
		positionals: 1,
		argsUsage:   "ID",
		meta: commandMeta{
			Resource: "user", Scenarios: []string{"admin"}, Stage: "m1",
			Endpoints: []string{"DELETE " + usersPath + "/{userId}", "GET " + usersPath + "/{userId}"}, Mutating: true,
			MinimumRole: "platform admin", OutputSchema: "UserDeletion", DryRun: "client-plan",
		},
		run: func(ctx context.Context, env *commandContext) error {
			id := env.args[0]
			if !env.globals.dryRun && !env.globals.yes {
				return &UsageError{Message: "agctl user delete needs --yes to delete a user", Hint: "re-run with --yes, or with --dry-run to see what would happen; disabling with agctl user update --state disabled is reversible"}
			}
			client, err := env.client()
			if err != nil {
				return err
			}
			if env.globals.dryRun {
				user, err := readUser(ctx, client, id)
				if err != nil {
					return err
				}
				plan := writePlan{Mode: "client-plan", Method: http.MethodDelete, Path: userPath(id), Headers: map[string]string{}, Current: user,
					Verified:    []string{"the user exists"},
					NotVerified: []string{"caller permission (platform admin)", "that the user is not the last active administrator"}}
				return env.render(plan, nil, func(w io.Writer) error { return renderPlan(w, plan) })
			}
			// Not retried: a repeat after a lost 204 answers 404.
			if _, err := client.sendJSON(ctx, http.MethodDelete, userPath(id), requestOptions{}, nil); err != nil {
				if outcomeUnknown(err) {
					return unknownOutcomeError(err, unknownOutcome{Operation: "user delete", ID: id, ReadBack: "agctl user show " + id},
						fmt.Sprintf("the user may have been deleted; agctl user show %s answers 404 (exit 4) once it is gone", id))
				}
				return err
			}
			result := map[string]any{"id": id, "deleted": true}
			return env.render(result, nil, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "user %s deleted\n", id)
				return err
			})
		},
	}
}

func userResetPasswordCommand() command {
	var ifMatch string
	var mustChange bool
	var password passwordInput
	return command{
		name:        "user reset-password",
		synopsis:    "set a user's password as an administrator (conditional on its version)",
		usage:       "agctl user reset-password ID (--password-stdin | --password-file FILE) [--must-change-password=true|false] [--if-match VERSION] [--dry-run] [--json]",
		positionals: 1,
		argsUsage:   "ID",
		setFlags: func(fs *flag.FlagSet) {
			fs.BoolVar(&mustChange, "must-change-password", false, "require a change at the next sign-in")
			fs.StringVar(&ifMatch, "if-match", "", "apply only if the user is still at this version (default: the version read just before)")
			password.register(fs)
		},
		meta: commandMeta{
			Resource: "user", Scenarios: []string{"admin"}, Stage: "m1",
			Endpoints: []string{"POST " + usersPath + "/{userId}/password", "GET " + usersPath + "/{userId}"}, Mutating: true,
			MinimumRole: "platform admin", InputSchema: "ResetUserPassword", OutputSchema: "User", DryRun: "client-plan",
		},
		run: func(ctx context.Context, env *commandContext) error {
			id := env.args[0]
			if env.flagSet["if-match"] && strings.TrimSpace(ifMatch) == "" {
				return &UsageError{Message: "--if-match cannot be empty", Hint: "pass the version from agctl user show"}
			}
			secret, err := password.read(env)
			if err != nil {
				return err
			}
			body := adminopenapi.ResetUserPassword{Password: secret}
			if env.flagSet["must-change-password"] {
				body.MustChangePassword = &mustChange
			}
			client, err := env.client()
			if err != nil {
				return err
			}
			if ifMatch == "" {
				user, err := readUser(ctx, client, id)
				if err != nil {
					return err
				}
				ifMatch = user.Version
			}
			path := userPath(id) + "/password"
			if env.globals.dryRun {
				shown := body
				shown.Password = passwordPlaceholder
				plan := writePlan{Mode: "client-plan", Method: http.MethodPost, Path: path, Headers: map[string]string{"If-Match": ifMatch}, Body: shown,
					Verified:    []string{"password length", "the user's current version was read"},
					NotVerified: []string{"caller permission (platform admin)", "that the version still matches"}}
				return env.render(plan, nil, func(w io.Writer) error { return renderPlan(w, plan) })
			}
			encoded, err := encodeBody(body)
			if err != nil {
				return err
			}
			// Conditional and therefore not retried.
			user, err := sendUserWrite(ctx, client, http.MethodPost, path, requestOptions{body: encoded, header: http.Header{"If-Match": {ifMatch}}})
			if err != nil {
				userConflictHint(err, ifMatch, id)
				if outcomeUnknown(err) {
					return unknownOutcomeError(err, unknownOutcome{Operation: "user reset-password", ID: id, ReadBack: "agctl user show " + id},
						fmt.Sprintf("the password may have been reset; compare passwordChangedAt and the version (%s before) in agctl user show %s", ifMatch, id))
				}
				return err
			}
			return env.render(user, nil, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "password of %s reset at %s (must change: %s)\n", user.Name, formatTime(user.PasswordChangedAt), yesNo(user.MustChangePassword))
				return err
			})
		},
	}
}

func renderSessions(w io.Writer, sessions []adminopenapi.UserSession) error {
	if len(sessions) == 0 {
		return emptyLine(w, "sessions")
	}
	table := [][]string{{"KIND", "STATE", "CREATED", "EXPIRES", "ADDRESS", "ID"}}
	for _, session := range sessions {
		state := "active"
		if session.RevokedAt != nil {
			state = "revoked"
		} else if !session.ExpiresAt.After(time.Now()) {
			state = "expired"
		}
		if session.Current {
			state += " (this request)"
		}
		created, expires := session.CreatedAt, session.ExpiresAt
		table = append(table, []string{string(session.Kind), state, formatTime(&created), formatTime(&expires), session.IpAddress, session.Id.String()})
	}
	return writeColumns(w, table)
}

func userSessionsListCommand() command {
	var inactive bool
	return command{
		name:        "user sessions list",
		synopsis:    "list a user's sign-in sessions",
		usage:       "agctl user sessions list ID [--include-inactive] [--json]",
		positionals: 1,
		argsUsage:   "ID",
		setFlags: func(fs *flag.FlagSet) {
			fs.BoolVar(&inactive, "include-inactive", false, "also list revoked and expired sessions")
		},
		meta: commandMeta{
			Resource: "user", Scenarios: []string{"admin"}, Stage: "m1",
			Endpoints: []string{"GET " + usersPath + "/{userId}/sessions"}, MinimumRole: "platform admin",
			OutputSchema: "UserSessionList", DryRun: "unsupported", Retry: true,
		},
		run: func(ctx context.Context, env *commandContext) error {
			client, err := env.client()
			if err != nil {
				return err
			}
			query := url.Values{}
			if inactive {
				query.Set("includeInactive", "true")
			}
			var page serverPage[adminopenapi.UserSession]
			if err := client.getJSON(ctx, userPath(env.args[0])+"/sessions", query, &page); err != nil {
				return err
			}
			meta := &envelopeMeta{Pagination: &paginationMeta{Mode: "none"}}
			return env.render(newListPayload(page.Items), meta, func(w io.Writer) error { return renderSessions(w, page.Items) })
		},
	}
}

func userSessionsRevokeCommand() command {
	var session, ifMatch string
	var all bool
	return command{
		name:        "user sessions revoke",
		synopsis:    "end one session of a user, or all of them",
		usage:       "agctl user sessions revoke ID (--session SESSION_ID | --all [--if-match VERSION]) --yes [--dry-run] [--json]",
		positionals: 1,
		argsUsage:   "ID",
		setFlags: func(fs *flag.FlagSet) {
			fs.StringVar(&session, "session", "", "the session to end")
			fs.BoolVar(&all, "all", false, "end every session of the user")
			fs.StringVar(&ifMatch, "if-match", "", "with --all: apply only if the user is still at this version (default: the version read just before)")
		},
		meta: commandMeta{
			Resource: "user", Scenarios: []string{"admin"}, Stage: "m1",
			Endpoints: []string{
				"DELETE " + usersPath + "/{userId}/sessions/{sessionId}",
				"POST " + usersPath + "/{userId}/sessions:revoke",
				"GET " + usersPath + "/{userId}",
			},
			Mutating: true, MinimumRole: "platform admin", OutputSchema: "UserSession | User", DryRun: "client-plan", Retry: true,
		},
		run: func(ctx context.Context, env *commandContext) error {
			id := env.args[0]
			if all == (session != "") {
				return &UsageError{Message: "choose one of --session SESSION_ID or --all", Hint: "agctl user sessions list ID shows the sessions"}
			}
			if env.flagSet["if-match"] && (!all || strings.TrimSpace(ifMatch) == "") {
				return &UsageError{Message: "--if-match needs --all and a version", Hint: "pass the version from agctl user show"}
			}
			if !env.globals.dryRun && !env.globals.yes {
				return &UsageError{Message: "ending sessions needs --yes; the user is signed out at once", Hint: "re-run with --yes, or with --dry-run to see the request"}
			}
			client, err := env.client()
			if err != nil {
				return err
			}
			if session != "" {
				path := userPath(id) + "/sessions/" + url.PathEscape(session)
				if env.globals.dryRun {
					plan := writePlan{Mode: "client-plan", Method: http.MethodDelete, Path: path, Headers: map[string]string{},
						NotVerified: []string{"caller permission (platform admin)", "that the session exists"}}
					return env.render(plan, nil, func(w io.Writer) error { return renderPlan(w, plan) })
				}
				// Ending a session records its end once and answers the same
				// session again, so the request is retried.
				var revoked adminopenapi.UserSession
				response, err := client.sendJSON(ctx, http.MethodDelete, path, requestOptions{idempotent: true}, &revoked)
				if err == nil && (revoked.RevokedAt == nil || !strings.EqualFold(nonZeroUUID(revoked.Id), session) || !strings.EqualFold(nonZeroUUID(revoked.UserId), id)) {
					err = incompleteResponse(response, client, "user session")
				}
				if err != nil {
					if outcomeUnknown(err) {
						return unknownOutcomeError(err, unknownOutcome{Operation: "user sessions revoke", ID: id, ReadBack: "agctl user sessions list " + id + " --include-inactive"},
							"the session may have been ended; check it with agctl user sessions list --include-inactive, or re-run the revoke, which is safe to repeat")
					}
					return err
				}
				return env.render(revoked, nil, func(w io.Writer) error {
					_, err := fmt.Fprintf(w, "session %s ended at %s\n", revoked.Id, formatTime(revoked.RevokedAt))
					return err
				})
			}
			if ifMatch == "" {
				user, err := readUser(ctx, client, id)
				if err != nil {
					return err
				}
				ifMatch = user.Version
			}
			path := userPath(id) + "/sessions:revoke"
			if env.globals.dryRun {
				plan := writePlan{Mode: "client-plan", Method: http.MethodPost, Path: path, Headers: map[string]string{"If-Match": ifMatch},
					Verified:    []string{"the user's current version was read"},
					NotVerified: []string{"caller permission (platform admin)", "that the version still matches"}}
				return env.render(plan, nil, func(w io.Writer) error { return renderPlan(w, plan) })
			}
			// Conditional and therefore not retried.
			user, err := sendUserWrite(ctx, client, http.MethodPost, path, requestOptions{header: http.Header{"If-Match": {ifMatch}}})
			if err != nil {
				userConflictHint(err, ifMatch, id)
				if outcomeUnknown(err) {
					return unknownOutcomeError(err, unknownOutcome{Operation: "user sessions revoke --all", ID: id, ReadBack: "agctl user sessions list " + id},
						fmt.Sprintf("the sessions may have been ended; check agctl user sessions list %s, whose version (%s before) changes when they are", id, ifMatch))
				}
				return err
			}
			return env.render(user, nil, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "every session of %s ended (version %s)\n", user.Name, user.Version)
				return err
			})
		},
	}
}

func renderUserIdentities(w io.Writer, identities []adminopenapi.UserIdentity) error {
	if len(identities) == 0 {
		return emptyLine(w, "linked identities")
	}
	table := [][]string{{"KIND", "ISSUER", "SUBJECT", "EMAIL", "LAST LOGIN", "ID"}}
	for _, identity := range identities {
		table = append(table, []string{string(identity.Kind), identity.Issuer, identity.Subject, identity.Email, formatTime(identity.LastLoginAt), identity.Id.String()})
	}
	return writeColumns(w, table)
}

func userIdentitiesListCommand() command {
	return command{
		name:        "user identities list",
		synopsis:    "list the external (OIDC) identities linked to a user",
		usage:       "agctl user identities list ID [--json]",
		positionals: 1,
		argsUsage:   "ID",
		meta: commandMeta{
			Resource: "user", Scenarios: []string{"admin"}, Stage: "m1",
			Endpoints: []string{"GET " + usersPath + "/{userId}/identities"}, MinimumRole: "platform admin",
			OutputSchema: "UserIdentityList", DryRun: "unsupported", Retry: true,
		},
		run: func(ctx context.Context, env *commandContext) error {
			client, err := env.client()
			if err != nil {
				return err
			}
			var page serverPage[adminopenapi.UserIdentity]
			if err := client.getJSON(ctx, userPath(env.args[0])+"/identities", nil, &page); err != nil {
				return err
			}
			meta := &envelopeMeta{Pagination: &paginationMeta{Mode: "none"}}
			return env.render(newListPayload(page.Items), meta, func(w io.Writer) error { return renderUserIdentities(w, page.Items) })
		},
	}
}

func userIdentitiesLinkCommand() command {
	var issuer, subject string
	return command{
		name:        "user identities link",
		synopsis:    "link an OIDC identity (issuer and subject) to a user",
		usage:       "agctl user identities link ID --issuer URL --subject SUBJECT [--dry-run] [--json]",
		positionals: 1,
		argsUsage:   "ID",
		setFlags: func(fs *flag.FlagSet) {
			fs.StringVar(&issuer, "issuer", "", "OIDC issuer; must be the configured issuer")
			fs.StringVar(&subject, "subject", "", "the identity's subject (sub claim)")
		},
		meta: commandMeta{
			Resource: "user", Scenarios: []string{"admin"}, Stage: "m1",
			Endpoints: []string{"POST " + usersPath + "/{userId}/identities"}, Mutating: true, MinimumRole: "platform admin",
			InputSchema: "CreateUserIdentity", OutputSchema: "UserIdentity", DryRun: "client-plan",
		},
		run: func(ctx context.Context, env *commandContext) error {
			id := env.args[0]
			if parsed, err := url.Parse(issuer); err != nil || !parsed.IsAbs() || parsed.Host == "" {
				return &UsageError{Message: "--issuer must be an absolute URL", Hint: "use the OIDC issuer configured on the Gateway"}
			}
			subject = strings.TrimSpace(subject)
			if subject == "" || utf8.RuneCountInString(subject) > 512 {
				return &UsageError{Message: "--subject must be 1-512 characters", Hint: "use the identity's sub claim"}
			}
			body := adminopenapi.CreateUserIdentity{Issuer: issuer, Subject: subject}
			path := userPath(id) + "/identities"
			if env.globals.dryRun {
				plan := writePlan{Mode: "client-plan", Method: http.MethodPost, Path: path, Headers: map[string]string{}, Body: body,
					Verified:    []string{"issuer is a URL and subject is set"},
					NotVerified: []string{"caller permission (platform admin)", "that OIDC is configured with this issuer", "that the identity is not linked to another user"}}
				return env.render(plan, nil, func(w io.Writer) error { return renderPlan(w, plan) })
			}
			client, err := env.client()
			if err != nil {
				return err
			}
			encoded, err := encodeBody(body)
			if err != nil {
				return err
			}
			// Not retried: a repeat after a lost response answers 409.
			var identity adminopenapi.UserIdentity
			response, err := client.sendJSON(ctx, http.MethodPost, path, requestOptions{body: encoded}, &identity)
			if err == nil && (identity.Id == (openapi_types.UUID{}) || identity.Subject == "") {
				err = incompleteResponse(response, client, "user identity")
			}
			if err != nil {
				if outcomeUnknown(err) {
					return unknownOutcomeError(err, unknownOutcome{Operation: "user identities link", ID: id, ReadBack: "agctl user identities list " + id},
						fmt.Sprintf("the identity may have been linked; check agctl user identities list %s before linking it again", id))
				}
				return err
			}
			return env.render(identity, nil, func(w io.Writer) error { return renderUserIdentities(w, []adminopenapi.UserIdentity{identity}) })
		},
	}
}

func userIdentitiesUnlinkCommand() command {
	var identity string
	return command{
		name:        "user identities unlink",
		synopsis:    "remove a linked OIDC identity from a user",
		usage:       "agctl user identities unlink ID --identity IDENTITY_ID --yes [--dry-run] [--json]",
		positionals: 1,
		argsUsage:   "ID",
		setFlags: func(fs *flag.FlagSet) {
			fs.StringVar(&identity, "identity", "", "the linked identity to remove")
		},
		meta: commandMeta{
			Resource: "user", Scenarios: []string{"admin"}, Stage: "m1",
			Endpoints: []string{"DELETE " + usersPath + "/{userId}/identities/{identityId}"}, Mutating: true, MinimumRole: "platform admin",
			OutputSchema: "IdentityUnlink", DryRun: "client-plan",
		},
		run: func(ctx context.Context, env *commandContext) error {
			id := env.args[0]
			if err := requireFlag("identity", identity, "agctl user identities list ID shows the linked identities"); err != nil {
				return err
			}
			if !env.globals.dryRun && !env.globals.yes {
				return &UsageError{Message: "unlinking an identity needs --yes; it can no longer sign in as this user", Hint: "re-run with --yes, or with --dry-run to see the request"}
			}
			path := userPath(id) + "/identities/" + url.PathEscape(identity)
			if env.globals.dryRun {
				plan := writePlan{Mode: "client-plan", Method: http.MethodDelete, Path: path, Headers: map[string]string{},
					NotVerified: []string{"caller permission (platform admin)", "that the identity is linked to this user"}}
				return env.render(plan, nil, func(w io.Writer) error { return renderPlan(w, plan) })
			}
			client, err := env.client()
			if err != nil {
				return err
			}
			// Not retried: a repeat after a lost 204 answers 404.
			if _, err := client.sendJSON(ctx, http.MethodDelete, path, requestOptions{}, nil); err != nil {
				if outcomeUnknown(err) {
					return unknownOutcomeError(err, unknownOutcome{Operation: "user identities unlink", ID: id, ReadBack: "agctl user identities list " + id},
						fmt.Sprintf("the identity may have been unlinked; check agctl user identities list %s", id))
				}
				return err
			}
			result := map[string]any{"userId": id, "identityId": identity, "unlinked": true}
			return env.render(result, nil, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "identity %s unlinked from user %s\n", identity, id)
				return err
			})
		},
	}
}
