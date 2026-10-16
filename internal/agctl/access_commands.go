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
	"sort"
	"strings"

	adminopenapi "github.com/ccsert/artifact-gateway/internal/admin/openapi"
)

const (
	repositoryGrantsPath  = managementPrefix + "/repository-grants"
	rolesPath             = managementPrefix + "/authorization-roles"
	templatesPath         = managementPrefix + "/authorization-templates"
	anonymousPolicyPath   = managementPrefix + "/anonymous-access-policy"
	maxGrantPrincipal     = 512
	maxGrantResourcePrefx = 255
)

var grantScopes = map[string]bool{
	"repositories:read": true, "repositories:write": true, "repositories:admin": true, "repositories:intelligence": true,
}

// grantRow is one grant in the CLI's own shape, so grants, aggregated
// records, and template grants compare the same way.
type grantRow struct {
	Principal      string   `json:"principal"`
	Scopes         []string `json:"scopes"`
	ResourcePrefix string   `json:"resourcePrefix,omitempty"`
}

func (g grantRow) key() string { return g.Principal + "\x00" + g.ResourcePrefix }

func (g grantRow) sameScopes(other grantRow) bool {
	a := append([]string(nil), g.Scopes...)
	b := append([]string(nil), other.Scopes...)
	sort.Strings(a)
	sort.Strings(b)
	return strings.Join(a, ",") == strings.Join(b, ",")
}

func grantRows(grants []adminopenapi.Grant) []grantRow {
	rows := make([]grantRow, 0, len(grants))
	for _, grant := range grants {
		row := grantRow{Principal: grant.Principal, ResourcePrefix: optional(grant.ResourcePrefix)}
		for _, scope := range grant.Scopes {
			row.Scopes = append(row.Scopes, string(scope))
		}
		rows = append(rows, row)
	}
	return rows
}

func templateGrantRows(grants []adminopenapi.AuthorizationTemplateGrant) []grantRow {
	rows := make([]grantRow, 0, len(grants))
	for _, grant := range grants {
		row := grantRow{Principal: grant.Principal, ResourcePrefix: optional(grant.ResourcePrefix)}
		for _, scope := range grant.Scopes {
			row.Scopes = append(row.Scopes, string(scope))
		}
		rows = append(rows, row)
	}
	return rows
}

func renderGrantRows(w io.Writer, rows []grantRow) error {
	if len(rows) == 0 {
		return emptyLine(w, "grants")
	}
	table := [][]string{{"PRINCIPAL", "SCOPES", "RESOURCE PREFIX"}}
	for _, row := range rows {
		table = append(table, []string{row.Principal, strings.Join(row.Scopes, ","), row.ResourcePrefix})
	}
	return writeColumns(w, table)
}

// grantSet is a repository's grants with the version that conditions the
// next whole-set write (template apply).
type grantSet struct {
	RepositoryID string     `json:"repositoryId"`
	Version      string     `json:"version"`
	Grants       []grantRow `json:"grants"`
}

// readGrantSet reads a repository's grants; their version travels in ETag.
func readGrantSet(ctx context.Context, client *apiClient, repo string) (grantSet, error) {
	var grants []adminopenapi.Grant
	response, err := client.sendJSON(ctx, http.MethodGet, repositorySubPath(repo, "grants"), requestOptions{idempotent: true}, &grants)
	if err != nil {
		return grantSet{}, err
	}
	return grantSet{RepositoryID: repo, Version: response.Header.Get("ETag"), Grants: grantRows(grants)}, nil
}

// grantWriteResult reads the grant list a write answers with. The body must
// be a JSON array and the new version must be present; anything else may
// hide an applied write and is reported as such.
func grantWriteResult(response *apiResponse, client *apiClient, repo string) (grantSet, error) {
	trimmed := strings.TrimSpace(string(response.Body))
	var grants []adminopenapi.Grant
	if !strings.HasPrefix(trimmed, "[") || json.Unmarshal(response.Body, &grants) != nil || response.Header.Get("ETag") == "" {
		return grantSet{}, incompleteResponse(response, client, "grant list")
	}
	for _, grant := range grants {
		if strings.TrimSpace(grant.Principal) == "" || grant.Scopes == nil {
			return grantSet{}, incompleteResponse(response, client, "grant list")
		}
	}
	return grantSet{RepositoryID: repo, Version: response.Header.Get("ETag"), Grants: grantRows(grants)}, nil
}

func validateGrantKey(principal, prefix string) error {
	trimmed := strings.TrimSpace(principal)
	if trimmed == "" || len(trimmed) > maxGrantPrincipal || strings.ContainsAny(trimmed, "\x00\r\n") {
		return &UsageError{Message: "--principal must be 1-512 characters without control characters", Hint: "for example --principal service-account:<id> or --principal user:alice"}
	}
	if len(prefix) > maxGrantResourcePrefx || strings.ContainsAny(prefix, "\x00\r\n") {
		return &UsageError{Message: "--resource-prefix must be at most 255 characters without control characters", Hint: "use the artifact path or coordinate prefix the grant applies to"}
	}
	return nil
}

