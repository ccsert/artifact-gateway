package repository

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	ErrNotFound            = errors.New("group not found")
	ErrDisabled            = errors.New("group is disabled")
	ErrNameExists          = errors.New("group name already exists")
	ErrIdempotencyConflict = errors.New("idempotency key conflicts with request")
)

const mavenObjectClaimLease = 5 * time.Minute

// Format is the immutable protocol family served by a Native Hosted
// Repository. It deliberately remains distinct from the legacy Group model.
type Format string

const (
	FormatRaw   Format = "raw"
	FormatOCI   Format = "oci"
	FormatMaven Format = "maven"
)

type RepositoryState string

const (
	RepositoryActive   RepositoryState = "active"
	RepositoryDeleting RepositoryState = "deleting"
)

type HostedRepository struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Format    Format          `json:"format"`
	State     RepositoryState `json:"state"`
	Version   string          `json:"version"`
	CreatedAt time.Time       `json:"-"`
}

// HostedRepositoryStore owns V3 Native Hosted configuration. Every protocol
// adapter must check State before it permits a read or write.
type HostedRepositoryStore interface {
	CreateHostedRepository(context.Context, HostedRepository) (HostedRepository, error)
	CreateHostedRepositoryIdempotently(context.Context, HostedRepository, string, string, string) (HostedRepository, bool, error)
	ListHostedRepositories(context.Context, int, string) ([]HostedRepository, string, error)
	GetHostedRepository(context.Context, string) (HostedRepository, error)
	GetHostedRepositoryByName(context.Context, string) (HostedRepository, error)
	DisableHostedRepository(context.Context, string) (HostedRepository, error)
}

// NativeMavenStore contains only committed Maven metadata. Object bytes live in
// the object store; staging rows never participate in protocol reads.
type NativeMavenStore interface {
	CreateMavenPublishSession(context.Context, MavenPublishSession) (MavenPublishSession, error)
	FindOpenMavenPublishSession(context.Context, string, string, string) (MavenPublishSession, error)
	FindMavenPublishSession(context.Context, string, string, string) (MavenPublishSession, error)
	FindAnyMavenPublishSession(context.Context, string, string) (MavenPublishSession, error)
	AppendMavenPublishObject(context.Context, string, MavenDeclaredObject) error
	SetMavenPublishPom(context.Context, string, string) error
	CreateMavenPublishSessionIdempotently(context.Context, MavenPublishSession, string, string, string, string) (MavenPublishSession, bool, error)
	GetMavenPublishSession(context.Context, string) (MavenPublishSession, error)
	MarkMavenPublishObject(context.Context, string, string, string) error
	CommitMavenPublishSession(context.Context, string, []MavenAsset) (MavenArtifact, error)
	GetMavenAsset(context.Context, string, string) (MavenAsset, error)
	ListMavenArtifacts(context.Context, string) ([]MavenArtifact, error)
	ClaimExpiredMavenObjectIntents(context.Context, time.Time, int) ([]MavenObjectIntent, error)
	MavenObjectIntentHasReference(context.Context, string) (bool, error)
	DeleteClaimedMavenObjectIntent(context.Context, string, string) error
	ReleaseClaimedMavenObjectIntent(context.Context, string, string) error
}

// NativeOCIStore owns the registry metadata. Blob bytes are deliberately not
// represented here: they live in the object store and are only made visible
// after one of these transactional metadata operations succeeds.
type NativeOCIStore interface {
	CreateOCIUpload(context.Context, OCIUpload) (OCIUpload, error)
	LockOCIUpload(context.Context, string) (func(), error)
	LockOCIObject(context.Context, string) (func(), error)
	GetOCIUpload(context.Context, string) (OCIUpload, error)
	UpdateOCIUpload(context.Context, string, int64) (OCIUpload, error)
	StageOCIObjectIntent(context.Context, OCIObjectIntent) error
	CompleteOCIUpload(context.Context, string, OCIBlob) (OCIBlob, error)
	ExpireOCIUploads(context.Context, time.Time, int) ([]OCIUpload, error)
	ListUncollectedOCIUploads(context.Context, int) ([]OCIUpload, error)
	MarkOCIUploadCollected(context.Context, string) error
	ListUnclaimedOCIObjectIntents(context.Context, time.Time, int) ([]OCIObjectIntent, error)
	OCIObjectIntentIsUnclaimed(context.Context, string) (bool, error)
	MarkOCIObjectIntentCollected(context.Context, string) error
	MountOCIBlob(context.Context, string, string) (OCIBlob, error)
	MountOCIBlobFrom(context.Context, string, string, string) (OCIBlob, error)
	GetOCIBlob(context.Context, string, string) (OCIBlob, error)
	PutOCIManifest(context.Context, OCIManifest, string) (OCIManifest, error)
	GetOCIManifest(context.Context, string, string, string) (OCIManifest, error)
	DeleteOCIManifest(context.Context, string, string, string) error
}

