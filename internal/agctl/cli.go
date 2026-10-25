package agctl

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ccsert/artifact-gateway/internal/buildinfo"
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
	globals    *globalFlags
	settings   profileSettings
	stdin      io.Reader
	stdout     io.Writer
	stderr     io.Writer
	args       []string
	format     outputFormat
	version    buildinfo.Info
	configPath string
	flagSet    map[string]bool
	// addKnownSecret registers a credential that later failure rendering must
	// redact even if it only appears inside server-controlled text.
	addKnownSecret func(string)
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

// scanFailureFlags finds output preferences and credential values even when
// ordinary parsing stops at an unknown command or invalid flag. The format
// scan follows the command path and honors each command's declared positional
// arguments; a stray positional beyond them keeps the conservative contract
// that later flags no longer select output. Credential collection is
// independent of the format scan: every --token occurrence is collected across
// the whole argument list (respecting --), so a token that appears after a
// failing argument is still redacted everywhere.
func scanFailureFlags(args []string) (outputFormat, []string) {
	return scanFailureFormat(args), scanFailureTokens(args)
}

func cutFlag(argument string) (name, value string, hasValue bool) {
	name = strings.TrimPrefix(argument, "-")
	name = strings.TrimPrefix(name, "-")
	return strings.Cut(name, "=")
}

func isBoolFlag(definition *flag.Flag) bool {
	if boolean, ok := definition.Value.(interface{ IsBoolFlag() bool }); ok {
		return boolean.IsBoolFlag()
	}
	return false
}