func grantListCommand() command {
	var repo string
	var all bool
	return command{
		name:     "grant list",
		synopsis: "list a repository's grants, or every grant the caller administers",
		usage:    "agctl grant list (--repo ID | --all-repositories) [--json]",
		setFlags: func(fs *flag.FlagSet) {
			fs.StringVar(&repo, "repo", "", "repository ID")
			fs.BoolVar(&all, "all-repositories", false, "every grant on repositories the caller administers")
		},
		meta: commandMeta{
			Resource:     "grant",
			Scenarios:    []string{"ci", "admin"},
			Stage:        "m1",
			Endpoints:    []string{"GET " + repositoriesPath + "/{repositoryId}/grants", "GET " + repositoryGrantsPath},
			MinimumRole:  "repositories:admin (--all-repositories: filtered to administered repositories)",
			OutputSchema: "GrantSet | RepositoryGrantRecordList",
			DryRun:       "unsupported",
			Retry:        true,
		},
		run: func(ctx context.Context, env *commandContext) error {
			if all == (repo != "") {
				return &UsageError{Message: "choose one of --repo ID or --all-repositories", Hint: "--repo lists one repository's grants with their version"}
			}
			client, err := env.client()
			if err != nil {
				return err
			}
			if repo != "" {
				set, err := readGrantSet(ctx, client, repo)
				if err != nil {
					return err
				}
				meta := &envelopeMeta{Pagination: &paginationMeta{Mode: "none"}}
				return env.render(set, meta, func(w io.Writer) error {
					if _, err := fmt.Fprintf(w, "grant set version %s\n", set.Version); err != nil {
						return err
					}
					return renderGrantRows(w, set.Grants)
				})
			}
			var records []adminopenapi.RepositoryGrantRecord
			if err := client.getJSON(ctx, repositoryGrantsPath, nil, &records); err != nil {
				return err
			}
			// The server filters rows to administered repositories after reading
			// them all; a repository the caller cannot administer contributes no
			// rows, indistinguishable from one without grants.
			meta := &envelopeMeta{Pagination: &paginationMeta{Mode: "none"}, Scope: map[string]any{"filteredTo": "administered repositories"}}
			return env.render(newListPayload(records), meta, func(w io.Writer) error {
				if len(records) == 0 {
					return emptyLine(w, "grants on repositories this credential administers")
				}
				table := [][]string{{"REPOSITORY", "FORMAT", "PRINCIPAL", "SCOPES", "RESOURCE PREFIX"}}
				for _, record := range records {
					scopes := make([]string, 0, len(record.Scopes))
					for _, scope := range record.Scopes {
						scopes = append(scopes, string(scope))
					}
					table = append(table, []string{record.RepositoryName, string(record.Format), record.Principal, strings.Join(scopes, ","), optional(record.ResourcePrefix)})
				}
				return writeColumns(w, table)
			})
		},
	}
}

// grantChange is the data of `grant upsert` and `grant delete`.
type grantChange struct {
	RepositoryID string    `json:"repositoryId"`
	Change       string    `json:"change"`
	Before       *grantRow `json:"before"`
	After        *grantRow `json:"after"`
	Version      string    `json:"version,omitempty"`
}

func findGrant(rows []grantRow, key string) *grantRow {
	for index := range rows {
		if rows[index].key() == key {
			row := rows[index]
			return &row
		}
	}
	return nil
}

func renderGrantChange(w io.Writer, change grantChange) error {
	describe := func(row *grantRow) string {
		if row == nil {
			return "(none)"
		}
		return strings.Join(row.Scopes, ",")
	}
	key := change.After
	if key == nil {
		key = change.Before
	}
	target := key.Principal
	if key.ResourcePrefix != "" {
		target += " on " + key.ResourcePrefix
	}
	if change.Change == "deleted" {
		_, err := fmt.Fprintf(w, "deleted grant for %s (grant set version %s)\n", target, change.Version)
		return err
	}
	_, err := fmt.Fprintf(w, "%s grant for %s: %s -> %s (grant set version %s)\n", change.Change, target, describe(change.Before), describe(change.After), change.Version)
	return err
}

