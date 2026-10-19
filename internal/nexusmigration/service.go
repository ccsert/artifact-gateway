package nexusmigration

import (
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/url"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/ccsert/artifact-gateway/internal/capacityplan"
	"github.com/ccsert/artifact-gateway/internal/nexussource"
	"github.com/google/uuid"
)

type Options struct {
	TargetID, PoolID  string
	Now               func() time.Time
	HeartbeatInterval time.Duration
	// Hooks are fault-injection seams. Production does not supply them.
	AfterUpload, AfterVerify, AfterPublish func(context.Context, Entry) error
}
type Service struct {
	store       Store
	source      Source
	publication Publication
	options     Options
}

func New(store Store, source Source, publication Publication, options Options) *Service {
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.HeartbeatInterval <= 0 {
		options.HeartbeatInterval = 20 * time.Second
	}
	return &Service{store, source, publication, options}
}
func (s *Service) Available() bool {
	return s != nil && s.store != nil && s.source != nil && s.publication != nil
}
func (s *Service) Capabilities() Capabilities {
	if s == nil {
		return Capabilities{PolicyVersion: PolicyVersion, MaxAttempts: MaxAttempts, CapacityEvidenceSource: "operator-reported"}
	}
	return Capabilities{TargetID: s.options.TargetID, PoolID: s.options.PoolID, ExecutionEnabled: s.Available() && s.options.TargetID != "" && s.options.PoolID != "", CapacityEvidenceSource: "operator-reported", PolicyVersion: PolicyVersion, MaxAttempts: MaxAttempts}
}
func validKey(v string) bool {
	return len(v) > 0 && len(v) <= 128 && !strings.ContainsAny(v, "\r\n\x00")
}
func digest(v any) string {
	data, _ := json.Marshal(v)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
func (s *Service) ValidateEvidence(e CapacityEvidence) error {
	if !s.Capabilities().ExecutionEnabled {
		return Failed("migration_execution_disabled")
	}
	now := s.options.Now().UTC()
	if e.TargetID != s.options.TargetID || e.PoolID != s.options.PoolID {
		return Failed("capacity_binding_mismatch")
	}
	if e.ObservedAt.IsZero() || e.ObservedAt.After(now) || !e.ValidUntil.After(now) || !e.ValidUntil.After(e.ObservedAt) || e.ValidUntil.Sub(e.ObservedAt) > time.Hour {
		return Failed("capacity_evidence_expired")
	}
	for _, v := range []*int64{e.FreeBytes, e.DownloadBytes, e.UploadBytes, e.BackupBytes, e.RestoreBytes, e.HeadroomBytes} {
		if v == nil {
			return Failed("capacity_unknown")
		}
		if *v < 0 {
			return Failed("invalid_request")
		}
	}
	return nil
}
func (s *Service) Start(ctx context.Context, in StartInput, actor, key string) (Batch, error) {
	if !validKey(key) || !validKey(actor) || in.PolicyVersion != PolicyVersion || len(in.Mappings) < 1 || len(in.Mappings) > 10 {
		return Batch{}, Failed("invalid_request")
	}
	if _, err := uuid.Parse(in.SourceID); err != nil {
		return Batch{}, Failed("invalid_request")
	}
	seenSource, seenTarget := map[string]bool{}, map[string]bool{}
	for _, mp := range in.Mappings {
		if !validKey(mp.SourceRepository) || seenSource[mp.SourceRepository] || seenTarget[mp.TargetRepositoryID] {
			return Batch{}, Failed("invalid_request")
		}
		if _, err := uuid.Parse(mp.TargetRepositoryID); err != nil {
			return Batch{}, Failed("invalid_request")
		}
		seenSource[mp.SourceRepository] = true
		seenTarget[mp.TargetRepositoryID] = true
	}
	if prior, found, err := s.store.ReplayStart(ctx, actor, key, digest(in)); err != nil || found {
		return prior, err
	}
	source, err := s.source.Get(ctx, in.SourceID)
	if err != nil {
		return Batch{}, err
	}
	if source.Version != in.SourceVersion {
		return Batch{}, Failed("source_version_changed")
	}
	if err = s.ValidateEvidence(in.CapacityEvidence); err != nil {
		return Batch{}, err
	}
	if err = s.publication.ValidateTargets(ctx, in.Mappings); err != nil {
		return Batch{}, err
	}
	return s.store.Create(ctx, Batch{StartInput: in, ID: uuid.NewString(), Actor: actor}, key, digest(in))
}
func (s *Service) Get(ctx context.Context, id string) (Batch, error) { return s.store.Get(ctx, id) }
func (s *Service) List(ctx context.Context, limit int, cursor string) ([]Batch, string, error) {
	if !validPage(limit, cursor) {
		return nil, "", Failed("invalid_request")
	}
	return s.store.List(ctx, limit, cursor)
}
func (s *Service) Entries(ctx context.Context, id string, limit int, cursor string) ([]Entry, string, error) {
	if !validPage(limit, cursor) {
		return nil, "", Failed("invalid_request")
	}
	if _, err := s.store.Get(ctx, id); err != nil {
		return nil, "", err
	}
	return s.store.Entries(ctx, id, limit, cursor)
}
func validPage(limit int, cursor string) bool {
	if limit < 1 || limit > 100 {
		return false
	}
	if cursor != "" {
		_, err := uuid.Parse(cursor)
		return err == nil
	}
	return true
}
func (s *Service) Control(ctx context.Context, id, expected, actor, key, action string, evidence *CapacityEvidence) (Batch, error) {
	if !validKey(key) || expected == "" {
		return Batch{}, Failed("invalid_request")
	}
	b, err := s.store.Get(ctx, id)
	if err != nil {
		return b, err
	}
	// The store replays an already-applied operation before evaluating its CAS.
	// Replays carry the same evidence; a stale current state is not a new write.
	if evidence != nil && b.Version == expected {
		if !evidence.ObservedAt.After(b.CapacityEvidence.ObservedAt) {
			return b, Failed("capacity_evidence_refresh_required")
		}
		if err = s.ValidateEvidence(*evidence); err != nil {
			return b, err
		}
		source, sourceErr := s.source.Get(ctx, b.SourceID)
		if sourceErr != nil || source.Version != b.SourceVersion {
			return b, Failed("source_version_changed")
		}
		b.CapacityEvidence = *evidence
		if b.ScanComplete {
			entries, err := s.allEntries(ctx, b.ID)
			if err != nil {
				return b, err
			}
			report, err := s.publication.Capacity(ctx, b, entries)
			if err != nil {
				return b, err
			}
			if report.Status != capacityplan.Sufficient {
				return b, Failed("capacity_" + string(report.Status))
			}
		}
	}
	return s.store.Control(ctx, id, expected, actor, key, action, digest(struct {
		Action   string
		Evidence *CapacityEvidence
	}{action, evidence}), evidence)
}
func (s *Service) allEntries(ctx context.Context, id string) ([]Entry, error) {
	out := []Entry{}
	cursor := ""
	for {
		page, next, err := s.store.Entries(ctx, id, 100, cursor)
		if err != nil {
			return nil, err
		}
		out = append(out, page...)
		if next == "" {
			return out, nil
		}
		cursor = next
	}
}

// RunOnce claims one complete batch. Parallel workers may own different batches;
// every checkpoint and final publication is fenced by this batch's token.
func (s *Service) RunOnce(ctx context.Context, formats []string) error {
	b, err := s.store.Claim(ctx, formats)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		tick := time.NewTicker(s.options.HeartbeatInterval)
		defer tick.Stop()
		for {
			select {
			case <-workCtx.Done():
				return
			case <-tick.C:
				if s.store.Renew(workCtx, b.ID, b.LeaseToken) != nil {
					cancel()
					return
				}
			}
		}
	}()
	defer func() { cancel(); <-done }()
	state, message := s.runBatch(workCtx, b)
	if ctx.Err() != nil {
		recoveryCtx, recoveryCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		latest, readErr := s.store.Get(recoveryCtx, b.ID)
		recoveryCancel()
		if readErr == nil && latest.ScanComplete {
			state, message = "ready", "worker_interrupted"
		}
	}
	finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer finishCancel()
	err = s.store.Finish(finishCtx, b.ID, b.LeaseToken, state, message)
	if errors.Is(err, ErrLeaseLost) {
		return nil
	}
	return err
}
func (s *Service) StartWorker(ctx context.Context, interval time.Duration, formats []string) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		if interval <= 0 {
			return
		}
		tick := time.NewTicker(interval)
		defer tick.Stop()
		for {
			_ = s.RunOnce(ctx, formats)
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
	}()
	return done
}
func (s *Service) sourceUnchanged(ctx context.Context, b Batch) error {
	source, err := s.source.Get(ctx, b.SourceID)
	if err != nil || source.Version != b.SourceVersion {
		return Failed("source_version_changed")
	}
	return nil
}
func (s *Service) runBatch(ctx context.Context, b Batch) (string, string) {
	if err := s.sourceUnchanged(ctx, b); err != nil {
		return "blocked", code(err)
	}
	if !b.ScanComplete {
		entries, hash, metadata, total, err := s.scan(ctx, b)
		if err != nil {
			if errors.Is(err, ErrStopped) {
				return "paused", ""
			}
			if strings.HasPrefix(code(err), "capacity_") || code(err) == "source_version_changed" || code(err) == "migration_execution_disabled" {
				return "blocked", code(err)
			}
			return "failed", code(err)
		}
		if err = s.store.Seal(ctx, b, entries, hash, metadata, total); err != nil {
			return "failed", code(err)
		}
	}
	entries, err := s.allEntries(ctx, b.ID)
	if err != nil {
		return "failed", "ledger_read_failed"
	}
	b, err = s.store.Get(ctx, b.ID)
	if err != nil {
		return "failed", "ledger_read_failed"
	}
	if b.CancelRequested {
		return "cancelled", "cancel_requested"
	}
	if b.PauseRequested {
		return "paused", ""
	}
	for i := range entries {
		entry := &entries[i]
		if entry.State == "unsupported" || entry.State == "source_deleted" || entry.State == "conflict" {
			continue
		}
		read, err := s.publication.Readback(ctx, *entry)
		if err != nil {
			return "blocked", code(err)
		}
		if read == Conflict {
			entry.State = "conflict"
			entry.LastError = "target_conflict"
			entry.VerifiedBytes = 0
			for j := range entry.Assets {
				entry.Assets[j].Verified = false
			}
			if err = s.store.Checkpoint(ctx, *entry, b.LeaseToken); err != nil {
				return "failed", code(err)
			}
		}
	}
	if err = s.ValidateEvidence(b.CapacityEvidence); err != nil {
		return "blocked", code(err)
	}
	report, err := s.publication.Capacity(ctx, b, entries)
	if err != nil {
		return "blocked", code(err)
	}
	if report.Status != capacityplan.Sufficient {
		return "blocked", "capacity_" + string(report.Status)
	}
	failed := false
	for _, entry := range entries {
		if entry.State == "unsupported" {
			if entry.LastError != "native_checksum_or_metadata_regenerated" && entry.LastError != "maven_mixed_metadata_requires_regeneration" {
				failed = true
			}
			continue
		}
		if entry.State == "source_deleted" {
			continue
		}
		if entry.State == "conflict" {
			failed = true
			continue
		}
		if entry.State == "failed" || entry.State == "source_missing" {
			failed = true
			continue
		}
		current, err := s.store.Get(ctx, b.ID)
		if err != nil {
			return "failed", "ledger_read_failed"
		}
		if current.CancelRequested {
			return "cancelled", "cancel_requested"
		}
		if current.PauseRequested {
			return "paused", ""
		}
		if err = s.sourceUnchanged(ctx, b); err != nil {
			return "blocked", code(err)
		}
		// Evidence is checked again before every possible first write, including
		// after waiting for format/GC locks inside execute.
		if err = s.ValidateEvidence(b.CapacityEvidence); err != nil {
			return "blocked", code(err)
		}
		entry.Actor = b.Actor
		if err = s.execute(ctx, b, &entry); err != nil {
			if errors.Is(err, ErrStopped) {
				current, _ := s.store.Get(ctx, b.ID)
				if current.CancelRequested {
					return "cancelled", "cancel_requested"
				}
				return "paused", ""
			}
			if errors.Is(err, ErrLeaseLost) || ctx.Err() != nil {
				return "failed", code(err)
			}
			if strings.HasPrefix(code(err), "capacity_") || code(err) == "migration_execution_disabled" || code(err) == "source_version_changed" {
				return "blocked", code(err)
			}
			entry.LastError = code(err)
			entry.State = "failed"
			entry.VerifiedBytes = 0
			for i := range entry.Assets {
				entry.Assets[i].Verified = false
			}
			if entry.LastError == "source_missing" {
				entry.State = "source_missing"
			}
			if entry.LastError == "maven_release_closure_unsupported" || entry.LastError == "maven_required_asset_missing" {
				entry.State = "unsupported"
			}
			if entry.LastError == "target_conflict" {
				entry.State = "conflict"
			}
			if e := s.store.Checkpoint(ctx, entry, b.LeaseToken); e != nil {
				return "failed", code(e)
			}
			failed = true
		}
	}
	if failed {
		return "failed", "entries_require_attention"
	}
	return "completed", ""
}
func code(err error) string {
	var own *Failure
	if errors.As(err, &own) {
		return own.Code
	}
	var source *nexussource.Failure
	if errors.As(err, &source) {
		return source.Code
	}
	if errors.Is(err, ErrLeaseLost) {
		return "lease_lost"
	}
	if errors.Is(err, context.Canceled) {
		return "operation_cancelled"
	}
	return "migration_operation_failed"
}

