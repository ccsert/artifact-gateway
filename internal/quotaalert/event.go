package quotaalert

import "time"

const TemplateVersion = "quota-1"

// Event is the immutable, bounded logical-quota evidence shared with the mail renderer.
type Event struct {
	ID              string    `json:"id"`
	RuleID          string    `json:"ruleId"`
	RuleVersion     string    `json:"ruleVersion"`
	RepositoryID    string    `json:"repositoryId"`
	RepositoryName  string    `json:"repositoryName"`
	EpisodeID       string    `json:"episodeId"`
	PreviousEventID string    `json:"previousEventId,omitempty"`
	Sequence        int64     `json:"sequence"`
	Scenario        string    `json:"scenario"`
	UsedBytes       int64     `json:"usedBytes"`
	QuotaBytes      int64     `json:"quotaBytes"`
	Policy          Policy    `json:"policy"`
	OccurredAt      time.Time `json:"occurredAt"`
	SampleAt        time.Time `json:"sampleAt"`
	EvidenceSince   time.Time `json:"evidenceSince"`
}
