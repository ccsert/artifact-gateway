package agctl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/chzyer/readline"
)

func shellCommand() command {
	return command{
		name:     "shell",
		synopsis: "open an interactive terminal shell",
		usage:    "agctl shell [global flags]",
		meta: commandMeta{
			Resource: "local", Scenarios: []string{"admin"}, Stage: "m1",
			MinimumRole: "none", Endpoints: []string{}, DryRun: "unsupported",
		},
		run: runShell,
	}
}

type shellSession struct {
	env                   *commandContext
	profile, repo         string
	json, dryRun, verbose bool
}

// Only options explicitly selected on shell startup are inherited. Profile
// credentials are resolved afresh by RunCLI on every invocation.
func (s *shellSession) arguments(args []string) []string {
	var prefix []string
	g := s.env.globals
	for _, option := range []struct{ name, value string }{{"server", g.server}, {"token", g.token}, {"timeout", g.timeout.String()}, {"insecure", strconv.FormatBool(g.insecure)}} {
		if s.env.flagSet[option.name] {
			prefix = append(prefix, "--"+option.name+"="+option.value)
		}
	}
	if s.profile != "" {
		prefix = append(prefix, "--profile="+s.profile)
	}
	prefix = append(prefix, fmt.Sprintf("--json=%t", s.json), fmt.Sprintf("--dry-run=%t", s.dryRun), fmt.Sprintf("--verbose=%t", s.verbose))
	// Parse the leading global flags to locate the registered command. Never
	// inject --repo into commands that do not declare it.
	fs := flag.NewFlagSet("shell", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	registerGlobalFlags(fs, &globalFlags{})
	if fs.Parse(args) == nil {
		if cmd, remaining, ok := resolveCommand(fs.Args()); ok && cmd.setFlags != nil && s.repo != "" {
			local := flag.NewFlagSet("command", flag.ContinueOnError)
			local.SetOutput(io.Discard)
			cmd.setFlags(local)
			if cmd.name == "artifact search" {
				// Match RunCLI's flag parsing, including values that look like
				// flags and repeated boolean overrides, before choosing scope.
				registerGlobalFlags(local, &globalFlags{})
				if _, err := parseInterspersed(local, remaining); err == nil {
					if global := local.Lookup("global"); global != nil && global.Value.String() == "true" {
						return append(prefix, args...)
					}
				}
			}
			if local.Lookup("repo") != nil {
				insert := len(args) - len(remaining)
				withRepo := append([]string{}, args[:insert]...)
				withRepo = append(withRepo, "--repo="+s.repo)
				args = append(withRepo, args[insert:]...)
			}
		}
	}
	return append(prefix, args...)
}

func (s *shellSession) builtin(args []string) (handled, exit bool, err error) {
	if len(args) == 0 {
		return true, false, nil
	}
	switch args[0] {
	case "exit", "quit":
		if len(args) != 1 {
			return true, false, &UsageError{Message: "exit takes no arguments"}
		}
		return true, true, nil
	case "use":
		if len(args) != 3 {
			return true, false, &UsageError{Message: "use repo <id> or use profile <name> (use repo - clears the repository)"}
		}
		switch args[1] {
		case "repo":
			if args[2] == "-" {
				s.repo = ""
			} else {
				s.repo = args[2]
			}
		case "profile":
			cfg, e := loadConfigFile(s.env.configPath)
			if e != nil {
				return true, false, e
			}
			if cfg == nil {
				return true, false, &UsageError{Message: "profile not found"}
			}
			if _, ok := cfg.Profiles[args[2]]; !ok {
				return true, false, &UsageError{Message: "profile not found"}
			}
			s.profile = args[2]
			s.repo = ""
		default:
			return true, false, &UsageError{Message: "use repo <id> or use profile <name>"}
		}
		return true, false, nil
	case "set":
		if len(args) != 3 || (args[2] != "on" && args[2] != "off") {
			return true, false, &UsageError{Message: "set json|dry-run|verbose on|off"}
		}
		value := args[2] == "on"
		switch args[1] {
		case "json":
			s.json = value
		case "dry-run":
			s.dryRun = value
		case "verbose":
			s.verbose = value
		default:
			return true, false, &UsageError{Message: "set json|dry-run|verbose on|off"}
		}
		return true, false, nil
	case "shell":
		return true, false, &UsageError{Message: "nested shells are not supported"}
	}
	{
		fs := flag.NewFlagSet("shell", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		registerGlobalFlags(fs, &globalFlags{})
		if fs.Parse(args) == nil {
			if cmd, _, ok := resolveCommand(fs.Args()); ok && cmd.name == "shell" {
				return true, false, &UsageError{Message: "nested shells are not supported"}
			}
		}
	}
	return false, false, nil
}

// splitShellLine supports literal quotes and backslash escapes, with no
// expansion, pipelines, redirection, command substitution, or subprocesses.
func splitShellLine(line string) ([]string, error) {
	var args []string
	var word strings.Builder
	var quote rune
	escaped, started := false, false
	for _, r := range line {
		if escaped {
			word.WriteRune(r)
			escaped = false
			started = true
			continue
		}
		if r == '\\' && quote != '\'' {
			escaped = true
			started = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				word.WriteRune(r)
			}
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			started = true
			continue
		}
		if unicode.IsSpace(r) {
			if started {
				args = append(args, word.String())
				word.Reset()
				started = false
			}
			continue
		}
		word.WriteRune(r)
		started = true
	}
	if escaped || quote != 0 {
		return nil, &UsageError{Message: "unfinished quote or escape"}
	}
	if started {
		args = append(args, word.String())
	}
	return args, nil
}

func shellHistoryEntry(line string) string {
	args, err := splitShellLine(line)
	if err != nil {
		return ""
	}
	// Persist only exact registered paths, never arguments, input, or output.
	fs := flag.NewFlagSet("history", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	registerGlobalFlags(fs, &globalFlags{})
	if fs.Parse(args) != nil {
		return ""
	}
	if cmd, _, ok := resolveCommand(fs.Args()); ok && cmd.name != "shell" {
		return cmd.name
	}
	return ""
}

const shellHistoryLimit = 500

func readShellHistory(path string) ([]string, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 1<<20 {
		return nil, fmt.Errorf("history must be a private regular file (0600), at most 1 MiB")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	actual, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(info, actual) {
		return nil, fmt.Errorf("history changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(f, 1<<20))
	if err != nil {
		return nil, err
	}
	var entries []string
	for _, line := range strings.Split(string(data), "\n") {
		if safe := shellHistoryEntry(line); safe != "" && safe == line {
			entries = append(entries, safe)
		}
	}
	if len(entries) > shellHistoryLimit {
		entries = entries[len(entries)-shellHistoryLimit:]
	}
	return entries, nil
}
func appendShellHistory(path, entry string) error {
	if entry == "" || shellHistoryEntry(entry) != entry {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	configMutationMu.Lock()
	defer configMutationMu.Unlock()
	lock, err := acquireConfigLock(path)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()
	entries, err := readShellHistory(path)
	if err != nil {
		return err
	}
	entries = append(entries, entry)
	if len(entries) > shellHistoryLimit {
		entries = entries[len(entries)-shellHistoryLimit:]
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".history-*")
	if err != nil {
		return err
	}
	temp := f.Name()
	defer func() { _ = os.Remove(temp) }()
	if _, err = f.WriteString(strings.Join(entries, "\n") + "\n"); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(temp, path)
}

func (s *shellSession) prompt() string {
	args := s.arguments([]string{"version"})
	fs := flag.NewFlagSet("prompt", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	flags := &globalFlags{}
	registerGlobalFlags(fs, flags)
	_ = fs.Parse(args)
	settings, err := resolveSettings(flags, collectFlags(fs), os.Getenv, s.env.configPath)
	if err != nil {
		return "agctl [configuration error]> "
	}
	clean := func(value string) string {
		return strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return -1
			}
			return r
		}, value)
	}
	return fmt.Sprintf("agctl [%s | %s | repo:%s]> ", clean(settings.Name), clean(redactServerURL(settings.Server, settings.Token)), clean(s.repo))
}

func (s *shellSession) candidates(ctx context.Context, line string) []string {
	words := strings.Fields(line)
	if strings.HasSuffix(line, " ") || len(words) == 0 {
		words = append(words, "")
	}
	prefix := words[len(words)-1]
	var values []string
	path := words[:len(words)-1]
	switch strings.Join(path, " ") {
	case "use":
		values = []string{"profile", "repo"}
	case "use profile":
		cfg, err := loadConfigFile(s.env.configPath)
		if err == nil && cfg != nil {
			for name := range cfg.Profiles {
				values = append(values, name)
			}
		}
	case "use repo":
		bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		var output bytes.Buffer
		args := s.arguments([]string{"repo", "list", "--all", "--page-size=200", "--json", "--timeout=2s"})
		code := RunCLI(bounded, args, strings.NewReader(""), &output, io.Discard)
		if code == ExitOK || code == ExitPartial {
			var result struct {
				Data struct {
					Items []struct {
						ID string `json:"id"`
					} `json:"items"`
				} `json:"data"`
			}
			if json.Unmarshal(output.Bytes(), &result) == nil {
				for _, item := range result.Data.Items {
					values = append(values, item.ID)
				}
			}
		}
	case "set":
		values = []string{"json", "dry-run", "verbose"}
	case "set json", "set dry-run", "set verbose":
		values = []string{"on", "off"}
	default:
		values = directChildren(path)
		if len(path) == 0 {
			values = append(values, "use", "set", "exit", "quit", "help")
		}
	}
	var matches []string
	for _, value := range values {
		if validProfileName(value) == nil && strings.HasPrefix(escapeShellCompletion(value), prefix) {
			matches = append(matches, value)
		}
	}
	sort.Strings(matches)
	return matches
}

func escapeShellCompletion(value string) string {
	return strings.NewReplacer(`\`, `\\`, `'`, `\'`, `"`, `\"`).Replace(value)
}

type shellCompleter struct {
	ctx     context.Context
	session *shellSession
}

func (c shellCompleter) Do(line []rune, pos int) ([][]rune, int) {
	text := string(line[:pos])
	words := strings.Fields(text)
	prefix := ""
	if len(words) > 0 && !strings.HasSuffix(text, " ") {
		prefix = words[len(words)-1]
	}
	var results [][]rune
	for _, candidate := range c.session.candidates(c.ctx, text) {
		// Completion suffixes become shell input, so quote characters and
		// backslashes must remain literal when splitShellLine parses them.
		suffix := strings.TrimPrefix(escapeShellCompletion(candidate), prefix)
		results = append(results, []rune(suffix+" "))
	}
	return results, len([]rune(prefix))
}

func runShell(ctx context.Context, env *commandContext) error {
	input, ok := env.stdin.(*os.File)
	if !ok || !readline.IsTerminal(int(input.Fd())) {
		return &UsageError{Message: "agctl shell requires a TTY on stdin", Hint: "use ordinary agctl commands in scripts"}
	}
	output, ok := env.stdout.(*os.File)
	if !ok || !readline.IsTerminal(int(output.Fd())) {
		return &UsageError{Message: "agctl shell requires a TTY on stdout"}
	}
	// Keep SIGINT from terminating the session between commands. RunCLI owns
	// a separate subscription while executing each client request.
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt)
	defer signal.Stop(interrupts)
	s := &shellSession{env: env, profile: env.settings.Name, json: env.format == outputJSON, dryRun: env.globals.dryRun, verbose: env.globals.verbose}
	var state *readline.State
	rl, err := readline.NewEx(&readline.Config{Stdin: readline.NewCancelableStdin(input), Stdout: env.stdout, Stderr: env.stderr, DisableAutoSaveHistory: true, HistoryLimit: shellHistoryLimit, AutoComplete: shellCompleter{ctx, s}, FuncIsTerminal: func() bool { return true }, FuncMakeRaw: func() error { var e error; state, e = readline.MakeRaw(int(input.Fd())); return e }, FuncExitRaw: func() error {
		if state != nil {
			return readline.Restore(int(input.Fd()), state)
		}
		return nil
	}})
	if err != nil {
		return err
	}
	defer func() { _ = rl.Close() }()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = rl.Close()
		case <-done:
		}
	}()
	historyPath := filepath.Join(filepath.Dir(env.configPath), "history")
	historyEnabled := env.configPath != ""
	var historyEntries []string
	if historyEnabled {
		entries, e := readShellHistory(historyPath)
		if e != nil {
			historyEnabled = false
			_, _ = fmt.Fprintln(env.stderr, "shell history disabled:", e)
		} else {
			historyEntries = entries
			for _, entry := range entries {
				_ = rl.SaveHistory(entry)
			}
		}
	}
	_, _ = fmt.Fprintln(env.stderr, "Shell: use repo <id>, use profile <name>, set json|dry-run|verbose on|off; exit or Ctrl-D. Ctrl-C cancels only the client request/wait.")
	for ctx.Err() == nil {
		rl.SetPrompt(s.prompt())
		line, e := rl.Readline()
		// readline retains temporary edits on recalled entries even with auto-save
		// disabled. SaveHistory can substitute those edits for its argument.
		// Rebuild from sanitized paths after every read, including invalid input
		// and builtins, before allowing another recall or sanitized insertion.
		rl.ResetHistory()
		for _, entry := range historyEntries {
			_ = rl.SaveHistory(entry)
		}
		if errors.Is(e, readline.ErrInterrupt) {
			continue
		}
		if errors.Is(e, io.EOF) {
			return nil
		}
		if e != nil {
			return e
		}
		args, e := splitShellLine(line)
		if e != nil {
			renderFailure(env.stdout, env.stderr, outputTable, e)
			continue
		}
		handled, exit, e := s.builtin(args)
		if e != nil {
			renderFailure(env.stdout, env.stderr, outputTable, e)
		}
		if exit {
			return nil
		}
		if handled {
			continue
		}
		if entry := shellHistoryEntry(line); entry != "" {
			historyEntries = append(historyEntries, entry)
			if len(historyEntries) > shellHistoryLimit {
				historyEntries = historyEntries[len(historyEntries)-shellHistoryLimit:]
			}
			_ = rl.SaveHistory(entry)
			if historyEnabled {
				if e := appendShellHistory(historyPath, entry); e != nil {
					historyEnabled = false
					_, _ = fmt.Fprintln(env.stderr, "shell history disabled:", e)
				}
			}
		}
		// A command never owns the terminal input stream. EOF makes password/token
		// and --file - readers fail promptly without consuming the next shell line.
		RunCLI(ctx, s.arguments(args), strings.NewReader(""), env.stdout, env.stderr)
	}
	return ctx.Err()
}
