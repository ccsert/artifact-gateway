package agctl

import (
	"flag"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var inlineCommand = regexp.MustCompile("`(agctl [^`]+)`")

// skillCommands extracts every agctl invocation the agent skill shows: the
// lines of shell blocks and inline code that start with agctl.
func skillCommands(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", ".agents", "skills", "agctl", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	var commands []string
	inBlock := false
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inBlock = !inBlock
			continue
		}
		if inBlock {
			if strings.HasPrefix(trimmed, "agctl ") {
				commands = append(commands, trimmed)
			}
			continue
		}
		for _, match := range inlineCommand.FindAllStringSubmatch(line, -1) {
			commands = append(commands, match[1])
		}
	}
	return commands
}

// shellWords splits a recipe line like a POSIX shell for the quoting the
// skill uses: double and single quotes group words.
func shellWords(line string) []string {
	var words []string
	var current strings.Builder
	quote := rune(0)
	inWord := false
	for _, r := range line {
		switch {
		case quote != 0 && r == quote:
			quote = 0
		case quote != 0:
			current.WriteRune(r)
		case r == '"' || r == '\'':
			quote, inWord = r, true
		case r == ' ' || r == '\t':
			if inWord {
				words = append(words, current.String())
				current.Reset()
				inWord = false
			}
		default:
			current.WriteRune(r)
			inWord = true
		}
	}
	if inWord {
		words = append(words, current.String())
	}
	return words
}

// TestSkillCommandsExist keeps the agent skill honest: every command it
// shows must be registered, accept every flag it passes, and take the
// number of arguments it gives. A recipe cannot advertise a planned command.
func TestSkillCommandsExist(t *testing.T) {
	commands := skillCommands(t)
	if len(commands) < 20 {
		t.Fatalf("expected the skill's recipes, found %d commands", len(commands))
	}
	for _, line := range commands {
		args := shellWords(strings.TrimPrefix(line, "agctl "))
		cmd, rest, ok := resolveCommand(args)
		if !ok {
			t.Errorf("%q names no registered command", line)
			continue
		}
		// A partial path such as `agctl repo connect` in prose is fine when
		// it names a command; flags and arguments are only checked when given.
		fs := flag.NewFlagSet(cmd.name, flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		registerGlobalFlags(fs, &globalFlags{timeout: defaultTimeout, output: string(outputTable)})
		if cmd.setFlags != nil {
			cmd.setFlags(fs)
		}
		positionals, err := parseInterspersed(fs, rest)
		if err != nil {
			t.Errorf("%q: %v", line, err)
			continue
		}
		if len(positionals) != 0 && len(positionals) != cmd.positionals {
			t.Errorf("%q passes %d arguments; %s takes %d", line, len(positionals), cmd.name, cmd.positionals)
		}
		if len(rest) > 0 && len(positionals) < cmd.positionals {
			t.Errorf("%q misses the required argument %s", line, cmd.argsUsage)
		}
	}
}
