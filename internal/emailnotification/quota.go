package emailnotification

import (
	"fmt"
	"github.com/artifact-gateway/artifact-gateway/internal/quotaalert"
	"math/big"
	"time"
)

func RenderQuota(e quotaalert.Event, locale, origin, version string) (Preview, error) {
	if version != quotaalert.TemplateVersion || e.Validate() != nil || (locale != "en" && locale != "zh-CN") {
		return Preview{}, ErrInvalidMessage
	}
	link, err := ConsoleURL(origin)
	if err != nil {
		return Preview{}, err
	}
	percent := new(big.Rat).Mul(new(big.Rat).SetFrac(big.NewInt(e.UsedBytes), big.NewInt(e.QuotaBytes)), big.NewRat(100, 1)).FloatString(2) + "%"
	v := view{Locale: locale, Color: "#b45309", Tint: "#fff7ed", Percent: percent, Amount: fmt.Sprintf("%d / %d bytes", e.UsedBytes, e.QuotaBytes), URL: link}
	threshold, seconds, op := e.Policy.WarningBasisPoints, e.Policy.WarningForSeconds, "≥"
	switch e.Scenario {
	case "critical":
		threshold, seconds = e.Policy.CriticalBasisPoints, e.Policy.CriticalForSeconds
		v.Color, v.Tint = "#b91c1c", "#fef2f2"
	case "resolved":
		threshold, seconds, op = e.Policy.RecoveryBelowBasisPoints, e.Policy.RecoveryForSeconds, "<"
		v.Color, v.Tint = "#047857", "#ecfdf5"
	}
	pct := fmt.Sprintf("%d.%02d%%", threshold/100, threshold%100)
	utc := func(t time.Time) string { return t.UTC().Format("2006-01-02 15:04:05 UTC") }
	if locale == "zh-CN" {
		v.Status = map[string]string{"warning": "警告", "critical": "严重", "resolved": "恢复"}[e.Scenario]
		v.Title = "仓库逻辑配额 · " + e.RepositoryName
		v.Summary = fmt.Sprintf("逻辑使用量 %s %s，已连续观测至少 %d 秒。", op, pct, seconds)
		v.ActionLabel = "建议行动"
		v.Action = "检查仓库最新容量与保留策略；按运维流程处理。"
		v.Note = "此事件仅表示仓库逻辑配额，不表示本地挂载、S3 或 NAS 的物理剩余空间。"
		v.Footer = "管理员告警邮件 · 此为记录时刻的历史事件；重试或手动重放保留原证据，请核对当前状态。"
		v.Details = []detail{{"仓库 ID", e.RepositoryID}, {"警告条件", fmt.Sprintf("≥ %d.%02d%% · %d 秒", e.Policy.WarningBasisPoints/100, e.Policy.WarningBasisPoints%100, e.Policy.WarningForSeconds)}, {"严重条件", fmt.Sprintf("≥ %d.%02d%% · %d 秒", e.Policy.CriticalBasisPoints/100, e.Policy.CriticalBasisPoints%100, e.Policy.CriticalForSeconds)}, {"恢复条件", fmt.Sprintf("< %d.%02d%% · %d 秒", e.Policy.RecoveryBelowBasisPoints/100, e.Policy.RecoveryBelowBasisPoints%100, e.Policy.RecoveryForSeconds)}, {"连续证据起点", utc(e.EvidenceSince)}, {"采样时间", utc(e.SampleAt)}, {"事件时间", utc(e.OccurredAt)}, {"事件 ID", e.ID}, {"关联周期", e.EpisodeID}, {"规则版本", e.RuleID + " / " + e.RuleVersion}, {"规则事件顺序", fmt.Sprint(e.Sequence)}}
	} else {
		v.Status = map[string]string{"warning": "Warning", "critical": "Critical", "resolved": "Resolved"}[e.Scenario]
		v.Title = "Repository logical quota · " + e.RepositoryName
		v.Summary = fmt.Sprintf("Logical usage %s %s, continuously observed for at least %d seconds.", op, pct, seconds)
		v.ActionLabel = "Suggested action"
		v.Action = "Review current repository capacity and retention policy, then follow the operations procedure."
		v.Note = "This event describes repository logical quota, not physical free space on a local mount, S3 or NAS."
		v.Footer = "Administrator alert email · Historical event evidence is retained on retries and manual replay; check the current state."
		v.Details = []detail{{"Repository ID", e.RepositoryID}, {"Warning condition", fmt.Sprintf("≥ %d.%02d%% · %d seconds", e.Policy.WarningBasisPoints/100, e.Policy.WarningBasisPoints%100, e.Policy.WarningForSeconds)}, {"Critical condition", fmt.Sprintf("≥ %d.%02d%% · %d seconds", e.Policy.CriticalBasisPoints/100, e.Policy.CriticalBasisPoints%100, e.Policy.CriticalForSeconds)}, {"Recovery condition", fmt.Sprintf("< %d.%02d%% · %d seconds", e.Policy.RecoveryBelowBasisPoints/100, e.Policy.RecoveryBelowBasisPoints%100, e.Policy.RecoveryForSeconds)}, {"Continuous evidence since", utc(e.EvidenceSince)}, {"Sample time", utc(e.SampleAt)}, {"Event time", utc(e.OccurredAt)}, {"Event ID", e.ID}, {"Episode ID", e.EpisodeID}, {"Rule version", e.RuleID + " / " + e.RuleVersion}, {"Rule sequence", fmt.Sprint(e.Sequence)}}
	}
	if e.PreviousEventID != "" {
		label := "Previous event"
		if locale == "zh-CN" {
			label = "前一事件"
		}
		v.Details = append(v.Details, detail{label, e.PreviousEventID})
	}
	v.Subject = "[Artifact Gateway] " + v.Status + " · " + v.Title
	return renderView(v, quotaalert.TemplateVersion)
}

func MIMEQuota(cfg Config, recipient string, e quotaalert.Event, locale, version string) ([]byte, error) {
	p, err := RenderQuota(e, locale, cfg.ConsoleOrigin, version)
	if err != nil {
		return nil, err
	}
	return encodeMIME(cfg, recipient, e.ID, e.OccurredAt, p)
}
