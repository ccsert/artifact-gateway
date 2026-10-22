package nexusmigration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"sort"
	"strings"

	"github.com/ccsert/artifact-gateway/internal/nexussource"
	npmprotocol "github.com/ccsert/artifact-gateway/internal/protocol/npm"
	"github.com/google/uuid"
)

// NPMSource is a format-owned read port, independent of ordinary asset reads.
type NPMSource interface {
	ReadNPMPackument(context.Context, string, string, string, string) (nexussource.NPMPackument, error)
}

const NPMPackumentPath = "@packument"

// NPMEntryMetadata validates the complete, immutable ledger closure. Raw source
// metadata remains private; management reports expose paths and digest receipts.
func NPMEntryMetadata(e Entry) (npmprotocol.ImportPackument, map[string]Asset, error) {
	var doc npmprotocol.ImportPackument
	var metadata *Asset
	versions := map[string]Asset{}
	if e.Format != "npm" || !npmprotocol.ValidPackageName(e.Identity) {
		return doc, nil, Failed("npm_closure_invalid")
	}
	for i := range e.Assets {
		a := &e.Assets[i]
		if a.TargetPath == NPMPackumentPath {
			if metadata != nil || a.SourcePath != e.Identity || len(a.NPMPackument) == 0 || a.Size != int64(len(a.NPMPackument)) {
				return doc, nil, Failed("npm_closure_invalid")
			}
			metadata = a
		} else {
			if !npmprotocol.ValidVersion(a.TargetPath) || versions[a.TargetPath].TargetPath != "" || a.Size <= 0 || a.Size > 256<<20 || !hexDigest(a.SHA256, 64) || !hexDigest(a.SHA1, 40) || !hexDigest(a.SHA512, 128) {
				return doc, nil, Failed("npm_closure_invalid")
			}
			versions[a.TargetPath] = *a
		}
	}
	if metadata == nil {
		return doc, nil, Failed("npm_closure_incomplete")
	}
	sum := sha256.Sum256(metadata.NPMPackument)
	if hex.EncodeToString(sum[:]) != metadata.SHA256 {
		return doc, nil, Failed("npm_closure_invalid")
	}
	var err error
	doc, err = npmprotocol.DecodeImportPackument(metadata.NPMPackument, e.Identity)
	if err != nil || len(doc.Versions) != len(versions) {
		return doc, nil, Failed("npm_closure_incomplete")
	}
	for version, raw := range doc.Versions {
		a := versions[version]
		if a.TargetPath == "" {
			return doc, nil, Failed("npm_closure_incomplete")
		}
		var identity npmprotocol.ImportVersion
		_ = json.Unmarshal(raw, &identity)
		u, parseErr := url.Parse(identity.Dist.Tarball)
		if parseErr != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || !strings.HasSuffix(u.Path, "/repository/"+e.SourceRepository+"/"+a.SourcePath) || !strings.HasPrefix(a.SourcePath, e.Identity+"/-/") || !npmprotocol.ValidTarballName(strings.TrimPrefix(a.SourcePath, e.Identity+"/-/")) {
			return doc, nil, Failed("npm_closure_invalid")
		}
	}
	return doc, versions, nil
}

func hexDigest(value string, size int) bool {
	return len(value) == size && strings.Trim(value, "0123456789abcdef") == ""
}

