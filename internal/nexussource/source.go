// Package nexussource owns read-only Nexus source discovery and native mapping.
package nexussource

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ccsert/artifact-gateway/internal/egress"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/ccsert/artifact-gateway/internal/secrets"
	"github.com/google/uuid"
)

var (
	ErrNotFound        = errors.New("migration source not found")
	ErrVersionConflict = errors.New("migration source version conflict")
)

// Failure carries a bounded public code, never an upstream response, URL or credential.
type Failure struct{ Code string }

func (e *Failure) Error() string { return e.Code }
func fail(code string) error     { return &Failure{Code: code} }

type Source struct {
	ID, Name, BaseURL, Version string
	CredentialCiphertext       string `json:"-"`
	CreatedAt, UpdatedAt       time.Time
}
type Credentials struct{ Username, Password string }
type Input struct {
	Name, BaseURL    string
	Credentials      *Credentials
	ClearCredentials bool
}

// Store is deliberately independent of the aggregate GatewayStore.
type Store interface {
	Create(context.Context, Source) (Source, error)
	Get(context.Context, string) (Source, error)
	List(context.Context, int, string) ([]Source, string, error)
	Update(context.Context, Source, string) (Source, error)
	Delete(context.Context, string, string) error
}
type TargetReader interface {
	GetHostedRepositoryByName(context.Context, string) (repository.HostedRepository, error)
	GetHostedGroupByName(context.Context, string) (repository.HostedGroup, error)
}
type Options struct {
	PrivateEndpoints map[string][]netip.Prefix
	Client           *http.Client
	EgressHooks      *egress.Hooks
	// OperationTimeout can shorten, but never increase, the 30 second ceiling.
	OperationTimeout time.Duration
}
type Service struct {
	store   Store
	targets TargetReader
	options Options
}

func New(store Store, targets TargetReader, options Options) *Service {
	return &Service{store: store, targets: targets, options: options}
}
func (s *Service) Available() bool { return s != nil && s.store != nil }

var sourceName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$`)
var targetName = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
var version = regexp.MustCompile(`^[1-9][0-9]{0,18}$`)

func validBase(raw string) (*url.URL, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || len(raw) > 2048 || strings.ContainsAny(raw, "\r\n\x00\\") {
		return nil, false
	}
	if u.RawPath != "" || strings.Contains(u.Path, "//") {
		return nil, false
	}
	for _, part := range strings.Split(u.Path, "/") {
		if part == "." || part == ".." {
			return nil, false
		}
	}
	u.Path = strings.TrimRight(u.Path, "/")
	return u, true
}
func (s *Service) apply(in Input, old Source) (Source, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" || utf8.RuneCountInString(name) > 128 || strings.ContainsAny(name, "\r\n\x00") {
		return Source{}, fail("invalid_source")
	}
	u, ok := validBase(in.BaseURL)
	if !ok {
		return Source{}, fail("invalid_source")
	}
	if in.Credentials != nil && in.ClearCredentials {
		return Source{}, fail("invalid_source")
	}
	if old.ID == "" {
		old.ID = uuid.NewString()
	}
	if old.CredentialCiphertext != "" && old.BaseURL != u.String() && in.Credentials == nil && !in.ClearCredentials {
		return Source{}, fail("credential_replacement_required")
	}
	old.Name, old.BaseURL = name, u.String()
	if in.ClearCredentials {
		old.CredentialCiphertext = ""
	}
	if in.Credentials != nil {
		c := in.Credentials
		if c.Username == "" || len(c.Username) > 256 || c.Password == "" || len(c.Password) > 4096 || strings.ContainsAny(c.Username, ":\r\n\x00") || strings.ContainsAny(c.Password, "\r\n\x00") {
			return Source{}, fail("invalid_source")
		}
		plaintext, _ := json.Marshal(c)
		sealed, err := secrets.Seal("nexus-source:"+old.ID, string(plaintext))
		if err != nil {
			return Source{}, fail("encryption_key_unavailable")
		}
		old.CredentialCiphertext = sealed
	}
	return old, nil
}
func (s *Service) Create(ctx context.Context, in Input) (Source, error) {
	v, err := s.apply(in, Source{})
	if err != nil {
		return Source{}, err
	}
	return s.store.Create(ctx, v)
}
func (s *Service) Get(ctx context.Context, id string) (Source, error) { return s.store.Get(ctx, id) }
func (s *Service) List(ctx context.Context, limit int, cursor string) ([]Source, string, error) {
	if limit < 1 || limit > 100 {
		return nil, "", fail("invalid_request")
	}
	if cursor != "" {
		if _, err := uuid.Parse(cursor); err != nil {
			return nil, "", fail("invalid_request")
		}
	}
	return s.store.List(ctx, limit, cursor)
}
func (s *Service) Update(ctx context.Context, id string, in Input, expected string) (Source, error) {
	if !version.MatchString(expected) {
		return Source{}, fail("invalid_request")
	}
	old, err := s.store.Get(ctx, id)
	if err != nil {
		return Source{}, err
	}
	if old.Version != expected {
		return Source{}, ErrVersionConflict
	}
	v, err := s.apply(in, old)
	if err != nil {
		return Source{}, err
	}
	return s.store.Update(ctx, v, expected)
}
func (s *Service) Delete(ctx context.Context, id, expected string) error {
	if !version.MatchString(expected) {
		return fail("invalid_request")
	}
	return s.store.Delete(ctx, id, expected)
}

type Field struct{ Field, Disposition, Reason string }
type Repository struct {
	Name, Format, Type, VersionPolicy, Status, Reason, RemoteURL string
	Members                                                      []string
	Fields                                                       []Field
}
type Stats struct {
	Requests, Pages int
	ResponseBytes   int64
}
type Preflight struct {
	SourceID, SourceVersion string
	Repositories            []Repository
	Stats                   Stats
}
type Asset struct {
	Repository, Path, DownloadURL string
	Size                          int64
	Checksum                      map[string]string
	// npm identities are supplied by Components REST and checked against packument.
	PackageName, Version string
}
type DiscoveryInput struct {
	Repositories        []string
	MaxPages, MaxAssets int
}
type UnsupportedAsset struct{ Repository, Path, Reason string }
type Discovery struct {
	UnsupportedAssets []UnsupportedAsset
	Preflight
	Assets []Asset
}
type Mapping struct {
	SourceRepository, TargetName, Format, Type string
	TargetMembers                              []string
}
type MappingResult struct {
	SourceRepository, TargetName, Status string
	Reasons, TargetMembers               []string
	Fields                               []Field
}
type MappingPreview struct {
	SourceID, SourceVersion string
	Valid                   bool
	Mappings                []MappingResult
	Stats                   Stats
}