func (s *Service) scan(ctx context.Context, b Batch) ([]Entry, string, int64, int64, error) {
	names := make([]string, 0, len(b.Mappings))
	targets := map[string]string{}
	for _, mp := range b.Mappings {
		names = append(names, mp.SourceRepository)
		targets[mp.SourceRepository] = mp.TargetRepositoryID
	}
	discovery, err := s.source.Discover(ctx, b.SourceID, nexussource.DiscoveryInput{Repositories: names, MaxPages: 100, MaxAssets: 10000})
	if err != nil {
		return nil, "", 0, 0, err
	}
	if discovery.SourceVersion != b.SourceVersion {
		return nil, "", 0, 0, Failed("source_version_changed")
	}
	formats := map[string]string{}
	for _, repo := range discovery.Repositories {
		format := repo.Format
		if format == "maven2" {
			format = "maven"
		}
		if repo.Status != "supported" || repo.Type != "hosted" {
			return nil, "", 0, 0, Failed("source_repository_unsupported")
		}
		formats[repo.Name] = format
	}
	byIdentity := map[string]*Entry{}
	entries := []Entry{}
	metadata := int64(len(discovery.Assets) + len(discovery.UnsupportedAssets))
	if err = s.store.RecordScanProgress(ctx, b.ID, b.LeaseToken, metadata, 0); err != nil {
		return nil, "", 0, 0, err
	}
	unsupported := func(repo, p, reason string) {
		entries = append(entries, Entry{ID: uuid.NewString(), BatchID: b.ID, SourceRepository: repo, TargetRepositoryID: targets[repo], Format: formats[repo], Identity: "unsupported:" + p, State: "unsupported", Assets: []Asset{}, LastError: reason})
	}
	for _, a := range discovery.UnsupportedAssets {
		unsupported(a.Repository, a.Path, a.Reason)
	}
	for _, a := range discovery.Assets {
		current, err := s.store.Get(ctx, b.ID)
		if err != nil {
			return nil, "", 0, 0, err
		}
		if current.CancelRequested || current.PauseRequested {
			return nil, "", 0, 0, ErrStopped
		}
		if err = s.ValidateEvidence(b.CapacityEvidence); err != nil {
			return nil, "", 0, 0, err
		}
		format := formats[a.Repository]
		if a.Size > 1<<30 {
			unsupported(a.Repository, a.Path, "asset_size_unsupported")
			continue
		}
		if isChecksumPath(a.Path) || path.Base(a.Path) == "maven-metadata.xml" && format == "maven" {
			reason := "native_checksum_or_metadata_regenerated"
			if format == "raw" {
				reason = "raw_checksum_path_unsupported"
			}
			unsupported(a.Repository, a.Path, reason)
			continue
		}
		identity := a.Path
		if format == "raw" {
			segments := strings.Split(a.Path, "/")
			for i := range segments {
				segments[i] = url.PathEscape(segments[i])
			}
			identity = strings.Join(segments, "/")
		}
		if format == "maven" {
			var ok bool
			identity, ok = mavenIdentity(a.Path)
			if !ok {
				unsupported(a.Repository, a.Path, "maven_release_path_unsupported")
				continue
			}
		}
		asset := Asset{SourcePath: a.Path, TargetPath: a.Path, Size: a.Size, SHA256: a.Checksum["sha256"], SHA1: a.Checksum["sha1"], MD5: a.Checksum["md5"]}
		if format == "raw" {
			asset.TargetPath = identity
		}
		if asset.SHA256 == "" || format == "maven" && (asset.SHA1 == "" || asset.MD5 == "") {
			// Missing mandatory hashes trigger a separately counted, read-only source
			// verification. Complete metadata hashes avoid this extra transfer.
			content, err := s.source.OpenAsset(ctx, b.SourceID, b.SourceVersion, a.Repository, AssetRequest{Path: a.Path, Size: a.Size})
			if err != nil {
				return nil, "", 0, 0, err
			}
			h256, h1, hm, h512 := sha256.New(), sha1.New(), md5.New(), sha512.New()
			written, readErr := io.Copy(io.MultiWriter(h256, h1, hm, h512, &scanByteWriter{ctx: ctx, store: s.store, batch: b.ID, token: b.LeaseToken}), io.LimitReader(content.Body, a.Size+1))
			closeErr := content.Body.Close()
			if readErr != nil || closeErr != nil || written != a.Size {
				return nil, "", 0, 0, Failed("source_changed")
			}
			asset.SHA256 = hex.EncodeToString(h256.Sum(nil))
			asset.SHA1 = hex.EncodeToString(h1.Sum(nil))
			asset.MD5 = hex.EncodeToString(hm.Sum(nil))
			asset.Validator = content.Validator
			for alg, want := range a.Checksum {
				got := ""
				switch alg {
				case "sha256":
					got = asset.SHA256
				case "sha1":
					got = asset.SHA1
				case "md5":
					got = asset.MD5
				case "sha512":
					got = hex.EncodeToString(h512.Sum(nil))
				}
				if got != "" && got != want {
					return nil, "", 0, 0, Failed("source_changed")
				}
			}
		}
		if asset.Validator == "" {
			asset.Validator = "sha256:" + asset.SHA256
		}
		key := a.Repository + "\x00" + identity
		entry := byIdentity[key]
		if entry == nil {
			entry = &Entry{ID: uuid.NewString(), BatchID: b.ID, SourceRepository: a.Repository, TargetRepositoryID: targets[a.Repository], Format: format, Identity: identity, State: "pending", Assets: []Asset{}}
			byIdentity[key] = entry
		}
		entry.Assets = append(entry.Assets, asset)
	}
	for _, entry := range byIdentity {
		sort.Slice(entry.Assets, func(i, j int) bool { return entry.Assets[i].TargetPath < entry.Assets[j].TargetPath })
		if entry.Format == "maven" {
			parts := strings.Split(entry.Identity, ":")
			pom := parts[1] + "-" + parts[2] + ".pom"
			hasPOM := false
			for _, a := range entry.Assets {
				hasPOM = hasPOM || path.Base(a.TargetPath) == pom
			}
			if !hasPOM {
				entry.State = "unsupported"
				entry.LastError = "maven_complete_release_requires_pom"
			}
		}
		entries = append(entries, *entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		a, c := entries[i], entries[j]
		return a.SourceRepository+"\x00"+a.Identity < c.SourceRepository+"\x00"+c.Identity
	})
	previous, err := s.store.PreviousEntries(ctx, b)
	if err != nil {
		return nil, "", 0, 0, err
	}
	seen := map[string]bool{}
	for _, e := range entries {
		seen[e.SourceRepository+"\x00"+e.Identity] = true
	}
	for _, old := range previous {
		if old.State == "unsupported" || old.State == "source_deleted" || seen[old.SourceRepository+"\x00"+old.Identity] {
			continue
		}
		old.ID = uuid.NewString()
		old.BatchID = b.ID
		old.State = "source_deleted"
		old.LastError = "source_deleted_target_retained"
		old.Attempts = 0
		old.TransferredBytes = 0
		old.VerifiedBytes = 0
		old.VisibleIdentities = 0
		for i := range old.Assets {
			old.Assets[i].Uploaded = false
			old.Assets[i].Verified = false
		}
		entries = append(entries, old)
	}
	var total int64
	unique := map[string]bool{}
	for _, e := range entries {
		if e.State != "pending" {
			continue
		}
		for _, a := range e.Assets {
			k := e.Format + ":" + a.SHA256
			if unique[k] {
				continue
			}
			unique[k] = true
			if a.Size > math.MaxInt64-total {
				return nil, "", 0, 0, Failed("inventory_size_overflow")
			}
			total += a.Size
		}
	}
	// Exclude generated record IDs and mutable checkpoints from the inventory
	// hash; bind every source validator, target mapping and policy explicitly.
	type manifestEntry struct {
		Source, Target, Format, Identity, State string
		Assets                                  []Asset
		Reason                                  string
	}
	frozen := make([]manifestEntry, 0, len(entries))
	for _, e := range entries {
		frozen = append(frozen, manifestEntry{e.SourceRepository, e.TargetRepositoryID, e.Format, e.Identity, e.State, e.Assets, e.LastError})
	}
	manifest := struct {
		SourceID, SourceVersion, Policy string
		Mappings                        []Mapping
		Entries                         []manifestEntry
	}{b.SourceID, b.SourceVersion, b.PolicyVersion, b.Mappings, frozen}
	if err = s.sourceUnchanged(ctx, b); err != nil {
		return nil, "", 0, 0, err
	}
	return entries, digest(manifest), metadata, total, nil
}
func isChecksumPath(p string) bool {
	for _, suffix := range []string{".sha1", ".sha256", ".sha512", ".md5"} {
		if strings.HasSuffix(p, suffix) {
			return true
		}
	}
	return false
}
func mavenIdentity(p string) (string, bool) {
	parts := strings.Split(p, "/")
	if len(parts) < 4 {
		return "", false
	}
	artifact, version, name := parts[len(parts)-3], parts[len(parts)-2], parts[len(parts)-1]
	if strings.HasSuffix(strings.ToUpper(version), "-SNAPSHOT") || !strings.HasPrefix(name, artifact+"-"+version+".") && !strings.HasPrefix(name, artifact+"-"+version+"-") {
		return "", false
	}
	group := strings.Join(parts[:len(parts)-3], ".")
	if group == "" || strings.ContainsAny(group+artifact+version, ":") {
		return "", false
	}
	return group + ":" + artifact + ":" + version, true
}