func scanFailureFormat(args []string) outputFormat {
	flags := &globalFlags{timeout: defaultTimeout, output: string(outputTable)}
	fs := flag.NewFlagSet("agctl", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	registerGlobalFlags(fs, flags)
	var path []string
	allowedPositionals := -1
	seenPositionals := 0
	for index := 0; index < len(args); index++ {
		argument := args[index]
		if argument == "--" {
			break
		}
		if len(argument) < 2 || argument[0] != '-' {
			if allowedPositionals >= 0 {
				if seenPositionals >= allowedPositionals {
					break
				}
				seenPositionals++
				continue
			}
			path = append(path, argument)
			if cmd, ok := lookupCommand(strings.Join(path, " ")); ok {
				allowedPositionals = cmd.positionals
				if cmd.setFlags != nil {
					cmd.setFlags(fs)
				}
			}
			continue
		}
		name, value, hasValue := cutFlag(argument)
		definition := fs.Lookup(name)
		if definition == nil {
			continue
		}
		if !hasValue {
			if isBoolFlag(definition) {
				value = "true"
			} else {
				if index+1 >= len(args) {
					continue
				}
				index++
				value = args[index]
			}
		}
		switch name {
		case "json":
			if enabled, err := strconv.ParseBool(value); err == nil {
				flags.jsonFlag = enabled
			}
		case "o":
			_ = definition.Value.Set(value)
		}
	}
	return currentFormat(flags)
}

// scanFailureTokens collects every --token value in the arguments, including
// ones that appear after a failing argument. Known non-boolean flags consume
// their values so a value cannot masquerade as the flag itself, and -- ends
// the scan.
func scanFailureTokens(args []string) []string {
	fs := flag.NewFlagSet("agctl", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	registerGlobalFlags(fs, &globalFlags{timeout: defaultTimeout, output: string(outputTable)})
	var tokens []string
	for index := 0; index < len(args); index++ {
		argument := args[index]
		if argument == "--" {
			break
		}
		if len(argument) < 2 || argument[0] != '-' {
			continue
		}
		name, value, hasValue := cutFlag(argument)
		definition := fs.Lookup(name)
		if !hasValue {
			if definition != nil && !isBoolFlag(definition) {
				if index+1 >= len(args) {
					continue
				}
				index++
				value = args[index]
			} else {
				continue
			}
		}
		if name == "token" {
			tokens = append(tokens, value)
		}
	}
	return tokens
}

// RunCLI executes one agctl invocation and returns its documented exit code.
// Resource commands never prompt: missing required input fails with a usage
// error before any request is sent. The optional shell supplies a TTY frontend.
func RunCLI(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	failureFormat, knownSecrets := scanFailureFlags(args)
	knownSecrets = append(knownSecrets, os.Getenv("AGCTL_TOKEN"))
	flags := &globalFlags{timeout: defaultTimeout, output: string(outputTable)}
	globalSet := flag.NewFlagSet("agctl", flag.ContinueOnError)
	globalSet.SetOutput(io.Discard)
	registerGlobalFlags(globalSet, flags)
	if err := globalSet.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printUsage(stdout)
			return ExitOK
		}
		knownSecrets = collectTokenSecret(globalSet, knownSecrets)
		renderFailure(stdout, stderr, failureFormat, &UsageError{Message: err.Error(), Hint: "run agctl --help for usage"}, knownSecrets...)
		return ExitUsage
	}
	set := collectFlags(globalSet)
	rest := globalSet.Args()
	if len(rest) == 0 {
		if currentFormat(flags) == outputJSON {
			renderFailure(stdout, stderr, outputJSON, &UsageError{Message: "no command given", Hint: "run agctl --help for the command list"}, knownSecrets...)
			return ExitUsage
		}
		printUsage(stderr)
		return ExitUsage
	}
	if rest[0] == "help" {
		printUsage(stdout)
		return ExitOK
	}
	cmd, commandArgs, ok := resolveCommand(rest)
	if !ok {
		return handleUnknownCommand(stdout, stderr, failureFormat, rest, knownSecrets)
	}

	// Command-level parse: global flags are registered again with their
	// current values as defaults so flags after the command name work too.
	localSet := flag.NewFlagSet("agctl "+cmd.name, flag.ContinueOnError)
	localSet.SetOutput(io.Discard)
	registerGlobalFlags(localSet, flags)
	if cmd.setFlags != nil {
		cmd.setFlags(localSet)
	}
	positionalArgs, err := parseInterspersed(localSet, commandArgs)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printCommandUsage(stdout, cmd)
			return ExitOK
		}
		knownSecrets = collectTokenSecret(localSet, knownSecrets)
		renderFailure(stdout, stderr, failureFormat, &UsageError{
			Message: err.Error(),
			Hint:    fmt.Sprintf("run agctl %s --help for usage", cmd.name),
		}, knownSecrets...)
		return ExitUsage
	}
	mergeFlags(set, localSet)

	resolvedFormat, err := flags.format()
	if err != nil {
		renderFailure(stdout, stderr, currentFormat(flags), err, knownSecrets...)
		return exitCodeFor(err)
	}
	format := resolvedFormat
	fail := func(err error) int {
		// A stream already reported its failure in its terminal event; a
		// second document would break NDJSON consumers.
		var streamed *streamedError
		if !errors.As(err, &streamed) {
			renderFailure(stdout, stderr, format, err, knownSecrets...)
		}
		return exitCodeFor(err)
	}

	if flags.timeout <= 0 {
		return fail(&UsageError{
			Message: fmt.Sprintf("timeout %s must be positive", flags.timeout),
			Hint:    "pass --timeout with a duration such as 30s",
		})
	}
	if len(positionalArgs) < cmd.positionals {
		message := fmt.Sprintf("agctl %s requires %d arguments", cmd.name, cmd.positionals)
		if cmd.argsUsage != "" {
			message = fmt.Sprintf("missing required argument %s", cmd.argsUsage)
		}
		return fail(&UsageError{
			Message: message,
			Hint:    fmt.Sprintf("run agctl %s --help for usage", cmd.name),
		})
	}
	if flags.dryRun && cmd.meta.Mutating && cmd.meta.DryRun == "unsupported" {
		// A write that cannot plan without side effects must refuse before any
		// configuration write or HTTP request, never run the write silently.
		return fail(&UsageError{
			Message: fmt.Sprintf("--dry-run is not supported by agctl %s", cmd.name),
			Hint:    "this write command cannot plan without side effects yet; re-run without --dry-run",
		})
	}
	if len(positionalArgs) > cmd.positionals {
		// A malformed positional tail must not select the output format of the
		// failure: post-command flags are not trusted once a stray positional
		// appears (see scanFailureFlags).
		renderFailure(stdout, stderr, failureFormat, &UsageError{
			Message: fmt.Sprintf("unexpected argument %q", positionalArgs[cmd.positionals]),
			Hint:    fmt.Sprintf("run agctl %s --help for usage", cmd.name),
		}, knownSecrets...)
		return ExitUsage
	}
	configPath := defaultConfigPath()
	settings, err := resolveSettings(flags, set, os.Getenv, configPath)
	if err != nil {
		return fail(err)
	}
	knownSecrets = append(knownSecrets, settings.Token)
	env := &commandContext{
		globals:    flags,
		settings:   settings,
		stdin:      stdin,
		stdout:     stdout,
		stderr:     stderr,
		args:       positionalArgs,
		format:     format,
		version:    buildinfo.Read(),
		configPath: configPath,
		flagSet:    set,
		addKnownSecret: func(secret string) {
			knownSecrets = append(knownSecrets, secret)
		},
	}
	if cmd.name != "shell" {
		var stop context.CancelFunc
		ctx, stop = signal.NotifyContext(ctx, os.Interrupt)
		defer stop()
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

// collectTokenSecret records a --token value that parsing already applied, so
// failure rendering can redact it even before settings are resolved.
func collectTokenSecret(fs *flag.FlagSet, knownSecrets []string) []string {
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "token" {
			knownSecrets = append(knownSecrets, f.Value.String())
		}
	})
	return knownSecrets
}

