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

	adminopenapi "github.com/ccsert/artifact-gateway/internal/admin/openapi"
)

const artifactSearchPath = managementPrefix + "/artifact-search"

// Reads of repository content follow the repository's browse authorization:
// repositories:read, or anonymous read where the repository and the platform
// policy allow it. The server decides.
const browseRole = "repositories:read (or anonymous read where allowed)"

func repositorySubPath(id, sub string) string {
	return repositoryPath(id) + "/" + sub
}

// copyListFlags copies the paging flags of one list into another, so a command
// that reads one of two endpoints registers its flags once.
func copyListFlags[A, B any](from *cursorList[A], to *cursorList[B]) {
	to.pageSize, to.maxPages, to.cursor, to.all = from.pageSize, from.maxPages, from.cursor, from.all
}

func renderSearchHits(w io.Writer, items []adminopenapi.GlobalArtifactSearchHit) error {
	if len(items) == 0 {
		return emptyLine(w, "matching artifacts visible to this credential")
	}
	rows := [][]string{{"REPOSITORY", "FORMAT", "COORDINATE", "VERSION", "SIZE", "DIGEST"}}
	for _, hit := range items {
		rows = append(rows, []string{hit.RepositoryName, string(hit.Format), hit.Coordinate, optional(hit.Version), formatBytes(hit.Size), shortDigest(optional(hit.Digest))})
	}
	return writeColumns(w, rows)
}

func renderSummaries(w io.Writer, items []adminopenapi.ArtifactSummary) error {
	if len(items) == 0 {
		return emptyLine(w, "matching artifacts")
	}
	rows := [][]string{{"COORDINATE", "VERSION", "SIZE", "CREATED", "DIGEST"}}
	for _, item := range items {
		rows = append(rows, []string{item.Coordinate, optional(item.Version), formatBytes(item.Size), formatTime(item.CreatedAt), shortDigest(optional(item.Digest))})
	}
	return writeColumns(w, rows)
}

func artifactSearchCommand() command {
	var repo, query, format, match string
	var global bool
	inRepo := &cursorList[adminopenapi.ArtifactSummary]{noun: "artifacts", render: renderSummaries}
	everywhere := &cursorList[adminopenapi.GlobalArtifactSearchHit]{
		path:   artifactSearchPath,
		noun:   "artifacts",
		render: renderSearchHits,
		scope: func(raw json.RawMessage) map[string]any {
			var page struct {
				SearchedRepositories *int `json:"searchedRepositories"`
			}
			if json.Unmarshal(raw, &page) != nil || page.SearchedRepositories == nil {
				return nil
			}
			return map[string]any{"searchedRepositories": *page.SearchedRepositories}
		},
	}
	return command{
		name:     "artifact search",
		synopsis: "search artifacts in one repository or across every repository the caller can read",
		usage:    "agctl artifact search (--repo ID | --global --q QUERY [--format FORMAT]) [--q QUERY] [--match prefix|contains] [--page-size N] [--cursor TOKEN | --all] [--json]",
		setFlags: func(fs *flag.FlagSet) {
			fs.StringVar(&repo, "repo", "", "repository ID to search")
			fs.BoolVar(&global, "global", false, "search every repository visible to the caller")
			fs.StringVar(&query, "q", "", "search text (required with --global)")
			fs.StringVar(&format, "format", "", "only this format (with --global)")
			fs.StringVar(&match, "match", "", "prefix or contains (server default when omitted)")
			everywhere.setFlags(fs)
		},
		meta: commandMeta{
			Resource:     "artifact",
			Scenarios:    []string{"ci", "admin"},
			Stage:        "m1",
			Endpoints:    []string{"GET " + artifactSearchPath, "GET " + repositoriesPath + "/{repositoryId}/artifact-search"},
			MinimumRole:  browseRole + "; --global filters to readable repositories",
			OutputSchema: "GlobalArtifactSearchPage | ArtifactSummaryPage",
			Pagination:   true,
			DryRun:       "unsupported",
			Retry:        true,
		},
		run: func(ctx context.Context, env *commandContext) error {
			if global == (repo != "") {
				return &UsageError{Message: "choose one of --repo ID or --global", Hint: "--repo searches one repository; --global searches every repository you can read"}
			}
			if match != "" && match != "prefix" && match != "contains" {
				return &UsageError{Message: fmt.Sprintf("invalid --match %q", match), Hint: "use --match prefix or --match contains"}
			}
			filters := url.Values{}
			if query != "" {
				filters.Set("q", query)
			}
			if match != "" {
				filters.Set("match", match)
			}
			if global {
				if err := requireFlag("q", query, "a global search needs search text, for example --q lodash"); err != nil {
					return err
				}
				if format != "" {
					if !adminopenapi.Format(format).Valid() {
						return &UsageError{Message: fmt.Sprintf("unknown format %q", format), Hint: "use one of apt, cargo, conan, go, maven, npm, oci, pypi, raw"}
					}
					filters.Set("format", format)
				}
				everywhere.request = func(*commandContext) (string, url.Values, error) { return artifactSearchPath, filters, nil }
				return everywhere.run(ctx, env)
			}
			if format != "" {
				return &UsageError{Message: "--format only applies to --global", Hint: "a repository search covers that repository's format"}
			}
			copyListFlags(everywhere, inRepo)
			inRepo.request = func(*commandContext) (string, url.Values, error) {
				return repositorySubPath(repo, "artifact-search"), filters, nil
			}
			return inRepo.run(ctx, env)
		},
	}
}