func grantUpsertCommand() command {
	var repo, principal, prefix string
	var scopes stringList
	return command{
		name:     "grant upsert",
		synopsis: "create or replace one grant (principal and resource prefix) without touching the others",
		usage:    "agctl grant upsert --repo ID --principal PRINCIPAL --scope SCOPE... [--resource-prefix PREFIX] [--dry-run] [--json]",
		setFlags: func(fs *flag.FlagSet) {
			fs.StringVar(&repo, "repo", "", "repository ID")
			fs.StringVar(&principal, "principal", "", "principal, for example service-account:<id> or user:alice")
			fs.Var(&scopes, "scope", "repositories:read, :write, :admin, or :intelligence (repeatable)")
			fs.StringVar(&prefix, "resource-prefix", "", "limit the grant to artifacts under this path or coordinate prefix")
		},
		meta: commandMeta{
			Resource:     "grant",
			Scenarios:    []string{"ci", "admin"},
			Stage:        "m1",
			Endpoints:    []string{"POST " + repositoriesPath + "/{repositoryId}/grants", "GET " + repositoriesPath + "/{repositoryId}/grants"},
			Mutating:     true,
			MinimumRole:  "repositories:admin",
			InputSchema:  "Grant",
			OutputSchema: "GrantChange",
			DryRun:       "client-plan",
			Retry:        true,
		},
		run: func(ctx context.Context, env *commandContext) error {
			if err := requireRepo(repo); err != nil {
				return err
			}
			if err := validateGrantKey(principal, prefix); err != nil {
				return err
			}
			if len(scopes) == 0 {
				return &UsageError{Message: "pass at least one --scope", Hint: "for example --scope repositories:read"}
			}
			seen := map[string]bool{}
			for _, scope := range scopes {
				if !grantScopes[scope] || seen[scope] {
					return &UsageError{Message: fmt.Sprintf("invalid or repeated --scope %q", scope), Hint: "use repositories:read, repositories:write, repositories:admin, or repositories:intelligence once each"}
				}
				seen[scope] = true
			}
			desired := grantRow{Principal: strings.TrimSpace(principal), Scopes: []string(scopes), ResourcePrefix: prefix}
			body := adminopenapi.Grant{Principal: desired.Principal}
			for _, scope := range desired.Scopes {
				body.Scopes = append(body.Scopes, adminopenapi.GrantScopes(scope))
			}
			if prefix != "" {
				body.ResourcePrefix = &prefix
			}
			client, err := env.client()
			if err != nil {
				return err
			}
			if env.globals.dryRun {
				set, err := readGrantSet(ctx, client, repo)
				if err != nil {
					return err
				}
				before := findGrant(set.Grants, desired.key())
				change := "create"
				if before != nil {
					change = "replace"
					if before.sameScopes(desired) {
						change = "unchanged"
					}
				}
				plan := writePlan{
					Mode:     "client-plan",
					Method:   http.MethodPost,
					Path:     repositorySubPath(repo, "grants"),
					Headers:  map[string]string{},
					Body:     body,
					Current:  grantChange{RepositoryID: repo, Change: change, Before: before, After: &desired, Version: set.Version},
					Verified: []string{"principal, scopes, and prefix shape", "the repository's current grant for this principal and prefix was read"},
					NotVerified: []string{
						"caller permission (repositories:admin)",
						"that the resource prefix is canonical for the repository's format",
					},
				}
				return env.render(plan, nil, func(w io.Writer) error { return renderPlan(w, plan) })
			}
			encoded, err := encodeBody(body)
			if err != nil {
				return err
			}
			// One grant row keyed by principal and prefix: sending the same row
			// again leaves the same state, so the request may be retried.
			response, err := client.sendJSON(ctx, http.MethodPost, repositorySubPath(repo, "grants"), requestOptions{body: encoded, idempotent: true}, nil)
			var set grantSet
			if err == nil {
				set, err = grantWriteResult(response, client, repo)
			}
			if err == nil {
				if written := findGrant(set.Grants, desired.key()); written == nil || !written.sameScopes(desired) {
					err = incompleteResponse(response, client, "grant list")
				}
			}
			if err != nil {
				if outcomeUnknown(err) {
					return unknownOutcomeError(err, unknownOutcome{Operation: "grant upsert", ID: repo, ReadBack: "agctl grant list --repo " + repo},
						fmt.Sprintf("the grant may have been written; run agctl grant list --repo %s, or re-run the same upsert, which is safe to repeat", repo))
				}
				return err
			}
			result := grantChange{RepositoryID: repo, Change: "upserted", After: &desired, Version: set.Version}
			return env.render(result, nil, func(w io.Writer) error { return renderGrantChange(w, result) })
		},
	}
}

