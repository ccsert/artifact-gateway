package agctl

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
)

const (
	repositoriesPath       = managementPrefix + "/repositories"
	repoIdempotencyKeyMax  = 128
	repoDeleteNeedsConfirm = "agctl repo delete needs --yes to delete a repository"
)

// repoWaitInterval spaces the read-backs of `repo delete --wait`. The deletion
// worker runs once a minute, so polling faster gains nothing.
var repoWaitInterval = 2 * time.Second

var repositoryNamePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

// stringList is a repeatable string flag.
type stringList []string

func (l *stringList) String() string { return strings.Join(*l, ",") }

func (l *stringList) Set(value string) error {
	*l = append(*l, value)
	return nil
}

func repositoryPath(id string) string {
	return repositoriesPath + "/" + url.PathEscape(id)
}

func repositoryType(repo adminopenapi.Repository) string {
	if repo.Type == nil {
		return string(adminopenapi.RepositoryTypeHosted)
	}
	return string(*repo.Type)
}

func renderRepositoryTable(w io.Writer, items []adminopenapi.Repository) error {
	if len(items) == 0 {
		_, err := fmt.Fprintln(w, "no repositories visible to this credential")
		return err
	}
	rows := [][]string{{"NAME", "FORMAT", "TYPE", "STATE", "ID"}}
	for _, repo := range items {
		rows = append(rows, []string{repo.Name, string(repo.Format), repositoryType(repo), string(repo.State), repo.Id})
	}
	return writeColumns(w, rows)
}

func writeColumns(w io.Writer, rows [][]string) error {
	widths := make([]int, len(rows[0]))
	for _, row := range rows {
		for index, cell := range row {
			if len(cell) > widths[index] {
				widths[index] = len(cell)
			}
		}
	}
	for _, row := range rows {
		var line strings.Builder
		for index, cell := range row {
			if index == len(row)-1 {
				line.WriteString(cell)
				break
			}
			fmt.Fprintf(&line, "%-*s  ", widths[index], cell)
		}
		if _, err := fmt.Fprintln(w, line.String()); err != nil {
			return err
		}
	}
	return nil
}

func renderRepository(w io.Writer, repo adminopenapi.Repository) error {
	lines := [][2]string{
		{"id", repo.Id},
		{"name", repo.Name},
		{"format", string(repo.Format)},
		{"type", repositoryType(repo)},
		{"state", string(repo.State)},
		{"version", repo.Version},
		{"anonymous read", yesNo(repo.AnonymousRead)},
	}
	if repo.Format == adminopenapi.FormatMaven && repositoryType(repo) == string(adminopenapi.RepositoryTypeHosted) {
		lines = append(lines, [2]string{"maven strict publication", yesNo(repo.MavenStrictPublication)})
	}
	if repo.Endpoint != nil && *repo.Endpoint != "" {
		lines = append(lines, [2]string{"endpoint", *repo.Endpoint})
	}
	if repo.AllowedHosts != nil && len(*repo.AllowedHosts) > 0 {
		lines = append(lines, [2]string{"allowed hosts", strings.Join(*repo.AllowedHosts, ", ")})
	}
	if repo.EgressProxy != nil {
		lines = append(lines, [2]string{"egress proxy", "configured"})
	}
	if repo.UpstreamAuth != nil {
		lines = append(lines, [2]string{"upstream auth", "configured"})
	}
	if repo.OciBearer != nil {
		lines = append(lines, [2]string{"oci bearer", "configured"})
	}
	for _, line := range lines {
		if _, err := fmt.Fprintf(w, "%-26s %s\n", line[0]+":", line[1]); err != nil {
			return err
		}
	}
	return nil
}

func repoListCommand() command {
	list := &cursorList[adminopenapi.Repository]{
		path:    repositoriesPath,
		command: "agctl repo list",
		noun:    "repositories",
		render:  renderRepositoryTable,
	}
	return command{
		name:     "repo list",
		synopsis: "list the repositories visible to the caller",
		usage:    "agctl repo list [--page-size N] [--cursor TOKEN | --all [--max-pages N]] [--json]",
		setFlags: list.setFlags,
		meta: commandMeta{
			Resource:     "repository",
			Scenarios:    []string{"ci", "admin"},
			Stage:        "m1",
			Endpoints:    []string{"GET " + repositoriesPath},
			MinimumRole:  "repositories:read (filtered to readable repositories)",
			OutputSchema: "RepositoryPage",
			Pagination:   true,
			DryRun:       "unsupported",
			Retry:        true,
		},
		run: list.run,
	}
}

