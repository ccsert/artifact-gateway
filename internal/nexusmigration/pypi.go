package nexusmigration

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/ccsert/artifact-gateway/internal/nexussource"
	pypi "github.com/ccsert/artifact-gateway/internal/protocol/pypi"
	"github.com/google/uuid"
)

const PyPISimplePath = "@simple"

type PyPISource interface {
	ReadPyPISimple(context.Context, string, string, string, string) (nexussource.PyPISimple, error)
}

func MetadataAsset(e Entry, a Asset) bool {
	return e.Format == "npm" && a.TargetPath == NPMPackumentPath || e.Format == "pypi" && a.TargetPath == PyPISimplePath
}
func PyPIEntryMetadata(e Entry) (pypi.Snapshot, []pypi.File, error) {
	var snapshot pypi.Snapshot
	project, version, ok := strings.Cut(e.Identity, "@")
	if e.Format != "pypi" || !ok || !pypi.ValidName(project) || !pypi.ValidVersion(version) {
		return snapshot, nil, Failed("pypi_closure_invalid")
	}
	assets := map[string]Asset{}
	var metadata *Asset
	for i := range e.Assets {
		a := &e.Assets[i]
		if a.TargetPath == PyPISimplePath {
			if metadata != nil || a.SourcePath != "simple/"+project+"/" {
				return snapshot, nil, Failed("pypi_closure_invalid")
			}
			metadata = a
			continue
		}
		if _, dup := assets[a.TargetPath]; dup || a.Size <= 0 || a.Size > pypi.ImportFileLimit || !pypi.ValidDigest(a.SHA256) {
			return snapshot, nil, Failed("pypi_closure_invalid")
		}
		assets[a.TargetPath] = *a
	}
	if metadata == nil || metadata.Size != int64(len(metadata.PyPISimple)) || metadata.SHA256 != digestBytes(metadata.PyPISimple) {
		return snapshot, nil, Failed("pypi_closure_invalid")
	}
	var err error
	snapshot, err = pypi.DecodeSnapshot(metadata.PyPISimple)
	if err != nil || snapshot.Project != project {
		return snapshot, nil, Failed("pypi_closure_invalid")
	}
	files := []pypi.File{}
	for _, f := range snapshot.Files {
		d, _ := pypi.FilenameIdentity(f.Filename)
		if d.Version != version {
			continue
		}
		a, present := assets[f.Filename]
		if !present || a.SourcePath != f.SourcePath || a.SHA256 != f.SHA256 {
			return snapshot, nil, Failed("pypi_closure_incomplete")
		}
		files = append(files, f)
	}
	if len(files) == 0 || len(files) != len(assets) {
		return snapshot, nil, Failed("pypi_closure_incomplete")
	}
	return snapshot, files, nil
}
func digestBytes(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }
func (s *Service) planPyPIEntries(ctx context.Context, b Batch, sourceRepo, targetRepo string, inventory []nexussource.Asset, failure *ScanFailure, budget *int64) ([]Entry, error) {
	source, ok := s.source.(PyPISource)
	if !ok {
		return nil, Failed("pypi_source_unavailable")
	}
	projects := map[string]map[string]nexussource.Asset{}
	for _, a := range inventory {
		if !pypi.ValidName(a.PackageName) || !pypi.ValidVersion(a.Version) {
			return nil, Failed("pypi_component_identity_invalid")
		}
		if projects[a.PackageName] == nil {
			projects[a.PackageName] = map[string]nexussource.Asset{}
		}
		if _, dup := projects[a.PackageName][a.Path]; dup {
			return nil, Failed("source_duplicate_asset")
		}
		projects[a.PackageName][a.Path] = a
	}
	names := make([]string, 0, len(projects))
	for n := range projects {
		names = append(names, n)
	}
	sort.Strings(names)
	entries := []Entry{}
	for _, name := range names {
		failure.SourceRepository, failure.SourcePath, failure.Identity, failure.Stage = sourceRepo, "simple/"+name+"/", name, "pypi_simple_validation"
		if err := s.ociScanAdmission(ctx, b); err != nil {
			return nil, err
		}
		observed, err := source.ReadPyPISimple(ctx, b.SourceID, b.SourceVersion, sourceRepo, name)
		if err != nil {
			return nil, err
		}
		if budget == nil || int64(len(observed.Body)) > *budget {
			return nil, Failed("pypi_metadata_limit_exceeded")
		}
		*budget -= int64(len(observed.Body))
		if err = s.store.RecordScanProgress(ctx, b.ID, b.LeaseToken, 0, int64(len(observed.Body))); err != nil {
			return nil, err
		}
		snapshot, err := pypi.DecodeSnapshot(observed.Body)
		if err != nil || snapshot.Project != name || len(snapshot.Files) != len(projects[name]) {
			return nil, Failed("pypi_closure_incomplete")
		}
		versions := map[string]*Entry{}
		metadata := Asset{SourcePath: "simple/" + name + "/", TargetPath: PyPISimplePath, Size: int64(len(observed.Body)), SHA256: digestBytes(observed.Body), PyPISimple: observed.Body, MediaType: "application/json"}
		metadata.Validator = "sha256:" + metadata.SHA256
		for _, file := range snapshot.Files {
			item, present := projects[name][file.SourcePath]
			d, identityErr := pypi.FilenameIdentity(file.Filename)
			if !present || identityErr != nil || d.Version != item.Version || d.Project != item.PackageName || item.Size <= 0 || item.Size > pypi.ImportFileLimit {
				return nil, Failed("pypi_closure_incomplete")
			}
			failure.SourcePath, failure.Stage = file.SourcePath, "source_verification_open"
			if err = s.ociScanAdmission(ctx, b); err != nil {
				return nil, err
			}
			content, err := s.source.OpenAsset(ctx, b.SourceID, b.SourceVersion, sourceRepo, AssetRequest{Format: "pypi", Path: item.Path, Size: item.Size})
			if err != nil {
				return nil, err
			}
			spool, err := os.CreateTemp("", "artifact-gateway-pypi-scan-*")
			if err != nil {
				_ = content.Body.Close()
				return nil, Failed("temporary_storage_unavailable")
			}
			temporary := spool.Name()
			_ = os.Remove(temporary)
			h, h1, hm, h512 := sha256.New(), sha1.New(), md5.New(), sha512.New()
			failure.Stage = "source_verification_read"
			n, readErr := io.Copy(io.MultiWriter(spool, h, h1, hm, h512, &scanByteWriter{ctx: ctx, store: s.store, batch: b.ID, token: b.LeaseToken}), io.LimitReader(content.Body, item.Size+1))
			closeErr := content.Body.Close()
			failure.Stage = "source_verification_digest"
			sums := map[string]string{"sha256": hex.EncodeToString(h.Sum(nil)), "sha1": hex.EncodeToString(h1.Sum(nil)), "md5": hex.EncodeToString(hm.Sum(nil)), "sha512": hex.EncodeToString(h512.Sum(nil))}
			if readErr == nil && (closeErr != nil || n != item.Size || sums["sha256"] != file.SHA256) {
				readErr = Failed("source_changed")
			}
			for alg, want := range item.Checksum {
				if got, ok := sums[alg]; ok && got != want && readErr == nil {
					readErr = Failed("source_changed")
				}
			}
			if readErr == nil {
				failure.Stage = "pypi_distribution_validation"
				parsed, err := pypi.Inspect(spool, item.Size, file.Filename)
				if err != nil || parsed.Project != name || parsed.Version != item.Version || parsed.RequiresPython != file.RequiresPython {
					readErr = Failed("pypi_distribution_identity_invalid")
				}
			}
			_ = spool.Close()
			_ = os.Remove(temporary)
			if readErr != nil {
				return nil, readErr
			}
			entry := versions[d.Version]
			if entry == nil {
				if len(versions) > 0 {
					if int64(len(observed.Body)) > *budget {
						return nil, Failed("pypi_metadata_limit_exceeded")
					}
					*budget -= int64(len(observed.Body))
				}
				entry = &Entry{ID: uuid.NewString(), BatchID: b.ID, SourceRepository: sourceRepo, TargetRepositoryID: targetRepo, Format: "pypi", Identity: name + "@" + d.Version, State: "pending", Assets: []Asset{metadata}}
				versions[d.Version] = entry
			}
			a := Asset{SourcePath: item.Path, TargetPath: file.Filename, Size: item.Size, SHA256: file.SHA256, Validator: content.Validator, MediaType: "application/octet-stream"}
			if a.Validator == "" {
				a.Validator = "sha256:" + a.SHA256
			}
			entry.Assets = append(entry.Assets, a)
		}
		for _, entry := range versions {
			if err = s.verifyPyPISource(ctx, b, *entry); err != nil {
				return nil, err
			}
			entries = append(entries, *entry)
		}
	}
	return entries, nil
}
func (s *Service) verifyPyPISource(ctx context.Context, b Batch, e Entry) error {
	if e.Format != "pypi" {
		return nil
	}
	_, _, err := PyPIEntryMetadata(e)
	if err != nil {
		return err
	}
	source, ok := s.source.(PyPISource)
	if !ok {
		return Failed("pypi_source_unavailable")
	}
	name, _, _ := strings.Cut(e.Identity, "@")
	current, err := source.ReadPyPISimple(ctx, b.SourceID, b.SourceVersion, e.SourceRepository, name)
	if err != nil {
		return err
	}
	if err = s.store.RecordScanProgress(ctx, b.ID, b.LeaseToken, 0, int64(len(current.Body))); err != nil {
		return err
	}
	for _, a := range e.Assets {
		if a.TargetPath == PyPISimplePath {
			if digestBytes(current.Body) != a.SHA256 {
				return Failed("source_changed")
			}
			return nil
		}
	}
	return Failed("pypi_closure_incomplete")
}
func (s *Service) openEntryAsset(ctx context.Context, b Batch, e Entry, a Asset) (*Content, error) {
	if e.Format != "pypi" || a.TargetPath != PyPISimplePath {
		return s.source.OpenAsset(ctx, b.SourceID, b.SourceVersion, e.SourceRepository, AssetRequest{Format: e.Format, Path: a.SourcePath, Size: a.Size, Validator: a.Validator})
	}
	source, ok := s.source.(PyPISource)
	if !ok {
		return nil, Failed("pypi_source_unavailable")
	}
	name, _, _ := strings.Cut(e.Identity, "@")
	observed, err := source.ReadPyPISimple(ctx, b.SourceID, b.SourceVersion, e.SourceRepository, name)
	if err != nil {
		return nil, err
	}
	return &Content{Body: io.NopCloser(bytes.NewReader(observed.Body)), Validator: "sha256:" + digestBytes(observed.Body), MediaType: "application/json"}, nil
}