func grantDeleteCommand() command {
	var repo, principal, prefix string
	return command{
		name:     "grant delete",
		synopsis: "remove one grant (principal and resource prefix) without touching the others",
		usage:    "agctl grant delete --repo ID --principal PRINCIPAL [--resource-prefix PREFIX] --yes [--dry-run] [--json]",
		setFlags: func(fs *flag.FlagSet) {
			fs.StringVar(&repo, "repo", "", "repository ID")
			fs.StringVar(&principal, "principal", "", "principal of the grant to remove")
			fs.StringVar(&prefix, "resource-prefix", "", "resource prefix of the grant to remove (empty: the repository-wide grant)")
		},
		meta: commandMeta{
			Resource:     "grant",
			Scenarios:    []string{"ci", "admin"},
			Stage:        "m1",
			Endpoints:    []string{"DELETE " + repositoriesPath + "/{repositoryId}/grants", "GET " + repositoriesPath + "/{repositoryId}/grants"},
			Mutating:     true,
			MinimumRole:  "repositories:admin",
			OutputSchema: "GrantChange",
			DryRun:       "client-plan",
		},
		run: func(ctx context.Context, env *commandContext) error {
			if err := requireRepo(repo); err != nil {
				return err
			}
			if strings.TrimSpace(principal) == "" {
				return &UsageError{Message: "missing required flag --principal", Hint: "agctl grant list --repo ID shows the grants"}
			}
			if !env.globals.dryRun && !env.globals.yes {
				return &UsageError{Message: "agctl grant delete needs --yes to remove a grant", Hint: "re-run with --yes to confirm, or with --dry-run to see what would happen"}
			}
			key := grantRow{Principal: strings.TrimSpace(principal), ResourcePrefix: prefix}.key()
			client, err := env.client()
			if err != nil {
				return err
			}
			query := url.Values{"principal": {strings.TrimSpace(principal)}}
			if prefix != "" {
				query.Set("resourcePrefix", prefix)
			}
			if env.globals.dryRun {
				set, err := readGrantSet(ctx, client, repo)
				if err != nil {
					return err
				}
				before := findGrant(set.Grants, key)
				plan := writePlan{
					Mode:        "client-plan",
					Method:      http.MethodDelete,
					Path:        repositorySubPath(repo, "grants") + "?" + query.Encode(),
					Headers:     map[string]string{},
					Current:     grantChange{RepositoryID: repo, Change: "delete", Before: before, Version: set.Version},
					Verified:    []string{"the repository's grants were read"},
					NotVerified: []string{"caller permission (repositories:admin)"},
				}
				if before == nil {
					plan.Verified = append(plan.Verified, "no such grant exists: the server would answer 404")
				}
				return env.render(plan, nil, func(w io.Writer) error { return renderPlan(w, plan) })
			}
			// Not retried: a repeat after a lost 204 answers 404 and would
			// misreport a removal that succeeded.
			response, err := client.sendJSON(ctx, http.MethodDelete, repositorySubPath(repo, "grants"), requestOptions{query: query}, nil)
			if err != nil {
				if outcomeUnknown(err) {
					return unknownOutcomeError(err, unknownOutcome{Operation: "grant delete", ID: repo, ReadBack: "agctl grant list --repo " + repo},
						fmt.Sprintf("the grant may have been removed; run agctl grant list --repo %s before retrying", repo))
				}
				return err
			}
			removed := grantRow{Principal: strings.TrimSpace(principal), ResourcePrefix: prefix}
			result := grantChange{RepositoryID: repo, Change: "deleted", Before: &removed, Version: response.Header.Get("ETag")}
			return env.render(result, nil, func(w io.Writer) error { return renderGrantChange(w, result) })
		},
	}
}

func renderRoles(w io.Writer, roles []adminopenapi.AuthorizationRole) error {
	if len(roles) == 0 {
		return emptyLine(w, "authorization roles")
	}
	table := [][]string{{"NAME", "SCOPES", "VERSION", "ID"}}
	for _, role := range roles {
		scopes := make([]string, 0, len(role.Scopes))
		for _, scope := range role.Scopes {
			scopes = append(scopes, string(scope))
		}
		table = append(table, []string{role.Name, strings.Join(scopes, ","), role.Version, role.Id.String()})
	}
	return writeColumns(w, table)
}

func renderTemplates(w io.Writer, templates []adminopenapi.AuthorizationTemplate) error {
	if len(templates) == 0 {
		return emptyLine(w, "authorization templates")
	}
	table := [][]string{{"NAME", "GRANTS", "VERSION", "ID"}}
	for _, template := range templates {
		table = append(table, []string{template.Name, fmt.Sprint(len(template.Grants)), template.Version, template.Id.String()})
	}
	return writeColumns(w, table)
}

