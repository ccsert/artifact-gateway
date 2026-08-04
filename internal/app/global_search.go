package app

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

type globalArtifactSearchCursor struct {
	Endpoint, Query, Format, RepositoryID, Coordinate string
	BuildNumber                                       int
	ExpiresAt                                         int64
}

func (h generatedRepositoryAPIAdapter) SearchArtifacts(w http.ResponseWriter, r *http.Request, params adminopenapi.SearchArtifactsParams) {
	principal, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	query := strings.TrimSpace(params.Q)
	if query == "" || len(query) > 255 || strings.ContainsRune(query, '\x00') {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "q must contain between 1 and 255 characters")
		return
	}
	format := repository.Format("")
	if params.Format != nil {
		format = repository.Format(*params.Format)
		if format != repository.FormatOCI && format != repository.FormatMaven && format != repository.FormatConan && format != repository.FormatRaw {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "format is not supported")
			return
		}
	}
	pageSize := 50
	if params.PageSize != nil {
		pageSize = int(*params.PageSize)
		if pageSize < 1 || pageSize > 200 {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "pageSize must be between 1 and 200")
			return
		}
	}
	pageToken := ""
	if params.PageToken != nil {
		pageToken = string(*params.PageToken)
	}
	cursor, err := h.decodeGlobalArtifactSearchCursor(pageToken, query, format)
	if err != nil {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_page_token", "page token is invalid or expired")
		return
	}

	repositories, err := h.readableSearchRepositories(r.Context(), principal, format, query)
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "list searchable repositories failed")
		return
	}
	start := 0
	if cursor.RepositoryID != "" {
		start = -1
		for index, repo := range repositories {
			if repo.ID == cursor.RepositoryID {
				start = index
				break
			}
		}
		if start < 0 {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_page_token", "page token no longer references a readable repository")
			return
		}
	}

	items := make([]adminopenapi.GlobalArtifactSearchHit, 0, pageSize)
	var next *string
	for index := start; index < len(repositories) && len(items) < pageSize; index++ {
		repo := repositories[index]
		after := artifactSearchPosition{}
		if repo.ID == cursor.RepositoryID {
			after = artifactSearchPosition{Coordinate: cursor.Coordinate, BuildNumber: cursor.BuildNumber}
		}
		remaining := pageSize - len(items)
		summaries, searchErr := h.searchGroupMemberArtifacts(r, repo, query, remaining+1, after)
		if searchErr != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "search repository artifacts failed")
			return
		}
		hasMoreInRepository := len(summaries) > remaining
		if hasMoreInRepository {
			summaries = summaries[:remaining]
		}
		for _, summary := range summaries {
			items = append(items, globalArtifactSearchHit(repo, summary))
		}
		if len(items) == pageSize {
			last := items[len(items)-1]
			buildNumber := 0
			if last.BuildNumber != nil {
				buildNumber = int(*last.BuildNumber)
			}
			hasLaterResults, searchErr := h.hasSearchResultsAfter(r, repositories, index+1, query)
			if searchErr != nil {
				writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "search repository artifacts failed")
				return
			}
			if hasMoreInRepository || hasLaterResults {
				token := h.encodeGlobalArtifactSearchCursor(query, format, repo.ID, last.Coordinate, buildNumber)
				next = &token
			}
		}
	}
	writeNativeMavenJSON(w, http.StatusOK, adminopenapi.GlobalArtifactSearchPage{Items: items, NextPageToken: next, SearchedRepositories: len(repositories)})
}

func (h generatedRepositoryAPIAdapter) readableSearchRepositories(ctx context.Context, principal Principal, format repository.Format, query string) ([]repository.HostedRepository, error) {
	repositories := make([]repository.HostedRepository, 0)
	after := ""
	for {
		page, next, err := h.store.ListHostedRepositories(ctx, 200, after)
		if err != nil {
			return nil, err
		}
		for _, repo := range page {
			if repo.State != repository.RepositoryActive || format != "" && repo.Format != format || !validArtifactSearchQuery(repo.Format, query) {
				continue
			}
			if h.authorizer.Authorize(ctx, principal, repo, RepositoryRead).Allowed {
				repositories = append(repositories, repo)
			}
		}
		if next == "" {
			sort.Slice(repositories, func(i, j int) bool {
				if repositories[i].Name != repositories[j].Name {
					return repositories[i].Name < repositories[j].Name
				}
				return repositories[i].ID < repositories[j].ID
			})
			return repositories, nil
		}
		after = next
	}
}

func (h generatedRepositoryAPIAdapter) hasSearchResultsAfter(r *http.Request, repositories []repository.HostedRepository, start int, query string) (bool, error) {
	for index := start; index < len(repositories); index++ {
		items, err := h.searchGroupMemberArtifacts(r, repositories[index], query, 1, artifactSearchPosition{})
		if err != nil {
			return false, err
		}
		if len(items) > 0 {
			return true, nil
		}
	}
	return false, nil
}

func globalArtifactSearchHit(repo repository.HostedRepository, summary adminopenapi.ArtifactSummary) adminopenapi.GlobalArtifactSearchHit {
	return adminopenapi.GlobalArtifactSearchHit{
		RepositoryId: uuid.MustParse(repo.ID), RepositoryName: repo.Name, Format: adminopenapi.Format(repo.Format),
		Coordinate: summary.Coordinate, Digest: summary.Digest, Size: summary.Size, ContentType: summary.ContentType,
		CreatedAt: summary.CreatedAt, BuildNumber: summary.BuildNumber, Publisher: summary.Publisher,
	}
}

func (h hostedRepositoryAPIHandler) encodeGlobalArtifactSearchCursor(query string, format repository.Format, repositoryID, coordinate string, buildNumber int) string {
	return encodeSignedCursor(h.authenticator.AdminToken, globalArtifactSearchCursor{
		Endpoint: "global-artifact-search", Query: query, Format: string(format), RepositoryID: repositoryID,
		Coordinate: coordinate, BuildNumber: buildNumber, ExpiresAt: time.Now().UTC().Add(15 * time.Minute).Unix(),
	})
}

func (h hostedRepositoryAPIHandler) decodeGlobalArtifactSearchCursor(token, query string, format repository.Format) (globalArtifactSearchCursor, error) {
	if token == "" {
		return globalArtifactSearchCursor{}, nil
	}
	var cursor globalArtifactSearchCursor
	if decodeSignedCursor(h.authenticator.AdminToken, token, &cursor) != nil || cursor.Endpoint != "global-artifact-search" || cursor.Query != query || cursor.Format != string(format) || cursor.RepositoryID == "" || cursor.Coordinate == "" || time.Now().UTC().Unix() >= cursor.ExpiresAt {
		return globalArtifactSearchCursor{}, errors.New("invalid cursor")
	}
	return cursor, nil
}
