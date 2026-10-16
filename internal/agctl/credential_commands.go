package agctl

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

const (
	serviceAccountsPath = managementPrefix + "/service-accounts"
	apiKeysPath         = managementPrefix + "/api-keys"
	maxCredentialTerm   = 365 * 24 * time.Hour
)

func serviceAccountPath(id string) string {
	return serviceAccountsPath + "/" + url.PathEscape(id)
}

func renderServiceAccounts(w io.Writer, accounts []adminopenapi.ServiceAccount) error {
	if len(accounts) == 0 {
		return emptyLine(w, "service accounts")
	}
	table := [][]string{{"NAME", "STATE", "VERSION", "PRINCIPAL"}}
	for _, account := range accounts {
		table = append(table, []string{account.Name, string(account.State), account.Version, "service-account:" + account.Id.String()})
	}
	return writeColumns(w, table)
}

func renderServiceAccount(w io.Writer, account adminopenapi.ServiceAccount) error {
	return keyValues(w, [][2]string{
		{"id", account.Id.String()},
		{"principal", "service-account:" + account.Id.String()},
		{"name", account.Name},
		{"description", account.Description},
		{"state", string(account.State)},
		{"version", account.Version},
		{"created", account.CreatedAt.UTC().Format(time.RFC3339)},
		{"updated", account.UpdatedAt.UTC().Format(time.RFC3339)},
	})
}

func serviceAccountListCommand() command {
	list := &cursorList[adminopenapi.ServiceAccount]{
		path:    serviceAccountsPath,
		command: "agctl service-account list",
		noun:    "service accounts",
		render:  renderServiceAccounts,
	}
	return command{
		name:     "service-account list",
		synopsis: "list service accounts (machine principals for CI)",
		usage:    "agctl service-account list [--page-size N] [--cursor TOKEN | --all [--max-pages N]] [--json]",
		setFlags: list.setFlags,
		meta: commandMeta{
			Resource: "service-account", Scenarios: []string{"ci", "admin"}, Stage: "m1",
			Endpoints: []string{"GET " + serviceAccountsPath}, MinimumRole: "platform admin",
			OutputSchema: "ServiceAccountList", Pagination: true, DryRun: "unsupported", Retry: true,
		},
		run: list.run,
	}
}

// findServiceAccount reads every page of the service account list, within
// --max-pages and the command's timeout, until it finds the ID. There is no
// endpoint for one account, so "not found" is only claimed after the whole
// list was read.
func findServiceAccount(ctx context.Context, client *apiClient, match func(adminopenapi.ServiceAccount) bool, maxPages int) (*adminopenapi.ServiceAccount, bool, error) {
	token := ""
	for pages := 0; pages < maxPages; pages++ {
		query := url.Values{"pageSize": {strconv.Itoa(maxPageSize)}}
		if token != "" {
			query.Set("pageToken", token)
		}
		var page serverPage[adminopenapi.ServiceAccount]
		if err := client.getJSON(ctx, serviceAccountsPath, query, &page); err != nil {
			return nil, false, err
		}
		for index := range page.Items {
			if match(page.Items[index]) {
				return &page.Items[index], true, nil
			}
		}
		if page.NextPageToken == nil || *page.NextPageToken == "" {
			return nil, true, nil
		}
		token = *page.NextPageToken
	}
	return nil, false, nil
}

// lookupServiceAccount finds an account by ID, failing with 404 only when
// the whole list was read and with a partial failure when the budget ran out.
func lookupServiceAccount(ctx context.Context, client *apiClient, id string, maxPages int) (adminopenapi.ServiceAccount, error) {
	account, complete, err := findServiceAccount(ctx, client, func(a adminopenapi.ServiceAccount) bool { return a.Id.String() == id }, maxPages)
	if err != nil {
		return adminopenapi.ServiceAccount{}, err
	}
	if account != nil {
		return *account, nil
	}
	if !complete {
		return adminopenapi.ServiceAccount{}, &PartialError{
			Message: fmt.Sprintf("service account %s was not among the first %d pages of the list", id, maxPages),
			Hint:    "raise --max-pages; there is no endpoint for one service account, so it is found by reading the list",
		}
	}
	return adminopenapi.ServiceAccount{}, &APIError{Code: "not_found", HTTPStatus: http.StatusNotFound,
		Message: fmt.Sprintf("service account %s does not exist", id), Hint: "agctl service-account list shows the accounts"}
}

