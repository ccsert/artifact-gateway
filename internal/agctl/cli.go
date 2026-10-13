package agctl

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/buildinfo"
)

const defaultTimeout = 30 * time.Second

type globalFlags struct {
	server   string
	token    string
	profile  string
	output   string
	jsonFlag bool
	timeout  time.Duration
	verbose  bool
	insecure bool
	dryRun   bool
	yes      bool
}

func registerGlobalFlags(fs *flag.FlagSet, flags *globalFlags) {
	fs.StringVar(&flags.server, "server", flags.server, "Gateway root URL (for example https://gateway.example.com)")
	fs.StringVar(&flags.token, "token", flags.token, "bearer token for the management API")
	fs.StringVar(&flags.profile, "profile", flags.profile, "profile name from ~/.config/agctl/config.yaml")
	fs.BoolVar(&flags.jsonFlag, "json", flags.jsonFlag, "emit the versioned JSON envelope instead of human text")
	fs.StringVar(&flags.output, "o", flags.output, "output format: table or json")
	fs.DurationVar(&flags.timeout, "timeout", flags.timeout, "overall request timeout including retries")
	fs.BoolVar(&flags.verbose, "verbose", flags.verbose, "print request method and URL to stderr (credentials are never printed)")
	fs.BoolVar(&flags.insecure, "insecure", flags.insecure, "skip TLS certificate verification")
	fs.BoolVar(&flags.dryRun, "dry-run", flags.dryRun, "plan a write without submitting it (write commands)")
	fs.BoolVar(&flags.yes, "yes", flags.yes, "assume yes for confirmations (write commands)")
}

func (flags *globalFlags) format() (outputFormat, error) {
	if flags.jsonFlag {
		return outputJSON, nil
	}
	value := flags.output
	if value == "" {
		value = string(outputTable)
	}
	switch outputFormat(value) {
	case outputTable, outputJSON:
		return outputFormat(value), nil
	}
	return "", &UsageError{
		Message: fmt.Sprintf("invalid output format %q", flags.output),
		Hint:    "use -o table or -o json",
	}
}

type commandContext struct {
	globals  *globalFlags
	settings profileSettings
	stdout   io.Writer
	stderr   io.Writer
	format   outputFormat
	version  buildinfo.Info
}

func (c *commandContext) client() (*apiClient, error) {
	var verbose io.Writer
	if c.globals.verbose {
		verbose = c.stderr
	}
	return newAPIClient(c.settings, clientOptions{
		timeout:   c.globals.timeout,
		verbose:   verbose,
		userAgent: fmt.Sprintf("agctl/%s", c.version.Version),
	})
}

func (c *commandContext) render(data any, meta *envelopeMeta, human func(io.Writer) error) error {
	return renderSuccess(c.stdout, c.format, data, meta, human)
}

// currentFormat resolves the output format for failures raised before the
// command flags are parsed. An invalid -o falls back to table until the
// validation error itself is reported.
func currentFormat(flags *globalFlags) outputFormat {
	if flags.jsonFlag || outputFormat(flags.output) == outputJSON {
		return outputJSON
	}
	return outputTable
}