func renderBrowseNodes(w io.Writer, items []adminopenapi.BrowseNode) error {
	if len(items) == 0 {
		return emptyLine(w, "entries under this node")
	}
	rows := [][]string{{"KIND", "NAME", "SIZE", "SOURCE", "NODE ID"}}
	for _, node := range items {
		name := node.Name
		if node.HasChildren {
			name += "/"
		}
		rows = append(rows, []string{string(node.Kind), name, formatBytes(node.Size), optional(node.SourceRepositoryName), node.Id})
	}
	return writeColumns(w, rows)
}

func browseFlags(list *cursorList[adminopenapi.BrowseNode], parent *string) func(*flag.FlagSet) {
	return func(fs *flag.FlagSet) {
		fs.StringVar(parent, "parent", "", "node ID to list the children of (default: the root)")
		list.setFlags(fs)
	}
}

func browseFilters(parent string) url.Values {
	filters := url.Values{}
	if parent != "" {
		filters.Set("parent", parent)
	}
	return filters
}

func browseMeta(path string) commandMeta {
	return commandMeta{
		Resource:     "artifact",
		Scenarios:    []string{"ci", "admin"},
		Stage:        "m1",
		Endpoints:    []string{"GET " + path},
		MinimumRole:  browseRole,
		OutputSchema: "BrowseNodePage",
		Pagination:   true,
		DryRun:       "unsupported",
		Retry:        true,
	}
}

func artifactBrowseCommand() command {
	var repo, parent string
	list := &cursorList[adminopenapi.BrowseNode]{noun: "entries", render: renderBrowseNodes}
	list.request = func(*commandContext) (string, url.Values, error) {
		if err := requireRepo(repo); err != nil {
			return "", nil, err
		}
		return repositorySubPath(repo, "browse"), browseFilters(parent), nil
	}
	return command{
		name:     "artifact browse",
		synopsis: "browse a repository's tree one level at a time",
		usage:    "agctl artifact browse --repo ID [--parent NODE_ID] [--page-size N] [--cursor TOKEN | --all] [--json]",
		setFlags: func(fs *flag.FlagSet) {
			fs.StringVar(&repo, "repo", "", "repository ID to browse")
			browseFlags(list, &parent)(fs)
		},
		meta: browseMeta(repositoriesPath + "/{repositoryId}/browse"),
		run:  list.run,
	}
}

// repoBrowseCommand is `artifact browse` addressed like the other repo
// commands; both send the same request.
func repoBrowseCommand() command {
	var parent string
	list := &cursorList[adminopenapi.BrowseNode]{noun: "entries", render: renderBrowseNodes}
	list.request = func(env *commandContext) (string, url.Values, error) {
		return repositorySubPath(env.args[0], "browse"), browseFilters(parent), nil
	}
	meta := browseMeta(repositoriesPath + "/{repositoryId}/browse")
	meta.Resource = "repository"
	return command{
		name:        "repo browse",
		synopsis:    "browse a repository's tree (same as agctl artifact browse --repo ID)",
		usage:       "agctl repo browse ID [--parent NODE_ID] [--page-size N] [--cursor TOKEN | --all] [--json]",
		positionals: 1,
		argsUsage:   "ID",
		setFlags:    browseFlags(list, &parent),
		meta:        meta,
		run:         list.run,
	}
}