func serviceAccountShowCommand() command {
	var maxPages int
	return command{
		name:        "service-account show",
		synopsis:    "show one service account, found by reading the list",
		usage:       "agctl service-account show ID [--max-pages N] [--json]",
		positionals: 1,
		argsUsage:   "ID",
		setFlags: func(fs *flag.FlagSet) {
			fs.IntVar(&maxPages, "max-pages", defaultMaxPages, "list pages to read while looking for the account; running out exits 3")
		},
		meta: commandMeta{
			Resource: "service-account", Scenarios: []string{"ci", "admin"}, Stage: "m1",
			Endpoints: []string{"GET " + serviceAccountsPath}, MinimumRole: "platform admin",
			OutputSchema: "ServiceAccount", DryRun: "unsupported", Retry: true,
		},
		run: func(ctx context.Context, env *commandContext) error {
			if maxPages < 1 {
				return &UsageError{Message: "--max-pages must be positive", Hint: "for example --max-pages 20"}
			}
			client, err := env.client()
			if err != nil {
				return err
			}
			account, err := lookupServiceAccount(ctx, client, env.args[0], maxPages)
			if err != nil {
				return err
			}
			return env.render(account, nil, func(w io.Writer) error { return renderServiceAccount(w, account) })
		},
	}
}

func completeServiceAccount(account adminopenapi.ServiceAccount) bool {
	return account.Id != (openapi_types.UUID{}) && account.Name != "" && account.State != "" && account.Version != ""
}

