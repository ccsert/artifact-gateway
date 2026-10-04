package emailnotification

import (
	"bytes"
	"github.com/artifact-gateway/artifact-gateway/internal/quotaalert"
	"github.com/google/uuid"
	"strings"
	"testing"
	"time"
)

func quotaMailFixture() quotaalert.Event {
	at := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	return quotaalert.Event{ID: uuid.NewString(), RuleID: uuid.NewString(), RuleVersion: "2", RepositoryID: uuid.NewString(), RepositoryName: "synthetic <demo>", EpisodeID: uuid.NewString(), Sequence: 1, Scenario: "warning", UsedBytes: 880, QuotaBytes: 1000, Policy: quotaalert.Policy{WarningBasisPoints: 8500, CriticalBasisPoints: 9500, RecoveryBelowBasisPoints: 8000, WarningForSeconds: 300, CriticalForSeconds: 120, RecoveryForSeconds: 180, MaxSampleAgeSeconds: 60}, OccurredAt: at, SampleAt: at, EvidenceSince: at.Add(-300 * time.Second)}
}
func TestQuotaVersionedMIMEPreservesSemanticEvidenceAndSafeHTML(t *testing.T) {
	e := quotaMailFixture()
	cfg := Config{From: "synthetic@example.test", ConsoleOrigin: "https://console.example.invalid"}
	for _, locale := range []string{"en", "zh-CN"} {
		for _, scenario := range []string{"warning", "critical", "resolved"} {
			v := e
			v.Scenario = scenario
			if scenario == "critical" {
				v.UsedBytes = 980
			}
			if scenario == "resolved" {
				v.UsedBytes = 790
			}
			p, err := RenderQuota(v, locale, cfg.ConsoleOrigin, "quota-1")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(p.HTML, "&lt;demo&gt;") || strings.Contains(p.HTML, "synthetic <demo>") || !strings.Contains(p.Text, "880 / 1000 bytes") && scenario == "warning" || !strings.Contains(p.Text, v.EpisodeID) || !strings.Contains(p.Text, "UTC") {
				t.Fatal("quota evidence/escaping mismatch")
			}
			if strings.Contains(p.HTML, "<img") || strings.Contains(p.HTML, "<script") || strings.Contains(p.HTML, "@import") {
				t.Fatal("quota mail includes external/active resources")
			}
			first, err := MIMEQuota(cfg, "recipient@example.test", v, locale, "quota-1")
			if err != nil {
				t.Fatal(err)
			}
			repeat, err := MIMEQuota(cfg, "recipient@example.test", v, locale, "quota-1")
			if err != nil || !bytes.Equal(first, repeat) {
				t.Fatal("retry changed quota MIME")
			}
		}
	}
}
func TestQuotaMailRejectsMalformedEvidenceAndHeaderInjection(t *testing.T) {
	e := quotaMailFixture()
	cfg := Config{From: "synthetic@example.test"}
	for _, mutate := range []func(*quotaalert.Event){func(v *quotaalert.Event) { v.RepositoryName = "name\r\nBcc: hidden" }, func(v *quotaalert.Event) { v.ID = "not-a-uuid" }, func(v *quotaalert.Event) { v.RuleVersion = "0" }, func(v *quotaalert.Event) { v.QuotaBytes = 0 }, func(v *quotaalert.Event) { v.UsedBytes = -1 }, func(v *quotaalert.Event) { v.UsedBytes = 1 }, func(v *quotaalert.Event) { v.EvidenceSince = v.SampleAt }, func(v *quotaalert.Event) { v.SampleAt = v.OccurredAt.Add(time.Second) }, func(v *quotaalert.Event) { v.Scenario = "custom" }} {
		v := e
		mutate(&v)
		if _, err := MIMEQuota(cfg, "recipient@example.test", v, "en", "quota-1"); err == nil {
			t.Fatal("malformed quota evidence rendered")
		}
	}
	if _, err := MIMEQuota(cfg, "recipient@example.test", e, "en", "quota-unknown"); err == nil {
		t.Fatal("unavailable version silently rendered")
	}
	if _, err := MIMEQuota(cfg, "recipient@example.test\r\nBcc: hidden", e, "en", "quota-1"); err == nil {
		t.Fatal("recipient header injection")
	}
}
func TestLegacySyntheticV1FooterIsRetained(t *testing.T) {
	p, err := renderSyntheticVersion("warning", "zh-CN", "synthetic", time.Now(), "", "1")
	if err != nil || !strings.Contains(p.Text, "尚未启用容量告警评估") || p.TemplateVersion != "1" {
		t.Fatal("legacy descriptor changed on replay")
	}
}