func groupBrowseCommand() command {
	var parent string
	list := &cursorList[adminopenapi.BrowseNode]{
		noun:   "entries",
		render: renderBrowseNodes,
		scope: func(raw json.RawMessage) map[string]any {
			var page struct {
				GroupID    string            `json:"groupId"`
				GroupName  string            `json:"groupName"`
				Format     string            `json:"format"`
				Candidates []json.RawMessage `json:"candidates"`
			}
			if json.Unmarshal(raw, &page) != nil {
				return nil
			}
			return map[string]any{"groupId": page.GroupID, "groupName": page.GroupName, "format": page.Format, "candidates": page.Candidates}
		},
	}
	list.request = func(env *commandContext) (string, url.Values, error) {
		return groupPath(env.args[0]) + "/browse", browseFilters(parent), nil
	}
	meta := browseMeta(groupsPath + "/{groupId}/browse")
	meta.Resource = "group"
	meta.MinimumRole = "read access to the members it shows (Maven and Raw Groups only)"
	meta.OutputSchema = "GroupBrowsePage"
	return command{
		name:        "group browse",
		synopsis:    "browse the union of a Group's readable members (Maven and Raw)",
		usage:       "agctl group browse ID [--parent NODE_ID] [--page-size N] [--cursor TOKEN | --all] [--json]",
		positionals: 1,
		argsUsage:   "ID",
		setFlags:    browseFlags(list, &parent),
		meta:        meta,
		run:         list.run,
	}
}

func renderArtifacts(w io.Writer, items []adminopenapi.Artifact) error {
	if len(items) == 0 {
		return emptyLine(w, "artifacts")
	}
	rows := [][]string{{"COORDINATE", "STATE", "CREATED", "DIGEST", "ID"}}
	for _, item := range items {
		created := item.CreatedAt
		rows = append(rows, []string{item.Coordinate, string(item.State), formatTime(&created), shortDigest(item.Digest), item.Id.String()})
	}
	return writeColumns(w, rows)
}

func artifactListCommand() command {
	var repo string
	return command{
		name:     "artifact list",
		synopsis: "list the published artifacts of a Maven hosted repository",
		usage:    "agctl artifact list --repo ID [--json]",
		setFlags: func(fs *flag.FlagSet) {
			fs.StringVar(&repo, "repo", "", "Maven hosted repository ID")
		},
		meta: commandMeta{
			Resource:     "artifact",
			Scenarios:    []string{"ci", "admin"},
			Stage:        "m1",
			Endpoints:    []string{"GET " + repositoriesPath + "/{repositoryId}/artifacts"},
			MinimumRole:  browseRole,
			OutputSchema: "ArtifactPage",
			DryRun:       "unsupported",
			Retry:        true,
		},
		run: func(ctx context.Context, env *commandContext) error {
			if err := requireRepo(repo); err != nil {
				return err
			}
			client, err := env.client()
			if err != nil {
				return err
			}
			// The endpoint answers every visible artifact at once; it takes no
			// page size and returns no cursor.
			var page serverPage[adminopenapi.Artifact]
			if err := client.getJSON(ctx, repositorySubPath(repo, "artifacts"), nil, &page); err != nil {
				return err
			}
			payload := newListPayload(page.Items)
			meta := &envelopeMeta{Pagination: &paginationMeta{Mode: "none"}}
			return env.render(payload, meta, func(w io.Writer) error { return renderArtifacts(w, page.Items) })
		},
	}
}

// artifactAspect is one optional part of `artifact show`: the record when it
// exists, or why it is absent. Absence is never reported as healthy.
type artifactAspect struct {
	Status string          `json:"status"`
	Record json.RawMessage `json:"record,omitempty"`
	Code   string          `json:"code,omitempty"`
}

type artifactDetail struct {
	Artifact     json.RawMessage `json:"artifact"`
	Intelligence artifactAspect  `json:"intelligence"`
	Quarantine   artifactAspect  `json:"quarantine"`
}

