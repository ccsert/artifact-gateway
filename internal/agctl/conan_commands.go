package agctl

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	adminopenapi "github.com/ccsert/artifact-gateway/internal/admin/openapi"
)

// conanSelection identifies the server's recipe/package scope. Conan references
// use the management API's path form, not the native client's @ notation.
type conanSelection struct {
	repo, reference, recipeRevision, packageID string
}

func (s *conanSelection) flags(fs *flag.FlagSet, recipe, pkg bool) {
	fs.StringVar(&s.repo, "repo", "", "repository ID")
	fs.StringVar(&s.reference, "reference", "", "recipe reference: name/version/user/channel")
	if recipe {
		fs.StringVar(&s.recipeRevision, "recipe-revision", "", "recipe revision")
	}
	if pkg {
		fs.StringVar(&s.packageID, "package-id", "", "package ID")
	}
}

func (s *conanSelection) query(recipe, pkg bool) (url.Values, error) {
	if err := requireRepo(s.repo); err != nil {
		return nil, err
	}
	s.reference = strings.TrimSuffix(strings.TrimSpace(s.reference), "/")
	s.recipeRevision = strings.TrimSpace(s.recipeRevision)
	s.packageID = strings.TrimSpace(s.packageID)
	values := url.Values{"reference": {s.reference}}
	for _, field := range []struct{ flag, key, value string }{
		{"reference", "reference", s.reference},
		{"recipe-revision", "recipeRevision", s.recipeRevision},
		{"package-id", "packageId", s.packageID},
	} {
		if field.flag == "recipe-revision" && !recipe || field.flag == "package-id" && !pkg {
			continue
		}
		if err := requireFlag(field.flag, field.value, "pass --"+field.flag); err != nil {
			return nil, err
		}
		values.Set(field.key, field.value)
	}
	return values, nil
}

func (s conanSelection) scope() map[string]any {
	scope := map[string]any{"repositoryId": s.repo, "reference": s.reference}
	if s.recipeRevision != "" {
		scope["recipeRevision"] = s.recipeRevision
	}
	if s.packageID != "" {
		scope["packageId"] = s.packageID
	}
	return scope
}

func conanReadMeta(sub, schema string, paged bool) commandMeta {
	return commandMeta{Resource: "artifact", Scenarios: []string{"ci", "admin"}, Stage: "m2",
		Endpoints:   []string{"GET " + repositoriesPath + "/{repositoryId}/conan/" + sub},
		MinimumRole: browseRole, OutputSchema: schema, Pagination: paged, DryRun: "unsupported", Retry: true}
}

func conanReferencesCommand() command {
	var repo, query string
	list := &cursorList[adminopenapi.ConanReference]{noun: "references", render: func(w io.Writer, items []adminopenapi.ConanReference) error {
		if len(items) == 0 {
			return emptyLine(w, "Conan references")
		}
		rows := [][]string{{"REFERENCE", "PUBLISHER"}}
		for _, item := range items {
			rows = append(rows, []string{item.Reference, optional(item.Publisher)})
		}
		return writeColumns(w, rows)
	}}
	list.request = func(*commandContext) (string, url.Values, error) {
		if err := requireRepo(repo); err != nil {
			return "", nil, err
		}
		values := url.Values{}
		if query != "" {
			values.Set("q", query)
		}
		return repositorySubPath(repo, "conan/references"), values, nil
	}
	list.scope = func(json.RawMessage) map[string]any {
		scope := map[string]any{"repositoryId": repo}
		if query != "" {
			scope["q"] = query
		}
		return scope
	}
	return command{name: "conan references", synopsis: "list visible Conan recipe references",
		usage: "agctl conan references --repo ID [--q PREFIX] [--page-size N] [--cursor TOKEN | --all] [--json]",
		setFlags: func(fs *flag.FlagSet) {
			fs.StringVar(&repo, "repo", "", "repository ID")
			fs.StringVar(&query, "q", "", "reference prefix filter")
			list.setFlags(fs)
		}, meta: conanReadMeta("references", "ConanReferencePage", true), run: list.run}
}

