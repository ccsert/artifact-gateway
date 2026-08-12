package refscanner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultMaximumEngineOutput = int64(64 << 20)
	maximumEngineOutput        = int64(512 << 20)
	maximumCommandLogBytes     = 64 << 10
)

type CommandExecutor interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type TrivyOptions struct {
	Binary         string
	Executor       CommandExecutor
	TempDir        string
	MaxOutputBytes int64
}

type trivyEngine struct {
	binary         string
	executor       CommandExecutor
	tempDir        string
	maxOutputBytes int64
}

func NewTrivyEngine(options TrivyOptions) (Engine, error) {
	binary := strings.TrimSpace(options.Binary)
	if binary == "" {
		binary = "trivy"
	}
	maximumBytes := options.MaxOutputBytes
	if maximumBytes == 0 {
		maximumBytes = defaultMaximumEngineOutput
	}
	if strings.ContainsAny(binary, "\x00\r\n") || maximumBytes < 1024 || maximumBytes > maximumEngineOutput {
		return nil, ErrInvalidOptions
	}
	executor := options.Executor
	if executor == nil {
		executor = execCommandExecutor{maxOutputBytes: maximumBytes}
	}
	return &trivyEngine{binary: binary, executor: executor, tempDir: options.TempDir, maxOutputBytes: maximumBytes}, nil
}

func (e *trivyEngine) Scan(ctx context.Context, root string) (EngineOutput, error) {
	outputDir, err := os.MkdirTemp(e.tempDir, "artifact-gateway-trivy-")
	if err != nil {
		return EngineOutput{}, ErrEngineFailed
	}
	defer func() { _ = os.RemoveAll(outputDir) }()
	reportPath := filepath.Join(outputDir, "report.json")
	report, err := e.executor.Run(ctx, e.binary,
		"filesystem", "--quiet", "--no-progress", "--scanners", "vuln,license",
		"--format", "json", "--", root,
	)
	if err != nil || int64(len(report)) > e.maxOutputBytes {
		return EngineOutput{}, ErrEngineFailed
	}
	if err = os.WriteFile(reportPath, report, 0o600); err != nil {
		return EngineOutput{}, ErrEngineFailed
	}
	sbom, err := e.executor.Run(ctx, e.binary,
		"convert", "--quiet", "--format", "cyclonedx", "--", reportPath,
	)
	if err != nil || int64(len(sbom)) > e.maxOutputBytes {
		return EngineOutput{}, ErrEngineFailed
	}
	return EngineOutput{Report: report, SBOM: sbom}, nil
}

func (e *trivyEngine) Health(ctx context.Context) (EngineHealth, error) {
	output, err := e.executor.Run(ctx, e.binary, "version", "--format", "json")
	if err != nil || int64(len(output)) > e.maxOutputBytes {
		return EngineHealth{}, ErrEngineFailed
	}
	var value struct {
		Version         string `json:"Version"`
		VulnerabilityDB *struct {
			Version   json.RawMessage `json:"Version"`
			UpdatedAt jsonTime        `json:"UpdatedAt"`
		} `json:"VulnerabilityDB"`
	}
	if json.Unmarshal(output, &value) != nil || strings.TrimSpace(value.Version) == "" {
		return EngineHealth{}, ErrEngineFailed
	}
	health := EngineHealth{Version: boundedLine(value.Version, 128)}
	if value.VulnerabilityDB != nil {
		health.DatabaseVersion = rawVersionString(value.VulnerabilityDB.Version)
		health.DatabaseUpdatedAt = value.VulnerabilityDB.UpdatedAt.Time
	}
	return health, nil
}

type jsonTime struct {
	Time time.Time
}

func (value *jsonTime) UnmarshalJSON(data []byte) error {
	if string(data) == "null" || string(data) == `""` {
		return nil
	}
	return json.Unmarshal(data, &value.Time)
}

type execCommandExecutor struct {
	maxOutputBytes int64
}

func (e execCommandExecutor) Run(ctx context.Context, binary string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, binary, args...)
	output := newLimitedBuffer(e.maxOutputBytes)
	var errorOutput boundedBuffer
	command.Stdout = output
	command.Stderr = &errorOutput
	if err := command.Run(); err != nil {
		return output.Bytes(), errors.Join(ErrEngineFailed, output.Err(), err)
	}
	if output.Err() != nil {
		return nil, errors.Join(ErrEngineFailed, output.Err())
	}
	return output.Bytes(), nil
}

type limitedBuffer struct {
	buffer bytes.Buffer
	limit  int64
	err    error
}

func newLimitedBuffer(limit int64) *limitedBuffer {
	return &limitedBuffer{limit: limit}
}

func (value *limitedBuffer) Write(data []byte) (int, error) {
	if value.err != nil {
		return 0, value.err
	}
	if int64(value.buffer.Len()) > value.limit-int64(len(data)) {
		value.err = errors.New("trivy output exceeds the configured limit")
		return 0, value.err
	}
	return value.buffer.Write(data)
}

func (value *limitedBuffer) Bytes() []byte {
	return value.buffer.Bytes()
}

func (value *limitedBuffer) Err() error {
	return value.err
}

type boundedBuffer struct {
	buffer bytes.Buffer
}

func (value *boundedBuffer) Write(data []byte) (int, error) {
	remaining := maximumCommandLogBytes - value.buffer.Len()
	if remaining > 0 {
		_, _ = value.buffer.Write(data[:min(len(data), remaining)])
	}
	return len(data), nil
}

func (value *boundedBuffer) Bytes() []byte {
	return value.buffer.Bytes()
}