// readAspect reads one optional record. A 404 means none is recorded, a 401
// or 403 that this credential may not see it; another refusal leaves it
// unavailable. Connection failures still fail the command.
func readAspect(ctx context.Context, client *apiClient, path string, query url.Values) (artifactAspect, error) {
	var raw json.RawMessage
	err := client.getJSON(ctx, path, query, &raw)
	if err == nil {
		return artifactAspect{Status: "recorded", Record: raw}, nil
	}
	var api *APIError
	if !errors.As(err, &api) {
		return artifactAspect{}, err
	}
	switch api.HTTPStatus {
	case http.StatusNotFound:
		return artifactAspect{Status: "none"}, nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return artifactAspect{Status: "not_permitted", Code: api.Code}, nil
	}
	return artifactAspect{Status: "unavailable", Code: api.Code}, nil
}

func artifactShowCommand() command {
	var repo string
	return command{
		name:        "artifact show",
		synopsis:    "show a Maven hosted artifact with its intelligence and quarantine records",
		usage:       "agctl artifact show ARTIFACT_ID --repo ID [--json]",
		positionals: 1,
		argsUsage:   "ARTIFACT_ID",
		setFlags: func(fs *flag.FlagSet) {
			fs.StringVar(&repo, "repo", "", "Maven hosted repository ID")
		},
		meta: commandMeta{
			Resource:  "artifact",
			Scenarios: []string{"ci", "admin"},
			Stage:     "m1",
			Endpoints: []string{
				"GET " + repositoriesPath + "/{repositoryId}/artifacts/{artifactId}",
				"GET " + repositoriesPath + "/{repositoryId}/artifact-intelligence",
				"GET " + repositoriesPath + "/{repositoryId}/artifact-quarantine",
			},
			MinimumRole:  browseRole + "; quarantine needs repositories:read",
			OutputSchema: "ArtifactDetail",
			DryRun:       "unsupported",
			Retry:        true,
		},
		run: func(ctx context.Context, env *commandContext) error {
			if err := requireRepo(repo); err != nil {
				return err
			}
			client, err := env.client()
			if err != nil {
				return err
			}
			raw, artifact, err := readDocument[adminopenapi.Artifact](ctx, client, repositorySubPath(repo, "artifacts/"+url.PathEscape(env.args[0])), nil)
			if err != nil {
				return err
			}
			identity := url.Values{"coordinate": {artifact.Coordinate}, "digest": {artifact.Digest}}
			detail := artifactDetail{Artifact: raw}
			if detail.Intelligence, err = readAspect(ctx, client, repositorySubPath(repo, "artifact-intelligence"), identity); err != nil {
				return err
			}
			if detail.Quarantine, err = readAspect(ctx, client, repositorySubPath(repo, "artifact-quarantine"), identity); err != nil {
				return err
			}
			return env.render(detail, nil, func(w io.Writer) error {
				created := artifact.CreatedAt
				if err := keyValues(w, [][2]string{
					{"id", artifact.Id.String()},
					{"coordinate", artifact.Coordinate},
					{"state", string(artifact.State)},
					{"digest", artifact.Digest},
					{"created", formatTime(&created)},
					{"intelligence", describeAspect(detail.Intelligence, summarizeIntelligence)},
					{"quarantine", describeAspect(detail.Quarantine, summarizeQuarantine)},
				}); err != nil {
					return err
				}
				return nil
			})
		},
	}
}

func describeAspect(aspect artifactAspect, summarize func(json.RawMessage) string) string {
	switch aspect.Status {
	case "recorded":
		return summarize(aspect.Record)
	case "none":
		return "none recorded"
	case "not_permitted":
		return "not visible to this credential"
	}
	if aspect.Code != "" {
		return "unavailable (" + aspect.Code + ")"
	}
	return "unavailable"
}

func summarizeIntelligence(raw json.RawMessage) string {
	var record adminopenapi.ArtifactIntelligence
	if json.Unmarshal(raw, &record) != nil {
		return "recorded"
	}
	parts := []string{
		fmt.Sprintf("%d signatures", len(record.Signatures)),
		fmt.Sprintf("%d SBOMs", len(record.Sboms)),
		fmt.Sprintf("%d licenses", len(record.Licenses)),
	}
	if record.Provenance != nil {
		parts = append(parts, "provenance")
	}
	if record.Vulnerability == nil {
		parts = append(parts, "no vulnerability summary")
	}
	return strings.Join(parts, ", ")
}

func summarizeQuarantine(raw json.RawMessage) string {
	var record adminopenapi.ArtifactQuarantine
	if json.Unmarshal(raw, &record) != nil {
		return "recorded"
	}
	if record.Reason == "" {
		return string(record.State)
	}
	return fmt.Sprintf("%s (%s)", record.State, record.Reason)
}