func serviceAccountCreateCommand() command {
	var description string
	return command{
		name:        "service-account create",
		synopsis:    "create a service account; grant it access with agctl grant upsert",
		usage:       "agctl service-account create NAME [--description TEXT] [--dry-run] [--json]",
		positionals: 1,
		argsUsage:   "NAME",
		setFlags: func(fs *flag.FlagSet) {
			fs.StringVar(&description, "description", "", "what the account is for (at most 1024 characters)")
		},
		meta: commandMeta{
			Resource: "service-account", Scenarios: []string{"ci", "admin"}, Stage: "m1",
			Endpoints: []string{"POST " + serviceAccountsPath}, Mutating: true, MinimumRole: "platform admin",
			InputSchema: "CreateServiceAccount", OutputSchema: "ServiceAccount", DryRun: "client-plan",
		},
		run: func(ctx context.Context, env *commandContext) error {
			name := strings.TrimSpace(env.args[0])
			if name == "" || utf8.RuneCountInString(name) > 128 {
				return &UsageError{Message: "the service account name must be 1-128 characters", Hint: "for example agctl service-account create release-bot"}
			}
			if utf8.RuneCountInString(strings.TrimSpace(description)) > 1024 {
				return &UsageError{Message: "--description must be at most 1024 characters", Hint: "shorten the description"}
			}
			body := adminopenapi.CreateServiceAccount{Name: name}
			if description != "" {
				trimmed := strings.TrimSpace(description)
				body.Description = &trimmed
			}
			if env.globals.dryRun {
				plan := writePlan{Mode: "client-plan", Method: http.MethodPost, Path: serviceAccountsPath, Headers: map[string]string{}, Body: body,
					Verified: []string{"name and description length"}, NotVerified: []string{"caller permission (platform admin)", "name uniqueness"}}
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
			// Not retried: the endpoint has no idempotency key, and a repeat after
			// a lost response would answer 409 for the account it created.
			var account adminopenapi.ServiceAccount
			response, err := client.sendJSON(ctx, http.MethodPost, serviceAccountsPath, requestOptions{body: encoded}, &account)
			if err == nil && !completeServiceAccount(account) {
				err = incompleteResponse(response, client, "service account")
			}
			if err != nil {
				if outcomeUnknown(err) {
					return unknownOutcomeError(err, unknownOutcome{Operation: "service-account create", Name: name, ReadBack: "agctl service-account list --all"},
						fmt.Sprintf("the service account may have been created; look for %q in agctl service-account list --all before creating it again (a duplicate name answers 409)", name))
				}
				return err
			}
			return env.render(account, nil, func(w io.Writer) error { return renderServiceAccount(w, account) })
		},
	}
}

func serviceAccountUpdateCommand() command {
	var description, state, ifMatch string
	var maxPages int
	return command{
		name:        "service-account update",
		synopsis:    "change a service account's description or disable it (conditional on its version)",
		usage:       "agctl service-account update ID [--description TEXT] [--state active|disabled] [--if-match VERSION] [--dry-run] [--json]",
		positionals: 1,
		argsUsage:   "ID",
		setFlags: func(fs *flag.FlagSet) {
			fs.StringVar(&description, "description", "", "new description")
			fs.StringVar(&state, "state", "", "active or disabled; a disabled account's credentials stop authenticating")
			fs.StringVar(&ifMatch, "if-match", "", "apply only if the account is still at this version (default: the version read from the list just before)")
			fs.IntVar(&maxPages, "max-pages", defaultMaxPages, "list pages to read while looking for the account's version")
		},
		meta: commandMeta{
			Resource: "service-account", Scenarios: []string{"ci", "admin"}, Stage: "m1",
			Endpoints: []string{"PUT " + serviceAccountsPath + "/{serviceAccountId}", "GET " + serviceAccountsPath}, Mutating: true,
			MinimumRole: "platform admin", InputSchema: "UpdateServiceAccount", OutputSchema: "ServiceAccount", DryRun: "client-plan",
		},
		run: func(ctx context.Context, env *commandContext) error {
			id := env.args[0]
			body := adminopenapi.UpdateServiceAccount{}
			if env.flagSet["description"] {
				trimmed := strings.TrimSpace(description)
				if utf8.RuneCountInString(trimmed) > 1024 {
					return &UsageError{Message: "--description must be at most 1024 characters", Hint: "shorten the description"}
				}
				body.Description = &trimmed
			}
			if env.flagSet["state"] {
				if state != "active" && state != "disabled" {
					return &UsageError{Message: "--state must be active or disabled", Hint: "disabling stops every credential of the account from authenticating"}
				}
				value := adminopenapi.UpdateServiceAccountState(state)
				body.State = &value
			}
			if body.Description == nil && body.State == nil {
				return &UsageError{Message: "nothing to update", Hint: "pass --description or --state"}
			}
			if env.flagSet["if-match"] && strings.TrimSpace(ifMatch) == "" {
				return &UsageError{Message: "--if-match cannot be empty", Hint: "pass the version from agctl service-account show"}
			}
			if maxPages < 1 {
				return &UsageError{Message: "--max-pages must be positive", Hint: "for example --max-pages 20"}
			}
			client, err := env.client()
			if err != nil {
				return err
			}
			var current *adminopenapi.ServiceAccount
			if ifMatch == "" {
				account, err := lookupServiceAccount(ctx, client, id, maxPages)
				if err != nil {
					return err
				}
				current = &account
				ifMatch = account.Version
			}
			path := serviceAccountPath(id)
			if env.globals.dryRun {
				plan := writePlan{Mode: "client-plan", Method: http.MethodPut, Path: path, Headers: map[string]string{"If-Match": ifMatch}, Body: body,
					Verified:    []string{"at least one change is given"},
					NotVerified: []string{"caller permission (platform admin)", "that the version still matches when the update is sent"}}
				if current != nil {
					plan.Current = current
					plan.Verified = append(plan.Verified, "the account and its version were read from the list")
				}
				return env.render(plan, nil, func(w io.Writer) error { return renderPlan(w, plan) })
			}
			encoded, err := encodeBody(body)
			if err != nil {
				return err
			}
			// Conditional and therefore not retried.
			var account adminopenapi.ServiceAccount
			response, err := client.sendJSON(ctx, http.MethodPut, path, requestOptions{body: encoded, header: http.Header{"If-Match": {ifMatch}}}, &account)
			if err == nil && !completeServiceAccount(account) {
				err = incompleteResponse(response, client, "service account")
			}
			if err != nil {
				var api *APIError
				if errors.As(err, &api) && api.HTTPStatus == http.StatusPreconditionFailed {
					api.Hint = fmt.Sprintf("the account changed since version %s and was not updated; run agctl service-account show %s and retry", ifMatch, id)
				}
				if outcomeUnknown(err) {
					return unknownOutcomeError(err, unknownOutcome{Operation: "service-account update", ID: id, ReadBack: "agctl service-account show " + id},
						fmt.Sprintf("the update may have been applied; run agctl service-account show %s and compare its version with %s", id, ifMatch))
				}
				return err
			}
			return env.render(account, nil, func(w io.Writer) error { return renderServiceAccount(w, account) })
		},
	}
}

func renderCredentials(w io.Writer, credentials []adminopenapi.ServiceAccountCredential) error {
	if len(credentials) == 0 {
		return emptyLine(w, "credentials")
	}
	table := [][]string{{"NAME", "STATE", "EXPIRES", "LAST USED", "ID"}}
	for _, credential := range credentials {
		table = append(table, []string{credential.Name, credentialState(credential.RevokedAt, credential.ExpiresAt), formatTime(credential.ExpiresAt), formatTime(credential.LastUsedAt), credential.Id.String()})
	}
	return writeColumns(w, table)
}

func credentialState(revokedAt, expiresAt *time.Time) string {
	switch {
	case revokedAt != nil:
		return "revoked"
	case expiresAt != nil && !expiresAt.After(time.Now()):
		return "expired"
	}
	return "active"
}

func credentialListCommand() command {
	var account string
	list := &cursorList[adminopenapi.ServiceAccountCredential]{noun: "credentials", render: renderCredentials}
	list.request = func(*commandContext) (string, url.Values, error) {
		if err := requireFlag("service-account", account, "pass the service account ID with --service-account"); err != nil {
			return "", nil, err
		}
		return serviceAccountPath(account) + "/credentials", nil, nil
	}
	return command{
		name:     "credential list",
		synopsis: "list a service account's credentials (never their secrets)",
		usage:    "agctl credential list --service-account ID [--page-size N] [--cursor TOKEN | --all] [--json]",
		setFlags: func(fs *flag.FlagSet) {
			fs.StringVar(&account, "service-account", "", "service account ID")
			list.setFlags(fs)
		},
		meta: commandMeta{
			Resource: "credential", Scenarios: []string{"ci", "admin"}, Stage: "m1",
			Endpoints: []string{"GET " + serviceAccountsPath + "/{serviceAccountId}/credentials"}, MinimumRole: "platform admin",
			OutputSchema: "ServiceAccountCredentialList", Pagination: true, DryRun: "unsupported", Retry: true,
		},
		run: list.run,
	}
}

// parseExpiry turns --expires-in into the absolute expiry the server
// accepts: in the future and at most 365 days away. Zero keeps the server's
// default (90 days).
func parseExpiry(expiresIn time.Duration) (*time.Time, error) {
	if expiresIn == 0 {
		return nil, nil
	}
	if expiresIn < time.Minute || expiresIn > maxCredentialTerm {
		return nil, &UsageError{Message: "--expires-in must be between 1m and 8760h (365 days)", Hint: "for example --expires-in 720h; omit it for the server default of 90 days"}
	}
	at := time.Now().UTC().Add(expiresIn).Truncate(time.Second)
	return &at, nil
}

// issuedCredential is the data of `credential issue` and `api-key create`:
// the issued object and where its secret went, never the secret itself.
type issuedCredential struct {
	ID               string         `json:"id"`
	Name             string         `json:"name"`
	ServiceAccountID string         `json:"serviceAccountId,omitempty"`
	Roles            []string       `json:"roles,omitempty"`
	ExpiresAt        *time.Time     `json:"expiresAt"`
	Secret           secretDelivery `json:"secret"`
}

func renderIssued(w io.Writer, issued issuedCredential) error {
	_, err := fmt.Fprintf(w, "issued %s (%s), expires %s; the secret was written to %s (mode 0600) and is not shown\n",
		issued.Name, issued.ID, formatTime(issued.ExpiresAt), issued.Secret.Path)
	return err
}

// issueSecret sends a request that answers with a one-time secret, delivers
// the secret, and returns the issued object's ID. The secret is registered
// for redaction before anything else happens to it.
func issueSecret(ctx context.Context, env *commandContext, client *apiClient, destination *secretDestination, path string, body []byte, target any, token func() string, id func() string) (*apiResponse, error) {
	response, err := client.sendJSON(ctx, http.MethodPost, path, requestOptions{body: body}, target)
	if err != nil {
		destination.discard()
		return response, err
	}
	secret := token()
	if secret != "" {
		env.addKnownSecret(secret)
	}
	if secret == "" || id() == "" {
		destination.discard()
		return response, incompleteResponse(response, client, "issued credential")
	}
	return response, destination.deliver(secret)
}

func credentialIssueCommand() command {
	var account, name, output string
	var expiresIn time.Duration
	return command{
		name:     "credential issue",
		synopsis: "issue a service account credential; its secret is written only to --secret-output",
		usage:    "agctl credential issue --service-account ID --name NAME --secret-output FILE [--expires-in DURATION] [--dry-run] [--json]",
		setFlags: func(fs *flag.FlagSet) {
			fs.StringVar(&account, "service-account", "", "service account ID")
			fs.StringVar(&name, "name", "", "credential name, for example the CI system that holds it")
			fs.StringVar(&output, "secret-output", "", "new file to write the secret to (mode 0600; never overwritten)")
			fs.DurationVar(&expiresIn, "expires-in", 0, "lifetime, up to 8760h (default: the server's 90 days)")
		},
		meta: commandMeta{
			Resource: "credential", Scenarios: []string{"ci", "admin"}, Stage: "m1",
			Endpoints: []string{"POST " + serviceAccountsPath + "/{serviceAccountId}/credentials"}, Mutating: true,
			MinimumRole: "platform admin", InputSchema: "CreateServiceAccountCredential", OutputSchema: "IssuedCredential", DryRun: "client-plan",
		},
		run: func(ctx context.Context, env *commandContext) error {
			if err := requireFlag("service-account", account, "pass the service account ID with --service-account"); err != nil {
				return err
			}
			name = strings.TrimSpace(name)
			if name == "" || utf8.RuneCountInString(name) > 128 {
				return &UsageError{Message: "--name must be 1-128 characters", Hint: "name the credential after where it is used, for example --name jenkins"}
			}
			expiresAt, err := parseExpiry(expiresIn)
			if err != nil {
				return err
			}
			destination, err := prepareSecretDestination(output)
			if err != nil {
				return err
			}
			body := adminopenapi.CreateServiceAccountCredential{Name: name, ExpiresAt: expiresAt}
			path := serviceAccountPath(account) + "/credentials"
			if env.globals.dryRun {
				destination.discard()
				plan := writePlan{Mode: "client-plan", Method: http.MethodPost, Path: path, Headers: map[string]string{}, Body: body,
					Verified:    []string{"name and expiry", "--secret-output is a new file in a writable directory"},
					NotVerified: []string{"caller permission (platform admin)", "that the service account exists and is active"}}
				return env.render(plan, nil, func(w io.Writer) error { return renderPlan(w, plan) })
			}
			client, err := env.client()
			if err != nil {
				destination.discard()
				return err
			}
			encoded, err := encodeBody(body)
			if err != nil {
				destination.discard()
				return err
			}
			// Never retried: every request mints a new secret.
			var created adminopenapi.CreatedServiceAccountCredential
			_, err = issueSecret(ctx, env, client, destination, path, encoded, &created,
				func() string { return created.Token }, func() string { return nonZeroUUID(created.Id) })
			issued := issuedCredential{ID: nonZeroUUID(created.Id), Name: created.Name, ServiceAccountID: account, ExpiresAt: created.ExpiresAt,
				Secret: secretDelivery{Path: destination.path, Mode: "0600", Delivered: err == nil}}
			if err != nil {
				var api *APIError
				if errors.As(err, &api) || errors.As(err, new(*ConnectionError)) {
					if outcomeUnknown(err) {
						return unknownOutcomeError(err, unknownOutcome{Operation: "credential issue", ID: account, Name: name, ReadBack: "agctl credential list --service-account " + account},
							fmt.Sprintf("a credential named %q may have been issued without its secret reaching you; revoke any such credential listed by agctl credential list --service-account %s, then issue again", name, account))
					}
					return err
				}
				return undeliveredSecret(err, issued, fmt.Sprintf("agctl credential revoke %s --service-account %s --yes", issued.ID, account))
			}
			return env.render(issued, nil, func(w io.Writer) error { return renderIssued(w, issued) })
		},
	}
}

func nonZeroUUID(id openapi_types.UUID) string {
	if id == (openapi_types.UUID{}) {
		return ""
	}
	return id.String()
}

// revokeCredential revokes a credential or an API key. Revocation sets
// revokedAt once and answers the same record again, so it is retried; the
// record stays listed with its revocation time.
func revokeCredential(ctx context.Context, env *commandContext, path, kind, readBack string, target any, revokedAt func() *time.Time) error {
	if !env.globals.dryRun && !env.globals.yes {
		return &UsageError{Message: fmt.Sprintf("revoking this %s needs --yes; it stops authenticating at once", kind), Hint: "re-run with --yes, or with --dry-run to see the request"}
	}
	if env.globals.dryRun {
		plan := writePlan{Mode: "client-plan", Method: http.MethodDelete, Path: path, Headers: map[string]string{},
			Verified: []string{"--yes is not needed for a plan"}, NotVerified: []string{"caller permission (platform admin)", "that the " + kind + " exists"}}
		return env.render(plan, nil, func(w io.Writer) error { return renderPlan(w, plan) })
	}
	client, err := env.client()
	if err != nil {
		return err
	}
	response, err := client.sendJSON(ctx, http.MethodDelete, path, requestOptions{idempotent: true}, target)
	if err == nil && revokedAt() == nil {
		err = incompleteResponse(response, client, kind)
	}
	if err != nil {
		if outcomeUnknown(err) {
			return unknownOutcomeError(err, unknownOutcome{Operation: kind + " revoke", ReadBack: readBack},
				fmt.Sprintf("the %s may have been revoked; check revokedAt in %s, or re-run the revoke, which is safe to repeat", kind, readBack))
		}
		return err
	}
	raw, _ := json.Marshal(target)
	return env.render(json.RawMessage(raw), nil, func(w io.Writer) error {
		_, err := fmt.Fprintf(w, "%s revoked at %s\n", kind, formatTime(revokedAt()))
		return err
	})
}

func credentialRevokeCommand() command {
	var account string
	return command{
		name:        "credential revoke",
		synopsis:    "revoke a service account credential; it stops authenticating at once",
		usage:       "agctl credential revoke CREDENTIAL_ID --service-account ID --yes [--dry-run] [--json]",
		positionals: 1,
		argsUsage:   "CREDENTIAL_ID",
		setFlags: func(fs *flag.FlagSet) {
			fs.StringVar(&account, "service-account", "", "service account ID")
		},
		meta: commandMeta{
			Resource: "credential", Scenarios: []string{"ci", "admin"}, Stage: "m1",
			Endpoints: []string{"DELETE " + serviceAccountsPath + "/{serviceAccountId}/credentials/{credentialId}"}, Mutating: true,
			MinimumRole: "platform admin", OutputSchema: "ServiceAccountCredential", DryRun: "client-plan", Retry: true,
		},
		run: func(ctx context.Context, env *commandContext) error {
			if err := requireFlag("service-account", account, "pass the service account ID with --service-account"); err != nil {
				return err
			}
			var credential adminopenapi.ServiceAccountCredential
			return revokeCredential(ctx, env, serviceAccountPath(account)+"/credentials/"+url.PathEscape(env.args[0]), "credential",
				"agctl credential list --service-account "+account, &credential, func() *time.Time { return credential.RevokedAt })
		},
	}
}

func renderAPIKeys(w io.Writer, keys []adminopenapi.APIKey) error {
	if len(keys) == 0 {
		return emptyLine(w, "API keys")
	}
	table := [][]string{{"NAME", "ROLES", "STATE", "EXPIRES", "LAST USED", "ID"}}
	for _, key := range keys {
		roles := make([]string, 0, len(key.Roles))
		for _, role := range key.Roles {
			roles = append(roles, string(role))
		}
		table = append(table, []string{key.Name, strings.Join(roles, ","), credentialState(key.RevokedAt, key.ExpiresAt), formatTime(key.ExpiresAt), formatTime(key.LastUsedAt), key.Id.String()})
	}
	return writeColumns(w, table)
}

func apiKeyListCommand() command {
	return command{
		name:     "api-key list",
		synopsis: "list platform API keys (never their secrets)",
		usage:    "agctl api-key list [--json]",
		meta: commandMeta{
			Resource: "api-key", Scenarios: []string{"admin"}, Stage: "m1",
			Endpoints: []string{"GET " + apiKeysPath}, MinimumRole: "platform admin",
			OutputSchema: "APIKeyList", DryRun: "unsupported", Retry: true,
		},
		run: func(ctx context.Context, env *commandContext) error {
			client, err := env.client()
			if err != nil {
				return err
			}
			var page serverPage[adminopenapi.APIKey]
			if err := client.getJSON(ctx, apiKeysPath, nil, &page); err != nil {
				return err
			}
			meta := &envelopeMeta{Pagination: &paginationMeta{Mode: "none"}}
			return env.render(newListPayload(page.Items), meta, func(w io.Writer) error { return renderAPIKeys(w, page.Items) })
		},
	}
}

func apiKeyCreateCommand() command {
	var output string
	var roles stringList
	var expiresIn time.Duration
	return command{
		name:        "api-key create",
		synopsis:    "create a platform API key with a global role; its secret is written only to --secret-output",
		usage:       "agctl api-key create NAME --secret-output FILE [--role member|admin]... [--expires-in DURATION] [--dry-run] [--json]",
		positionals: 1,
		argsUsage:   "NAME",
		setFlags: func(fs *flag.FlagSet) {
			fs.StringVar(&output, "secret-output", "", "new file to write the secret to (mode 0600; never overwritten)")
			fs.Var(&roles, "role", "global role: member or admin (repeatable; prefer service accounts with repository grants for CI)")
			fs.DurationVar(&expiresIn, "expires-in", 0, "lifetime, up to 8760h (default: the server's 90 days)")
		},
		meta: commandMeta{
			Resource: "api-key", Scenarios: []string{"admin"}, Stage: "m1",
			Endpoints: []string{"POST " + apiKeysPath}, Mutating: true, MinimumRole: "platform admin",
			InputSchema: "CreateAPIKey", OutputSchema: "IssuedCredential", DryRun: "client-plan",
		},
		run: func(ctx context.Context, env *commandContext) error {
			name := strings.TrimSpace(env.args[0])
			if name == "" || utf8.RuneCountInString(name) > 128 {
				return &UsageError{Message: "the API key name must be 1-128 characters", Hint: "for example agctl api-key create backup-job --secret-output ./key"}
			}
			body := adminopenapi.CreateAPIKey{Name: name}
			if len(roles) > 0 {
				list := make([]adminopenapi.CreateAPIKeyRoles, 0, len(roles))
				for _, role := range roles {
					if role != "member" && role != "admin" {
						return &UsageError{Message: fmt.Sprintf("invalid --role %q", role), Hint: "use --role member or --role admin"}
					}
					list = append(list, adminopenapi.CreateAPIKeyRoles(role))
				}
				body.Roles = &list
			}
			expiresAt, err := parseExpiry(expiresIn)
			if err != nil {
				return err
			}
			body.ExpiresAt = expiresAt
			destination, err := prepareSecretDestination(output)
			if err != nil {
				return err
			}
			if env.globals.dryRun {
				destination.discard()
				plan := writePlan{Mode: "client-plan", Method: http.MethodPost, Path: apiKeysPath, Headers: map[string]string{}, Body: body,
					Verified:    []string{"name, roles, and expiry", "--secret-output is a new file in a writable directory"},
					NotVerified: []string{"caller permission (platform admin)"}}
				return env.render(plan, nil, func(w io.Writer) error { return renderPlan(w, plan) })
			}
			client, err := env.client()
			if err != nil {
				destination.discard()
				return err
			}
			encoded, err := encodeBody(body)
			if err != nil {
				destination.discard()
				return err
			}
			// Never retried: every request mints a new secret.
			var created adminopenapi.CreatedAPIKey
			_, err = issueSecret(ctx, env, client, destination, apiKeysPath, encoded, &created,
				func() string { return created.Token }, func() string { return nonZeroUUID(created.Id) })
			issued := issuedCredential{ID: nonZeroUUID(created.Id), Name: created.Name, ExpiresAt: created.ExpiresAt,
				Secret: secretDelivery{Path: destination.path, Mode: "0600", Delivered: err == nil}}
			for _, role := range created.Roles {
				issued.Roles = append(issued.Roles, string(role))
			}
			if err != nil {
				var api *APIError
				if errors.As(err, &api) || errors.As(err, new(*ConnectionError)) {
					if outcomeUnknown(err) {
						return unknownOutcomeError(err, unknownOutcome{Operation: "api-key create", Name: name, ReadBack: "agctl api-key list"},
							fmt.Sprintf("an API key named %q may have been created without its secret reaching you; revoke any such key listed by agctl api-key list, then create again", name))
					}
					return err
				}
				return undeliveredSecret(err, issued, fmt.Sprintf("agctl api-key revoke %s --yes", issued.ID))
			}
			return env.render(issued, nil, func(w io.Writer) error { return renderIssued(w, issued) })
		},
	}
}

func apiKeyRevokeCommand() command {
	return command{
		name:        "api-key revoke",
		synopsis:    "revoke a platform API key; it stops authenticating at once",
		usage:       "agctl api-key revoke ID --yes [--dry-run] [--json]",
		positionals: 1,
		argsUsage:   "ID",
		meta: commandMeta{
			Resource: "api-key", Scenarios: []string{"admin"}, Stage: "m1",
			Endpoints: []string{"DELETE " + apiKeysPath + "/{apiKeyId}"}, Mutating: true, MinimumRole: "platform admin",
			OutputSchema: "APIKey", DryRun: "client-plan", Retry: true,
		},
		run: func(ctx context.Context, env *commandContext) error {
			var key adminopenapi.APIKey
			return revokeCredential(ctx, env, apiKeysPath+"/"+url.PathEscape(env.args[0]), "API key", "agctl api-key list", &key, func() *time.Time { return key.RevokedAt })
		},
	}
}
