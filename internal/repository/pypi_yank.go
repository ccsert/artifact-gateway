package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"unicode"
	"unicode/utf8"
)

// PyPIYankStore changes advisory selection metadata, never package bytes.
// Implementations serialize with publication/lifecycle operations and commit
// the mutation and its success audit in the same transaction.
type PyPIYankStore interface {
	SetPyPIYank(context.Context, PyPIYankMutation) ([]PyPIFile, error)
}

type PyPIYankMutation struct {
	RepositoryID     string
	Project          string
	Version          string
	Filename         string
	Yanked           bool
	Reason           string
	ExpectedRevision string
	Audit            AuditRecord
}

var ErrInvalidPyPIYank = errors.New("invalid PyPI yank mutation")

// ValidPyPIYankReason bounds untrusted upstream and administrator metadata.
func ValidPyPIYankReason(reason string) bool {
	if len(reason) > 1024 || !utf8.ValidString(reason) {
		return false
	}
	for _, r := range reason {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func validatePyPIProxyFiles(files []PyPIFile) error {
	seen := make(map[string]PyPIFile, len(files))
	for _, file := range files {
		if file.SourceVersion != files[0].SourceVersion {
			return ErrVersionConflict
		}
		if !ValidPyPIYankReason(file.YankReason) || !file.Yanked && file.YankReason != "" {
			return ErrInvalidPyPIYank
		}
		if previous, exists := seen[file.Filename]; exists && (previous.Digest != file.Digest || previous.Yanked != file.Yanked || previous.YankReason != file.YankReason || previous.SourceURL != file.SourceURL || previous.Version != file.Version) {
			return ErrUpstreamChanged
		}
		seen[file.Filename] = file
	}
	return nil
}

// PyPIYankRevision binds a complete visible version file set. Per-file counters
// prevent yank/unyank ABA; membership and creation time detect concurrent upload
// or restore even if all pre-existing files have unchanged metadata.
func PyPIYankRevision(files []PyPIFile) string {
	ordered := append([]PyPIFile(nil), files...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Filename < ordered[j].Filename })
	type entry struct {
		RepositoryID, Filename, Digest, CreatedAt, Reason string
		Yanked                                            bool
		Revision                                          int64
	}
	entries := make([]entry, 0, len(ordered))
	for _, file := range ordered {
		entries = append(entries, entry{file.RepositoryID, file.Filename, file.Digest, file.CreatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"), file.YankReason, file.Yanked, file.YankRevision})
	}
	raw, _ := json.Marshal(entries)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func applyPyPIYank(files []PyPIFile, mutation PyPIYankMutation) ([]PyPIFile, int, error) {
	if len(files) == 0 {
		return nil, 0, ErrNotFound
	}
	if mutation.RepositoryID == "" || mutation.Project == "" || mutation.Version == "" || !ValidPyPIYankReason(mutation.Reason) {
		return nil, 0, ErrInvalidPyPIYank
	}
	if mutation.ExpectedRevision != PyPIYankRevision(files) {
		return nil, 0, ErrVersionConflict
	}
	reason := mutation.Reason
	if !mutation.Yanked {
		reason = ""
	}
	matched, changed := false, 0
	result := append([]PyPIFile(nil), files...)
	for i := range result {
		file := &result[i]
		if mutation.Filename != "" && file.Filename != mutation.Filename {
			continue
		}
		matched = true
		if file.Yanked != mutation.Yanked || file.YankReason != reason {
			file.Yanked, file.YankReason = mutation.Yanked, reason
			file.YankRevision++
			changed++
		}
	}
	if !matched {
		return nil, 0, ErrNotFound
	}
	return result, changed, nil
}

func (m PyPIYankMutation) auditRecord(files []PyPIFile, changed int) AuditRecord {
	record := m.Audit
	record.Evidence = make(map[string]string, len(m.Audit.Evidence)+8)
	for key, value := range m.Audit.Evidence {
		record.Evidence[key] = value
	}
	record.Evidence["project"] = m.Project
	record.Evidence["version"] = m.Version
	record.Evidence["filename"] = m.Filename
	record.Evidence["changedFiles"] = strconv.Itoa(changed)
	record.Evidence["revision"] = PyPIYankRevision(files)
	record.Evidence["previousRevision"] = m.ExpectedRevision
	record.Evidence["yanked"] = strconv.FormatBool(m.Yanked)
	record.Evidence["reason"] = ""
	if m.Yanked {
		record.Evidence["reason"] = m.Reason
	}
	return record
}