// artifactDeletion is the data of `artifact delete`. The artifact is hidden
// (tombstoned) when the 202 arrives; byte reclamation stays with the
// format's lifecycle jobs.
type artifactDeletion struct {
	ID           string `json:"id"`
	RepositoryID string `json:"repositoryId"`
	Status       string `json:"status"`
	ServerState  string `json:"serverState"`
}

func artifactDeleteCommand() command {
	var repo string
	return command{
		name:        "artifact delete",
		synopsis:    "tombstone a Maven hosted artifact",
		usage:       "agctl artifact delete ARTIFACT_ID --repo ID --yes [--dry-run] [--json]",
		positionals: 1,
		argsUsage:   "ARTIFACT_ID",
		setFlags: func(fs *flag.FlagSet) {
			fs.StringVar(&repo, "repo", "", "Maven hosted repository ID")
		},
		meta: commandMeta{
			Resource:     "artifact",
			Scenarios:    []string{"admin"},
			Stage:        "m1",
			Endpoints:    []string{"DELETE " + repositoriesPath + "/{repositoryId}/artifacts/{artifactId}", "GET " + repositoriesPath + "/{repositoryId}/artifacts/{artifactId}"},
			Mutating:     true,
			MinimumRole:  "repositories:write",
			OutputSchema: "ArtifactDeletion",
			DryRun:       "client-plan",
			Retry:        true,
		},
		run: func(ctx context.Context, env *commandContext) error {
			if err := requireRepo(repo); err != nil {
				return err
			}
			id := env.args[0]
			if !env.globals.dryRun && !env.globals.yes {
				return &UsageError{Message: "agctl artifact delete needs --yes to delete an artifact", Hint: "re-run with --yes to confirm, or with --dry-run to see what would happen"}
			}
			client, err := env.client()
			if err != nil {
				return err
			}
			path := repositorySubPath(repo, "artifacts/"+url.PathEscape(id))
			if env.globals.dryRun {
				raw, artifact, err := readDocument[adminopenapi.Artifact](ctx, client, path, nil)
				if err != nil {
					return err
				}
				plan := writePlan{
					Mode:     "client-plan",
					Method:   http.MethodDelete,
					Path:     path,
					Headers:  map[string]string{},
					Current:  raw,
					Verified: []string{"the artifact exists and is readable"},
					NotVerified: []string{
						"caller permission (repositories:write)",
						"retention, promotion, or replication references that may still hold its bytes",
					},
				}
				if artifact.State == adminopenapi.ArtifactStateDeleted {
					plan.Verified = append(plan.Verified, "the artifact is already deleted: a delete would be accepted again without another change")
				}
				return env.render(plan, nil, func(w io.Writer) error { return renderPlan(w, plan) })
			}
			// Tombstoning an artifact that is already deleted is accepted again
			// without another change, so the request may be retried.
			var deletion adminopenapi.Deletion
			response, err := client.sendJSON(ctx, http.MethodDelete, path, requestOptions{idempotent: true}, &deletion)
			if err == nil && deletion.State == "" {
				err = incompleteResponse(response, client, "deletion")
			}
			if err != nil {
				if outcomeUnknown(err) {
					return unknownOutcomeError(err, unknownOutcome{
						Operation: "artifact delete",
						ID:        id,
						ReadBack:  fmt.Sprintf("agctl artifact show %s --repo %s", id, repo),
					}, fmt.Sprintf("the artifact may have been deleted; agctl artifact show %s --repo %s reports state deleted once it is", id, repo))
				}
				return err
			}
			result := artifactDeletion{ID: id, RepositoryID: repo, Status: "accepted", ServerState: string(deletion.State)}
			return env.render(result, nil, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "artifact %s deleted from repository %s (server state: %s)\n", id, repo, result.ServerState)
				return err
			})
		},
	}
}

func renderIdentities(w io.Writer, items []adminopenapi.ArtifactIdentity) error {
	if len(items) == 0 {
		return emptyLine(w, "artifact identities")
	}
	rows := [][]string{{"COORDINATE", "SIZE", "PUBLISHED", "DIGEST"}}
	for _, item := range items {
		published := item.PublishedAt
		rows = append(rows, []string{item.Coordinate, formatBytes(item.Size), formatTime(&published), item.Digest})
	}
	return writeColumns(w, rows)
}