type NativeRawStore interface {
	LockRawObject(context.Context, string) (func(), error)
	StageRawObject(context.Context, RawObject) error
	PutRawAsset(context.Context, RawAsset) (RawAsset, error)
	GetRawAsset(context.Context, string, string) (RawAsset, error)
	DeleteRawAsset(context.Context, string, string) error
	ListUnreferencedRawObjects(context.Context, time.Time, int) ([]RawObject, error)
	RawObjectIsUnreferenced(context.Context, string) (bool, error)
	MarkRawObjectCollected(context.Context, string) error
}

type RawAsset struct {
	RepositoryID, Path, Digest, ObjectKey, ContentType string
	Size                                               int64
}
type RawObject struct {
	Digest, ObjectKey      string
	Size                   int64
	CreatedAt, CollectedAt time.Time
}

type OCIUpload struct {
	ID, RepositoryID, Name, ObjectKey, State string
	Offset                                   int64
	ExpiresAt                                time.Time
	CollectedAt                              time.Time
}

type OCIBlob struct {
	Digest, ObjectKey string
	Size              int64
}
type OCIObjectIntent struct {
	ObjectKey, Digest                 string
	Size                              int64
	CreatedAt, ClaimedAt, CollectedAt time.Time
}

type OCIManifest struct {
	RepositoryID, Name, Digest, ObjectKey, MediaType string
	Size                                             int64
}

type MavenDeclaredObject struct {
	Name, Digest string
	Size         int64
}
type MavenPublishSession struct {
	ID, RepositoryID, Coordinate, Publisher, PomObject, State string
	Objects                                                   []MavenDeclaredObject
	ExpiresAt                                                 time.Time
}
type MavenAsset struct {
	RepositoryID, Path, ObjectKey, Digest string
	Size                                  int64
}
type MavenArtifact struct {
	ID, RepositoryID, Coordinate, Digest, State string
	CreatedAt                                   time.Time
}
type MavenObjectIntent struct{ ObjectKey, ClaimToken string }

type MemberType string

const (
	MemberHosted MemberType = "hosted"
	MemberProxy  MemberType = "proxy"
)

type Member struct {
	Name         string     `json:"name"`
	Type         MemberType `json:"type"`
	Endpoint     string     `json:"endpoint"`
	Position     int        `json:"position"`
	Anonymous    bool       `json:"anonymous"`
	AllowedHosts []string   `json:"allowedHosts,omitempty"`
}

type Group struct {
	Name            string    `json:"name"`
	Enabled         bool      `json:"enabled"`
	Anonymous       bool      `json:"anonymous"`
	CacheQuotaBytes int64     `json:"cacheQuotaBytes,omitempty"`
	Members         []Member  `json:"members"`
	CreatedAt       time.Time `json:"createdAt"`
}

type AuditOutcome string

const (
	AuditResolved          AuditOutcome = "resolved"
	AuditInternalPreferred AuditOutcome = "internal_preferred"
	AuditNotFound          AuditOutcome = "not_found"
	AuditGroupDisabled     AuditOutcome = "group_disabled"
	AuditStorageError      AuditOutcome = "storage_error"
	AuditUpstreamError     AuditOutcome = "upstream_error"
	AuditAccessDenied      AuditOutcome = "access_denied"
	AuditProxyDenied       AuditOutcome = "proxy_denied"
)

type AuditRecord struct {
	GroupName                                                                               string
	Repository                                                                              string
	MemberName                                                                              string
	Outcome                                                                                 AuditOutcome
	Actor                                                                                   string
	OccurredAt                                                                              time.Time
	Format, Resource, Representation, MemberType, UpstreamHost, Operation, CacheDisposition string
	RequestID, TraceID                                                                      string
	Status                                                                                  int
	Bytes                                                                                   int64
}

type AuditQuery struct {
	GroupName  string
	Repository string
	Limit      int
}

type Store interface {
	CreateGroup(context.Context, Group) (Group, error)
	GetGroup(context.Context, string) (Group, error)
	DisableGroup(context.Context, string) error
	RecordAudit(context.Context, AuditRecord) error
	ListAudits(context.Context, AuditQuery) ([]AuditRecord, error)
}

type AuditStore interface {
	ListAudits(context.Context, AuditQuery) ([]AuditRecord, error)
}

// MavenStore keeps Maven Group configuration separate from OCI Groups.
type MavenStore interface {
	CreateMavenGroup(context.Context, Group) (Group, error)
	GetMavenGroup(context.Context, string) (Group, error)
	DisableMavenGroup(context.Context, string) error
	RecordAudit(context.Context, AuditRecord) error
}

type RawStore interface {
	CreateRawGroup(context.Context, Group) (Group, error)
	GetRawGroup(context.Context, string) (Group, error)
	DisableRawGroup(context.Context, string) error
	RecordAudit(context.Context, AuditRecord) error
}

// ConanStore deliberately uses a separate Group namespace from OCI because a
// Conan Group is a distinct format and authorization boundary.
type ConanStore interface {
	CreateConanGroup(context.Context, Group) (Group, error)
	GetConanGroup(context.Context, string) (Group, error)
	DisableConanGroup(context.Context, string) error
	RecordAudit(context.Context, AuditRecord) error
}

