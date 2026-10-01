package preflight

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/capacityplan"
	"github.com/artifact-gateway/artifact-gateway/internal/opsjson"
)

const maxCapacityInputBytes = 8 << 20

func runCapacity(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("preflight capacity", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	input := flags.String("input", "", "normalized JSON plan file")
	format := flags.String("format", "json", "output format: json")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, _ = fmt.Fprintln(stdout, "usage: gateway preflight capacity --input plan.json [--format json]")
			return 0
		}
		_, _ = fmt.Fprintln(stderr, "invalid capacity preflight arguments")
		return 2
	}
	if *input == "" || *format != "json" || flags.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "capacity preflight requires --input and JSON output, without positional arguments")
		return 2
	}
	if ctx.Err() != nil {
		return writeCapacityReport(stdout, stderr, capacityplan.Report{SchemaVersion: 1, CheckedAt: time.Now().UTC(), Status: capacityplan.Unknown, Reasons: []string{"cancelled"}, Repositories: []capacityplan.RepositoryResult{}, Storage: capacityplan.StorageResult{Status: capacityplan.Unknown, Reasons: []string{"cancelled"}}})
	}
	info, err := os.Stat(*input)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxCapacityInputBytes {
		_, _ = fmt.Fprintln(stderr, "capacity input is unavailable or exceeds the regular-file limit")
		return 2
	}
	file, err := os.Open(*input)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "capacity input is unavailable")
		return 2
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, maxCapacityInputBytes+1))
	if err != nil || len(data) > maxCapacityInputBytes {
		_, _ = fmt.Fprintln(stderr, "capacity input could not be read within its limit")
		return 2
	}
	var plan *capacityplan.Plan
	if opsjson.Decode(data, &plan) != nil || plan == nil {
		// Never include parser errors, paths, or excerpts of operator evidence.
		_, _ = fmt.Fprintln(stderr, "capacity input must be one valid, unambiguous versioned JSON plan")
		return 2
	}
	now := time.Now().UTC()
	report := capacityplan.Evaluate(*plan, now)
	if ctx.Err() != nil {
		report.Status, report.Reasons = capacityplan.Unknown, []string{"cancelled"}
	}
	sum := sha256.Sum256(data)
	report.InputSHA256 = "sha256:" + hex.EncodeToString(sum[:])
	return writeCapacityReport(stdout, stderr, report)
}

func writeCapacityReport(stdout, stderr io.Writer, report capacityplan.Report) int {
	if err := json.NewEncoder(stdout).Encode(report); err != nil {
		_, _ = fmt.Fprintln(stderr, "capacity report could not be written")
		return 2
	}
	switch report.Status {
	case capacityplan.Sufficient:
		return 0
	case capacityplan.Insufficient:
		return 1
	default:
		return 3
	}
}