func artifactIdentitiesCommand() command {
	var repo, purpose, query string
	var limit int
	return command{
		name:     "artifact identities",
		synopsis: "list canonical coordinate and digest pairs ready for scanning or distribution",
		usage:    "agctl artifact identities --repo ID --purpose scan|distribution [--q PREFIX] [--limit N] [--json]",
		setFlags: func(fs *flag.FlagSet) {
			fs.StringVar(&repo, "repo", "", "repository ID")
			fs.StringVar(&purpose, "purpose", "", "scan or distribution")
			fs.StringVar(&query, "q", "", "coordinate filter")
			fs.IntVar(&limit, "limit", defaultPageSize, "maximum identities to return (1-200; the endpoint has no cursor)")
		},
		meta: commandMeta{
			Resource:     "artifact",
			Scenarios:    []string{"ci", "admin"},
			Stage:        "m1",
			Endpoints:    []string{"GET " + repositoriesPath + "/{repositoryId}/artifact-identities"},
			MinimumRole:  "repositories:read",
			OutputSchema: "ArtifactIdentityPage",
			DryRun:       "unsupported",
			Retry:        true,
		},
		run: func(ctx context.Context, env *commandContext) error {
			if err := requireRepo(repo); err != nil {
				return err
			}
			if purpose != "scan" && purpose != "distribution" {
				return &UsageError{Message: "--purpose must be scan or distribution", Hint: "scan lists what scanners accept; distribution what promotion and replication accept"}
			}
			if limit < 1 || limit > maxPageSize {
				return &UsageError{Message: fmt.Sprintf("--limit %d is out of range", limit), Hint: "use a limit from 1 to 200"}
			}
			filters := url.Values{"purpose": {purpose}, "pageSize": {strconv.Itoa(limit)}}
			if query != "" {
				filters.Set("q", query)
			}
			client, err := env.client()
			if err != nil {
				return err
			}
			var page serverPage[adminopenapi.ArtifactIdentity]
			if err := client.getJSON(ctx, repositorySubPath(repo, "artifact-identities"), filters, &page); err != nil {
				return err
			}
			// The endpoint has no cursor: a full window may hide more identities.
			meta := &envelopeMeta{Pagination: &paginationMeta{Mode: "bounded", Limit: limit}}
			if len(page.Items) < limit {
				complete := true
				meta.Pagination.Complete = &complete
			}
			return env.render(newListPayload(page.Items), meta, func(w io.Writer) error {
				if err := renderIdentities(w, page.Items); err != nil {
					return err
				}
				if len(page.Items) >= limit {
					_, _ = fmt.Fprintf(env.stderr, "the window of %d is full; narrow it with --q or raise --limit (the endpoint has no cursor)\n", limit)
				}
				return nil
			})
		},
	}
}

