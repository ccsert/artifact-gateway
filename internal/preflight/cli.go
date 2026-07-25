package preflight

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/artifact-gateway/artifact-gateway/internal/config"
)

func RunCLI(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		_, _ = fmt.Fprintln(stdout, "usage: gateway preflight <list|run> [--check name] [--format json]")
		return 0
	}
	switch args[0] {
	case "list":
		for _, name := range CheckNames() {
			_, _ = fmt.Fprintln(stdout, name)
		}
		return 0
	case "run":
		return run(ctx, args[1:], stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "unknown preflight command %q\n", args[0])
		return 2
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("preflight run", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var selected checks
	format := flags.String("format", "json", "output format: json")
	flags.Var(&selected, "check", "run only this named check; repeatable")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *format != "json" {
		_, _ = fmt.Fprintln(stderr, "only --format json is supported")
		return 2
	}
	for _, name := range selected {
		if !isCheckName(name) {
			_, _ = fmt.Fprintf(stderr, "unknown preflight check %q\n", name)
			return 2
		}
	}
	cfg, err := config.Load()
	if err != nil {
		return writeReport(stdout, Report{Results: []Result{{Name: "configuration", Status: StatusFail, Summary: "configuration could not be loaded"}}}, true)
	}
	report := NewRunner().Run(ctx, cfg, selected)
	return writeReport(stdout, report, report.Failed())
}

func writeReport(writer io.Writer, report Report, failed bool) int {
	encoder := json.NewEncoder(writer)
	if err := encoder.Encode(report); err != nil {
		return 1
	}
	if failed {
		return 1
	}
	return 0
}

type checks []string

func (c *checks) String() string { return strings.Join(*c, ",") }
func (c *checks) Set(value string) error {
	*c = append(*c, value)
	return nil
}

func isCheckName(value string) bool {
	for _, name := range CheckNames() {
		if name == value {
			return true
		}
	}
	return false
}