func mergeFlags(set map[string]bool, fs *flag.FlagSet) {
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
}

// parseInterspersed parses command arguments where flags and positional
// arguments may be interleaved. The stdlib flag package stops at the first
// positional, so each consumed positional triggers a re-parse of the rest.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var positionals []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return positionals, nil
		}
		positionals = append(positionals, rest[0])
		args = rest[1:]
	}
}

// resolveCommand matches the longest registered command name at the start of
// args (commands may span several words, such as `config profile add`) and
// returns the remaining arguments.
func resolveCommand(args []string) (command, []string, bool) {
	for length := len(args); length >= 1; length-- {
		if cmd, ok := lookupCommand(strings.Join(args[:length], " ")); ok {
			return cmd, args[length:], true
		}
	}
	return command{}, nil, false
}

// directChildren lists the distinct next subcommand words registered under a
// group path. An empty prefix lists the top-level commands.
func directChildren(prefix []string) []string {
	seen := map[string]bool{}
	var children []string
	for _, cmd := range registry() {
		parts := strings.Split(cmd.name, " ")
		if len(parts) <= len(prefix) {
			continue
		}
		matches := true
		for index, part := range prefix {
			if parts[index] != part {
				matches = false
				break
			}
		}
		if !matches {
			continue
		}
		child := parts[len(prefix)]
		if !seen[child] {
			seen[child] = true
			children = append(children, child)
		}
	}
	sort.Strings(children)
	return children
}

// nearestGroup finds the deepest known group path in args, used to explain an
// unknown subcommand with its parent's available subcommands.
func nearestGroup(args []string) []string {
	for length := len(args) - 1; length >= 1; length-- {
		if len(directChildren(args[:length])) > 0 {
			return args[:length]
		}
	}
	return nil
}

func handleUnknownCommand(stdout, stderr io.Writer, format outputFormat, rest []string, knownSecrets []string) int {
	// Only the leading command path belongs in the message: flag names and
	// their values (which may be credentials) must never be echoed.
	path := rest
	for index, argument := range rest {
		if strings.HasPrefix(argument, "-") {
			path = rest[:index]
			break
		}
	}
	described := strings.Join(path, " ")
	// `agctl <group…> --help` prints that group's subcommands.
	if len(rest) > 1 {
		last := rest[len(rest)-1]
		if last == "--help" || last == "-h" {
			prefix := rest[:len(rest)-1]
			if children := directChildren(prefix); len(children) > 0 {
				printGroupUsage(stdout, prefix, children)
				return ExitOK
			}
		}
	}
	// A path that names a group needs a subcommand.
	if children := directChildren(path); len(children) > 0 {
		renderFailure(stdout, stderr, format, &UsageError{
			Message: fmt.Sprintf("%q requires a subcommand", described),
			Hint:    fmt.Sprintf("choose one of: %s", strings.Join(children, ", ")),
		}, knownSecrets...)
		return ExitUsage
	}
	// An unknown subcommand names its nearest group's alternatives.
	if parent := nearestGroup(path); parent != nil {
		children := directChildren(parent)
		renderFailure(stdout, stderr, format, &UsageError{
			Message: fmt.Sprintf("unknown command %q", described),
			Hint:    fmt.Sprintf("subcommands of `agctl %s`: %s", strings.Join(parent, " "), strings.Join(children, ", ")),
		}, knownSecrets...)
		return ExitUsage
	}
	renderFailure(stdout, stderr, format, &UsageError{
		Message: fmt.Sprintf("unknown command %q", described),
		Hint:    "run agctl --help for the command list",
	}, knownSecrets...)
	return ExitUsage
}

