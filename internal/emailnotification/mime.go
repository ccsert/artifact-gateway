package emailnotification

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"github.com/google/uuid"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/textproto"
	"time"
)

// MIME encodes a bounded synthetic descriptor. No caller-supplied header/body is accepted.
func MIME(cfg Config, recipient, eventID, scenario, locale, version string, occurred time.Time) ([]byte, error) {
	if ValidAddress(cfg.From) != nil || ValidAddress(recipient) != nil {
		return nil, ErrInvalidMessage
	}
	if _, err := uuid.Parse(eventID); err != nil {
		return nil, ErrInvalidMessage
	}
	p, err := renderSyntheticVersion(scenario, locale, eventID, occurred, cfg.ConsoleOrigin, version)
	if err != nil {
		return nil, err
	}
	return encodeMIME(cfg, recipient, eventID, occurred, p)
}

func encodeMIME(cfg Config, recipient, eventID string, occurred time.Time, p Preview) ([]byte, error) {
	if ValidAddress(cfg.From) != nil || ValidAddress(recipient) != nil {
		return nil, ErrInvalidMessage
	}
	if _, err := uuid.Parse(eventID); err != nil {
		return nil, ErrInvalidMessage
	}
	var b bytes.Buffer
	hash := sha256.Sum256([]byte(eventID))
	boundary := fmt.Sprintf("gateway-%x", hash[:24])
	fmt.Fprintf(&b, "From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMessage-ID: <%s@artifact-gateway.invalid>\r\nMIME-Version: 1.0\r\nContent-Type: multipart/alternative; boundary=\"%s\"\r\n\r\n", cfg.From, recipient, mime.QEncoding.Encode("utf-8", p.Subject), occurred.UTC().Format(time.RFC1123Z), eventID, boundary)
	m := multipart.NewWriter(&b)
	if err := m.SetBoundary(boundary); err != nil {
		return nil, ErrInvalidMessage
	}
	for _, part := range []struct{ kind, body string }{{"text/plain", p.Text}, {"text/html", p.HTML}} {
		writer, e := m.CreatePart(textproto.MIMEHeader{"Content-Type": {part.kind + "; charset=utf-8"}, "Content-Transfer-Encoding": {"quoted-printable"}})
		if e != nil {
			return nil, ErrInvalidMessage
		}
		q := quotedprintable.NewWriter(writer)
		if _, e = q.Write([]byte(part.body)); e != nil {
			return nil, ErrInvalidMessage
		}
		if e = q.Close(); e != nil {
			return nil, ErrInvalidMessage
		}
	}
	if err := m.Close(); err != nil {
		return nil, ErrInvalidMessage
	}
	return b.Bytes(), nil
}
