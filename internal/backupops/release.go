// Package backupops implements bounded, explicit backup data transfers. It does
// not infer production targets from Gateway configuration or manage cloud IAM.
package backupops

import (
	"bufio"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/artifact-gateway/artifact-gateway/internal/backupmanifest"
)

var errRelease = errors.New("approved release and schema do not match backup")
var migrationFilename = regexp.MustCompile(`^[0-9]{6}_[A-Za-z0-9_]+\.sql$`)

// Release is an explicitly approved local artifact, never a URL from a bundle.
// Image identity is observed by the Docker adapter before any container starts.
type Release struct {
	Directory      string                         `json:"directory"`
	Identity       backupmanifest.GatewayIdentity `json:"identity"`
	observedImage  bool
	imageID        string
	ImageReference string `json:"imageReference,omitempty"`
}

// VerifyRelease compares full artifact bytes/build identity and the complete
// applied migration ledger. Forward migration is intentionally a separate step.
func VerifyRelease(m backupmanifest.Manifest, release Release, ledger io.Reader) error {
	if !backupmanifest.ValidSoftwareIdentity(m.SchemaVersion, m.Gateway) || !sameSoftware(m.Gateway, release.Identity) {
		return errRelease
	}
	if (m.SchemaVersion == 1 || (m.Gateway.Artifact != nil && m.Gateway.Artifact.Kind == "oci-image")) && !release.observedImage {
		return errRelease
	}
	if m.SchemaVersion == 2 && m.Gateway.Artifact.Kind == "binary" {
		path := filepath.Join(release.Directory, "gateway")
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			return errRelease
		}
		file, err := os.Open(path)
		if err != nil {
			return errRelease
		}
		sum, _, err := hashReader(file)
		_ = file.Close()
		if err != nil || sum != m.Gateway.Artifact.SHA256 {
			return errRelease
		}
		bi, err := buildinfo.ReadFile(path)
		if err != nil {
			return errRelease
		}
		values := map[string]string{}
		for _, setting := range bi.Settings {
			values[setting.Key] = setting.Value
			if setting.Key == "-ldflags" {
				flags := strings.Fields(setting.Value)
				for i := 0; i < len(flags); i++ {
					assignment := ""
					if flags[i] == "-X" && i+1 < len(flags) {
						i++
						assignment = flags[i]
					} else if strings.HasPrefix(flags[i], "-X=") {
						assignment = strings.TrimPrefix(flags[i], "-X=")
					}
					name, value, ok := strings.Cut(assignment, "=")
					if ok {
						if _, exists := values[name]; exists {
							return errRelease
						}
						values[name] = value
					}
				}
			}
		}
		prefix := "github.com/artifact-gateway/artifact-gateway/internal/buildinfo."
		if bi.Path != "github.com/artifact-gateway/artifact-gateway/cmd/gateway" || values[prefix+"injectedVersion"] != m.Gateway.Version || values[prefix+"injectedRevision"] != m.Gateway.Revision || values["GOOS"]+"/"+values["GOARCH"] != m.Gateway.Artifact.Platform {
			return errRelease
		}
	}
	root, err := os.OpenRoot(filepath.Join(release.Directory, "migrations"))
	if err != nil {
		return errRelease
	}
	defer func() { _ = root.Close() }()
	entries, err := os.ReadDir(filepath.Join(release.Directory, "migrations"))
	if err != nil {
		return errRelease
	}
	expected := map[string]string{}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		if !migrationFilename.MatchString(entry.Name()) || entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return errRelease
		}
		f, err := root.Open(entry.Name())
		if err != nil {
			return errRelease
		}
		sum, _, err := hashReader(f)
		_ = f.Close()
		if err != nil {
			return errRelease
		}
		expected[entry.Name()] = strings.TrimPrefix(sum, "sha256:")
	}
	if len(expected) == 0 {
		return errRelease
	}
	scanner := bufio.NewScanner(io.LimitReader(ledger, 64<<20+1))
	scanner.Buffer(make([]byte, 4096), 64<<10)
	if !scanner.Scan() || scanner.Text() != "filename\tsha256" {
		return errRelease
	}
	seen := map[string]bool{}
	previous := ""
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), "\t")
		if len(fields) != 2 || fields[0] <= previous || seen[fields[0]] || expected[fields[0]] == "" || expected[fields[0]] != fields[1] {
			return errRelease
		}
		seen[fields[0]] = true
		previous = fields[0]
	}
	if scanner.Err() != nil || len(seen) != len(expected) {
		return errRelease
	}
	return nil
}

func sameSoftware(a, b backupmanifest.GatewayIdentity) bool {
	if a.Version != b.Version || a.Revision != b.Revision || a.ImageDigest != b.ImageDigest {
		return false
	}
	if a.Artifact == nil || b.Artifact == nil {
		return a.Artifact == nil && b.Artifact == nil
	}
	return *a.Artifact == *b.Artifact
}

func hashReader(r io.Reader) (string, int64, error) {
	h := sha256.New()
	n, err := io.Copy(h, r)
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), n, err
}

// MigrationLedger produces the stable source ledger from filenames/checksums
// read from the actual PostgreSQL database, not a claimed schema number.
func MigrationLedger(rows map[string]string) ([]byte, error) {
	names := make([]string, 0, len(rows))
	for name := range rows {
		if !migrationFilename.MatchString(name) || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(rows[name]) {
			return nil, errRelease
		}
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("filename\tsha256\n")
	for _, name := range names {
		_, _ = fmt.Fprintf(&b, "%s\t%s\n", name, rows[name])
	}
	if len(names) == 0 {
		return nil, errRelease
	}
	return []byte(b.String()), nil
}