type MemoryStore struct {
	mu                 sync.RWMutex
	groups             map[string]Group
	mavenGroups        map[string]Group
	rawGroups          map[string]Group
	conanGroups        map[string]Group
	Audits             []AuditRecord
	hostedRepositories map[string]HostedRepository
	idempotencyRecords map[string]idempotencyRecord
	mavenSessions      map[string]MavenPublishSession
	mavenUploads       map[string]map[string]string
	mavenAssets        map[string]MavenAsset
	mavenArtifacts     map[string]MavenArtifact
	mavenSessionKeys   map[string]idempotencyRecord
	mavenObjectIntents map[string]mavenObjectIntent
	mavenObjectRefs    map[string]bool
	ociUploads         map[string]OCIUpload
	ociBlobs           map[string]OCIBlob
	ociRepositoryBlobs map[string]map[string]bool
	ociManifests       map[string]OCIManifest
	ociTags            map[string]string
	ociUploadLocks     map[string]*sync.Mutex
	ociObjectLocks     map[string]*sync.Mutex
	rawAssets          map[string]RawAsset
	rawObjects         map[string]RawObject
	rawObjectLocks     map[string]*sync.Mutex
	ociObjectIntents   map[string]OCIObjectIntent
}

type mavenObjectIntent struct {
	createdAt, claimedAt, deletedAt time.Time
	claimToken                      string
}

type idempotencyRecord struct {
	payload, repositoryID string
	expiresAt             time.Time
}

func ociManifestKey(repositoryID, name, digest string) string {
	return repositoryID + "\x00" + name + "\x00" + digest
}
func ociTagKey(repositoryID, name, tag string) string {
	return repositoryID + "\x00" + name + "\x00" + tag
}
func rawAssetKey(repositoryID, path string) string { return repositoryID + "\x00" + path }

func (s *MemoryStore) LockRawObject(_ context.Context, digest string) (func(), error) {
	s.mu.Lock()
	lock := s.rawObjectLocks[digest]
	if lock == nil {
		lock = &sync.Mutex{}
		s.rawObjectLocks[digest] = lock
	}
	s.mu.Unlock()
	lock.Lock()
	return lock.Unlock, nil
}

func (s *MemoryStore) StageRawObject(_ context.Context, object RawObject) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.rawObjects[object.Digest]; ok {
		existing.CollectedAt = time.Time{}
		s.rawObjects[object.Digest] = existing
		return nil
	}
	object.CreatedAt = time.Now().UTC()
	s.rawObjects[object.Digest] = object
	return nil
}

func (s *MemoryStore) PutRawAsset(_ context.Context, asset RawAsset) (RawAsset, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if object, ok := s.rawObjects[asset.Digest]; ok {
		asset.ObjectKey = object.ObjectKey
		asset.Size = object.Size
	} else {
		s.rawObjects[asset.Digest] = RawObject{Digest: asset.Digest, ObjectKey: asset.ObjectKey, Size: asset.Size, CreatedAt: time.Now().UTC()}
	}
	s.rawAssets[rawAssetKey(asset.RepositoryID, asset.Path)] = asset
	return asset, nil
}
func (s *MemoryStore) GetRawAsset(_ context.Context, repositoryID, path string) (RawAsset, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	asset, ok := s.rawAssets[rawAssetKey(repositoryID, path)]
	if !ok {
		return RawAsset{}, ErrNotFound
	}
	return asset, nil
}
func (s *MemoryStore) DeleteRawAsset(_ context.Context, repositoryID, path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := rawAssetKey(repositoryID, path)
	if _, ok := s.rawAssets[key]; !ok {
		return ErrNotFound
	}
	delete(s.rawAssets, key)
	return nil
}
func (s *MemoryStore) ListUnreferencedRawObjects(_ context.Context, before time.Time, limit int) ([]RawObject, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var objects []RawObject
	for digest, object := range s.rawObjects {
		if len(objects) >= limit || !object.CollectedAt.IsZero() || !object.CreatedAt.Before(before) {
			continue
		}
		referenced := false
		for _, asset := range s.rawAssets {
			if asset.Digest == digest {
				referenced = true
				break
			}
		}
		if !referenced {
			objects = append(objects, object)
		}
	}
	return objects, nil
}
func (s *MemoryStore) RawObjectIsUnreferenced(_ context.Context, digest string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	object, ok := s.rawObjects[digest]
	if !ok || !object.CollectedAt.IsZero() {
		return false, nil
	}
	for _, asset := range s.rawAssets {
		if asset.Digest == digest {
			return false, nil
		}
	}
	return true, nil
}
func (s *MemoryStore) MarkRawObjectCollected(_ context.Context, digest string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	object, ok := s.rawObjects[digest]
	if !ok || !object.CollectedAt.IsZero() {
		return ErrNotFound
	}
	for _, asset := range s.rawAssets {
		if asset.Digest == digest {
			return ErrNotFound
		}
	}
	object.CollectedAt = time.Now().UTC()
	s.rawObjects[digest] = object
	return nil
}

