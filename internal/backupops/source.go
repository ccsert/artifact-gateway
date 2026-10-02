package backupops

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/artifact-gateway/artifact-gateway/internal/backupmanifest"
)

type WriterSpec struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Reference string `json:"reference"`
}
type SourceSpec struct {
	BackupID                  string       `json:"backupId"`
	ScopeID                   string       `json:"scopeId"`
	InventoryDeclaredComplete bool         `json:"inventoryDeclaredComplete"`
	Writers                   []WriterSpec `json:"writers"`
	Release                   Release      `json:"release"`
	Postgres                  Postgres     `json:"postgres"`
	S3                        S3Settings   `json:"s3"`
	Docker                    Docker       `json:"docker"`
}
type observedSource struct {
	spec    SourceSpec
	objects *s3Objects
}

func newSource(ctx context.Context, spec SourceSpec) (*observedSource, error) {
	validID := regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:+-]{0,127}$`)
	if !spec.InventoryDeclaredComplete || len(spec.Writers) == 0 || len(spec.Writers) > 100 || !validID.MatchString(spec.BackupID) || !validID.MatchString(spec.ScopeID) {
		return nil, errors.New("source writer scope unconfirmed")
	}
	seen := map[string]bool{}
	if _, err := spec.Postgres.config(); err != nil {
		return nil, errors.New("explicit source database settings required")
	}
	dockerRequired := spec.Postgres.ToolsContainer != "" || spec.Release.Identity.Artifact != nil && spec.Release.Identity.Artifact.Kind == "oci-image"
	for _, w := range spec.Writers {
		if !validID.MatchString(w.ID) || seen[w.ID] {
			return nil, errors.New("source writer scope invalid")
		}
		seen[w.ID] = true
		switch w.Kind {
		case "docker-container":
			if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(w.Reference) {
				return nil, errors.New("explicit writer container ID required")
			}
			dockerRequired = true
		case "systemd-unit":
			if !regexp.MustCompile(`^[A-Za-z0-9_.@-]{1,120}\.service$`).MatchString(w.Reference) {
				return nil, errors.New("explicit writer unit required")
			}
		default:
			return nil, errors.New("unsupported writer controller")
		}
	}
	if dockerRequired {
		if spec.Docker.Validate(ctx) != nil {
			return nil, errors.New("local source Docker context unavailable")
		}
		spec.Postgres.DockerContext = spec.Docker.Context
		release, err := spec.Docker.ObserveRelease(ctx, spec.Release)
		if err != nil {
			return nil, err
		}
		spec.Release = release
	}
	if spec.Postgres.ToolsContainer != "" {
		if validatePGContainer(ctx, spec.Docker, spec.Postgres) != nil {
			return nil, errors.New("source PG tools do not identify the configured database")
		}
	}
	objects, err := newS3(spec.S3)
	if err != nil {
		return nil, err
	}
	return &observedSource{spec: spec, objects: objects}, nil
}
func (s *observedSource) ObserveFrozen(ctx context.Context) (string, error) {
	type state struct {
		ID       string
		Evidence string
	}
	states := make([]state, 0, len(s.spec.Writers))
	for _, writer := range s.spec.Writers {
		switch writer.Kind {
		case "docker-container":
			data, err := s.spec.Docker.output(ctx, "inspect", writer.Reference)
			if err != nil {
				return "", errors.New("writer observation unavailable")
			}
			var entries []struct {
				ID     string `json:"Id"`
				Image  string
				Config struct{ Entrypoint []string }
				Mounts []struct {
					Type, Source, Destination string
					RW                        bool
				}
				State struct {
					Running, Restarting, Paused bool
					StartedAt, FinishedAt       string
				}
			}
			if json.Unmarshal(data, &entries) != nil || len(entries) != 1 || entries[0].ID != writer.Reference || entries[0].State.Running || entries[0].State.Restarting || entries[0].State.Paused || entries[0].State.FinishedAt == "" {
				return "", errors.New("writer not stopped")
			}
			entry := entries[0]
			if g := s.spec.Release.Identity; g.Artifact != nil && g.Artifact.Kind == "binary" {
				found := false
				for _, mount := range entry.Mounts {
					if mount.Destination == "/gateway" && mount.Type == "bind" && !mount.RW {
						sum, _, err := describeBinary(mount.Source)
						found = err == nil && sum == g.Artifact.SHA256
					}
				}
				if !found || len(entry.Config.Entrypoint) != 1 || entry.Config.Entrypoint[0] != "/gateway" {
					return "", errRelease
				}
			} else if !s.spec.Release.observedImage || entry.Image != s.spec.Release.imageID {
				return "", errRelease
			}
			evidence, _ := json.Marshal(entries[0].State)
			states = append(states, state{writer.ID, string(evidence)})
		case "systemd-unit":
			command := exec.CommandContext(ctx, "systemctl", "show", writer.Reference, "--property=ActiveState", "--property=ExecMainStartTimestampMonotonic", "--property=InactiveEnterTimestampMonotonic", "--property=ExecStart")
			command.Stderr = io.Discard
			data, err := command.Output()
			if err != nil || len(data) > 64<<10 {
				return "", errors.New("writer observation unavailable")
			}
			values := map[string]string{}
			for _, line := range strings.Split(string(data), "\n") {
				k, v, ok := strings.Cut(line, "=")
				if ok {
					values[k] = v
				}
			}
			if values["ActiveState"] != "inactive" || values["InactiveEnterTimestampMonotonic"] == "" || values["InactiveEnterTimestampMonotonic"] == "0" {
				return "", errors.New("writer not stopped")
			}
			expected, err := filepath.EvalSymlinks(filepath.Join(s.spec.Release.Directory, "gateway"))
			if err != nil {
				return "", errRelease
			}
			match := regexp.MustCompile(`path=([^ ;]+)`).FindStringSubmatch(values["ExecStart"])
			if len(match) != 2 {
				return "", errRelease
			}
			executable, err := filepath.EvalSymlinks(match[1])
			if err != nil || executable != expected {
				return "", errRelease
			}
			states = append(states, state{writer.ID, values["ExecMainStartTimestampMonotonic"] + ":" + values["InactiveEnterTimestampMonotonic"]})
		}
	}
	sort.Slice(states, func(i, j int) bool { return states[i].ID < states[j].ID })
	data, _ := json.Marshal(states)
	return string(data), nil
}
func (s *observedSource) Dump(ctx context.Context, w io.Writer) error {
	return s.spec.Postgres.Dump(ctx, w)
}
func (s *observedSource) Ledger(ctx context.Context) ([]byte, error) {
	return s.spec.Postgres.Ledger(ctx)
}
func (s *observedSource) Metadata(ctx context.Context) ([]byte, error) {
	return s.spec.Postgres.Metadata(ctx)
}
func (s *observedSource) WalkObjects(ctx context.Context, visit func(string, int64) error) error {
	return s.objects.WalkObjects(ctx, visit)
}
func (s *observedSource) WalkReferences(ctx context.Context, visit func(string) error) error {
	return s.spec.Postgres.WalkReferences(ctx, visit)
}
func (s *observedSource) Open(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	return s.objects.Open(ctx, key)
}
func (s *observedSource) writers() backupmanifest.WriterEvidence {
	w := backupmanifest.WriterEvidence{ScopeID: s.spec.ScopeID, InventoryDeclaredComplete: s.spec.InventoryDeclaredComplete}
	for _, writer := range s.spec.Writers {
		w.Writers = append(w.Writers, backupmanifest.Writer{ID: writer.ID})
	}
	return w
}

func validatePGContainer(ctx context.Context, d Docker, p Postgres) error {
	cfg, err := p.config()
	if err != nil || (cfg.Host != "127.0.0.1" && cfg.Host != "localhost") {
		return errors.New("PG container must use its own loopback binding")
	}
	data, err := d.output(ctx, "inspect", p.ToolsContainer)
	if err != nil {
		return err
	}
	var entries []struct {
		State           struct{ Running bool }
		NetworkSettings struct {
			Ports map[string][]struct{ HostIP, HostPort string }
		}
	}
	if json.Unmarshal(data, &entries) != nil || len(entries) != 1 || !entries[0].State.Running {
		return errors.New("PG container unavailable")
	}
	bindings := entries[0].NetworkSettings.Ports["5432/tcp"]
	if len(bindings) != 1 || bindings[0].HostIP != "127.0.0.1" || bindings[0].HostPort != stringPort(cfg.Port) {
		return errors.New("PG tools source identity differs")
	}
	return nil
}
func stringPort(port uint16) string { return strconv.Itoa(int(port)) }

var _ FrozenSource = (*observedSource)(nil)
