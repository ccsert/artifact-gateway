package agctl

import (
	"net/url"
	"regexp"
	"strings"
)

var (
	sensitiveQuery = regexp.MustCompile(`(?i)(token|secret|password|credential|key)`)
	textURL        = regexp.MustCompile(`(?i)https?://[^\s<>"']+`)
	bearerValue    = regexp.MustCompile(`(?i)\bBearer[ \t]+[^\s,;"'<>]+`)
	secretField    = regexp.MustCompile(`(?i)(["']?[a-z0-9_.-]*(?:token|secret|password|credential|key)[a-z0-9_.-]*["']?\s*[=:]\s*)(?:"[^"]*"|'[^']*'|[^\s&;,]+)`)
)

// redactText preserves diagnostic context while removing credentials from
// server-controlled messages and locally generated errors.
func redactText(text string, knownSecrets ...string) string {
	text = textURL.ReplaceAllStringFunc(text, func(raw string) string {
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Host == "" {
			return "REDACTED_URL"
		}
		return redactURL(parsed)
	})
	text = bearerValue.ReplaceAllString(text, "Bearer REDACTED")
	text = secretField.ReplaceAllString(text, "${1}REDACTED")
	for _, secret := range knownSecrets {
		if secret == "" {
			continue
		}
		for _, value := range []string{secret, url.QueryEscape(secret), url.PathEscape(secret)} {
			text = strings.ReplaceAll(text, value, "REDACTED")
		}
	}
	return text
}

// redactURL hides credential-bearing URL parts before they reach any writer.
func redactURL(target *url.URL) string {
	sanitized := *target
	sanitized.User = nil
	sanitized.Fragment = ""
	sanitized.RawFragment = ""
	query := sanitized.Query()
	for key := range query {
		if sensitiveQuery.MatchString(key) {
			query.Set(key, "REDACTED")
		}
	}
	sanitized.RawQuery = query.Encode()
	return sanitized.String()
}