// RunCLI executes one agctl invocation and returns its documented exit code.
// Commands never prompt: missing required input fails with a usage error
// before any request is sent.
func RunCLI(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := &globalFlags{timeout: defaultTimeout, output: string(outputTable)}
	globalSet := flag.NewFlagSet("agctl", flag.ContinueOnError)
	globalSet.SetOutput(io.Discard)
	registerGlobalFlags(globalSet, flags)
	if err := globalSet.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printUsage(stdout)
			return ExitOK
		}
		renderFailure(stdout, stderr, currentFormat(flags), &UsageError{Message: err.Error(), Hint: "run agctl --help for usage"})
		return ExitUsage
	}
	set := collectFlags(globalSet)
	rest := globalSet.Args()
	if len(rest) == 0 {
		if currentFormat(flags) == outputJSON {
			renderFailure(stdout, stderr, outputJSON, &UsageError{Message: "no command given", Hint: "run agctl --help for the command list"})
			return ExitUsage
		}
		printUsage(stderr)
		return ExitUsage
	}
	if rest[0] == "help" {
		printUsage(stdout)
		return ExitOK
	}
	cmd, ok := lookupCommand(rest[0])
	if !ok {
		renderFailure(stdout, stderr, currentFormat(flags), &UsageError{
			Message: fmt.Sprintf("unknown command %q", rest[0]),
			Hint:    "run agctl --help for the command list",
		})
		return ExitUsage
	}

	// Command-level parse: global flags are registered again with their
	// current values as defaults so flags after the command name work too.
	localSet := flag.NewFlagSet("agctl "+cmd.name, flag.ContinueOnError)
	localSet.SetOutput(io.Discard)
	registerGlobalFlags(localSet, flags)
	if cmd.setFlags != nil {
		cmd.setFlags(localSet)
	}
	if err := localSet.Parse(rest[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printCommandUsage(stdout, cmd)
			return ExitOK
		}
		renderFailure(stdout, stderr, currentFormat(flags), &UsageError{
			Message: err.Error(),
			Hint:    fmt.Sprintf("run agctl %s --help for usage", cmd.name),
		})
		return ExitUsage
	}
	mergeFlags(set, localSet)

	resolvedFormat, err := flags.format()
	if err != nil {
		renderFailure(stdout, stderr, currentFormat(flags), err)
		return exitCodeFor(err)
	}
	format := resolvedFormat
	fail := func(err error) int {
		renderFailure(stdout, stderr, format, err)
		return exitCodeFor(err)
	}

	if flags.timeout <= 0 {
		return fail(&UsageError{
			Message: fmt.Sprintf("timeout %s must be positive", flags.timeout),
			Hint:    "pass --timeout with a duration such as 30s",
		})
	}
	if extra := localSet.Args(); len(extra) > 0 {
		return fail(&UsageError{
			Message: fmt.Sprintf("unexpected argument %q", extra[0]),
			Hint:    fmt.Sprintf("run agctl %s --help for usage", cmd.name),
		})
	}
	settings, err := resolveSettings(flags, set, os.Getenv, defaultConfigPath())
	if err != nil {
		return fail(err)
	}
	env := &commandContext{
		globals:  flags,
		settings: settings,
		stdout:   stdout,
		stderr:   stderr,
		format:   format,
		version:  buildinfo.Read(),
	}
	if err := cmd.run(ctx, env); err != nil {
		return fail(err)
	}
	return ExitOK
}

func collectFlags(fs *flag.FlagSet) map[string]bool {
	set := make(map[string]bool)
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	return set
}

func mergeFlags(set map[string]bool, fs *flag.FlagSet) {
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
}

func printUsage(w io.Writer) {
	_, _ = fmt.Fprintln(w, "agctl — Gateway command line for CI, platform management, and agents")
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "usage: agctl [global flags] <command> [command flags]")
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "commands:")
	for _, cmd := range registry() {
		_, _ = fmt.Fprintf(w, "  %-10s %s\n", cmd.name, cmd.synopsis)
	}
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "run `agctl <command> --help` for command flags; `agctl commands --json` prints the machine-readable tree")
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "global flags:")
	printGlobalFlags(w)
}

func printCommandUsage(w io.Writer, cmd command) {
	_, _ = fmt.Fprintf(w, "usage: %s\n\n%s\n\nflags:\n", cmd.usage, cmd.synopsis)
	fs := flag.NewFlagSet(cmd.name, flag.ContinueOnError)
	fs.SetOutput(w)
	registerGlobalFlags(fs, &globalFlags{timeout: defaultTimeout, output: string(outputTable)})
	if cmd.setFlags != nil {
		cmd.setFlags(fs)
	}
	fs.PrintDefaults()
}

func printGlobalFlags(w io.Writer) {
	fs := flag.NewFlagSet("agctl", flag.ContinueOnError)
	fs.SetOutput(w)
	registerGlobalFlags(fs, &globalFlags{timeout: defaultTimeout, output: string(outputTable)})
	fs.PrintDefaults()
}
