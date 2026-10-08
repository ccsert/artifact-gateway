package repository

import (
	"errors"
	"net/url"
	"strconv"
	"strings"
	"unicode"
)

// Validate keeps the first anonymous exchange contract deliberately narrow:
// one HTTPS issuer URL without credentials, query parameters, or a fragment,
// and one nonempty audience. Network authorization is evaluated separately.
func (b *OCIBearer) Validate() error {
	if b == nil {
		return nil
	}
	realm, err := url.Parse(b.Realm)
	if err != nil || realm.Scheme != "https" || realm.Hostname() == "" || realm.Opaque != "" || realm.User != nil || realm.RawQuery != "" || realm.ForceQuery || strings.Contains(b.Realm, "#") || strings.HasSuffix(realm.Host, ":") || strings.IndexFunc(b.Realm, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return errors.New("ociBearer realm must be a complete https URL without userinfo, query, or fragment")
	}
	if port := realm.Port(); port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return errors.New("ociBearer realm port must be between 1 and 65535")
		}
	}
	if b.Service == "" || len(b.Service) > 256 || strings.IndexFunc(b.Service, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return errors.New("ociBearer service must be between 1 and 256 bytes without whitespace or control characters")
	}
	return nil
}

func cloneOCIBearer(b *OCIBearer) *OCIBearer {
	if b == nil {
		return nil
	}
	cloned := *b
	return &cloned
}

func cloneHostedRepositoryOCIBearer(repo HostedRepository) HostedRepository {
	repo.OCIBearer = cloneOCIBearer(repo.OCIBearer)
	return repo
}
