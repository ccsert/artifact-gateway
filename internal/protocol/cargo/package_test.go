package cargo

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParsePublishEnvelopeAndCrateIdentity(t *testing.T) {
	crate := crateArchive(t, []crateEntry{
		{name: "demo-crate-1.2.3/Cargo.toml", body: "[package]\nname = \"demo-crate\"\nversion = \"1.2.3\"\n"},
		{name: "demo-crate-1.2.3/src/lib.rs", body: "pub fn answer() -> u8 { 42 }\n"},
	})
	metadata := PublishMetadata{Name: "demo-crate", Version: "1.2.3", Dependencies: []PublishDependency{}, Features: map[string][]string{}}
	body := publishBody(t, metadata, crate)
	envelope, err := ParsePublishEnvelope(context.Background(), bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	if envelope.Metadata.Name != "demo-crate" || envelope.Metadata.Version != "1.2.3" || envelope.CrateSize != int64(len(crate)) {
		t.Fatalf("envelope=%#v", envelope)
	}
	parsed, err := ParseCrate(context.Background(), bytes.NewReader(body[envelope.CrateOffset:]), envelope.CrateSize)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Name != "demo-crate" || parsed.Version != "1.2.3" || parsed.VersionKey != "1.2.3" ||
		parsed.CanonicalIdentity != "demo-crate@1.2.3" || parsed.IndexPath != "de/mo/demo-crate" {
		t.Fatalf("crate metadata=%#v", parsed)
	}
	if err = CrossCheckPublishIdentity(envelope.Metadata, parsed); err != nil {
		t.Fatal(err)
	}
}

func TestParseCrateAcceptsOfficialCargoPackage(t *testing.T) {
	cargoPath := requireCargo(t)
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "[package]\nname = \"official-fixture\"\nversion = \"0.1.0+build.7\"\nedition = \"2024\"\nlicense = \"MIT\"\ndescription = \"Cargo parser fixture\"\n"
	if err := os.WriteFile(filepath.Join(project, "Cargo.toml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "src", "lib.rs"), []byte("pub fn fixture() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "target")
	commandContext, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	command := exec.CommandContext(commandContext, cargoPath, "package", "--allow-dirty", "--no-verify", "--manifest-path", filepath.Join(project, "Cargo.toml"), "--target-dir", target)
	command.Env = append(os.Environ(), "CARGO_NET_OFFLINE=true")
	if output, commandErr := command.CombinedOutput(); commandErr != nil {
		t.Fatalf("cargo package: %v\n%s", commandErr, output)
	}
	packagePath := filepath.Join(target, "package", "official-fixture-0.1.0+build.7.crate")
	file, err := os.Open(packagePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseCrate(context.Background(), file, info.Size())
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Name != "official-fixture" || parsed.Version != "0.1.0+build.7" || parsed.VersionKey != "0.1.0" {
		t.Fatalf("official Cargo package metadata=%#v", parsed)
	}
}

func TestParseOfficialCargoPublishRequest(t *testing.T) {
	cargoPath := requireCargo(t)
	var err error
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("loopback listener is unavailable: %v", err)
	}
	var (
		serverURL string
		indexRow  []byte
		captured  PublishEnvelope
		captureMu sync.Mutex
	)
	handler := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/config.json":
			response.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(response, `{"dl":"`+serverURL+`/api/v1/crates","api":"`+serverURL+`","auth-required":true}`)
		case request.Method == http.MethodGet && request.URL.Path == "/of/fi/official-publish-fixture":
			captureMu.Lock()
			row := append([]byte(nil), indexRow...)
			captureMu.Unlock()
			if len(row) == 0 {
				http.NotFound(response, request)
				return
			}
			response.Header().Set("Content-Type", "text/plain")
			_, _ = response.Write(append(row, '\n'))
		case request.Method == http.MethodPut && request.URL.Path == "/api/v1/crates/new":
			body, readErr := io.ReadAll(io.LimitReader(request.Body, maxPublishMetadataBytes+maxCompressedCrateBytes+9))
			if readErr != nil {
				http.Error(response, "read publish body", http.StatusBadRequest)
				return
			}
			envelope, readErr := ParsePublishEnvelope(context.Background(), bytes.NewReader(body), int64(len(body)))
			if readErr != nil {
				http.Error(response, readErr.Error(), http.StatusBadRequest)
				return
			}
			crateBody := body[envelope.CrateOffset:]
			crateMetadata, parseErr := ParseCrate(context.Background(), bytes.NewReader(crateBody), envelope.CrateSize)
			if parseErr != nil || CrossCheckPublishIdentity(envelope.Metadata, crateMetadata) != nil {
				http.Error(response, "invalid crate", http.StatusBadRequest)
				return
			}
			checksum := sha256.Sum256(crateBody)
			entry, translateErr := TranslateIndexEntry(envelope.Metadata, hex.EncodeToString(checksum[:]), time.Now().UTC(), "sparse+https://gateway.example/cargo/fixture/")
			if translateErr != nil {
				http.Error(response, translateErr.Error(), http.StatusBadRequest)
				return
			}
			row, _ := json.Marshal(entry)
			captureMu.Lock()
			captured = envelope
			indexRow = row
			captureMu.Unlock()
			response.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(response, `{"warnings":{"invalid_categories":[],"invalid_badges":[],"other":[]}}`)
		default:
			http.NotFound(response, request)
		}
	})
	server := httptest.NewUnstartedServer(handler)
	server.Listener = listener
	server.Start()
	defer server.Close()
	serverURL = server.URL

	project := t.TempDir()
	if err = os.MkdirAll(filepath.Join(project, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "[package]\nname = \"official-publish-fixture\"\nversion = \"0.2.0\"\nedition = \"2024\"\nlicense = \"MIT\"\ndescription = \"Cargo publish frame fixture\"\n"
	if err = os.WriteFile(filepath.Join(project, "Cargo.toml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(project, "src", "lib.rs"), []byte("pub fn fixture() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cargoHome := t.TempDir()
	configuration := "[registries.fixture]\nindex = \"sparse+" + serverURL + "/\"\ncredential-provider = \"cargo:token\"\n"
	if err = os.WriteFile(filepath.Join(cargoHome, "config.toml"), []byte(configuration), 0o600); err != nil {
		t.Fatal(err)
	}
	commandContext, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	command := exec.CommandContext(commandContext, cargoPath, "publish", "--registry", "fixture", "--allow-dirty", "--no-verify", "--manifest-path", filepath.Join(project, "Cargo.toml"))
	command.Env = append(os.Environ(), "CARGO_HOME="+cargoHome, "CARGO_REGISTRIES_FIXTURE_TOKEN=Bearer fixture-token", "HTTP_PROXY=", "HTTPS_PROXY=", "ALL_PROXY=", "NO_PROXY=127.0.0.1,localhost")
	if output, commandErr := command.CombinedOutput(); commandErr != nil {
		t.Fatalf("cargo publish: %v\n%s", commandErr, output)
	}
	captureMu.Lock()
	capturedSnapshot := captured
	indexSnapshot := append([]byte(nil), indexRow...)
	captureMu.Unlock()
	if capturedSnapshot.Metadata.Name != "official-publish-fixture" || capturedSnapshot.Metadata.Version != "0.2.0" || capturedSnapshot.CrateSize == 0 || len(indexSnapshot) == 0 {
		t.Fatalf("official Cargo publish was not captured: envelope=%#v index=%s", capturedSnapshot, indexSnapshot)
	}
}

func TestParsePublishEnvelopeRejectsMalformedFraming(t *testing.T) {
	validMetadata := PublishMetadata{Name: "demo", Version: "1.0.0", Dependencies: []PublishDependency{}, Features: map[string][]string{}}
	validCrate := crateArchive(t, []crateEntry{{name: "demo-1.0.0/Cargo.toml", body: "[package]\nname='demo'\nversion='1.0.0'\n"}})
	valid := publishBody(t, validMetadata, validCrate)
	trailing := append(append([]byte(nil), valid...), 'x')
	oversizedMetadata := make([]byte, maxPublishMetadataBytes+1)
	for _, testCase := range []struct {
		name string
		body []byte
	}{
		{name: "truncated header", body: []byte{1, 2, 3}},
		{name: "trailing bytes", body: trailing},
		{name: "oversized metadata", body: framedBody(oversizedMetadata, validCrate)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := ParsePublishEnvelope(context.Background(), bytes.NewReader(testCase.body), int64(len(testCase.body))); err == nil {
				t.Fatal("malformed Cargo publish body was accepted")
			}
		})
	}
}

func TestParsePublishEnvelopeIgnoresUnknownAndDefaultsMissingFields(t *testing.T) {
	crate := crateArchive(t, []crateEntry{{name: "demo-1.0.0/Cargo.toml", body: "[package]\nname='demo'\nversion='1.0.0'\n"}})
	metadata := []byte(`{"name":"demo","vers":"1.0.0","unknown_future_field":{"enabled":true}}`)
	body := framedBody(metadata, crate)
	envelope, err := ParsePublishEnvelope(context.Background(), bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	if envelope.Metadata.Dependencies != nil || envelope.Metadata.Features != nil {
		t.Fatalf("missing fields did not retain null defaults: %#v", envelope.Metadata)
	}
}

func TestCargoParsersHonorCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ParsePublishEnvelope(ctx, bytes.NewReader(make([]byte, 8)), 8); !errors.Is(err, context.Canceled) {
		t.Fatalf("publish cancellation err=%v", err)
	}
	if _, err := ParseCrate(ctx, bytes.NewReader([]byte{0x1f, 0x8b}), 2); !errors.Is(err, context.Canceled) {
		t.Fatalf("crate cancellation err=%v", err)
	}
}

func TestParseCrateEnforcesDeclaredResourceLimits(t *testing.T) {
	if _, err := ParseCrate(context.Background(), bytes.NewReader([]byte("small")), maxCompressedCrateBytes+1); err == nil {
		t.Fatal("compressed crate size limit was not enforced")
	}
	longPath := "demo-1.0.0/" + strings.Repeat("x", maxCratePathBytes)
	longPathCrate := crateArchive(t, []crateEntry{{name: longPath, body: "x"}})
	if _, err := ParseCrate(context.Background(), bytes.NewReader(longPathCrate), int64(len(longPathCrate))); err == nil {
		t.Fatal("crate path size limit was not enforced")
	}
	largeManifest := crateArchive(t, []crateEntry{{name: "demo-1.0.0/Cargo.toml", body: strings.Repeat("x", maxCargoManifestBytes+1)}})
	if _, err := ParseCrate(context.Background(), bytes.NewReader(largeManifest), int64(len(largeManifest))); err == nil {
		t.Fatal("Cargo.toml size limit was not enforced")
	}
	entries := make([]crateEntry, 0, maxCrateEntries+1)
	entries = append(entries, crateEntry{name: "demo-1.0.0/Cargo.toml", body: "[package]\nname='demo'\nversion='1.0.0'\n"})
	for index := 1; index <= maxCrateEntries; index++ {
		entries = append(entries, crateEntry{name: fmt.Sprintf("demo-1.0.0/files/%05d", index)})
	}
	tooManyEntries := crateArchive(t, entries)
	if _, err := ParseCrate(context.Background(), bytes.NewReader(tooManyEntries), int64(len(tooManyEntries))); err == nil {
		t.Fatal("crate entry count limit was not enforced")
	}

	t.Run("expanded bytes", func(t *testing.T) {
		body := crateArchive(t, []crateEntry{
			{name: "demo-1.0.0/Cargo.toml", body: "[package]\nname='demo'\nversion='1.0.0'\n"},
			{name: "demo-1.0.0/src/lib.rs", body: strings.Repeat("x", 2_048)},
		})
		limits := defaultCrateLimits
		limits.expandedBytes = 1_024
		if _, err := parseCrate(context.Background(), bytes.NewReader(body), int64(len(body)), limits); err == nil {
			t.Fatal("expanded crate size limit was not enforced")
		}
	})

	t.Run("single file bytes", func(t *testing.T) {
		body := crateArchive(t, []crateEntry{{name: "demo-1.0.0/Cargo.toml", body: "[package]\nname='demo'\nversion='1.0.0'\n"}})
		limits := defaultCrateLimits
		limits.singleFileBytes = 8
		if _, err := parseCrate(context.Background(), bytes.NewReader(body), int64(len(body)), limits); err == nil {
			t.Fatal("single crate file size limit was not enforced")
		}
	})
}

func TestParseCrateRejectsUnsafeArchiveShapesAndIdentityMismatch(t *testing.T) {
	validManifest := "[package]\nname='demo'\nversion='1.0.0'\n"
	for _, testCase := range []struct {
		name    string
		entries []crateEntry
	}{
		{name: "traversal", entries: []crateEntry{{name: "../Cargo.toml", body: validManifest}}},
		{name: "duplicate", entries: []crateEntry{{name: "demo-1.0.0/Cargo.toml", body: validManifest}, {name: "demo-1.0.0/Cargo.toml", body: validManifest}}},
		{name: "symlink", entries: []crateEntry{{name: "demo-1.0.0/Cargo.toml", body: validManifest}, {name: "demo-1.0.0/link", kind: tar.TypeSymlink}}},
		{name: "wrong root", entries: []crateEntry{{name: "other-1.0.0/Cargo.toml", body: validManifest}}},
		{name: "multiple roots", entries: []crateEntry{{name: "demo-1.0.0/Cargo.toml", body: validManifest}, {name: "other-1.0.0/src/lib.rs"}}},
		{name: "multiple manifests", entries: []crateEntry{{name: "demo-1.0.0/Cargo.toml", body: validManifest}, {name: "demo-1.0.0/nested/Cargo.toml", body: validManifest}}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			body := crateArchive(t, testCase.entries)
			if _, err := ParseCrate(context.Background(), bytes.NewReader(body), int64(len(body))); err == nil {
				t.Fatal("unsafe Cargo crate was accepted")
			}
		})
	}

	valid := crateArchive(t, []crateEntry{{name: "demo-1.0.0/Cargo.toml", body: validManifest}})
	if _, err := ParseCrate(context.Background(), bytes.NewReader(append(valid, 'x')), int64(len(valid)+1)); err == nil {
		t.Fatal("Cargo crate with trailing compressed bytes was accepted")
	}

	body := valid
	parsed, err := ParseCrate(context.Background(), bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	if err = CrossCheckPublishIdentity(PublishMetadata{Name: "demo", Version: "1.0.1"}, parsed); err == nil {
		t.Fatal("publish metadata mismatch was accepted")
	}
}

func TestTranslateIndexEntryPreservesCargoSemantics(t *testing.T) {
	metadata := PublishMetadata{
		Name: "demo_crate", Version: "1.2.3+ci.5",
		Dependencies: []PublishDependency{{
			Name: "actual-package", VersionRequirement: "^2.0", Features: []string{"serde"}, Optional: true,
			DefaultFeatures: false, Kind: "build", ExplicitNameInTOML: stringPointer("alias"),
		}},
		Features: map[string][]string{"default": {"alias"}, "extra": {"dep:alias", "alias?/serde"}},
		Links:    stringPointer("native-demo"), RustVersion: stringPointer("1.75"),
	}
	publishedAt := time.Date(2026, time.August, 13, 1, 2, 3, 999, time.FixedZone("test", 8*60*60))
	checksumBytes := sha256.Sum256([]byte("crate"))
	entry, err := TranslateIndexEntry(metadata, hex.EncodeToString(checksumBytes[:]), publishedAt, "sparse+https://gateway.example/cargo/repository/")
	if err != nil {
		t.Fatal(err)
	}
	if entry.Name != "demo_crate" || entry.Version != "1.2.3+ci.5" || entry.Checksum != hex.EncodeToString(checksumBytes[:]) ||
		entry.SchemaVersion != 2 || entry.PublishedAt != "2026-08-12T17:02:03Z" || entry.Yanked {
		t.Fatalf("index entry=%#v", entry)
	}
	if len(entry.Dependencies) != 1 || entry.Dependencies[0].Name != "alias" || entry.Dependencies[0].Package == nil ||
		*entry.Dependencies[0].Package != "actual-package" || entry.Dependencies[0].Registry != nil {
		t.Fatalf("index dependencies=%#v", entry.Dependencies)
	}
	if len(entry.Features["extra"]) != 0 || len(entry.Features2["extra"]) != 2 {
		t.Fatalf("feature translation features=%#v features2=%#v", entry.Features, entry.Features2)
	}
	encoded, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(`"description"`)) || !bytes.Contains(encoded, []byte(`"v":2`)) {
		t.Fatalf("index JSON=%s", encoded)
	}
}

func TestTranslateIndexEntryRejectsInvalidDependencyRequirementAndRegistry(t *testing.T) {
	checksum := strings.Repeat("a", 64)
	base := PublishMetadata{Name: "demo", Version: "1.0.0", Dependencies: []PublishDependency{}, Features: map[string][]string{}}
	for _, dependency := range []PublishDependency{
		{Name: "dep", VersionRequirement: "not a requirement", Features: []string{}, Kind: "normal"},
		{Name: "dep", VersionRequirement: "1 || 2", Features: []string{}, Kind: "normal"},
		{Name: "dep", VersionRequirement: "!=1.2.3", Features: []string{}, Kind: "normal"},
		{Name: "dep", VersionRequirement: "1.2.3 - 2.0.0", Features: []string{}, Kind: "normal"},
		{Name: "dep", VersionRequirement: "~>1.2.3", Features: []string{}, Kind: "normal"},
		{Name: "dep", VersionRequirement: "01.2.3", Features: []string{}, Kind: "normal"},
		{Name: "dep", VersionRequirement: "^*", Features: []string{}, Kind: "normal"},
		{Name: "dep", VersionRequirement: "^1.0", Features: []string{}, Kind: "normal", Registry: stringPointer("https:/missing-host")},
		{Name: "dep", VersionRequirement: "^1.0", Features: []string{}, Kind: "normal", Registry: stringPointer("https://:443/index")},
		{Name: "dep", VersionRequirement: "^1.0", Features: []string{}, Kind: "normal", Registry: stringPointer("https://registry.example/index?token=secret")},
	} {
		metadata := base
		metadata.Dependencies = []PublishDependency{dependency}
		if _, err := TranslateIndexEntry(metadata, checksum, time.Now().UTC(), "sparse+https://gateway.example/cargo/repository/"); err == nil {
			t.Fatalf("invalid dependency was accepted: %#v", dependency)
		}
	}
}

func TestTranslateIndexEntryAcceptsCargoVersionRequirements(t *testing.T) {
	checksum := strings.Repeat("a", 64)
	for _, requirement := range []string{"1.2.3", "^1.2.3", "~1.2", "1.*", "1.*.*", ">=1.*", ">= 1.2, < 2", "= 1.2.3-alpha.1"} {
		metadata := PublishMetadata{
			Name: "demo", Version: "1.0.0", Features: map[string][]string{},
			Dependencies: []PublishDependency{{Name: "dep", VersionRequirement: requirement, Features: []string{}, Kind: "normal"}},
		}
		if _, err := TranslateIndexEntry(metadata, checksum, time.Now().UTC(), "sparse+https://gateway.example/cargo/repository/"); err != nil {
			t.Fatalf("Cargo version requirement %q was rejected: %v", requirement, err)
		}
	}
}

func TestCargoVersionRequirementGrammarMatchesOfficialClient(t *testing.T) {
	cargoPath := requireCargo(t)
	for _, testCase := range []struct {
		name        string
		requirement string
		accepted    bool
	}{
		{name: "comparators", requirement: ">= 1.2, < 2", accepted: true},
		{name: "prerelease", requirement: "= 1.2.3-alpha.1", accepted: true},
		{name: "logical or", requirement: "1 || 2", accepted: false},
		{name: "not equal", requirement: "!=1.2.3", accepted: false},
		{name: "hyphen range", requirement: "1.2.3 - 2.0.0", accepted: false},
		{name: "pessimistic operator", requirement: "~>1.2.3", accepted: false},
		{name: "leading zero", requirement: "01.2.3", accepted: false},
		{name: "comparator wildcard", requirement: ">=1.*", accepted: true},
		{name: "caret wildcard", requirement: "^*", accepted: false},
		{name: "repeated wildcard", requirement: "1.*.*", accepted: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			project := t.TempDir()
			if err := os.MkdirAll(filepath.Join(project, "src"), 0o755); err != nil {
				t.Fatal(err)
			}
			manifest := fmt.Sprintf("[package]\nname='requirement-fixture'\nversion='0.1.0'\nedition='2024'\n[dependencies]\ndep={version=%q}\n", testCase.requirement)
			if err := os.WriteFile(filepath.Join(project, "Cargo.toml"), []byte(manifest), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(project, "src", "lib.rs"), []byte("pub fn fixture() {}\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			commandContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			command := exec.CommandContext(commandContext, cargoPath, "metadata", "--no-deps", "--offline", "--format-version", "1", "--manifest-path", filepath.Join(project, "Cargo.toml"))
			command.Env = append(os.Environ(), "CARGO_HOME="+t.TempDir())
			err := command.Run()
			if (err == nil) != testCase.accepted {
				t.Fatalf("cargo acceptance=%t err=%v", err == nil, err)
			}
			if validCargoVersionRequirement(testCase.requirement) != testCase.accepted {
				t.Fatalf("gateway acceptance differs from Cargo for %q", testCase.requirement)
			}
		})
	}
}

func TestCargoIdentityAndSparseIndexPath(t *testing.T) {
	first, err := NormalizeIdentity("Demo_Crate", "1.2.3+build.1")
	if err != nil {
		t.Fatal(err)
	}
	second, err := NormalizeIdentity("demo-crate", "1.2.3+build.2")
	if err != nil {
		t.Fatal(err)
	}
	if first.CollisionKey != second.CollisionKey || first.CanonicalIdentity != second.CanonicalIdentity {
		t.Fatalf("confusable/build metadata identity mismatch first=%#v second=%#v", first, second)
	}
	repeated, err := NormalizeIdentity("a--b", "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	single, err := NormalizeIdentity("a_b", "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if repeated.CollisionKey == single.CollisionKey {
		t.Fatalf("separator count was collapsed: repeated=%#v single=%#v", repeated, single)
	}
	for name, expected := range map[string]string{"a": "1/a", "ab": "2/ab", "abc": "3/a/abc", "serde": "se/rd/serde", "Demo_Crate": "de/mo/demo_crate"} {
		actual, pathErr := SparseIndexPath(name)
		if pathErr != nil || actual != expected {
			t.Fatalf("SparseIndexPath(%q)=%q err=%v", name, actual, pathErr)
		}
	}
	for _, invalid := range []string{"", "1crate", "-crate", "crate.name", "crate/name"} {
		if _, err = NormalizeIdentity(invalid, "1.0.0"); err == nil {
			t.Fatalf("invalid Cargo name %q was accepted", invalid)
		}
	}
}

func FuzzParsePublishEnvelope(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{0, 0, 0, 0, 0, 0, 0, 0})
	crate := validCrateSeed()
	f.Add(framedBody([]byte(`{"name":"seed","vers":"1.0.0","deps":[],"features":{}}`), crate))
	f.Fuzz(func(t *testing.T, body []byte) {
		_, _ = ParsePublishEnvelope(context.Background(), bytes.NewReader(body), int64(len(body)))
	})
}

func FuzzParseCrate(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{0x1f, 0x8b})
	f.Add(validCrateSeed())
	f.Fuzz(func(t *testing.T, body []byte) {
		_, _ = ParseCrate(context.Background(), bytes.NewReader(body), int64(len(body)))
	})
}

func requireCargo(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("cargo")
	if err == nil {
		expectedVersion := os.Getenv("CARGO_EXPECTED_VERSION")
		if expectedVersion == "" {
			return path
		}
		versionContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		output, versionErr := exec.CommandContext(versionContext, path, "--version").Output()
		if versionErr != nil || !strings.HasPrefix(string(output), "cargo "+expectedVersion+" ") {
			t.Fatalf("cargo version=%q, expected %s", strings.TrimSpace(string(output)), expectedVersion)
		}
		return path
	}
	if os.Getenv("CARGO_REQUIRED") == "1" {
		t.Fatal("the required pinned Cargo client is unavailable")
	}
	t.Skip("cargo client is unavailable outside the required contract gate")
	return ""
}

func validCrateSeed() []byte {
	var compressed bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressed)
	tarWriter := tar.NewWriter(gzipWriter)
	manifest := []byte("[package]\nname='seed'\nversion='1.0.0'\n")
	if err := tarWriter.WriteHeader(&tar.Header{Name: "seed-1.0.0/Cargo.toml", Mode: 0o644, Size: int64(len(manifest)), Typeflag: tar.TypeReg}); err != nil {
		panic(err)
	}
	if _, err := tarWriter.Write(manifest); err != nil {
		panic(err)
	}
	if err := tarWriter.Close(); err != nil {
		panic(err)
	}
	if err := gzipWriter.Close(); err != nil {
		panic(err)
	}
	return compressed.Bytes()
}

type crateEntry struct {
	name string
	body string
	kind byte
}

func crateArchive(t *testing.T, entries []crateEntry) []byte {
	t.Helper()
	var compressed bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressed)
	tarWriter := tar.NewWriter(gzipWriter)
	for _, entry := range entries {
		kind := entry.kind
		if kind == 0 {
			kind = tar.TypeReg
		}
		header := &tar.Header{Name: entry.name, Mode: 0o644, Size: int64(len(entry.body)), Typeflag: kind}
		if kind == tar.TypeSymlink {
			header.Linkname = "Cargo.toml"
			header.Size = 0
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if header.Size > 0 {
			if _, err := tarWriter.Write([]byte(entry.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return compressed.Bytes()
}

func publishBody(t *testing.T, metadata PublishMetadata, crate []byte) []byte {
	t.Helper()
	body, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	return framedBody(body, crate)
}

func framedBody(metadata, crate []byte) []byte {
	var body bytes.Buffer
	_ = binary.Write(&body, binary.LittleEndian, uint32(len(metadata)))
	body.Write(metadata)
	_ = binary.Write(&body, binary.LittleEndian, uint32(len(crate)))
	body.Write(crate)
	return body.Bytes()
}

func stringPointer(value string) *string { return &value }