func (s *MemoryStore) CreateOCIUpload(_ context.Context, upload OCIUpload) (OCIUpload, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ociUploads[upload.ID] = upload
	return upload, nil
}
func (s *MemoryStore) StageOCIObjectIntent(_ context.Context, intent OCIObjectIntent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.ociObjectIntents[intent.ObjectKey]; !exists {
		intent.CreatedAt = time.Now().UTC()
		s.ociObjectIntents[intent.ObjectKey] = intent
	}
	return nil
}
func (s *MemoryStore) LockOCIUpload(_ context.Context, id string) (func(), error) {
	s.mu.Lock()
	lock := s.ociUploadLocks[id]
	if lock == nil {
		lock = &sync.Mutex{}
		s.ociUploadLocks[id] = lock
	}
	s.mu.Unlock()
	lock.Lock()
	return lock.Unlock, nil
}
func (s *MemoryStore) LockOCIObject(_ context.Context, objectKey string) (func(), error) {
	s.mu.Lock()
	lock := s.ociObjectLocks[objectKey]
	if lock == nil {
		lock = &sync.Mutex{}
		s.ociObjectLocks[objectKey] = lock
	}
	s.mu.Unlock()
	lock.Lock()
	return lock.Unlock, nil
}
func (s *MemoryStore) GetOCIUpload(_ context.Context, id string) (OCIUpload, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.ociUploads[id]
	if !ok {
		return OCIUpload{}, ErrNotFound
	}
	return v, nil
}
func (s *MemoryStore) UpdateOCIUpload(_ context.Context, id string, offset int64) (OCIUpload, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.ociUploads[id]
	if !ok || v.State != "open" || time.Now().After(v.ExpiresAt) {
		return OCIUpload{}, ErrNotFound
	}
	v.Offset = offset
	s.ociUploads[id] = v
	return v, nil
}
func (s *MemoryStore) CompleteOCIUpload(_ context.Context, id string, blob OCIBlob) (OCIBlob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.ociUploads[id]
	if !ok || v.State != "open" || time.Now().After(v.ExpiresAt) {
		return OCIBlob{}, ErrNotFound
	}
	if current, exists := s.ociBlobs[blob.Digest]; exists {
		blob = current
	} else {
		s.ociBlobs[blob.Digest] = blob
	}
	if s.ociRepositoryBlobs[v.RepositoryID] == nil {
		s.ociRepositoryBlobs[v.RepositoryID] = map[string]bool{}
	}
	s.ociRepositoryBlobs[v.RepositoryID][blob.Digest] = true
	v.State = "completed"
	s.ociUploads[id] = v
	delete(s.ociObjectIntents, blob.ObjectKey)
	return blob, nil
}
func (s *MemoryStore) ExpireOCIUploads(_ context.Context, before time.Time, limit int) ([]OCIUpload, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var uploads []OCIUpload
	for id, upload := range s.ociUploads {
		if len(uploads) >= limit || upload.State != "open" || !upload.ExpiresAt.Before(before) {
			continue
		}
		upload.State = "expired"
		s.ociUploads[id] = upload
		uploads = append(uploads, upload)
	}
	return uploads, nil
}
func (s *MemoryStore) ListUncollectedOCIUploads(_ context.Context, limit int) ([]OCIUpload, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var uploads []OCIUpload
	for _, upload := range s.ociUploads {
		if len(uploads) >= limit {
			break
		}
		if upload.State == "expired" && upload.CollectedAt.IsZero() {
			uploads = append(uploads, upload)
		}
	}
	return uploads, nil
}
func (s *MemoryStore) MarkOCIUploadCollected(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	upload, ok := s.ociUploads[id]
	if !ok || upload.State != "expired" {
		return ErrNotFound
	}
	upload.CollectedAt = time.Now().UTC()
	s.ociUploads[id] = upload
	return nil
}
func (s *MemoryStore) ListUnclaimedOCIObjectIntents(_ context.Context, before time.Time, limit int) ([]OCIObjectIntent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var intents []OCIObjectIntent
	for _, intent := range s.ociObjectIntents {
		if len(intents) >= limit {
			break
		}
		if intent.ClaimedAt.IsZero() && intent.CollectedAt.IsZero() && intent.CreatedAt.Before(before) {
			intents = append(intents, intent)
		}
	}
	return intents, nil
}
func (s *MemoryStore) OCIObjectIntentIsUnclaimed(_ context.Context, objectKey string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	intent, ok := s.ociObjectIntents[objectKey]
	return ok && intent.ClaimedAt.IsZero() && intent.CollectedAt.IsZero(), nil
}
func (s *MemoryStore) MarkOCIObjectIntentCollected(_ context.Context, objectKey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	intent, ok := s.ociObjectIntents[objectKey]
	if !ok || !intent.ClaimedAt.IsZero() || !intent.CollectedAt.IsZero() {
		return ErrNotFound
	}
	intent.CollectedAt = time.Now().UTC()
	s.ociObjectIntents[objectKey] = intent
	return nil
}
func (s *MemoryStore) MountOCIBlob(_ context.Context, repositoryID, digest string) (OCIBlob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.ociBlobs[digest]
	if !ok {
		return OCIBlob{}, ErrNotFound
	}
	if s.ociRepositoryBlobs[repositoryID] == nil {
		s.ociRepositoryBlobs[repositoryID] = map[string]bool{}
	}
	s.ociRepositoryBlobs[repositoryID][digest] = true
	return v, nil
}
func (s *MemoryStore) MountOCIBlobFrom(_ context.Context, repositoryID, sourceRepositoryID, digest string) (OCIBlob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ociRepositoryBlobs[sourceRepositoryID][digest] {
		return OCIBlob{}, ErrNotFound
	}
	v, ok := s.ociBlobs[digest]
	if !ok {
		return OCIBlob{}, ErrNotFound
	}
	if s.ociRepositoryBlobs[repositoryID] == nil {
		s.ociRepositoryBlobs[repositoryID] = map[string]bool{}
	}
	s.ociRepositoryBlobs[repositoryID][digest] = true
	return v, nil
}
func (s *MemoryStore) GetOCIBlob(_ context.Context, repositoryID, digest string) (OCIBlob, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.ociRepositoryBlobs[repositoryID][digest] {
		return OCIBlob{}, ErrNotFound
	}
	v, ok := s.ociBlobs[digest]
	if !ok {
		return OCIBlob{}, ErrNotFound
	}
	return v, nil
}
func (s *MemoryStore) PutOCIManifest(_ context.Context, manifest OCIManifest, reference string) (OCIManifest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := ociManifestKey(manifest.RepositoryID, manifest.Name, manifest.Digest)
	if existing, ok := s.ociManifests[key]; ok {
		manifest = existing
	} else {
		s.ociManifests[key] = manifest
	}
	if !strings.HasPrefix(reference, "sha256:") {
		s.ociTags[ociTagKey(manifest.RepositoryID, manifest.Name, reference)] = manifest.Digest
	}
	delete(s.ociObjectIntents, manifest.ObjectKey)
	return manifest, nil
}
func (s *MemoryStore) GetOCIManifest(_ context.Context, repositoryID, name, reference string) (OCIManifest, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	digest := reference
	if !strings.HasPrefix(digest, "sha256:") {
		digest = s.ociTags[ociTagKey(repositoryID, name, reference)]
	}
	v, ok := s.ociManifests[ociManifestKey(repositoryID, name, digest)]
	if !ok {
		return OCIManifest{}, ErrNotFound
	}
	return v, nil
}
func (s *MemoryStore) DeleteOCIManifest(_ context.Context, repositoryID, name, digest string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := ociManifestKey(repositoryID, name, digest)
	if _, ok := s.ociManifests[key]; !ok {
		return ErrNotFound
	}
	manifest := s.ociManifests[key]
	delete(s.ociManifests, key)
	s.ociObjectIntents[manifest.ObjectKey] = OCIObjectIntent{ObjectKey: manifest.ObjectKey, Digest: manifest.Digest, Size: manifest.Size, CreatedAt: time.Now().UTC()}
	for tag, target := range s.ociTags {
		if target == digest && strings.HasPrefix(tag, repositoryID+"\x00"+name+"\x00") {
			delete(s.ociTags, tag)
		}
	}
	return nil
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{groups: make(map[string]Group), mavenGroups: make(map[string]Group), rawGroups: make(map[string]Group), conanGroups: make(map[string]Group), hostedRepositories: make(map[string]HostedRepository), idempotencyRecords: make(map[string]idempotencyRecord), mavenSessions: make(map[string]MavenPublishSession), mavenUploads: make(map[string]map[string]string), mavenAssets: make(map[string]MavenAsset), mavenArtifacts: make(map[string]MavenArtifact), mavenSessionKeys: make(map[string]idempotencyRecord), mavenObjectIntents: make(map[string]mavenObjectIntent), mavenObjectRefs: make(map[string]bool), ociUploads: make(map[string]OCIUpload), ociBlobs: make(map[string]OCIBlob), ociRepositoryBlobs: make(map[string]map[string]bool), ociManifests: make(map[string]OCIManifest), ociTags: make(map[string]string), ociUploadLocks: make(map[string]*sync.Mutex), ociObjectLocks: make(map[string]*sync.Mutex), rawAssets: make(map[string]RawAsset), rawObjects: make(map[string]RawObject), rawObjectLocks: make(map[string]*sync.Mutex), ociObjectIntents: make(map[string]OCIObjectIntent)}
}