func artifactUsageCommand() command {
	var repo, query string
	var limit, offset int
	return command{
		name:     "artifact usage",
		synopsis: "show per-artifact download counts and bytes for a repository",
		usage:    "agctl artifact usage --repo ID [--q PREFIX] [--limit N] [--offset N] [--json]",
		setFlags: func(fs *flag.FlagSet) {
			fs.StringVar(&repo, "repo", "", "repository ID")
			fs.StringVar(&query, "q", "", "resource filter")
			fs.IntVar(&limit, "limit", 100, "rows per window (1-500)")
			fs.IntVar(&offset, "offset", 0, "rows to skip (0-1000000)")
		},
		meta: commandMeta{
			Resource:     "artifact",
			Scenarios:    []string{"admin"},
			Stage:        "m1",
			Endpoints:    []string{"GET " + repositoriesPath + "/{repositoryId}/artifact-usage"},
			MinimumRole:  browseRole,
			OutputSchema: "RepositoryArtifactUsage",
			Pagination:   true,
			DryRun:       "unsupported",
			Retry:        true,
		},
		run: documentCommand[adminopenapi.RepositoryArtifactUsage]{
			request: func(*commandContext) (string, url.Values, error) {
				if err := requireRepo(repo); err != nil {
					return "", nil, err
				}
				if limit < 1 || limit > 500 || offset < 0 || offset > 1000000 {
					return "", nil, &UsageError{Message: "--limit or --offset is out of range", Hint: "use --limit 1-500 and --offset 0-1000000"}
				}
				filters := url.Values{"limit": {strconv.Itoa(limit)}, "offset": {strconv.Itoa(offset)}}
				if query != "" {
					filters.Set("q", query)
				}
				return repositorySubPath(repo, "artifact-usage"), filters, nil
			},
			meta: func(usage adminopenapi.RepositoryArtifactUsage) *envelopeMeta {
				total := int(usage.TotalCount)
				start := offset
				complete := offset+len(usage.Items) >= total
				return &envelopeMeta{Pagination: &paginationMeta{Mode: "offset", Limit: limit, Offset: &start, Total: &total, Complete: &complete}}
			},
			render: func(w io.Writer, usage adminopenapi.RepositoryArtifactUsage) error {
				if _, err := fmt.Fprintf(w, "%d resources, %d downloads, %s served (as of %s)\n", usage.Totals.Resources, usage.Totals.DownloadCount, humanBytes(usage.Totals.TotalBytes), usage.GeneratedAt.UTC().Format("2006-01-02T15:04:05Z")); err != nil {
					return err
				}
				if len(usage.Items) == 0 {
					return emptyLine(w, "downloads in this window")
				}
				rows := [][]string{{"RESOURCE", "FORMAT", "DOWNLOADS", "BYTES", "LAST"}}
				for _, item := range usage.Items {
					last := item.LastDownloadedAt
					rows = append(rows, []string{item.Resource, item.Format, strconv.FormatInt(item.DownloadCount, 10), humanBytes(item.TotalBytes), formatTime(&last)})
				}
				if err := writeColumns(w, rows); err != nil {
					return err
				}
				if next := offset + len(usage.Items); int64(next) < usage.TotalCount {
					_, err := fmt.Fprintf(w, "rows %d-%d of %d; continue with --offset %d\n", offset+1, next, usage.TotalCount, next)
					return err
				}
				return nil
			},
		}.run,
	}
}

// artifactRecordCommand reads the intelligence or quarantine record of one
// coordinate and digest.
func artifactRecordCommand(name, sub, synopsis, role, schema string, render func(io.Writer, json.RawMessage) error) command {
	var repo, coordinate, digest string
	return command{
		name:     name,
		synopsis: synopsis,
		usage:    "agctl " + name + " --repo ID --coordinate COORDINATE --digest DIGEST [--json]",
		setFlags: func(fs *flag.FlagSet) {
			fs.StringVar(&repo, "repo", "", "repository ID")
			fs.StringVar(&coordinate, "coordinate", "", "artifact coordinate")
			fs.StringVar(&digest, "digest", "", "artifact digest (sha256:…)")
		},
		meta: commandMeta{
			Resource:     "artifact",
			Scenarios:    []string{"ci", "admin"},
			Stage:        "m1",
			Endpoints:    []string{"GET " + repositoriesPath + "/{repositoryId}/" + sub},
			MinimumRole:  role,
			OutputSchema: schema,
			DryRun:       "unsupported",
			Retry:        true,
		},
		run: documentCommand[json.RawMessage]{
			request: func(*commandContext) (string, url.Values, error) {
				if err := requireRepo(repo); err != nil {
					return "", nil, err
				}
				if err := requireFlag("coordinate", coordinate, "pass the artifact coordinate, for example from agctl artifact identities"); err != nil {
					return "", nil, err
				}
				if err := requireFlag("digest", digest, "pass the artifact digest, for example from agctl artifact identities"); err != nil {
					return "", nil, err
				}
				return repositorySubPath(repo, sub), url.Values{"coordinate": {coordinate}, "digest": {digest}}, nil
			},
			render: render,
		}.run,
	}
}