func repoShowCommand() command {
	return command{
		name:        "repo show",
		synopsis:    "show one repository",
		usage:       "agctl repo show ID [--json]",
		positionals: 1,
		argsUsage:   "ID",
		meta: commandMeta{
			Resource:     "repository",
			Scenarios:    []string{"ci", "admin"},
			Stage:        "m1",
			Endpoints:    []string{"GET " + repositoriesPath + "/{repositoryId}"},
			MinimumRole:  "repositories:read",
			OutputSchema: "Repository",
			DryRun:       "unsupported",
			Retry:        true,
		},
		run: func(ctx context.Context, env *commandContext) error {
			client, err := env.client()
			if err != nil {
				return err
			}
			var repo adminopenapi.Repository
			if err := client.getJSON(ctx, repositoryPath(env.args[0]), nil, &repo); err != nil {
				return err
			}
			return env.render(repo, nil, func(w io.Writer) error { return renderRepository(w, repo) })
		},
	}
}

// writePlan is the data of a client-plan --dry-run: the request a write would
// send, what the client checked, and what only the server can decide. Nothing
// is submitted.
type writePlan struct {
	Mode        string            `json:"mode"`
	Method      string            `json:"method"`
	Path        string            `json:"path"`
	Headers     map[string]string `json:"headers"`
	Body        any               `json:"body"`
	Current     any               `json:"current"`
	Verified    []string          `json:"verified"`
	NotVerified []string          `json:"notVerified"`
}

