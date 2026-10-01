package preflight

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/artifact-gateway/artifact-gateway/internal/capacityplan"
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
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if !utf8.Valid(data) || !uniqueJSONKeys(data) || decoder.Decode(&plan) != nil || plan == nil || decoder.Decode(new(any)) != io.EOF {
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

// JSON permits duplicate keys in some decoders. They are ambiguous evidence and
// are rejected here, including duplicates in nested capacity or object records.
func uniqueJSONKeys(data []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var consume func(int) bool
	consume = func(depth int) bool {
		if depth > 32 {
			return false
		}
		token, err := decoder.Token()
		if err != nil {
			return false
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return true
		}
		keys := make(map[string]bool)
		for decoder.More() {
			if delim == '{' {
				key, keyErr := decoder.Token()
				name, valid := key.(string)
				// All schema field names are ASCII. The struct decoder matches
				// ASCII names case-insensitively; reject its alias collisions too.
				if keyErr != nil || !valid || !asciiField(name) || keys[strings.ToLower(name)] {
					return false
				}
				keys[strings.ToLower(name)] = true
			}
			if !consume(depth + 1) {
				return false
			}
		}
		end, endErr := decoder.Token()
		return endErr == nil && ((delim == '{' && end == json.Delim('}')) || (delim == '[' && end == json.Delim(']')))
	}
	if !consume(0) {
		return false
	}
	_, err := decoder.Token()
	return err == io.EOF
}

func asciiField(name string) bool {
	for i := range len(name) {
		if name[i] >= 128 {
			return false
		}
	}
	return true
}