func (s *Service) planNPMEntries(ctx context.Context, b Batch, sourceRepo, targetRepo string, inventory []nexussource.Asset, failure *ScanFailure, metadataBudget *int64) ([]Entry, error) {
	source, ok := s.source.(NPMSource)
	if !ok {
		return nil, Failed("npm_source_unavailable")
	}
	packages := map[string]map[string]nexussource.Asset{}
	for _, a := range inventory {
		if !npmprotocol.ValidPackageName(a.PackageName) || !npmprotocol.ValidVersion(a.Version) {
			return nil, Failed("npm_component_identity_invalid")
		}
		if packages[a.PackageName] == nil {
			packages[a.PackageName] = map[string]nexussource.Asset{}
		}
		if _, duplicate := packages[a.PackageName][a.Version]; duplicate {
			return nil, Failed("npm_component_identity_invalid")
		}
		packages[a.PackageName][a.Version] = a
	}
	names := make([]string, 0, len(packages))
	for name := range packages {
		names = append(names, name)
	}
	sort.Strings(names)
	entries := []Entry{}
	for _, name := range names {
		failure.SourceRepository, failure.SourcePath, failure.Identity, failure.Stage = sourceRepo, name, name, "npm_packument_validation"
		if err := s.ociScanAdmission(ctx, b); err != nil {
			return nil, err
		}
		observed, err := source.ReadNPMPackument(ctx, b.SourceID, b.SourceVersion, sourceRepo, name)
		if err != nil {
			return nil, err
		}
		if metadataBudget == nil || int64(len(observed.Body)) > *metadataBudget {
			return nil, Failed("npm_metadata_limit_exceeded")
		}
		*metadataBudget -= int64(len(observed.Body))
		if err = s.store.RecordScanProgress(ctx, b.ID, b.LeaseToken, 0, int64(len(observed.Body))); err != nil {
			return nil, err
		}
		doc, err := npmprotocol.DecodeImportPackument(observed.Body, name)
		if err != nil {
			return nil, Failed("npm_packument_invalid")
		}
		if len(doc.Versions) != len(packages[name]) {
			return nil, Failed("npm_closure_incomplete")
		}
		sum := sha256.Sum256(observed.Body)
		metadata := Asset{SourcePath: name, TargetPath: NPMPackumentPath, Size: int64(len(observed.Body)), SHA256: hex.EncodeToString(sum[:]), Validator: observed.Validator, MediaType: "application/json", NPMPackument: observed.Body}
		if metadata.Validator == "" {
			metadata.Validator = "sha256:" + metadata.SHA256
		}
		entry := Entry{ID: uuid.NewString(), BatchID: b.ID, SourceRepository: sourceRepo, TargetRepositoryID: targetRepo, Format: "npm", Identity: name, State: "pending", Assets: []Asset{metadata}}
		versions := make([]string, 0, len(doc.Versions))
		for version := range doc.Versions {
			versions = append(versions, version)
		}
		sort.Strings(versions)
		for _, version := range versions {
			item, present := packages[name][version]
			if !present || item.Path != observed.TarballPaths[version] || item.Size <= 0 || item.Size > 256<<20 {
				return nil, Failed("npm_closure_incomplete")
			}
			failure.SourcePath, failure.Stage = item.Path, "source_verification_open"
			if err = s.ociScanAdmission(ctx, b); err != nil {
				return nil, err
			}
			content, err := s.source.OpenAsset(ctx, b.SourceID, b.SourceVersion, sourceRepo, AssetRequest{Format: "npm", Path: item.Path, Size: item.Size})
			if err != nil {
				return nil, err
			}
			spool, err := os.CreateTemp("", "artifact-gateway-npm-scan-*")
			if err != nil {
				_ = content.Body.Close()
				return nil, Failed("temporary_storage_unavailable")
			}
			temporary := spool.Name()
			_ = os.Remove(temporary)
			hashers := npmprotocol.ImportHashers()
			writers := []io.Writer{spool, &scanByteWriter{ctx: ctx, store: s.store, batch: b.ID, token: b.LeaseToken}}
			for _, h := range hashers {
				writers = append(writers, h)
			}
			failure.Stage = "source_verification_read"
			written, readErr := io.CopyBuffer(io.MultiWriter(writers...), io.LimitReader(content.Body, item.Size+1), make([]byte, 1<<20))
			closeErr := content.Body.Close()
			sums := map[string][]byte{}
			for algorithm, h := range hashers {
				sums[algorithm] = h.Sum(nil)
			}
			var native npmprotocol.ImportVersion
			_ = json.Unmarshal(doc.Versions[version], &native)
			failure.Stage = "source_verification_digest"
			if readErr == nil && (closeErr != nil || written != item.Size) {
				readErr = Failed("source_changed")
			}
			if readErr == nil && !npmprotocol.ImportIntegrity(native.Dist.Integrity, native.Dist.Shasum, sums) {
				readErr = Failed("npm_integrity_mismatch")
			}
			for algorithm, want := range item.Checksum {
				if got, exists := sums[algorithm]; exists && hex.EncodeToString(got) != want && readErr == nil {
					readErr = Failed("source_changed")
				}
			}
			if readErr == nil {
				_, readErr = spool.Seek(0, io.SeekStart)
			}
			if readErr == nil && npmprotocol.ValidateTarballReader(spool, name, version) != nil {
				readErr = Failed("npm_tarball_identity_invalid")
			}
			_ = spool.Close()
			_ = os.Remove(temporary)
			if readErr != nil {
				return nil, readErr
			}
			a := Asset{SourcePath: item.Path, TargetPath: version, Size: item.Size, SHA256: hex.EncodeToString(sums["sha256"]), SHA1: hex.EncodeToString(sums["sha1"]), SHA512: hex.EncodeToString(sums["sha512"]), Validator: content.Validator, MediaType: "application/octet-stream"}
			if a.Validator == "" {
				a.Validator = "sha256:" + a.SHA256
			}
			entry.Assets = append(entry.Assets, a)
		}
		if err = s.verifyNPMSource(ctx, b, entry); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func (s *Service) verifyNPMSource(ctx context.Context, b Batch, e Entry) error {
	if e.Format != "npm" {
		return nil
	}
	source, ok := s.source.(NPMSource)
	if !ok {
		return Failed("npm_source_unavailable")
	}
	current, err := source.ReadNPMPackument(ctx, b.SourceID, b.SourceVersion, e.SourceRepository, e.Identity)
	if err != nil {
		return err
	}
	if err = s.store.RecordScanProgress(ctx, b.ID, b.LeaseToken, 0, int64(len(current.Body))); err != nil {
		return err
	}
	sum := sha256.Sum256(current.Body)
	for _, a := range e.Assets {
		if a.TargetPath == NPMPackumentPath {
			if hex.EncodeToString(sum[:]) != a.SHA256 {
				return Failed("source_changed")
			}
			return nil
		}
	}
	return Failed("npm_closure_incomplete")
}
