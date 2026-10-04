package backupops

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/quotaalert"
)

// Compare safe public fields explicitly; a successful empty response must fail
// even when lower-level database fingerprints and Store readback match.
func assertRecoverySafeAPI(t *testing.T, path string, data []byte, f recoveryEmail) {
	t.Helper()
	switch {
	case strings.HasPrefix(path, "/api/v2/email-targets/"):
		var v struct {
			ID, Name, Locale, Version    string
			Enabled, RecipientConfigured bool
		}
		if json.Unmarshal(data, &v) != nil || v.ID != f.Target.ID || v.Name != f.Target.Name || v.Locale != f.Target.Locale || v.Version != f.Target.Version || v.Enabled != f.Target.Enabled || !v.RecipientConfigured {
			t.Fatal("safe target API identity/config changed")
		}
	case path == "/api/v2/email-deliveries":
		var values []struct {
			ID, EventID, TargetID, TargetVersion, Version, Scenario, Locale, TemplateVersion, State, Kind, QuotaRuleID, EpisodeID, ErrorCode, AutomaticCancellationCode string
			Attempts                                                                                                                                                    int
			PossibleDuplicate                                                                                                                                           bool
			EventSequence                                                                                                                                               int64
		}
		if json.Unmarshal(data, &values) != nil || len(values) != len(f.Deliveries) {
			t.Fatal("safe delivery API inventory changed")
		}
		seen := map[string]bool{}
		for _, v := range values {
			before, ok := f.Deliveries[v.ID]
			if !ok || seen[v.ID] || v.EventID != before.EventID || v.TargetID != before.TargetID || v.TargetVersion != before.TargetVersion || v.Version != before.Version || v.Scenario != before.Scenario || v.Locale != before.Locale || v.TemplateVersion != before.TemplateVersion || v.State != before.State || v.Attempts != before.Attempts || v.PossibleDuplicate != before.PossibleDuplicate || v.Kind != before.Kind || v.QuotaRuleID != before.QuotaRuleID || v.EpisodeID != before.EpisodeID || v.EventSequence != before.EventSequence || v.ErrorCode != before.ErrorCode || v.AutomaticCancellationCode != before.AutomaticCancellationCode {
				t.Fatal("safe delivery API identity/state/permission changed")
			}
			seen[v.ID] = true
		}
	case path == "/api/v2/repository-quota-alert-rules":
		var values []struct {
			ID, RepositoryID, TargetID, TargetVersion, Version, StateVersion, ActiveEpisodeID, LastEventID string
			Enabled, Deleted                                                                               bool
			Sequence                                                                                       int64
			Policy                                                                                         quotaalert.Policy
			State                                                                                          struct {
				Severity, DataState, Phase string
				UsedBytes, QuotaBytes      int64
			}
		}
		if json.Unmarshal(data, &values) != nil || len(values) != len(f.Rules) {
			t.Fatal("safe rule API inventory changed")
		}
		seen := map[string]bool{}
		for _, v := range values {
			matched := false
			for _, before := range f.Rules {
				if v.ID == before.ID {
					matched = true
					phase := "normal"
					if before.State.Severity != "normal" {
						phase = "firing"
					}
					if seen[v.ID] || v.RepositoryID != before.RepositoryID || v.TargetID != before.TargetID || v.TargetVersion != before.TargetVersion || v.Version != before.Version || v.StateVersion != before.StateVersion || v.ActiveEpisodeID != before.ActiveEpisodeID || v.LastEventID != before.LastEventID || v.Enabled != before.Enabled || v.Deleted != before.Deleted || v.Sequence != before.Sequence || v.Policy != before.Policy || v.State.Severity != before.State.Severity || v.State.DataState != before.State.DataState || v.State.Phase != phase || v.State.UsedBytes != before.State.UsedBytes || v.State.QuotaBytes != before.State.QuotaBytes {
						t.Fatal("safe rule API identity/policy/state changed")
					}
				}
			}
			if !matched {
				t.Fatal("safe rule API unknown identity")
			}
			seen[v.ID] = true
		}
	case strings.HasSuffix(path, "/events"):
		var values []struct {
			quotaalert.Event
			TargetID, TargetVersion, DeliveryID, DeliveryState, DeliveryErrorCode, NotificationCode, TemplateVersion string
		}
		if json.Unmarshal(data, &values) != nil || len(values) != len(f.Events[1]) {
			t.Fatal("safe event API inventory changed")
		}
		for i, v := range values {
			before := f.Events[1][i]
			if !reflect.DeepEqual(v.Event, before.Snapshot) || v.TargetID != before.TargetID || v.TargetVersion != before.TargetVersion || v.DeliveryID != before.DeliveryID || v.DeliveryState != before.DeliveryState || v.DeliveryErrorCode != before.DeliveryErrorCode || v.NotificationCode != before.NotificationCode || v.TemplateVersion != quotaalert.TemplateVersion {
				t.Fatal("safe event API order/identity/descriptor changed")
			}
		}
	default:
		t.Fatal("unverified safe API path")
	}
}