func printUsage(w io.Writer) {
	_, _ = fmt.Fprintln(w, "agctl — Gateway command line for CI, platform management, and agents")
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "usage: agctl [global flags] <command> [command flags]")
	_, _ = fmt.Fprintln(w)
	type usageLine struct{ name, synopsis string }
	seenGroups := map[string]bool{}
	var lines []usageLine
	width := 0
	for _, cmd := range registry() {
		parts := strings.SplitN(cmd.name, " ", 2)
		line := usageLine{name: parts[0], synopsis: cmd.synopsis}
		if len(parts) == 2 {
			if seenGroups[parts[0]] {
				continue
			}
			seenGroups[parts[0]] = true
			line.name = parts[0] + " <subcommand>"
			line.synopsis = groupSynopses[parts[0]]
		}
		if len(line.name) > width {
			width = len(line.name)
		}
		lines = append(lines, line)
	}
	_, _ = fmt.Fprintln(w, "commands:")
	for _, line := range lines {
		_, _ = fmt.Fprintf(w, "  %-*s  %s\n", width, line.name, line.synopsis)
	}
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "run `agctl <command> --help` for command flags; `agctl commands --json` prints the machine-readable tree")
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "global flags:")
	printGlobalFlags(w)
}

var groupSynopses = map[string]string{
	"auth":                  "authenticate, inspect the caller, and end the local session",
	"config":                "manage local profiles in ~/.config/agctl/config.yaml",
	"access":                "explain effective access and manage the anonymous read switch",
	"api-key":               "list, create, and revoke platform API keys (secrets go only to --secret-output)",
	"credential":            "list, issue, and revoke service account credentials (secrets go only to --secret-output)",
	"access anonymous":      "read or set the platform switch for anonymous reads",
	"artifact":              "search, browse, inspect, and delete artifacts",
	"artifact intelligence": "read artifact intelligence records",
	"artifact quarantine":   "read artifact quarantine records",
	"audit":                 "read administrative audit records",
	"grant":                 "list, write, and remove single repository grants",
	"email":                 "email channel capability, targets, previews, tests, and deliveries",
	"email delivery":        "list, inspect, wait for, and replay email deliveries",
	"email target":          "list, inspect, create, and update email targets",
	"group":                 "list, inspect, create, update, and delete Groups and their members",
	"lifecycle-job":         "read lifecycle jobs (retention, promotion, replication, reclaim, scans)",
	"maven":                 "Maven views of a repository",
	"oci":                   "OCI views of a repository",
	"overview":              "request and capacity overview",
	"runtime":               "Gateway runtime processes",
	"group members":         "list or replace a Group's members",
	"quota-alert":           "repository quota alert rules and their events",
	"publish":               "publish Raw files and Maven coordinates with verified digests",
	"role":                  "read authorization roles",
	"service-account":       "list, inspect, create, and update service accounts",
	"user":                  "manage local users, their passwords, sessions, and linked identities",
	"user identities":       "list, link, and unlink a user's external identities",
	"user sessions":         "list and end a user's sessions",
	"template":              "read authorization templates and apply them to repositories",
	"repo":                  "manage repositories, browse them, and print client configuration",
}

func printGroupUsage(w io.Writer, prefix, children []string) {
	path := strings.Join(prefix, " ")
	_, _ = fmt.Fprintf(w, "usage: agctl %s <subcommand> [flags]\n\nsubcommands:\n", path)
	width := 0
	for _, child := range children {
		if len(child) > width {
			width = len(child)
		}
	}
	for _, child := range children {
		synopsis := ""
		if cmd, ok := lookupCommand(path + " " + child); ok {
			synopsis = cmd.synopsis
		} else {
			// A nested group such as `group members` has no command of its own.
			synopsis = groupSynopses[path+" "+child]
		}
		_, _ = fmt.Fprintf(w, "  %-*s  %s\n", width, child, synopsis)
	}
	_, _ = fmt.Fprintf(w, "\nrun `agctl %s <subcommand> --help` for flags\n", path)
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