// catalogueList reads one of the platform catalogues; neither pages.
func catalogueList[T any](name, synopsis, path, schema string, render func(io.Writer, []T) error) command {
	return command{
		name:     name,
		synopsis: synopsis,
		usage:    "agctl " + name + " [--json]",
		meta: commandMeta{
			Resource:     "authorization",
			Scenarios:    []string{"admin"},
			Stage:        "m1",
			Endpoints:    []string{"GET " + path},
			MinimumRole:  "platform admin",
			OutputSchema: schema,
			DryRun:       "unsupported",
			Retry:        true,
		},
		run: func(ctx context.Context, env *commandContext) error {
			client, err := env.client()
			if err != nil {
				return err
			}
			var items []T
			if err := client.getJSON(ctx, path, nil, &items); err != nil {
				return err
			}
			meta := &envelopeMeta{Pagination: &paginationMeta{Mode: "none"}}
			return env.render(newListPayload(items), meta, func(w io.Writer) error { return render(w, items) })
		},
	}
}

func roleListCommand() command {
	return catalogueList("role list", "list authorization roles (named scope sets)", rolesPath, "AuthorizationRoleList", renderRoles)
}

func templateListCommand() command {
	return catalogueList("template list", "list authorization templates", templatesPath, "AuthorizationTemplateList", renderTemplates)
}

func roleShowCommand() command {
	return command{
		name:        "role show",
		synopsis:    "show one authorization role",
		usage:       "agctl role show ID [--json]",
		positionals: 1,
		argsUsage:   "ID",
		meta: commandMeta{
			Resource: "authorization", Scenarios: []string{"admin"}, Stage: "m1",
			Endpoints: []string{"GET " + rolesPath + "/{roleId}"}, MinimumRole: "platform admin",
			OutputSchema: "AuthorizationRole", DryRun: "unsupported", Retry: true,
		},
		run: documentCommand[adminopenapi.AuthorizationRole]{
			request: func(env *commandContext) (string, url.Values, error) {
				return rolesPath + "/" + url.PathEscape(env.args[0]), nil, nil
			},
			render: func(w io.Writer, role adminopenapi.AuthorizationRole) error {
				return renderRoles(w, []adminopenapi.AuthorizationRole{role})
			},
		}.run,
	}
}

func templateShowCommand() command {
	return command{
		name:        "template show",
		synopsis:    "show one authorization template and its grants",
		usage:       "agctl template show ID [--json]",
		positionals: 1,
		argsUsage:   "ID",
		meta: commandMeta{
			Resource: "authorization", Scenarios: []string{"admin"}, Stage: "m1",
			Endpoints: []string{"GET " + templatesPath + "/{templateId}"}, MinimumRole: "platform admin",
			OutputSchema: "AuthorizationTemplate", DryRun: "unsupported", Retry: true,
		},
		run: documentCommand[adminopenapi.AuthorizationTemplate]{
			request: func(env *commandContext) (string, url.Values, error) {
				return templatesPath + "/" + url.PathEscape(env.args[0]), nil, nil
			},
			render: func(w io.Writer, template adminopenapi.AuthorizationTemplate) error {
				if err := keyValues(w, [][2]string{
					{"id", template.Id.String()}, {"name", template.Name}, {"description", optional(template.Description)}, {"version", template.Version},
				}); err != nil {
					return err
				}
				return renderGrantRows(w, templateGrantRows(template.Grants))
			},
		}.run,
	}
}

// grantDiff compares a repository's grants with the grants a template would
// leave: applying a template replaces the whole set.
type grantDiff struct {
	Added   []grantRow `json:"added"`
	Removed []grantRow `json:"removed"`
	Changed []grantRow `json:"changed"`
	Kept    []grantRow `json:"kept"`
}

func diffGrants(current, next []grantRow) grantDiff {
	diff := grantDiff{Added: []grantRow{}, Removed: []grantRow{}, Changed: []grantRow{}, Kept: []grantRow{}}
	for _, row := range next {
		before := findGrant(current, row.key())
		switch {
		case before == nil:
			diff.Added = append(diff.Added, row)
		case before.sameScopes(row):
			diff.Kept = append(diff.Kept, row)
		default:
			diff.Changed = append(diff.Changed, row)
		}
	}
	for _, row := range current {
		if findGrant(next, row.key()) == nil {
			diff.Removed = append(diff.Removed, row)
		}
	}
	return diff
}

