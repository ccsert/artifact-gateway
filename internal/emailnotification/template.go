// Package emailnotification owns the finite, versioned administrative mail channel.
package emailnotification

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"html/template"
	"net/url"
	"strings"
	"time"
)

const TemplateVersion = "1"

//go:embed template.html
var templateSource string
var mailTemplate = template.Must(template.New("notification").Funcs(template.FuncMap{
	// These no-argument functions emit only compiled layout constants. Dynamic
	// semantic values never pass through template.HTML. html/template strips
	// ordinary comments, including the conditional wrappers needed by Outlook.
	"outlookOpen": func() template.HTML {
		return `<!--[if mso]><table role="presentation" width="600" cellpadding="0" cellspacing="0"><tr><td><![endif]-->`
	},
	"outlookClose": func() template.HTML { return `<!--[if mso]></td></tr></table><![endif]-->` },
}).Parse(templateSource))

var ErrInvalidMessage = errors.New("invalid email message")

type Preview struct {
	Subject         string `json:"subject"`
	HTML            string `json:"html"`
	Text            string `json:"text"`
	TemplateVersion string `json:"templateVersion"`
}
type detail struct{ Label, Value string }
type view struct {
	Locale, Status, Color, Tint, Title, Summary, Percent, Amount, ActionLabel, Action, Note, Footer, URL, Subject string
	Details                                                                                                       []detail
}

// ConsoleURL accepts only an explicit HTTPS origin. Request Host is never used.
func ConsoleURL(origin string) (string, error) {
	if origin == "" {
		return "", nil
	}
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || strings.ContainsAny(origin, "\r\n\x00") {
		return "", ErrInvalidMessage
	}
	u.Path = "/system"
	u.RawQuery = "tab=diagnostics"
	return u.String(), nil
}

// Render uses fixed synthetic content until capacity evaluation is implemented.
// Keeping version 1 is required to replay already queued descriptors faithfully.
func Render(scenario, locale, eventID string, occurred time.Time, origin string) (Preview, error) {
	if locale != "en" && locale != "zh-CN" {
		return Preview{}, ErrInvalidMessage
	}
	v := view{Locale: locale, Percent: "12%", Amount: "30.72 / 256 GiB", Color: "#b45309", Tint: "#fff7ed"}
	v.URL, _ = ConsoleURL(origin)
	if origin != "" && v.URL == "" {
		return Preview{}, ErrInvalidMessage
	}
	if locale == "zh-CN" {
		v.Status = "警告"
		v.Title = "临时挂载的可用空间偏低"
		v.Summary = "合成测试：可用比例低于 15%，持续 5 分钟。"
		v.ActionLabel = "建议行动"
		v.Action = "检查空间趋势与在途任务，按运维流程处理。"
		v.Note = "本地挂载观测不表示 S3 或 NAS 后端池容量。"
		v.Footer = "管理员测试邮件 · 数据为合成样例 · 尚未启用容量告警评估"
		v.Details = []detail{{"观测范围", "示例节点 A · temporary"}, {"数据来源", "合成的有效挂载采样"}, {"恢复条件", "可用比例 > 20%，持续 3 分钟"}, {"事件时间", occurred.UTC().Format("2006-01-02 15:04:05 UTC")}, {"事件 ID", eventID}}
	} else {
		v.Status = "Warning"
		v.Title = "Temporary mount is running low on space"
		v.Summary = "Synthetic test: available space below 15% for 5 minutes."
		v.ActionLabel = "Suggested action"
		v.Action = "Review space trends and in-flight tasks, then follow the operations procedure."
		v.Note = "A local mount observation does not describe the S3 or NAS backend pool."
		v.Footer = "Administrator test email · Synthetic data · Capacity evaluation is not enabled"
		v.Details = []detail{{"Scope", "Example node A · temporary"}, {"Source", "Synthetic valid mount sample"}, {"Recovery condition", "Available space > 20% for 3 minutes"}, {"Event time", occurred.UTC().Format("2006-01-02 15:04:05 UTC")}, {"Event ID", eventID}}
	}
	switch scenario {
	case "warning":
	case "critical":
		v.Percent = "4%"
		v.Amount = "10.24 / 256 GiB"
		v.Color = "#b91c1c"
		v.Tint = "#fef2f2"
		if locale == "zh-CN" {
			v.Status = "严重"
			v.Summary = "合成测试：可用比例低于 5%，持续 2 分钟。"
		} else {
			v.Status = "Critical"
			v.Summary = "Synthetic test: available space below 5% for 2 minutes."
		}
	case "resolved":
		v.Percent = "26%"
		v.Amount = "66.56 / 256 GiB"
		v.Color = "#047857"
		v.Tint = "#ecfdf5"
		if locale == "zh-CN" {
			v.Status = "恢复"
			v.Title = "临时挂载空间已恢复"
			v.Summary = "合成测试：可用比例高于 20%，持续 3 分钟。"
		} else {
			v.Status = "Resolved"
			v.Title = "Temporary mount space has recovered"
			v.Summary = "Synthetic test: available space above 20% for 3 minutes."
		}
	default:
		return Preview{}, ErrInvalidMessage
	}
	v.Subject = "[Artifact Gateway] " + v.Status + " · " + v.Title
	var html bytes.Buffer
	if err := mailTemplate.Execute(&html, v); err != nil {
		return Preview{}, ErrInvalidMessage
	}
	var plain strings.Builder
	fmt.Fprintf(&plain, "%s\n\n%s\n%s\n%s\n\n", v.Subject, v.Summary, v.Percent, v.Amount)
	for _, d := range v.Details {
		fmt.Fprintf(&plain, "%s: %s\n", d.Label, d.Value)
	}
	fmt.Fprintf(&plain, "\n%s\n%s\n\n%s\n", v.ActionLabel, v.Action, v.Note)
	if v.URL != "" {
		fmt.Fprintf(&plain, "\n%s\n", v.URL)
	}
	fmt.Fprintf(&plain, "\n%s\n", v.Footer)
	return Preview{Subject: v.Subject, HTML: html.String(), Text: plain.String(), TemplateVersion: TemplateVersion}, nil
}
