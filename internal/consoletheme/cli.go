package consoletheme

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func RunCLI(_ context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr)
		return 2
	}
	switch args[0] {
	case "validate":
		return runValidate(args[1:], stdout, stderr)
	case "install":
		return runInstall(args[1:], stdout, stderr)
	case "list":
		return runList(args[1:], stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "unknown theme command %q\n", args[0])
		printUsage(stderr)
		return 2
	}
}

func runValidate(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("theme validate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	file := flags.String("file", "", "theme package JSON file")
	format := flags.String("format", "text", "output format: text or json")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*file) == "" {
		_, _ = fmt.Fprintln(stderr, "--file is required")
		return 2
	}
	theme, err := LoadFileForInstall(*file)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "invalid theme: %v\n", err)
		return 1
	}
	if err := writeThemeResult(stdout, *format, "valid", theme, ""); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 2
	}
	return 0
}

func runInstall(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("theme install", flag.ContinueOnError)
	flags.SetOutput(stderr)
	file := flags.String("file", "", "theme package JSON file")
	directory := flags.String("dir", defaultDirectory(), "external Console theme directory")
	format := flags.String("format", "text", "output format: text or json")
	force := flags.Bool("force", false, "replace an existing external theme with the same id")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*file) == "" {
		_, _ = fmt.Fprintln(stderr, "--file is required")
		return 2
	}
	theme, err := LoadFileForInstall(*file)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "invalid theme: %v\n", err)
		return 1
	}
	builtinThemes, err := Builtins()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "load builtin themes: %v\n", err)
		return 1
	}
	for _, builtin := range builtinThemes {
		if builtin.ID == theme.ID {
			_, _ = fmt.Fprintf(stderr, "theme id %q is reserved by a builtin theme\n", theme.ID)
			return 1
		}
	}
	path, err := installTheme(theme, *directory, *force)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "install theme: %v\n", err)
		return 1
	}
	if err := writeThemeResult(stdout, *format, "installed", theme, path); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 2
	}
	return 0
}

func runList(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("theme list", flag.ContinueOnError)
	flags.SetOutput(stderr)
	directory := flags.String("dir", defaultDirectory(), "external Console theme directory")
	format := flags.String("format", "text", "output format: text or json")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	themes, err := NewRegistry(*directory).List()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "list themes: %v\n", err)
		return 1
	}
	if *format == "json" {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(map[string]any{"themes": themes}); err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	}
	if *format != "text" {
		_, _ = fmt.Fprintln(stderr, "--format must be text or json")
		return 2
	}
	for _, theme := range themes {
		if _, err := fmt.Fprintf(stdout, "%s\t%s\t%s\n", theme.ID, theme.Mode, theme.Name); err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
	}
	return 0
}

func installTheme(theme Theme, directory string, force bool) (string, error) {
	directory = strings.TrimSpace(directory)
	if directory == "" {
		return "", errors.New("theme directory is required")
	}
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return "", err
	}
	destination := filepath.Join(directory, theme.ID+".theme.json")
	if info, err := os.Lstat(destination); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return "", errors.New("destination exists and is not a regular file")
		}
		if !force {
			return "", errors.New("destination already exists; use --force to replace it")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	temporary, err := os.CreateTemp(directory, ".theme-install-*.json")
	if err != nil {
		return "", err
	}
	temporaryPath := temporary.Name()
	cleanup := true
	defer func() {
		_ = temporary.Close()
		if cleanup {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o640); err != nil {
		return "", err
	}
	encoder := json.NewEncoder(temporary)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(theme); err != nil {
		return "", err
	}
	if err := temporary.Sync(); err != nil {
		return "", err
	}
	if err := temporary.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(temporaryPath, destination); err != nil {
		return "", err
	}
	cleanup = false
	return destination, nil
}

func writeThemeResult(output io.Writer, format, status string, theme Theme, path string) error {
	switch format {
	case "text":
		if path == "" {
			_, err := fmt.Fprintf(output, "%s: %s (%s, %s)\n", status, theme.Name, theme.ID, theme.Mode)
			return err
		}
		_, err := fmt.Fprintf(output, "%s: %s (%s, %s) at %s\n", status, theme.Name, theme.ID, theme.Mode, path)
		return err
	case "json":
		encoder := json.NewEncoder(output)
		encoder.SetIndent("", "  ")
		return encoder.Encode(map[string]any{"status": status, "path": path, "theme": theme})
	default:
		return errors.New("--format must be text or json")
	}
}

func defaultDirectory() string {
	if value := strings.TrimSpace(os.Getenv("GATEWAY_CONSOLE_THEME_DIR")); value != "" {
		return value
	}
	return "themes"
}

func printUsage(output io.Writer) {
	_, _ = fmt.Fprintln(output, "usage: gateway theme {validate|install|list} [options]")
}