func templateApplyCommand() command {
	var repo, ifMatch string
	return command{
		name:        "template apply",
		synopsis:    "replace a repository's grants with an authorization template's grants",
		usage:       "agctl template apply TEMPLATE_ID --repo ID --yes [--if-match VERSION] [--dry-run] [--json]",
		positionals: 1,
		argsUsage:   "TEMPLATE_ID",
		setFlags: func(fs *flag.FlagSet) {
			fs.StringVar(&repo, "repo", "", "repository ID whose grants are replaced")
			fs.StringVar(&ifMatch, "if-match", "", "apply only if the repository's grant set is still at this version (default: the version read just before)")
		},
		meta: commandMeta{
			Resource:  "grant",
			Scenarios: []string{"admin"},
			Stage:     "m1",
			Endpoints: []string{
				"POST " + templatesPath + "/{templateId}/apply",
				"GET " + repositoriesPath + "/{repositoryId}/grants",
				"GET " + templatesPath + "/{templateId}",
			},
			Mutating:     true,
			MinimumRole:  "repositories:admin on the target; the --dry-run diff also reads the template (platform admin)",
			InputSchema:  "ApplyAuthorizationTemplate",
			OutputSchema: "GrantSet",
			DryRun:       "client-plan",
		},
		run: func(ctx context.Context, env *commandContext) error {
			if err := requireRepo(repo); err != nil {
				return err
			}
			if env.flagSet["if-match"] && strings.TrimSpace(ifMatch) == "" {
				return &UsageError{Message: "--if-match cannot be empty", Hint: "pass the grant set version from agctl grant list --repo ID"}
			}
			if !env.globals.dryRun && !env.globals.yes {
				return &UsageError{Message: "agctl template apply needs --yes: it replaces every grant of the repository", Hint: "run it with --dry-run first to see the grants it adds and removes"}
			}
			client, err := env.client()
			if err != nil {
				return err
			}
			id := env.args[0]
			path := templatesPath + "/" + url.PathEscape(id) + "/apply"
			body := map[string]string{"repositoryId": repo}
			var current *grantSet
			if ifMatch == "" || env.globals.dryRun {
				set, err := readGrantSet(ctx, client, repo)
				if err != nil {
					return err
				}
				current = &set
				if ifMatch == "" {
					ifMatch = set.Version
				}
			}
			if env.globals.dryRun {
				var template adminopenapi.AuthorizationTemplate
				if err := client.getJSON(ctx, templatesPath+"/"+url.PathEscape(id), nil, &template); err != nil {
					return err
				}
				diff := diffGrants(current.Grants, templateGrantRows(template.Grants))
				plan := writePlan{
					Mode:     "client-plan",
					Method:   http.MethodPost,
					Path:     path,
					Headers:  map[string]string{"If-Match": ifMatch},
					Body:     body,
					Current:  map[string]any{"grantSetVersion": current.Version, "templateVersion": template.Version, "diff": diff},
					Verified: []string{"the repository's grants and the template were read; diff lists every grant added, removed, changed, and kept"},
					NotVerified: []string{
						"caller permission (repositories:admin on the target)",
						"that the template's resource prefixes are valid for the repository's format",
						"that the grant set version still matches when the template is applied",
					},
				}
				return env.render(plan, nil, func(w io.Writer) error {
					if err := renderPlan(w, plan); err != nil {
						return err
					}
					_, err := fmt.Fprintf(w, "grants: %d added, %d removed, %d changed, %d kept\n", len(diff.Added), len(diff.Removed), len(diff.Changed), len(diff.Kept))
					return err
				})
			}
			encoded, err := encodeBody(body)
			if err != nil {
				return err
			}
			// A conditional whole-set write is not retried: if a lost attempt
			// applied it, the retry would fail on the new version.
			response, err := client.sendJSON(ctx, http.MethodPost, path, requestOptions{body: encoded, header: http.Header{"If-Match": {ifMatch}}}, nil)
			var set grantSet
			if err == nil {
				set, err = grantWriteResult(response, client, repo)
			}
			if err != nil {
				var api *APIError
				if errors.As(err, &api) && api.HTTPStatus == http.StatusPreconditionFailed {
					api.Hint = fmt.Sprintf("the repository's grants changed since version %s and were not replaced; review them with agctl grant list --repo %s and apply again", ifMatch, repo)
				}
				if outcomeUnknown(err) {
					return unknownOutcomeError(err, unknownOutcome{Operation: "template apply", ID: repo, ReadBack: "agctl grant list --repo " + repo},
						fmt.Sprintf("the template may have been applied; compare agctl grant list --repo %s (version %s before) with the template before retrying", repo, ifMatch))
				}
				return err
			}
			return env.render(set, nil, func(w io.Writer) error {
				if _, err := fmt.Fprintf(w, "template %s applied; grant set version %s\n", id, set.Version); err != nil {
					return err
				}
				return renderGrantRows(w, set.Grants)
			})
		},
	}
}

// accessDeniedError fails `access check --require` when a required
// permission is not granted. It exits 6 like a refused request.
type accessDeniedError struct {
	Message string
	Hint    string
}

func (e *accessDeniedError) Error() string { return e.Message }