func conanRecipeRevisionsCommand() command {
	var selected conanSelection
	var query string
	list := &cursorList[adminopenapi.ConanRecipeRevision]{noun: "recipe revisions", render: func(w io.Writer, items []adminopenapi.ConanRecipeRevision) error {
		if len(items) == 0 {
			return emptyLine(w, "Conan recipe revisions")
		}
		rows := [][]string{{"REFERENCE", "REVISION", "DIGEST", "STATE", "CREATED"}}
		for _, item := range items {
			rows = append(rows, []string{item.Reference, item.Revision, item.Digest, string(item.State), formatTime(&item.CreatedAt)})
		}
		return writeColumns(w, rows)
	}}
	list.request = func(*commandContext) (string, url.Values, error) {
		values, err := selected.query(false, false)
		if err != nil {
			return "", nil, err
		}
		if query != "" {
			values.Set("q", query)
		}
		return repositorySubPath(selected.repo, "conan/recipe-revisions"), values, err
	}
	list.scope = func(json.RawMessage) map[string]any {
		scope := selected.scope()
		if query != "" {
			scope["q"] = query
		}
		return scope
	}
	return command{name: "conan recipe-revisions", synopsis: "list visible revisions of one Conan recipe",
		usage: "agctl conan recipe-revisions --repo ID --reference NAME/VERSION/USER/CHANNEL [--q QUERY] [--page-size N] [--cursor TOKEN | --all] [--json]",
		setFlags: func(fs *flag.FlagSet) {
			selected.flags(fs, false, false)
			fs.StringVar(&query, "q", "", "revision ID or digest filter")
			list.setFlags(fs)
		}, meta: conanReadMeta("recipe-revisions", "ConanRecipeRevisionList", true), run: list.run}
}

// These two endpoints return the whole selected collection and have no cursor.
// Do not offer --all/page-size or describe one response as a paginated window.
func conanCollectionCommand[T any](name, synopsis, sub, schema string, pkg bool, render func(io.Writer, []T) error) command {
	var selected conanSelection
	usage := "agctl " + name + " --repo ID --reference NAME/VERSION/USER/CHANNEL --recipe-revision REVISION"
	if pkg {
		usage += " --package-id ID"
	}
	return command{name: name, synopsis: synopsis, usage: usage + " [--json]",
		setFlags: func(fs *flag.FlagSet) { selected.flags(fs, true, pkg) },
		meta:     conanReadMeta(sub, schema, false),
		run: func(ctx context.Context, env *commandContext) error {
			query, err := selected.query(true, pkg)
			if err != nil {
				return err
			}
			client, err := env.client()
			if err != nil {
				return err
			}
			items, err := readConanCollection[T](ctx, client, repositorySubPath(selected.repo, "conan/"+sub), query)
			if err != nil {
				return err
			}
			meta := &envelopeMeta{Pagination: &paginationMeta{Mode: "none"}, Scope: selected.scope()}
			return env.render(newListPayload(items), meta, func(w io.Writer) error { return render(w, items) })
		}}
}

func readConanCollection[T any](ctx context.Context, client *apiClient, path string, query url.Values) ([]T, error) {
	var page struct {
		Items *[]T `json:"items"`
	}
	response, err := client.sendJSON(ctx, http.MethodGet, path, requestOptions{query: query, idempotent: true}, &page)
	if err != nil {
		return nil, err
	}
	if page.Items == nil {
		return nil, incompleteResponse(response, client, "Conan collection")
	}
	return *page.Items, nil
}

func conanPackageIDsCommand() command {
	return conanCollectionCommand("conan package-ids", "list visible package IDs of one Conan recipe revision", "package-ids", "ConanPackageIdList", false,
		func(w io.Writer, items []string) error {
			if len(items) == 0 {
				return emptyLine(w, "Conan package IDs")
			}
			rows := [][]string{{"PACKAGE ID"}}
			for _, id := range items {
				rows = append(rows, []string{id})
			}
			return writeColumns(w, rows)
		})
}

func conanPackageRevisionsCommand() command {
	return conanCollectionCommand("conan package-revisions", "list visible revisions of one Conan package", "package-revisions", "ConanPackageRevisionList", true,
		func(w io.Writer, items []adminopenapi.ConanPackageRevision) error {
			if len(items) == 0 {
				return emptyLine(w, "Conan package revisions")
			}
			rows := [][]string{{"REFERENCE", "RECIPE REVISION", "PACKAGE ID", "REVISION", "DIGEST", "STATE", "CREATED"}}
			for _, item := range items {
				rows = append(rows, []string{item.Reference, item.RecipeRevision, item.PackageId, item.Revision, item.Digest, string(item.State), formatTime(&item.CreatedAt)})
			}
			return writeColumns(w, rows)
		})
}

type conanPackageRevisionDeletion struct {
	RepositoryID   string `json:"repositoryId"`
	Reference      string `json:"reference"`
	RecipeRevision string `json:"recipeRevision"`
	PackageID      string `json:"packageId"`
	Revision       string `json:"revision"`
	Status         string `json:"status"`
	Visibility     string `json:"visibility"`
}