func renderPlan(w io.Writer, plan writePlan) error {
	if _, err := fmt.Fprintf(w, "dry run (%s): would send %s %s\n", plan.Mode, plan.Method, plan.Path); err != nil {
		return err
	}
	for _, name := range sortedKeys(plan.Headers) {
		if _, err := fmt.Fprintf(w, "  header %s: %s\n", name, plan.Headers[name]); err != nil {
			return err
		}
	}
	if plan.Body != nil {
		body, _ := encodeBody(plan.Body)
		if _, err := fmt.Fprintf(w, "  body: %s\n", body); err != nil {
			return err
		}
	}
	for _, line := range plan.Verified {
		if _, err := fmt.Fprintf(w, "  checked: %s\n", line); err != nil {
			return err
		}
	}
	for _, line := range plan.NotVerified {
		if _, err := fmt.Fprintf(w, "  not checked: %s\n", line); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(w, "nothing was submitted")
	return err
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// unknownOutcome is the data of a write whose result the client could not
// observe: the server may or may not have applied it.
type unknownOutcome struct {
	Outcome        string `json:"outcome"`
	Operation      string `json:"operation"`
	ID             string `json:"id,omitempty"`
	Name           string `json:"name,omitempty"`
	IdempotencyKey string `json:"idempotencyKey,omitempty"`
	ReadBack       string `json:"readBack"`
}

// outcomeUnknown reports whether a failed write may still have been applied:
// the connection failed or timed out after the request could have been sent,
// or the server failed without refusing the request. An unreadable success
// response cannot establish the resulting repository either.
func outcomeUnknown(err error) bool {
	var connection *ConnectionError
	if errors.As(err, &connection) {
		return true
	}
	var api *APIError
	return errors.As(err, &api) && (api.HTTPStatus >= 500 ||
		(api.Code == "invalid_response" && api.HTTPStatus >= 200 && api.HTTPStatus < 300))
}

// sendRepositoryWrite requires the identifying fields of the returned
// repository before reporting success. Empty, null, or incomplete JSON must
// preserve recovery information just like an unreadable response.
func sendRepositoryWrite(ctx context.Context, client *apiClient, method, path string, options requestOptions) (adminopenapi.Repository, error) {
	var repo adminopenapi.Repository
	response, err := client.sendJSON(ctx, method, path, options, &repo)
	if err != nil {
		return repo, err
	}
	if repo.Id == "" || repo.Name == "" || repo.Format == "" || repo.State == "" || repo.Version == "" {
		return repo, &APIError{
			Code:       "invalid_response",
			HTTPStatus: response.StatusCode,
			Message:    "server returned an incomplete repository response",
			RequestID:  redactText(response.Header.Get("X-Request-ID"), client.token),
		}
	}
	return repo, nil
}

func unknownOutcomeError(err error, outcome unknownOutcome, hint string) error {
	outcome.Outcome = "unknown"
	return &resultError{err: err, data: outcome, hint: hint}
}

func newIdempotencyKey() (string, error) {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("cannot generate an idempotency key: %w", err)
	}
	return "agctl-" + hex.EncodeToString(buffer), nil
}

func repoCreateCommand() command {
	var format, repoType, endpoint, idempotencyKey string
	var allowedHosts stringList
	var anonymousRead, mavenStrict bool
	return command{
		name:        "repo create",
		synopsis:    "create a hosted or proxy repository",
		usage:       "agctl repo create NAME --format FORMAT [--type hosted|proxy] [--endpoint URL] [--allowed-host HOST]... [--anonymous-read] [--maven-strict-publication] [--idempotency-key KEY] [--dry-run] [--json]",
		positionals: 1,
		argsUsage:   "NAME",
		setFlags: func(fs *flag.FlagSet) {
			fs.StringVar(&format, "format", "", "repository format: apt, cargo, conan, go, maven, npm, oci, pypi, raw")
			fs.StringVar(&repoType, "type", string(adminopenapi.CreateRepositoryTypeHosted), "repository type: hosted or proxy")
			fs.StringVar(&endpoint, "endpoint", "", "upstream base URL (proxy repositories)")
			fs.Var(&allowedHosts, "allowed-host", "host the proxy may reach (repeatable; proxy repositories)")
			fs.BoolVar(&anonymousRead, "anonymous-read", false, "allow unauthenticated protocol reads where effective policy permits")
			fs.BoolVar(&mavenStrict, "maven-strict-publication", false, "require Gateway coordinate commits (Maven hosted only)")
			fs.StringVar(&idempotencyKey, "idempotency-key", "", "reuse the key of an earlier attempt whose outcome was unknown (default: a new key)")
		},
		meta: commandMeta{
			Resource:     "repository",
			Scenarios:    []string{"admin"},
			Stage:        "m1",
			Endpoints:    []string{"POST " + repositoriesPath},
			Mutating:     true,
			MinimumRole:  "platform admin",
			InputSchema:  "CreateRepository",
			OutputSchema: "Repository",
			DryRun:       "client-plan",
			Retry:        true,
		},
		run: func(ctx context.Context, env *commandContext) error {
			name := env.args[0]
			if !repositoryNamePattern.MatchString(name) {
				return &UsageError{Message: fmt.Sprintf("invalid repository name %q", name), Hint: "use 1-63 lowercase letters, digits, and inner hyphens"}
			}
			request := adminopenapi.CreateRepository{Name: name, Format: adminopenapi.Format(format)}
			if format == "" {
				return &UsageError{Message: "missing required flag --format", Hint: "pass --format, for example --format npm"}
			}
			if !request.Format.Valid() {
				return &UsageError{Message: fmt.Sprintf("unknown format %q", format), Hint: "use one of apt, cargo, conan, go, maven, npm, oci, pypi, raw"}
			}
			kind := adminopenapi.CreateRepositoryType(repoType)
			if !kind.Valid() {
				return &UsageError{Message: fmt.Sprintf("unknown repository type %q", repoType), Hint: "use --type hosted or --type proxy"}
			}
			request.Type = &kind
			if kind == adminopenapi.CreateRepositoryTypeProxy {
				if endpoint == "" {
					return &UsageError{Message: "a proxy repository needs --endpoint", Hint: "pass the upstream base URL with --endpoint"}
				}
				request.Endpoint = &endpoint
				if len(allowedHosts) > 0 {
					hosts := []string(allowedHosts)
					request.AllowedHosts = &hosts
				}
			} else if endpoint != "" || len(allowedHosts) > 0 {
				return &UsageError{Message: "--endpoint and --allowed-host only apply to proxy repositories", Hint: "add --type proxy, or drop them for a hosted repository"}
			}
			if env.flagSet["anonymous-read"] {
				request.AnonymousRead = &anonymousRead
			}
			if env.flagSet["maven-strict-publication"] {
				if request.Format != adminopenapi.FormatMaven || kind != adminopenapi.CreateRepositoryTypeHosted {
					return &UsageError{Message: "--maven-strict-publication only applies to Maven hosted repositories", Hint: "drop the flag, or create a hosted maven repository"}
				}
				request.MavenStrictPublication = &mavenStrict
			}
			if env.flagSet["idempotency-key"] {
				idempotencyKey = strings.TrimSpace(idempotencyKey)
				if idempotencyKey == "" || len(idempotencyKey) > repoIdempotencyKeyMax {
					return &UsageError{Message: "--idempotency-key must be 1-128 characters", Hint: "pass the key reported by the earlier attempt"}
				}
			} else {
				generated, err := newIdempotencyKey()
				if err != nil {
					return err
				}
				idempotencyKey = generated
			}
			if env.globals.dryRun {
				plan := writePlan{
					Mode:    "client-plan",
					Method:  http.MethodPost,
					Path:    repositoriesPath,
					Headers: map[string]string{"Idempotency-Key": idempotencyKey},
					Body:    request,
					Verified: []string{
						"name pattern, format, and type",
						"endpoint and allowed hosts only on proxy repositories",
					},
					NotVerified: []string{
						"caller permission (platform admin)",
						"name uniqueness",
						"endpoint reachability and server-side egress policy",
					},
				}
				return env.render(plan, nil, func(w io.Writer) error { return renderPlan(w, plan) })
			}
			client, err := env.client()
			if err != nil {
				return err
			}
			body, err := encodeBody(request)
			if err != nil {
				return err
			}
			// The endpoint replays the same response for the same key and body,
			// so every attempt of this call may resend it.
			repo, err := sendRepositoryWrite(ctx, client, http.MethodPost, repositoriesPath, requestOptions{
				body:       body,
				idempotent: true,
				header:     http.Header{"Idempotency-Key": {idempotencyKey}},
			})
			if err != nil {
				if outcomeUnknown(err) {
					return unknownOutcomeError(err, unknownOutcome{
						Operation:      "repo create",
						Name:           name,
						IdempotencyKey: idempotencyKey,
						ReadBack:       "agctl repo list",
					}, fmt.Sprintf("the repository may have been created; re-run the same command with --idempotency-key %s to retry safely, or check agctl repo list", idempotencyKey))
				}
				return err
			}
			return env.render(repo, nil, func(w io.Writer) error { return renderRepository(w, repo) })
		},
	}
}

func repoUpdateCommand() command {
	var endpoint, ifMatch string
	var allowedHosts stringList
	var anonymousRead, mavenStrict bool
	return command{
		name:        "repo update",
		synopsis:    "change repository policy or proxy settings (conditional on its version)",
		usage:       "agctl repo update ID [--anonymous-read=true|false] [--maven-strict-publication=true|false] [--endpoint URL] [--allowed-host HOST]... [--if-match VERSION] [--dry-run] [--json]",
		positionals: 1,
		argsUsage:   "ID",
		setFlags: func(fs *flag.FlagSet) {
			fs.BoolVar(&anonymousRead, "anonymous-read", false, "set the anonymous read policy")
			fs.BoolVar(&mavenStrict, "maven-strict-publication", false, "set strict Maven publication (Maven hosted only)")
			fs.StringVar(&endpoint, "endpoint", "", "replace the upstream base URL (proxy repositories)")
			fs.Var(&allowedHosts, "allowed-host", "replace the allowed hosts (repeatable; proxy repositories)")
			fs.StringVar(&ifMatch, "if-match", "", "apply only if the repository is still at this version (default: the version read just before the update)")
		},
		meta: commandMeta{
			Resource:     "repository",
			Scenarios:    []string{"admin"},
			Stage:        "m1",
			Endpoints:    []string{"GET " + repositoriesPath + "/{repositoryId}", "PATCH " + repositoriesPath + "/{repositoryId}"},
			Mutating:     true,
			MinimumRole:  "repositories:admin",
			InputSchema:  "UpdateRepository",
			OutputSchema: "Repository",
			DryRun:       "client-plan",
		},
		run: func(ctx context.Context, env *commandContext) error {
			id := env.args[0]
			request := adminopenapi.UpdateRepository{}
			changed := false
			if env.flagSet["anonymous-read"] {
				request.AnonymousRead = &anonymousRead
				changed = true
			}
			if env.flagSet["maven-strict-publication"] {
				request.MavenStrictPublication = &mavenStrict
				changed = true
			}
			if env.flagSet["endpoint"] {
				if endpoint == "" {
					return &UsageError{Message: "--endpoint cannot be empty", Hint: "proxy repositories need an upstream base URL"}
				}
				request.Endpoint = &endpoint
				changed = true
			}
			if len(allowedHosts) > 0 {
				hosts := []string(allowedHosts)
				request.AllowedHosts = &hosts
				changed = true
			}
			if !changed {
				return &UsageError{Message: "nothing to update", Hint: "pass at least one of --anonymous-read, --maven-strict-publication, --endpoint, --allowed-host"}
			}
			if env.flagSet["if-match"] && strings.TrimSpace(ifMatch) == "" {
				return &UsageError{Message: "--if-match cannot be empty", Hint: "pass the repository version from agctl repo show"}
			}
			client, err := env.client()
			if err != nil {
				return err
			}
			// Without --if-match the update is conditional on the version read
			// here, so a concurrent change between read and write fails with 412
			// instead of being overwritten.
			var current *adminopenapi.Repository
			if ifMatch == "" {
				var repo adminopenapi.Repository
				if err := client.getJSON(ctx, repositoryPath(id), nil, &repo); err != nil {
					return err
				}
				current = &repo
				ifMatch = repo.Version
			}
			if env.globals.dryRun {
				plan := writePlan{
					Mode:     "client-plan",
					Method:   http.MethodPatch,
					Path:     repositoryPath(id),
					Headers:  map[string]string{"If-Match": ifMatch},
					Body:     request,
					Verified: []string{"at least one setting is given"},
					NotVerified: []string{
						"caller permission (repositories:admin)",
						"which settings the repository type accepts (hosted repositories only take anonymous read and Maven publication policy)",
						"that the version still matches when the update is sent",
					},
				}
				if current != nil {
					plan.Current = current
					plan.Verified = append(plan.Verified, "the repository exists and its current version was read")
				}
				return env.render(plan, nil, func(w io.Writer) error { return renderPlan(w, plan) })
			}
			body, err := encodeBody(request)
			if err != nil {
				return err
			}
			// A conditional write is not retried: if a lost attempt applied it,
			// the retry would fail on the new version and misreport a conflict.
			repo, err := sendRepositoryWrite(ctx, client, http.MethodPatch, repositoryPath(id), requestOptions{
				body:   body,
				header: http.Header{"If-Match": {ifMatch}},
			})
			if err != nil {
				var api *APIError
				if errors.As(err, &api) && api.HTTPStatus == http.StatusPreconditionFailed {
					api.Hint = fmt.Sprintf("the repository changed since version %s and was not updated; run agctl repo show %s and retry against its current state", ifMatch, id)
				}
				if outcomeUnknown(err) {
					return unknownOutcomeError(err, unknownOutcome{
						Operation: "repo update",
						ID:        id,
						ReadBack:  "agctl repo show " + id,
					}, fmt.Sprintf("the update may have been applied; run agctl repo show %s and compare its version with %s before retrying", id, ifMatch))
				}
				return err
			}
			return env.render(repo, nil, func(w io.Writer) error { return renderRepository(w, repo) })
		},
	}
}

// repositoryDeletion is the data of `repo delete`. A 202 is acceptance, not
// completion: status stays "accepted" until a read-back observes the terminal
// "deleted" state. repositoryState is the last state read back, null when the
// command did not read the repository.
type repositoryDeletion struct {
	ID              string  `json:"id"`
	Status          string  `json:"status"`
	RepositoryState *string `json:"repositoryState"`
	Terminal        bool    `json:"terminal"`
}

func renderDeletion(w io.Writer, deletion repositoryDeletion) error {
	if deletion.Terminal {
		_, err := fmt.Fprintf(w, "repository %s deleted\n", deletion.ID)
		return err
	}
	state := "not read"
	if deletion.RepositoryState != nil {
		state = *deletion.RepositoryState
	}
	_, err := fmt.Fprintf(w, "deletion of repository %s accepted (state: %s); check with agctl repo show %s, or wait with --wait\n", deletion.ID, state, deletion.ID)
	return err
}

func repoDeleteCommand() command {
	var wait bool
	return command{
		name:        "repo delete",
		synopsis:    "delete a repository (asynchronous: deleting, then deleted)",
		usage:       "agctl repo delete ID --yes [--wait [--timeout DURATION]] [--dry-run] [--json]",
		positionals: 1,
		argsUsage:   "ID",
		setFlags: func(fs *flag.FlagSet) {
			fs.BoolVar(&wait, "wait", false, "read the repository back until it is deleted; --timeout bounds the whole command")
		},
		meta: commandMeta{
			Resource:     "repository",
			Scenarios:    []string{"admin"},
			Stage:        "m1",
			Endpoints:    []string{"DELETE " + repositoriesPath + "/{repositoryId}", "GET " + repositoriesPath + "/{repositoryId}"},
			Mutating:     true,
			MinimumRole:  "repositories:admin",
			OutputSchema: "RepositoryDeletion",
			DryRun:       "client-plan",
			Retry:        true,
			Wait:         true,
		},
		run: func(ctx context.Context, env *commandContext) error {
			id := env.args[0]
			if !env.globals.dryRun && !env.globals.yes {
				return &UsageError{Message: repoDeleteNeedsConfirm, Hint: "re-run with --yes to confirm, or with --dry-run to see what would happen"}
			}
			client, err := env.client()
			if err != nil {
				return err
			}
			if env.globals.dryRun {
				var repo adminopenapi.Repository
				if err := client.getJSON(ctx, repositoryPath(id), nil, &repo); err != nil {
					return err
				}
				plan := writePlan{
					Mode:     "client-plan",
					Method:   http.MethodDelete,
					Path:     repositoryPath(id),
					Headers:  map[string]string{},
					Current:  repo,
					Verified: []string{"the repository exists and is readable"},
					NotVerified: []string{
						"caller permission (repositories:admin)",
					},
				}
				switch repo.State {
				case adminopenapi.RepositoryStateActive:
					plan.Verified = append(plan.Verified, "the repository is active: protocol access would stop at once and the worker would advance it to deleted")
				case adminopenapi.RepositoryStateDeleting:
					plan.Verified = append(plan.Verified, "the repository is already deleting: a delete would be accepted again without another change")
				case adminopenapi.RepositoryStateDeleted:
					plan.Verified = append(plan.Verified, "the repository is already deleted: the server would answer 404")
				}
				return env.render(plan, nil, func(w io.Writer) error { return renderPlan(w, plan) })
			}
			// --wait spends one overall budget on the delete and its read-backs.
			if wait {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, env.globals.timeout)
				defer cancel()
			}
			// Repeating a delete while the repository is deleting is accepted
			// again without another change, so the request may be retried.
			if _, err := client.sendJSON(ctx, http.MethodDelete, repositoryPath(id), requestOptions{idempotent: true}, nil); err != nil {
				if outcomeUnknown(err) {
					return unknownOutcomeError(err, unknownOutcome{
						Operation: "repo delete",
						ID:        id,
						ReadBack:  "agctl repo show " + id,
					}, fmt.Sprintf("the deletion may have been accepted; run agctl repo show %s to read its state", id))
				}
				return err
			}
			deletion := repositoryDeletion{ID: id, Status: "accepted"}
			if wait {
				if err := waitForDeletion(ctx, client, &deletion); err != nil {
					return err
				}
			}
			return env.render(deletion, nil, func(w io.Writer) error { return renderDeletion(w, deletion) })
		},
	}
}

