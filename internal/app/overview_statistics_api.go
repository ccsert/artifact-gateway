package app

import (
	"net/http"
	"time"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func (h generatedRepositoryAPIAdapter) GetOverviewStatistics(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.repositoryViewPrincipal(w, r)
	if !ok {
		return
	}
	capacityStore, ok := h.capacities.(repository.RepositoryCapacityRecordStore)
	if !ok {
		writeHostedProblem(w, http.StatusNotImplemented, "not_supported", "repository capacity aggregation is unavailable")
		return
	}
	requestStore, ok := h.audit.(repository.RepositoryRequestStatisticsStore)
	if !ok {
		writeHostedProblem(w, http.StatusNotImplemented, "not_supported", "repository request aggregation is unavailable")
		return
	}
	now := time.Now().UTC()
	records, err := capacityStore.ListRepositoryCapacityRecords(r.Context())
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "list repository capacities failed")
		return
	}
	requests, err := requestStore.ListRepositoryRequestStatistics(r.Context(), now)
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "list repository request statistics failed")
		return
	}
	byName := make(map[string]repository.RepositoryRequestStatistics, len(requests))
	for _, item := range requests {
		byName[item.Repository] = item
	}
	proxyCapacities, proxyErr := (proxyCacheBrowseHandler{store: h.store, maintenance: h.maintenance, authenticator: h.authenticator, authorizer: h.authorizer}).proxyCacheCapacities(r.Context(), records)
	response := adminopenapi.OverviewStatistics{
		GeneratedAt:  now,
		Repositories: make([]adminopenapi.OverviewRepositoryStatistics, 0, len(records)),
	}
	for _, record := range records {
		if record.Repository.State == repository.RepositoryDeleted || !h.mayAdministerRepository(r, principal, record.Repository.ID) {
			continue
		}
		capacity := record.Capacity
		if proxyErr == nil {
			capacity = proxyCapacities[record.Repository.ID]
		}
		count := byName[record.Repository.Name]
		item := adminopenapi.OverviewRepositoryStatistics{
			RepositoryId: uuid.MustParse(record.Repository.ID),
			Name:         record.Repository.Name,
			Format:       adminopenapi.Format(record.Repository.Format),
			Requests:     overviewWindowCounts(count.Requests),
			Denied:       overviewWindowCounts(count.Denied),
			ObjectCount:  capacity.ObjectCount,
			UsedBytes:    capacity.UsedBytes,
		}
		response.Repositories = append(response.Repositories, item)
		addOverviewWindowCounts(&response.Totals.Requests, item.Requests)
		addOverviewWindowCounts(&response.Totals.Denied, item.Denied)
		response.Totals.ObjectCount += item.ObjectCount
		response.Totals.UsedBytes += item.UsedBytes
	}
	writeNativeMavenJSON(w, http.StatusOK, response)
}

func overviewWindowCounts(count repository.RequestWindowCounts) adminopenapi.OverviewWindowCounts {
	return adminopenapi.OverviewWindowCounts{OneDay: count.OneDay, SevenDays: count.SevenDays, ThirtyDays: count.ThirtyDays}
}

func addOverviewWindowCounts(total *adminopenapi.OverviewWindowCounts, next adminopenapi.OverviewWindowCounts) {
	total.OneDay += next.OneDay
	total.SevenDays += next.SevenDays
	total.ThirtyDays += next.ThirtyDays
}
