package repository

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

func cargoPromotionWebhookEvent(job LifecycleJob) (WebhookEvent, bool) {
	if job.Kind != LifecycleJobPromotion || !lifecycleJobMatchesFormat(job.Payload, FormatCargo) ||
		(job.State != LifecycleJobCompleted && job.State != LifecycleJobFailed) {
		return WebhookEvent{}, false
	}
	var payload struct {
		SourceRepositoryID string `json:"sourceRepositoryId"`
		Name               string `json:"name"`
		Version            string `json:"version"`
		Digest             string `json:"digest"`
	}
	_ = json.Unmarshal(job.Payload, &payload)
	eventType := WebhookEventCargoPromotionCompleted
	if job.State == LifecycleJobFailed {
		eventType = WebhookEventCargoPromotionFailed
	}
	data, _ := json.Marshal(CargoDistributionWebhookData{
		OperationID: job.ID, Kind: "promotion", Format: FormatCargo,
		SourceRepositoryID: payload.SourceRepositoryID, TargetRepositoryID: job.RepositoryID,
		Coordinate: payload.Name + "@" + payload.Version, Digest: payload.Digest,
		State: string(job.State), Attempts: job.Attempts,
	})
	return WebhookEvent{ID: uuid.NewString(), Type: eventType, OccurredAt: job.CompletedAt, Data: data}, true
}

func cargoReplicationWebhookEvent(plan ReplicationPlan) (WebhookEvent, bool) {
	if plan.Format != FormatCargo || (plan.State != "completed" && (plan.State != "failed" || !plan.NextAttemptAt.IsZero())) {
		return WebhookEvent{}, false
	}
	eventType := WebhookEventCargoReplicationCompleted
	if plan.State == "failed" {
		eventType = WebhookEventCargoReplicationFailed
	}
	data, _ := json.Marshal(CargoDistributionWebhookData{
		OperationID: plan.ID, Kind: "replication", Format: FormatCargo,
		SourceRepositoryID: plan.SourceRepositoryID, TargetRepositoryID: plan.TargetRepositoryID,
		Coordinate: plan.Coordinate, Digest: plan.Digest, State: plan.State, Attempts: plan.Attempts,
	})
	occurred := plan.CompletedAt
	if occurred.IsZero() {
		occurred = time.Now().UTC()
	}
	return WebhookEvent{ID: uuid.NewString(), Type: eventType, OccurredAt: occurred, Data: data}, true
}