// waitForDeletion reads the repository until it reaches the terminal deleted
// state or the context's budget runs out. A timeout reports the accepted ID
// and the last state read; the server keeps deleting and nothing is resent.
func waitForDeletion(ctx context.Context, client *apiClient, deletion *repositoryDeletion) error {
	for {
		var repo adminopenapi.Repository
		err := client.getJSON(ctx, repositoryPath(deletion.ID), nil, &repo)
		if err == nil {
			state := string(repo.State)
			deletion.RepositoryState = &state
			if repo.State == adminopenapi.RepositoryStateDeleted {
				deletion.Status = "deleted"
				deletion.Terminal = true
				return nil
			}
		} else if ctx.Err() == nil {
			return err
		}
		if ctx.Err() == nil {
			timer := time.NewTimer(repoWaitInterval)
			select {
			case <-timer.C:
				continue
			case <-ctx.Done():
				timer.Stop()
			}
		}
		state := "not read"
		if deletion.RepositoryState != nil {
			state = *deletion.RepositoryState
		}
		snapshot := *deletion
		return &resultError{
			err: &WaitTimeoutError{
				Message: fmt.Sprintf("repository %s was not deleted within the wait budget (last state: %s)", deletion.ID, state),
				Hint:    fmt.Sprintf("the deletion was accepted and continues on the server; check with agctl repo show %s, or wait again with a longer --timeout", deletion.ID),
			},
			data:  snapshot,
			human: func(w io.Writer) error { return renderDeletion(w, snapshot) },
		}
	}
}