func accessCheckCommand() command {
	var repo, actor, role, resource string
	var require stringList
	return command{
		name:     "access check",
		synopsis: "explain what an actor may do on a repository; with --require, exit 6 when it may not",
		usage:    "agctl access check --repo ID [--actor ACTOR [--role member|admin]] [--resource PATH] [--require read|write|admin|intelligence]... [--json]",
		setFlags: func(fs *flag.FlagSet) {
			fs.StringVar(&repo, "repo", "", "repository ID")
			fs.StringVar(&actor, "actor", "", "explain another actor (platform admin only; default: the caller)")
			fs.StringVar(&role, "role", "", "simulate the actor's global role: member or admin (with --actor)")
			fs.StringVar(&resource, "resource", "", "artifact path or coordinate, for resource-prefix grants")
			fs.Var(&require, "require", "permission that must be allowed (repeatable); exit 6 otherwise")
		},
		meta: commandMeta{
			Resource:     "grant",
			Scenarios:    []string{"ci", "admin"},
			Stage:        "m1",
			Endpoints:    []string{"GET " + repositoriesPath + "/{repositoryId}/effective-access"},
			MinimumRole:  "the caller itself; --actor needs platform admin",
			OutputSchema: "RepositoryEffectiveAccess",
			DryRun:       "unsupported",
			Retry:        true,
		},
		run: func(ctx context.Context, env *commandContext) error {
			if err := requireRepo(repo); err != nil {
				return err
			}
			if role != "" && (actor == "" || (role != "member" && role != "admin")) {
				return &UsageError{Message: "--role needs --actor and must be member or admin", Hint: "simulate another actor's global role, for example --actor user:alice --role member"}
			}
			for _, permission := range require {
				if permission != "read" && permission != "write" && permission != "admin" && permission != "intelligence" {
					return &UsageError{Message: fmt.Sprintf("invalid --require %q", permission), Hint: "use read, write, admin, or intelligence"}
				}
			}
			query := url.Values{}
			for name, value := range map[string]string{"actor": actor, "role": role, "resource": resource} {
				if value != "" {
					query.Set(name, value)
				}
			}
			client, err := env.client()
			if err != nil {
				return err
			}
			raw, access, err := readDocument[adminopenapi.RepositoryEffectiveAccess](ctx, client, repositorySubPath(repo, "effective-access"), query)
			if err != nil {
				return err
			}
			decisions := map[string]adminopenapi.EffectiveAccessDecision{
				"read": access.Permissions.Read, "write": access.Permissions.Write,
				"admin": access.Permissions.Admin, "intelligence": access.Permissions.Intelligence,
			}
			human := func(w io.Writer) error { return renderAccess(w, access, decisions) }
			var denied []string
			for _, permission := range require {
				if !decisions[permission].Allowed {
					denied = append(denied, fmt.Sprintf("%s (%s)", permission, decisions[permission].Reason))
				}
			}
			if len(denied) > 0 {
				return &resultError{
					err: &accessDeniedError{
						Message: fmt.Sprintf("%s may not %s on repository %s", access.Actor, strings.Join(denied, ", "), access.Repository.Name),
						Hint:    "grant the missing scope with agctl grant upsert, or check the resource prefix with --resource",
					},
					data:  raw,
					human: human,
				}
			}
			return env.render(raw, nil, human)
		},
	}
}

func renderAccess(w io.Writer, access adminopenapi.RepositoryEffectiveAccess, decisions map[string]adminopenapi.EffectiveAccessDecision) error {
	subject := access.Actor
	if access.Simulated {
		subject += " (simulated)"
	}
	target := access.Repository.Name
	if access.Resource != "" {
		target += " at " + access.Resource
	}
	if _, err := fmt.Fprintf(w, "%s on %s:\n", subject, target); err != nil {
		return err
	}
	table := [][]string{{"PERMISSION", "ALLOWED", "SOURCE", "REASON"}}
	for _, permission := range []string{"read", "write", "admin", "intelligence"} {
		decision := decisions[permission]
		table = append(table, []string{permission, yesNo(decision.Allowed), decision.Source, decision.Reason})
	}
	table = append(table, []string{"anonymous read", yesNo(access.AnonymousRead.Allowed), access.AnonymousRead.Source, access.AnonymousRead.Reason})
	return writeColumns(w, table)
}

func accessAnonymousShowCommand() command {
	return command{
		name:     "access anonymous show",
		synopsis: "show the platform switch for anonymous reads",
		usage:    "agctl access anonymous show [--json]",
		meta: commandMeta{
			Resource: "authorization", Scenarios: []string{"admin"}, Stage: "m1",
			Endpoints: []string{"GET " + anonymousPolicyPath}, MinimumRole: "platform admin",
			OutputSchema: "AnonymousAccessPolicy", DryRun: "unsupported", Retry: true,
		},
		run: documentCommand[adminopenapi.AnonymousAccessPolicy]{
			request: func(*commandContext) (string, url.Values, error) { return anonymousPolicyPath, nil, nil },
			render:  renderAnonymousPolicy,
		}.run,
	}
}