func (s *MemoryStore) CreateMavenPublishSession(_ context.Context, session MavenPublishSession) (MavenPublishSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mavenSessions[session.ID] = session
	s.mavenUploads[session.ID] = map[string]string{}
	return session, nil
}
func (s *MemoryStore) CreateMavenPublishSessionIdempotently(ctx context.Context, session MavenPublishSession, actor, target, key, payload string) (MavenPublishSession, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	recordKey := actor + "\x00" + target + "\x00" + key
	if record, ok := s.mavenSessionKeys[recordKey]; ok && time.Now().UTC().Before(record.expiresAt) {
		if record.payload != payload {
			return MavenPublishSession{}, false, ErrIdempotencyConflict
		}
		return s.mavenSessions[record.repositoryID], true, nil
	}
	s.mavenSessions[session.ID] = session
	s.mavenUploads[session.ID] = map[string]string{}
	s.mavenSessionKeys[recordKey] = idempotencyRecord{payload: payload, repositoryID: session.ID, expiresAt: time.Now().UTC().Add(24 * time.Hour)}
	return session, false, nil
}
func (s *MemoryStore) GetMavenPublishSession(_ context.Context, id string) (MavenPublishSession, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.mavenSessions[id]
	if !ok {
		return MavenPublishSession{}, ErrNotFound
	}
	return v, nil
}
func (s *MemoryStore) FindOpenMavenPublishSession(_ context.Context, repoID, coordinate, publisher string) (MavenPublishSession, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, v := range s.mavenSessions {
		if v.RepositoryID == repoID && v.Coordinate == coordinate && v.Publisher == publisher && v.State == "open" {
			return v, nil
		}
	}
	return MavenPublishSession{}, ErrNotFound
}
func (s *MemoryStore) FindMavenPublishSession(_ context.Context, repoID, coordinate, publisher string) (MavenPublishSession, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, v := range s.mavenSessions {
		if v.RepositoryID == repoID && v.Coordinate == coordinate && v.Publisher == publisher && v.State == "open" {
			return v, nil
		}
	}
	for _, v := range s.mavenSessions {
		if v.RepositoryID == repoID && v.Coordinate == coordinate && v.Publisher == publisher {
			return v, nil
		}
	}
	return MavenPublishSession{}, ErrNotFound
}
func (s *MemoryStore) FindAnyMavenPublishSession(_ context.Context, repoID, coordinate string) (MavenPublishSession, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, v := range s.mavenSessions {
		if v.RepositoryID == repoID && v.Coordinate == coordinate && v.State == "open" {
			return v, nil
		}
	}
	for _, v := range s.mavenSessions {
		if v.RepositoryID == repoID && v.Coordinate == coordinate {
			return v, nil
		}
	}
	return MavenPublishSession{}, ErrNotFound
}
func (s *MemoryStore) AppendMavenPublishObject(_ context.Context, id string, object MavenDeclaredObject) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.mavenSessions[id]
	if !ok || v.State != "open" {
		return ErrNotFound
	}
	for _, o := range v.Objects {
		if o.Name == object.Name {
			if o.Digest != object.Digest || o.Size != object.Size {
				return ErrNameExists
			}
			return nil
		}
	}
	v.Objects = append(v.Objects, object)
	s.mavenSessions[id] = v
	return nil
}
func (s *MemoryStore) SetMavenPublishPom(_ context.Context, id, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.mavenSessions[id]
	if !ok || v.State != "open" {
		return ErrNotFound
	}
	v.PomObject = name
	s.mavenSessions[id] = v
	return nil
}
func (s *MemoryStore) MarkMavenPublishObject(_ context.Context, id, name, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.mavenSessions[id]; !ok {
		return ErrNotFound
	}
	if intent, exists := s.mavenObjectIntents[key]; exists {
		if !intent.claimedAt.IsZero() && intent.deletedAt.IsZero() {
			return ErrDisabled
		}
		if !intent.deletedAt.IsZero() {
			intent.claimedAt, intent.deletedAt, intent.createdAt, intent.claimToken = time.Time{}, time.Time{}, time.Now().UTC(), ""
			s.mavenObjectIntents[key] = intent
		}
	} else {
		s.mavenObjectIntents[key] = mavenObjectIntent{createdAt: time.Now().UTC()}
	}
	s.mavenUploads[id][name] = key
	return nil
}
func (s *MemoryStore) CommitMavenPublishSession(_ context.Context, id string, assets []MavenAsset) (MavenArtifact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.mavenSessions[id]
	if !ok {
		return MavenArtifact{}, ErrNotFound
	}
	if session.State != "open" || time.Now().After(session.ExpiresAt) {
		return MavenArtifact{}, ErrDisabled
	}
	for _, o := range session.Objects {
		if s.mavenUploads[id][o.Name] == "" {
			return MavenArtifact{}, ErrDisabled
		}
		if intent := s.mavenObjectIntents[s.mavenUploads[id][o.Name]]; !intent.claimedAt.IsZero() {
			return MavenArtifact{}, ErrDisabled
		}
	}
	for _, existing := range s.mavenArtifacts {
		if existing.RepositoryID == session.RepositoryID && existing.Coordinate == session.Coordinate {
			return MavenArtifact{}, ErrNameExists
		}
	}
	for _, a := range assets {
		if intent := s.mavenObjectIntents[a.ObjectKey]; !intent.claimedAt.IsZero() || !intent.deletedAt.IsZero() {
			return MavenArtifact{}, ErrDisabled
		}
		k := a.RepositoryID + "\x00" + a.Path
		if _, exists := s.mavenAssets[k]; exists {
			return MavenArtifact{}, ErrNameExists
		}
		s.mavenAssets[k] = a
		s.mavenObjectRefs[a.ObjectKey] = true
	}
	artifact := MavenArtifact{ID: id, RepositoryID: session.RepositoryID, Coordinate: session.Coordinate, Digest: session.Objects[0].Digest, State: "visible", CreatedAt: time.Now().UTC()}
	s.mavenArtifacts[id] = artifact
	session.State = "committed"
	s.mavenSessions[id] = session
	return artifact, nil
}
func (s *MemoryStore) GetMavenAsset(_ context.Context, repositoryID, path string) (MavenAsset, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.mavenAssets[repositoryID+"\x00"+path]
	if !ok {
		return MavenAsset{}, ErrNotFound
	}
	return v, nil
}
func (s *MemoryStore) ListMavenArtifacts(_ context.Context, repositoryID string) ([]MavenArtifact, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []MavenArtifact{}
	for _, a := range s.mavenArtifacts {
		if a.RepositoryID == repositoryID {
			out = append(out, a)
		}
	}
	return out, nil
}
func (s *MemoryStore) ClaimExpiredMavenObjectIntents(_ context.Context, before time.Time, limit int) ([]MavenObjectIntent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	claimed := make([]MavenObjectIntent, 0, limit)
	for key, intent := range s.mavenObjectIntents {
		if len(claimed) == limit || (!intent.claimedAt.IsZero() && now.Sub(intent.claimedAt) < mavenObjectClaimLease) || !intent.deletedAt.IsZero() || intent.createdAt.After(before) || s.mavenObjectRefs[key] {
			continue
		}
		liveSession := false
		for sessionID, uploads := range s.mavenUploads {
			if uploadsKey := uploads; uploadsKey != nil {
				for _, uploadedKey := range uploadsKey {
					if uploadedKey == key {
						session := s.mavenSessions[sessionID]
						liveSession = session.State == "open" && session.ExpiresAt.After(time.Now())
						break
					}
				}
			}
			if liveSession {
				break
			}
		}
		if liveSession {
			continue
		}
		intent.claimedAt, intent.claimToken = now, newMavenObjectClaimToken()
		s.mavenObjectIntents[key] = intent
		claimed = append(claimed, MavenObjectIntent{ObjectKey: key, ClaimToken: intent.claimToken})
	}
	return claimed, nil
}
func (s *MemoryStore) MavenObjectIntentHasReference(_ context.Context, key string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.mavenObjectRefs[key], nil
}
func (s *MemoryStore) DeleteClaimedMavenObjectIntent(_ context.Context, key, claimToken string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	intent, ok := s.mavenObjectIntents[key]
	if !ok || intent.claimedAt.IsZero() || intent.claimToken != claimToken || !intent.deletedAt.IsZero() || s.mavenObjectRefs[key] {
		return ErrNotFound
	}
	intent.deletedAt = time.Now().UTC()
	s.mavenObjectIntents[key] = intent
	return nil
}
func (s *MemoryStore) ReleaseClaimedMavenObjectIntent(_ context.Context, key, claimToken string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	intent, ok := s.mavenObjectIntents[key]
	if !ok || intent.claimedAt.IsZero() || intent.claimToken != claimToken || !intent.deletedAt.IsZero() || s.mavenObjectRefs[key] {
		return ErrNotFound
	}
	intent.claimedAt, intent.claimToken = time.Time{}, ""
	s.mavenObjectIntents[key] = intent
	return nil
}