func conanPackageRevisionDeleteCommand() command {
	var selected conanSelection
	var revision string
	return command{name: "conan package-revision delete", synopsis: "tombstone one Conan package revision and verify it is no longer listed",
		usage: "agctl conan package-revision delete --repo ID --reference NAME/VERSION/USER/CHANNEL --recipe-revision REVISION --package-id ID --revision REVISION --yes [--dry-run] [--json]",
		setFlags: func(fs *flag.FlagSet) {
			selected.flags(fs, true, true)
			fs.StringVar(&revision, "revision", "", "package revision to tombstone")
		}, meta: commandMeta{Resource: "artifact", Scenarios: []string{"admin"}, Stage: "m2",
			Endpoints: []string{"DELETE " + repositoriesPath + "/{repositoryId}/conan/package-revisions/{revision}", "GET " + repositoriesPath + "/{repositoryId}/conan/package-revisions"},
			Mutating:  true, MinimumRole: "repositories:write; read-back requires repositories:read", OutputSchema: "ConanPackageRevisionDeletion", DryRun: "client-plan", Retry: true},
		run: func(ctx context.Context, env *commandContext) error {
			query, err := selected.query(true, true)
			if err != nil {
				return err
			}
			revision = strings.TrimSpace(revision)
			if err := requireFlag("revision", revision, "pass the exact package revision with --revision"); err != nil {
				return err
			}
			if !env.globals.dryRun && !env.globals.yes {
				return &UsageError{Message: "conan package-revision delete needs --yes", Hint: "use --dry-run to inspect the selected revision or --yes to tombstone it"}
			}
			client, err := env.client()
			if err != nil {
				return err
			}
			path := repositorySubPath(selected.repo, "conan/package-revisions")
			deletePath := path + "/" + url.PathEscape(revision)
			if env.globals.dryRun {
				items, err := readConanCollection[adminopenapi.ConanPackageRevision](ctx, client, path, query)
				if err != nil {
					return err
				}
				var current *adminopenapi.ConanPackageRevision
				for _, item := range items {
					if item.Reference == selected.reference && item.RecipeRevision == selected.recipeRevision && item.PackageId == selected.packageID && item.Revision == revision {
						current = &item
						break
					}
				}
				plan := writePlan{Mode: "client-plan", Method: http.MethodDelete, Path: deletePath + "?" + query.Encode(), Headers: map[string]string{},
					NotVerified: []string{"caller permission (repositories:write)", "tombstone state when already absent", "byte reclamation or references held by lifecycle jobs"}}
				if current != nil {
					encoded, _ := json.Marshal(current)
					plan.Current = json.RawMessage(encoded)
					plan.Verified = []string{"the exact package revision is currently visible"}
				} else {
					plan.Verified = []string{"the exact package revision is not currently listed; absence does not prove it was tombstoned"}
				}
				return env.render(plan, nil, func(w io.Writer) error { return renderPlan(w, plan) })
			}
			quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }
			readBack := fmt.Sprintf("agctl conan package-revisions --repo %s --reference %s --recipe-revision %s --package-id %s --json",
				quote(selected.repo), quote(selected.reference), quote(selected.recipeRevision), quote(selected.packageID))
			unknown := func(err error) error {
				return unknownOutcomeError(err, unknownOutcome{Operation: "conan package-revision delete", ID: selected.reference + "#" + selected.recipeRevision + "/" + selected.packageID + "#" + revision, ReadBack: readBack}, "the revision may have been tombstoned; read the exact package's visible revisions before retrying")
			}
			response, err := client.sendJSON(ctx, http.MethodDelete, deletePath, requestOptions{query: query, idempotent: true}, nil)
			if err != nil {
				if outcomeUnknown(err) {
					return unknown(err)
				}
				// A refusal after an uncertain attempt cannot disprove a
				// tombstone already applied by that earlier attempt. Keep the
				// final refusal's status and do not probe the revoked scope.
				if response != nil {
					for _, prior := range response.priorErrors {
						if outcomeUnknown(prior) {
							return unknown(err)
						}
					}
				}
				return err
			}
			if response.StatusCode != http.StatusNoContent {
				return unknown(incompleteResponse(response, client, "Conan deletion acknowledgement (expected HTTP 204)"))
			}
			items, err := readConanCollection[adminopenapi.ConanPackageRevision](ctx, client, path, query)
			if err != nil {
				return unknown(err)
			}
			for _, item := range items {
				if item.Reference != selected.reference || item.RecipeRevision != selected.recipeRevision || item.PackageId != selected.packageID || item.Revision == "" || item.Revision == revision {
					return unknown(incompleteResponse(response, client, "Conan deletion read-back"))
				}
			}
			result := conanPackageRevisionDeletion{selected.repo, selected.reference, selected.recipeRevision, selected.packageID, revision, "tombstoned", "not_listed"}
			return env.render(result, nil, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "Conan package revision %s#%s/%s#%s tombstoned and no longer listed; bytes are reclaimed separately\n", selected.reference, selected.recipeRevision, selected.packageID, revision)
				return err
			})
		}}
}