func artifactIntelligenceShowCommand() command {
	return artifactRecordCommand("artifact intelligence show", "artifact-intelligence",
		"show the signatures, SBOMs, provenance, licenses, and vulnerability summary recorded for an artifact",
		browseRole, "ArtifactIntelligence",
		func(w io.Writer, raw json.RawMessage) error {
			var record adminopenapi.ArtifactIntelligence
			if err := json.Unmarshal(raw, &record); err != nil {
				return err
			}
			return keyValues(w, [][2]string{
				{"coordinate", record.Coordinate},
				{"digest", record.Digest},
				{"recorded", summarizeIntelligence(raw)},
				{"version", record.Version},
				{"updated", record.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z") + " by " + record.UpdatedBy},
			})
		})
}

func artifactQuarantineShowCommand() command {
	return artifactRecordCommand("artifact quarantine show", "artifact-quarantine",
		"show an artifact's quarantine record (quarantine blocks promotion and replication, not reads)",
		"repositories:read", "ArtifactQuarantine",
		func(w io.Writer, raw json.RawMessage) error {
			var record adminopenapi.ArtifactQuarantine
			if err := json.Unmarshal(raw, &record); err != nil {
				return err
			}
			return keyValues(w, [][2]string{
				{"coordinate", record.Coordinate},
				{"digest", record.Digest},
				{"state", string(record.State)},
				{"reason", record.Reason},
				{"version", record.Version},
				{"updated", record.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z") + " by " + record.UpdatedBy},
			})
		})
}

// formatViewCommand builds a cursor list scoped to one repository with an
// optional prefix filter, used by the format views.
func formatViewCommand[T any](name, synopsis, sub, schema, noun string, required string, render func(io.Writer, []T) error) command {
	var repo, query string
	list := &cursorList[T]{noun: noun, render: render}
	list.request = func(*commandContext) (string, url.Values, error) {
		if err := requireRepo(repo); err != nil {
			return "", nil, err
		}
		filters := url.Values{}
		if required != "" {
			if err := requireFlag(required, query, "pass --"+required); err != nil {
				return "", nil, err
			}
			filters.Set(required, query)
		} else if query != "" {
			filters.Set("q", query)
		}
		return repositorySubPath(repo, sub), filters, nil
	}
	filter, usage := "q", "[--q PREFIX]"
	if required != "" {
		filter, usage = required, "--"+required+" "+strings.ToUpper(required)
	}
	return command{
		name:     name,
		synopsis: synopsis,
		usage:    "agctl " + name + " --repo ID " + usage + " [--page-size N] [--cursor TOKEN | --all] [--json]",
		setFlags: func(fs *flag.FlagSet) {
			fs.StringVar(&repo, "repo", "", "repository ID")
			if filter == "q" {
				fs.StringVar(&query, "q", "", "prefix filter")
			} else {
				fs.StringVar(&query, filter, "", "image name")
			}
			list.setFlags(fs)
		},
		meta: commandMeta{
			Resource:     "artifact",
			Scenarios:    []string{"ci", "admin"},
			Stage:        "m1",
			Endpoints:    []string{"GET " + repositoriesPath + "/{repositoryId}/" + sub},
			MinimumRole:  browseRole,
			OutputSchema: schema,
			Pagination:   true,
			DryRun:       "unsupported",
			Retry:        true,
		},
		run: list.run,
	}
}

func ociImagesCommand() command {
	return formatViewCommand("oci images", "list the images of an OCI repository", "oci/images", "OCIImagePage", "images", "",
		func(w io.Writer, items []adminopenapi.OCIImage) error {
			if len(items) == 0 {
				return emptyLine(w, "images")
			}
			rows := [][]string{{"IMAGE"}}
			for _, item := range items {
				rows = append(rows, []string{item.Name})
			}
			return writeColumns(w, rows)
		})
}

func ociManifestsCommand() command {
	return formatViewCommand("oci manifests", "list the manifests of one OCI image, including untagged ones", "oci/manifests", "OCIManifestSummaryPage", "manifests", "name",
		func(w io.Writer, items []adminopenapi.OCIManifestSummary) error {
			if len(items) == 0 {
				return emptyLine(w, "manifests")
			}
			rows := [][]string{{"DIGEST", "TAGS", "MEDIA TYPE", "SIZE", "CREATED"}}
			for _, item := range items {
				created := item.CreatedAt
				size := item.Size
				rows = append(rows, []string{item.Digest, strings.Join(item.Tags, ","), item.MediaType, formatBytes(&size), formatTime(&created)})
			}
			return writeColumns(w, rows)
		})
}

func mavenCoordinatesCommand() command {
	return formatViewCommand("maven coordinates", "list the committed coordinates of a Maven repository", "maven/coordinates", "MavenCoordinatePage", "coordinates", "",
		func(w io.Writer, items []adminopenapi.MavenCoordinate) error {
			if len(items) == 0 {
				return emptyLine(w, "coordinates")
			}
			rows := [][]string{{"COORDINATE", "CREATED", "DIGEST"}}
			for _, item := range items {
				created := item.CreatedAt
				rows = append(rows, []string{item.Coordinate, formatTime(&created), shortDigest(item.Digest)})
			}
			return writeColumns(w, rows)
		})
}