func renderAnonymousPolicy(w io.Writer, policy adminopenapi.AnonymousAccessPolicy) error {
	_, err := fmt.Fprintf(w, "anonymous reads: %s (version %s)\nrepositories and Groups still need their own anonymous read setting\n", map[bool]string{true: "enabled", false: "disabled"}[policy.Enabled], policy.Version)
	return err
}

func accessAnonymousSetCommand() command {
	var enabled bool
	var ifMatch string
	return command{
		name:     "access anonymous set",
		synopsis: "turn the platform switch for anonymous reads on or off (conditional on its version)",
		usage:    "agctl access anonymous set --enabled=true|false [--if-match VERSION] [--yes] [--dry-run] [--json]",
		setFlags: func(fs *flag.FlagSet) {
			fs.BoolVar(&enabled, "enabled", false, "allow anonymous reads where a repository or Group also allows them")
			fs.StringVar(&ifMatch, "if-match", "", "apply only if the policy is still at this version (default: the version read just before)")
		},
		meta: commandMeta{
			Resource:     "authorization",
			Scenarios:    []string{"admin"},
			Stage:        "m1",
			Endpoints:    []string{"PUT " + anonymousPolicyPath, "GET " + anonymousPolicyPath},
			Mutating:     true,
			MinimumRole:  "platform admin",
			InputSchema:  "AnonymousAccessPolicy",
			OutputSchema: "AnonymousAccessPolicy",
			DryRun:       "client-plan",
		},
		run: func(ctx context.Context, env *commandContext) error {
			if !env.flagSet["enabled"] {
				return &UsageError{Message: "missing required flag --enabled", Hint: "pass --enabled=true or --enabled=false"}
			}
			if env.flagSet["if-match"] && strings.TrimSpace(ifMatch) == "" {
				return &UsageError{Message: "--if-match cannot be empty", Hint: "pass the version from agctl access anonymous show"}
			}
			// Enabling widens who can read; it needs explicit confirmation.
			if enabled && !env.globals.dryRun && !env.globals.yes {
				return &UsageError{Message: "enabling anonymous reads needs --yes", Hint: "anonymous reads then apply to every repository and Group that allows them; re-run with --yes"}
			}
			client, err := env.client()
			if err != nil {
				return err
			}
			var current *adminopenapi.AnonymousAccessPolicy
			if ifMatch == "" {
				var policy adminopenapi.AnonymousAccessPolicy
				if err := client.getJSON(ctx, anonymousPolicyPath, nil, &policy); err != nil {
					return err
				}
				current = &policy
				ifMatch = policy.Version
			}
			body := adminopenapi.AnonymousAccessPolicy{Version: ifMatch, Enabled: enabled}
			if env.globals.dryRun {
				plan := writePlan{
					Mode: "client-plan", Method: http.MethodPut, Path: anonymousPolicyPath,
					Headers: map[string]string{"If-Match": ifMatch}, Body: body,
					Verified:    []string{"--enabled is given"},
					NotVerified: []string{"caller permission (platform admin)", "that the version still matches when the policy is sent"},
				}
				if current != nil {
					plan.Current = current
					plan.Verified = append(plan.Verified, "the current policy and version were read")
				}
				return env.render(plan, nil, func(w io.Writer) error { return renderPlan(w, plan) })
			}
			encoded, err := encodeBody(body)
			if err != nil {
				return err
			}
			// Conditional and therefore not retried.
			var policy adminopenapi.AnonymousAccessPolicy
			response, err := client.sendJSON(ctx, http.MethodPut, anonymousPolicyPath, requestOptions{body: encoded, header: http.Header{"If-Match": {ifMatch}}}, &policy)
			if err == nil {
				// A bool's zero value cannot distinguish a confirmed false from
				// a missing/null enabled field in a truncated success response.
				var fields struct {
					Enabled *bool `json:"enabled"`
				}
				if json.Unmarshal(response.Body, &fields) != nil || fields.Enabled == nil || policy.Version == "" || policy.Enabled != enabled {
					err = incompleteResponse(response, client, "anonymous access policy")
				}
			}
			if err != nil {
				var api *APIError
				if errors.As(err, &api) && api.HTTPStatus == http.StatusPreconditionFailed {
					api.Hint = fmt.Sprintf("the policy changed since version %s and was not replaced; run agctl access anonymous show and retry", ifMatch)
				}
				if outcomeUnknown(err) {
					return unknownOutcomeError(err, unknownOutcome{Operation: "access anonymous set", ReadBack: "agctl access anonymous show"},
						fmt.Sprintf("the policy may have been changed; run agctl access anonymous show and compare its version with %s before retrying", ifMatch))
				}
				return err
			}
			return env.render(policy, nil, func(w io.Writer) error { return renderAnonymousPolicy(w, policy) })
		},
	}
}