func newMavenObjectClaimToken() string {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	return hex.EncodeToString(value)
}

func (s *MemoryStore) CreateHostedRepository(_ context.Context, repo HostedRepository) (HostedRepository, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.hostedRepositories {
		if existing.Name == repo.Name {
			return HostedRepository{}, ErrNameExists
		}
	}
	repo.State = RepositoryActive
	repo.Version = "1"
	repo.CreatedAt = time.Now().UTC()
	s.hostedRepositories[repo.ID] = repo
	return repo, nil
}

func (s *MemoryStore) CreateHostedRepositoryIdempotently(_ context.Context, repo HostedRepository, actor, key, payload string) (HostedRepository, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	recordKey := actor + "\x00/repositories\x00" + key
	if record, ok := s.idempotencyRecords[recordKey]; ok && time.Now().UTC().Before(record.expiresAt) {
		if record.payload != payload {
			return HostedRepository{}, false, ErrIdempotencyConflict
		}
		return s.hostedRepositories[record.repositoryID], true, nil
	}
	for _, existing := range s.hostedRepositories {
		if existing.Name == repo.Name {
			return HostedRepository{}, false, ErrNameExists
		}
	}
	repo.State, repo.Version, repo.CreatedAt = RepositoryActive, "1", time.Now().UTC()
	s.hostedRepositories[repo.ID] = repo
	s.idempotencyRecords[recordKey] = idempotencyRecord{payload: payload, repositoryID: repo.ID, expiresAt: time.Now().UTC().Add(24 * time.Hour)}
	return repo, false, nil
}

