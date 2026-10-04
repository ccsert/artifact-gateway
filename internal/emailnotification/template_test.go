package emailnotification

import (
	"bytes"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"testing"
	"time"
)

func TestSyntheticPreviewDoesNotClaimQuotaEvaluationIsDisabled(t *testing.T) {
	p, err := Render("warning", "zh-CN", "synthetic", time.Now(), "")
	if err != nil || strings.Contains(p.Text, "尚未启用容量告警评估") || p.TemplateVersion != "2" {
		t.Fatalf("obsolete synthetic footer: %s %s %v", p.TemplateVersion, p.Text, err)
	}
}

func TestVersionedMIMEContainsEquivalentTextAndEscapedHTML(t *testing.T) {
	for _, locale := range []string{"en", "zh-CN"} {
		for _, scenario := range []string{"warning", "critical", "resolved"} {
			t.Run(locale+"/"+scenario, func(t *testing.T) {
				id := "1d8a5d8c-327d-4c33-a6f2-776f1a52bd45"
				cfg := Config{From: "gateway@example.test", ConsoleOrigin: "https://console.example.invalid"}
				date := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
				raw, err := MIME(cfg, "recipient@example.test", id, scenario, locale, "1", date)
				if err != nil {
					t.Fatal(err)
				}
				repeat, _ := MIME(cfg, "recipient@example.test", id, scenario, locale, "1", date)
				if !bytes.Equal(raw, repeat) {
					t.Fatal("retry changed immutable MIME")
				}
				m, err := mail.ReadMessage(bytes.NewReader(raw))
				if err != nil {
					t.Fatal(err)
				}
				subject, err := new(mime.WordDecoder).DecodeHeader(m.Header.Get("Subject"))
				if err != nil || !strings.Contains(subject, "Artifact Gateway") {
					t.Fatal("invalid subject")
				}
				_, params, err := mime.ParseMediaType(m.Header.Get("Content-Type"))
				if err != nil {
					t.Fatal(err)
				}
				parts := multipart.NewReader(m.Body, params["boundary"])
				for _, kind := range []string{"text/plain", "text/html"} {
					part, e := parts.NextPart()
					if e != nil {
						t.Fatal(e)
					}
					body, e := io.ReadAll(part)
					if part.Header.Get("Content-Transfer-Encoding") == "quoted-printable" {
						body, e = io.ReadAll(quotedprintable.NewReader(bytes.NewReader(body)))
					}
					if e != nil {
						t.Fatal(e)
					}
					if !strings.HasPrefix(part.Header.Get("Content-Type"), kind) || !bytes.Contains(body, []byte(id)) || !bytes.Contains(body, []byte("https://console.example.invalid/system?tab=diagnostics")) {
						t.Fatal("HTML/text parity missing")
					}
					if kind == "text/html" && (bytes.Contains(body, []byte("<img")) || bytes.Contains(body, []byte("<script"))) {
						t.Fatal("external assets or script")
					}
				}
			})
		}
	}
	p, err := Render("warning", "en", "<img src=x onerror=evil>", time.Now(), "")
	if err != nil || strings.Contains(p.HTML, "<img") {
		t.Fatal("template did not escape semantic value")
	}
}
func TestMailInputRejectsHeaderAndLinkInjection(t *testing.T) {
	for _, address := range []string{"a@example.test\r\nBcc: other@example.test", "a@example.test\x00", "A <a@example.test>", "a@example.test,b@example.test"} {
		if ValidAddress(address) == nil {
			t.Fatal("unsafe address accepted")
		}
	}
	for _, origin := range []string{"http://console.example.test", "https://user:pass@console.example.test", "https://console.example.test/path", "https://console.example.test/?q=secret"} {
		if _, err := ConsoleURL(origin); err == nil {
			t.Fatal("unsafe link accepted")
		}
	}
}

func TestRendererRetainsFixedOutlookWrapper(t *testing.T) {
	p, err := Render("warning", "en", "synthetic", time.Now(), "")
	if err != nil || !strings.Contains(p.HTML, `<!--[if mso]><table role="presentation" width="600"`) || !strings.Contains(p.HTML, `<!--[if mso]></td></tr></table><![endif]-->`) {
		t.Fatal("Outlook fallback lost during escaped rendering")
	}
}
