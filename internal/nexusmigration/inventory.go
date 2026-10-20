package nexusmigration

import (
	"context"
	"errors"

	"github.com/ccsert/artifact-gateway/internal/nexussource"
)

// These are independent durable-inventory budgets, not the preview endpoint's
// operation-wide HTTP/page budget. Planning still admits only a bounded manifest.
const (
	MaxInventoryPages         = 10000
	MaxInventoryAssets        = 100000
	MaxInventoryMetadataBytes = 64 << 20
)

// InventoryScan and InventoryPage are private ledger receipts. Raw source cursors
// never enter Batch responses, management cursors, diagnostics or audit records.
type InventoryScan struct {
	BindingSHA256                                       string
	Repositories                                        []nexussource.Repository
	RepositoryIndex                                     int
	Cursor                                              string
	PageCount, VerifiedPages, AssetCount, MetadataBytes int64
}

func (s InventoryScan) Enumerated() bool { return s.RepositoryIndex == len(s.Repositories) }
func (s InventoryScan) Verified() bool   { return s.Enumerated() && s.PageCount == s.VerifiedPages }

type InventoryPage struct {
	SourceRepository, Cursor, SHA256 string
	Page                             nexussource.InventoryPage
}

func InventoryBinding(b Batch, repos []nexussource.Repository) string {
	return digest(struct {
		SourceID, SourceVersion, Policy string
		Mappings                        []Mapping
		Repositories                    []nexussource.Repository
	}{b.SourceID, b.SourceVersion, b.PolicyVersion, b.Mappings, repos})
}

func InventoryPageDigest(page nexussource.InventoryPage) string {
	// Wire whitespace/JSON field ordering and transport byte counters are not
	// source identities. Canonical paths, complete hashes and cursor links are.
	return digest(struct {
		Assets               []nexussource.Asset
		Unsupported          []nexussource.UnsupportedAsset
		Next, MetadataSHA256 string
	}{page.Assets, page.UnsupportedAssets, page.NextCursor, page.MetadataSHA256})
}

func (s *Service) discoverInventory(ctx context.Context, b Batch, failure *ScanFailure) (nexussource.Discovery, error) {
	names := make([]string, 0, len(b.Mappings))
	for _, mp := range b.Mappings {
		names = append(names, mp.SourceRepository)
	}
	repos, err := s.source.InventoryRepositories(ctx, b.SourceID, b.SourceVersion, names)
	if err != nil {
		return nexussource.Discovery{}, err
	}
	binding := InventoryBinding(b, repos)
	scan, err := s.store.InventoryScan(ctx, b.ID)
	if errors.Is(err, ErrNotFound) {
		err = s.store.BeginInventoryScan(ctx, b, InventoryScan{BindingSHA256: binding, Repositories: repos})
		if err == nil {
			scan, err = s.store.InventoryScan(ctx, b.ID)
		}
	}
	if err != nil {
		return nexussource.Discovery{}, err
	}
	if scan.BindingSHA256 != binding {
		return nexussource.Discovery{}, Failed("source_changed")
	}
	for !scan.Enumerated() {
		repo := scan.Repositories[scan.RepositoryIndex]
		failure.SourceRepository = repo.Name
		page, err := s.source.ReadInventoryPage(ctx, b.SourceID, b.SourceVersion, repo, scan.Cursor)
		if err != nil {
			return nexussource.Discovery{}, err
		}
		if err = s.store.AppendInventoryPage(ctx, b, scan, page); err != nil {
			return nexussource.Discovery{}, err
		}
		scan, err = s.store.InventoryScan(ctx, b.ID)
		if err != nil {
			return nexussource.Discovery{}, err
		}
	}
	// A second bounded observation checks every committed page and terminal link.
	// It detects observed drift; Nexus REST still supplies no transactional snapshot.
	byName := map[string]nexussource.Repository{}
	for _, repo := range scan.Repositories {
		byName[repo.Name] = repo
	}
	for !scan.Verified() {
		receipt, err := s.store.InventoryPage(ctx, b.ID, scan.VerifiedPages)
		if err != nil {
			return nexussource.Discovery{}, err
		}
		failure.SourceRepository = receipt.SourceRepository
		current, err := s.source.ReadInventoryPage(ctx, b.SourceID, b.SourceVersion, byName[receipt.SourceRepository], receipt.Cursor)
		if err != nil {
			return nexussource.Discovery{}, err
		}
		if InventoryPageDigest(current) != receipt.SHA256 {
			return nexussource.Discovery{}, Failed("source_changed")
		}
		if err = s.store.VerifyInventoryPage(ctx, b, scan.VerifiedPages, receipt.SHA256, current.MetadataBytes); err != nil {
			return nexussource.Discovery{}, err
		}
		scan, err = s.store.InventoryScan(ctx, b.ID)
		if err != nil {
			return nexussource.Discovery{}, err
		}
	}
	out := nexussource.Discovery{Preflight: nexussource.Preflight{SourceID: b.SourceID, SourceVersion: b.SourceVersion, Repositories: repos}, Assets: []nexussource.Asset{}, UnsupportedAssets: []nexussource.UnsupportedAsset{}}
	for ordinal := int64(0); ordinal < scan.PageCount; ordinal++ {
		page, err := s.store.InventoryPage(ctx, b.ID, ordinal)
		if err != nil {
			return nexussource.Discovery{}, err
		}
		out.Assets = append(out.Assets, page.Page.Assets...)
		out.UnsupportedAssets = append(out.UnsupportedAssets, page.Page.UnsupportedAssets...)
	}
	return out, nil
}