func (s *MemoryStore) ListHostedRepositories(_ context.Context, limit int, after string) ([]HostedRepository, string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	items := make([]HostedRepository, 0, len(s.hostedRepositories))
	for _, repo := range s.hostedRepositories {
		items = append(items, repo)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	start := 0
	if after != "" {
		found := false
		for i, repo := range items {
			if repo.ID == after {
				start = i + 1
				found = true
				break
			}
		}
		if !found {
			return nil, "", ErrNotFound
		}
	}
	end := start + limit
	next := ""
	if end < len(items) {
		next = items[end-1].ID
	} else if start > len(items) {
		start = len(items)
		end = start
	} else {
		end = len(items)
	}
	return items[start:end], next, nil
}

func (s *MemoryStore) GetHostedRepository(_ context.Context, id string) (HostedRepository, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	repo, ok := s.hostedRepositories[id]
	if !ok {
		return HostedRepository{}, ErrNotFound
	}
	return repo, nil
}

func (s *MemoryStore) GetHostedRepositoryByName(_ context.Context, name string) (HostedRepository, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, repo := range s.hostedRepositories {
		if repo.Name == name {
			return repo, nil
		}
	}
	return HostedRepository{}, ErrNotFound
}

func (s *MemoryStore) DisableHostedRepository(_ context.Context, id string) (HostedRepository, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	repo, ok := s.hostedRepositories[id]
	if !ok || repo.State != RepositoryActive {
		return HostedRepository{}, ErrNotFound
	}
	repo.State = RepositoryDeleting
	repo.Version = "2"
	s.hostedRepositories[id] = repo
	return repo, nil
}

func (s *MemoryStore) CreateRawGroup(_ context.Context, group Group) (Group, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.rawGroups[group.Name]; ok {
		return Group{}, ErrNameExists
	}
	group.CreatedAt = time.Now().UTC()
	normalizeGroup(&group)
	s.rawGroups[group.Name] = group
	return group, nil
}
func (s *MemoryStore) GetRawGroup(_ context.Context, name string) (Group, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	group, ok := s.rawGroups[name]
	if !ok {
		return Group{}, ErrNotFound
	}
	return group, nil
}
func (s *MemoryStore) DisableRawGroup(_ context.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	group, ok := s.rawGroups[name]
	if !ok {
		return ErrNotFound
	}
	group.Enabled = false
	s.rawGroups[name] = group
	return nil
}

func (s *MemoryStore) CreateConanGroup(ctx context.Context, group Group) (Group, error) {
	return s.createConanGroup(ctx, group)
}
func (s *MemoryStore) createConanGroup(_ context.Context, group Group) (Group, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.conanGroups[group.Name]; exists {
		return Group{}, ErrNameExists
	}
	group.CreatedAt = time.Now().UTC()
	if group.CacheQuotaBytes == 0 {
		group.CacheQuotaBytes = 1 << 30
	}
	normalizeGroup(&group)
	s.conanGroups[group.Name] = group
	return group, nil
}
func (s *MemoryStore) GetConanGroup(_ context.Context, name string) (Group, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	group, exists := s.conanGroups[name]
	if !exists {
		return Group{}, ErrNotFound
	}
	return group, nil
}
func (s *MemoryStore) DisableConanGroup(_ context.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	group, exists := s.conanGroups[name]
	if !exists {
		return ErrNotFound
	}
	group.Enabled = false
	s.conanGroups[name] = group
	return nil
}

func (s *MemoryStore) CreateGroup(_ context.Context, group Group) (Group, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.groups[group.Name]; exists {
		return Group{}, ErrNameExists
	}
	group.CreatedAt = time.Now().UTC()
	normalizeGroup(&group)
	s.groups[group.Name] = group
	return group, nil
}

func normalizeGroup(group *Group) {
	group.Enabled = true
	sort.Slice(group.Members, func(i, j int) bool { return group.Members[i].Position < group.Members[j].Position })
}

func (s *MemoryStore) GetGroup(_ context.Context, name string) (Group, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	group, exists := s.groups[name]
	if !exists {
		return Group{}, ErrNotFound
	}
	return group, nil
}

func (s *MemoryStore) DisableGroup(_ context.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	group, exists := s.groups[name]
	if !exists {
		return ErrNotFound
	}
	group.Enabled = false
	s.groups[name] = group
	return nil
}

func (s *MemoryStore) RecordAudit(_ context.Context, record AuditRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Audits = append(s.Audits, record)
	return nil
}

func (s *MemoryStore) ListAudits(_ context.Context, query AuditQuery) ([]AuditRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	limit := query.Limit
	if limit <= 0 {
		limit = 100
	}
	records := make([]AuditRecord, 0, limit)
	for i := len(s.Audits) - 1; i >= 0 && len(records) < limit; i-- {
		record := s.Audits[i]
		if query.GroupName != "" && record.GroupName != query.GroupName {
			continue
		}
		if query.Repository != "" && record.Repository != query.Repository {
			continue
		}
		records = append(records, record)
	}
	return records, nil
}

func (s *MemoryStore) CreateMavenGroup(_ context.Context, group Group) (Group, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.mavenGroups[group.Name]; exists {
		return Group{}, ErrNameExists
	}
	group.CreatedAt = time.Now().UTC()
	normalizeGroup(&group)
	s.mavenGroups[group.Name] = group
	return group, nil
}

func (s *MemoryStore) GetMavenGroup(_ context.Context, name string) (Group, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	group, exists := s.mavenGroups[name]
	if !exists {
		return Group{}, ErrNotFound
	}
	return group, nil
}

func (s *MemoryStore) DisableMavenGroup(_ context.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	group, exists := s.mavenGroups[name]
	if !exists {
		return ErrNotFound
	}
	group.Enabled = false
	s.mavenGroups[name] = group
	return nil
}