func (s *Service) execute(ctx context.Context, b Batch, e *Entry) error {
	previousState := e.State
	if e.State != "committed" && e.State != "skipped" {
		if e.Attempts >= MaxAttempts {
			return Failed("attempts_exhausted")
		}
		e.Attempts++
	}
	if err := s.store.BeginEntry(ctx, *e, b.LeaseToken); err != nil {
		return err
	}
	ctx, release, err := s.publication.Lock(ctx, *e)
	if err != nil {
		return err
	}
	defer release()
	if err = s.ValidateEvidence(b.CapacityEvidence); err != nil {
		return err
	}
	all, err := s.allEntries(ctx, b.ID)
	if err != nil {
		return err
	}
	report, err := s.publication.Capacity(ctx, b, all)
	if err != nil {
		return err
	}
	if report.Status != capacityplan.Sufficient {
		return Failed("capacity_" + string(report.Status))
	}
	state, err := s.publication.Readback(ctx, *e)
	if err != nil {
		return err
	}
	if state == Conflict {
		return Failed("target_conflict")
	}
	e.VerifiedBytes = 0
	for i := range e.Assets {
		e.Assets[i].Verified = false
	}
	if state == Absent {
		e.SessionID, err = s.publication.Begin(ctx, *e)
		if err != nil {
			return err
		}
	}
	if err = s.store.Checkpoint(ctx, *e, b.LeaseToken); err != nil {
		return err
	}
	for i := range e.Assets {
		asset := &e.Assets[i]
		content, err := s.source.OpenAsset(ctx, b.SourceID, b.SourceVersion, e.SourceRepository, AssetRequest{Path: asset.SourcePath, Size: asset.Size, Validator: asset.Validator})
		if err != nil {
			return err
		}
		spool, err := os.CreateTemp("", "artifact-gateway-migration-*")
		if err != nil {
			_ = content.Body.Close()
			return Failed("temporary_storage_unavailable")
		}
		name := spool.Name()
		unlinked := os.Remove(name) == nil
		h := sha256.New()
		writer := &transferWriter{ctx: ctx, store: s.store, entry: e.ID, batch: b.ID, token: b.LeaseToken, writer: io.MultiWriter(spool, h)}
		written, copyErr := io.CopyBuffer(writer, io.LimitReader(content.Body, asset.Size+1), make([]byte, 1<<20))
		closeErr := content.Body.Close()
		if copyErr == nil && closeErr != nil {
			copyErr = closeErr
		}
		if copyErr == nil && (written != asset.Size || hex.EncodeToString(h.Sum(nil)) != asset.SHA256) {
			copyErr = Failed("source_changed")
		}
		if copyErr == nil {
			_, copyErr = spool.Seek(0, io.SeekStart)
		}
		if copyErr == nil && state == Absent {
			copyErr = s.publication.Stage(ctx, *e, *asset, spool)
		}
		_ = spool.Close()
		if !unlinked {
			_ = os.Remove(name)
		}
		if copyErr != nil {
			return copyErr
		}
		asset.Uploaded = state == Absent || asset.Uploaded
		if s.options.AfterUpload != nil && state == Absent {
			if err = s.options.AfterUpload(ctx, *e); err != nil {
				return err
			}
		}
		e.State = "uploaded"
		if err = s.store.Checkpoint(ctx, *e, b.LeaseToken); err != nil {
			return err
		}
	}
	if err = s.sourceUnchanged(ctx, b); err != nil {
		return err
	}
	if state == Absent {
		if err = s.publication.Verify(ctx, *e); err != nil {
			return err
		}
	} else {
		read, err := s.publication.Readback(ctx, *e)
		if err != nil {
			return err
		}
		if read != Identical {
			return Failed("target_conflict")
		}
	}
	for i := range e.Assets {
		e.Assets[i].Verified = true
		e.VerifiedBytes += e.Assets[i].Size
	}
	e.State = "verified"
	if s.options.AfterVerify != nil {
		if err = s.options.AfterVerify(ctx, *e); err != nil {
			return err
		}
	}
	if err = s.store.Checkpoint(ctx, *e, b.LeaseToken); err != nil {
		return err
	}
	// The same advisory fence is taken by recovery and administrative controls.
	// It stays held across final verification, native metadata commit and ledger.
	releaseFence, err := s.store.Fence(ctx, b.ID, b.LeaseToken)
	if err != nil {
		return err
	}
	defer releaseFence()
	if err = s.sourceUnchanged(ctx, b); err != nil {
		return err
	}
	if err = s.ValidateEvidence(b.CapacityEvidence); err != nil {
		return err
	}
	if state == Absent {
		if err = s.publication.Verify(ctx, *e); err != nil {
			return err
		}
		if err = s.publication.Publish(ctx, *e); err != nil {
			return err
		}
		if s.options.AfterPublish != nil {
			if err = s.options.AfterPublish(ctx, *e); err != nil {
				return err
			}
		}
	}
	read, err := s.publication.Readback(ctx, *e)
	if err != nil {
		return err
	}
	if read != Identical {
		return Failed("target_conflict")
	}
	e.State = "committed"
	if state == Identical && previousState != "committed" {
		e.State = "skipped"
	}
	e.VisibleIdentities = 1
	e.LastError = ""
	return s.store.Checkpoint(ctx, *e, b.LeaseToken)
}

type transferWriter struct {
	ctx                 context.Context
	store               Store
	entry, batch, token string
	writer              io.Writer
}

func (w *transferWriter) Write(p []byte) (int, error) {
	if err := w.store.AddTransferred(w.ctx, w.entry, w.batch, w.token, int64(len(p))); err != nil {
		return 0, err
	}
	return w.writer.Write(p)
}

// TargetBinding identifies one explicitly assembled object-store target without
// disclosing its endpoint or bucket through management responses.
func TargetBinding(endpoint, bucket string) string {
	return "target-" + digest(struct{ Endpoint, Bucket string }{strings.TrimRight(endpoint, "/"), bucket})
}

type scanByteWriter struct {
	ctx          context.Context
	store        Store
	batch, token string
}

func (w *scanByteWriter) Write(p []byte) (int, error) {
	if err := w.store.RecordScanProgress(w.ctx, w.batch, w.token, 0, int64(len(p))); err != nil {
		return 0, err
	}
	return len(p), nil
}
