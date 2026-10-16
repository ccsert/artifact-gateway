package app

import (
	"errors"
	"net/http"
	"strings"

	"github.com/ccsert/artifact-gateway/internal/repository"
)

var errRawConditionUnsupported = errors.New("If-Unmodified-Since is not supported for Raw writes; use If-Match")

// rawWriteCondition parses the RFC 9110 preconditions a Raw PUT may carry.
// If-Match uses strong comparison, so a weak tag never matches; If-None-Match
// uses weak comparison. Both the "sha256-<hex>" tag Raw reads return and the
// "sha256:<hex>" tag Raw writes return identify the same digest. A malformed
// list, or a date precondition that cannot be evaluated atomically, is an
// error rather than a silently unconditional write.
func rawWriteCondition(header http.Header) (repository.RawWriteCondition, error) {
	var condition repository.RawWriteCondition
	if len(header.Values("If-Unmodified-Since")) > 0 {
		return condition, errRawConditionUnsupported
	}
	if values := header.Values("If-Match"); len(values) > 0 {
		wildcard, tags, err := parseRawEntityTags(values)
		if err != nil {
			return condition, err
		}
		if wildcard {
			condition.RequirePresent = true
		}
		for _, tag := range tags {
			if strings.HasPrefix(tag, "W/") {
				// Keep the list non-empty so an all-weak If-Match still fails.
				condition.MatchDigests = append(condition.MatchDigests, tag)
				continue
			}
			condition.MatchDigests = append(condition.MatchDigests, rawEntityTagDigest(tag))
		}
	}
	if values := header.Values("If-None-Match"); len(values) > 0 {
		wildcard, tags, err := parseRawEntityTags(values)
		if err != nil {
			return condition, err
		}
		if wildcard {
			condition.RequireAbsent = true
		}
		for _, tag := range tags {
			condition.NoneMatchDigests = append(condition.NoneMatchDigests, rawEntityTagDigest(strings.TrimPrefix(tag, "W/")))
		}
	}
	return condition, nil
}

// parseRawEntityTags reads "*" or a comma-separated list of entity tags from
// every field line. "*" must stand alone.
func parseRawEntityTags(values []string) (bool, []string, error) {
	value := strings.Trim(strings.Join(values, ","), " \t")
	if value == "*" {
		return true, nil, nil
	}
	tags := make([]string, 0, len(values))
	for index := 0; index < len(value); {
		// Empty list members are ignored, as required for received HTTP lists.
		if value[index] == ' ' || value[index] == '\t' || value[index] == ',' {
			index++
			continue
		}
		start := index
		if strings.HasPrefix(value[index:], "W/") {
			index += 2
		}
		if index == len(value) || value[index] != '"' {
			return false, nil, errors.New("malformed entity tag in precondition")
		}
		// An opaque tag can contain commas and backslashes. Only a double
		// quote ends it; entity tags do not use quoted-string escaping.
		end := strings.IndexByte(value[index+1:], '"')
		if end < 0 {
			return false, nil, errors.New("malformed entity tag in precondition")
		}
		index += end + 2
		if !validRawEntityTag(value[start:index]) {
			return false, nil, errors.New("malformed entity tag in precondition")
		}
		tags = append(tags, value[start:index])
		for index < len(value) && (value[index] == ' ' || value[index] == '\t') {
			index++
		}
		if index < len(value) && value[index] != ',' {
			return false, nil, errors.New("malformed entity tag in precondition")
		}
	}
	if len(tags) == 0 {
		return false, nil, errors.New("malformed entity tag in precondition")
	}
	return false, tags, nil
}

func validRawEntityTag(tag string) bool {
	tag = strings.TrimPrefix(tag, "W/")
	if len(tag) < 2 || tag[0] != '"' || tag[len(tag)-1] != '"' {
		return false
	}
	for i := 1; i < len(tag)-1; i++ {
		if c := tag[i]; c != 0x21 && (c < 0x23 || c == 0x7f) {
			return false
		}
	}
	return true
}

func rawEntityTagDigest(tag string) string {
	opaque := strings.Trim(tag, `"`)
	if hex, ok := strings.CutPrefix(opaque, "sha256-"); ok {
		return "sha256:" + hex
	}
	return opaque
}

// checkRawWriteCondition fails fast before the body is read. The store
// re-evaluates the same condition atomically when it replaces the reference.
func (h nativeRawHandler) checkRawWriteCondition(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, path string) (repository.RawWriteCondition, bool) {
	condition, err := rawWriteCondition(r.Header)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return condition, false
	}
	if condition.Unconditional() {
		return condition, true
	}
	current, err := h.store.GetRawAsset(r.Context(), repo.ID, path)
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		http.Error(w, "raw path state is unavailable", http.StatusInternalServerError)
		return condition, false
	}
	if !condition.Allows(current, err == nil) {
		writeRawPreconditionFailed(w)
		return condition, false
	}
	return condition, true
}

// writeRawPreconditionFailed does not echo the current validator: a writer
// is not necessarily allowed to read the path.
func writeRawPreconditionFailed(w http.ResponseWriter) {
	http.Error(w, "raw path precondition failed", http.StatusPreconditionFailed)
}
