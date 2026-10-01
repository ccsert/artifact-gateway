package main

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/config"
	"github.com/artifact-gateway/artifact-gateway/internal/operationalog"
)

func TestRuntimeLogOutputClosesAfterRuntimeCleanup(t *testing.T) {
	for _, code := range []int{0, 1} {
		t.Run(map[int]string{0: "normal shutdown", 1: "startup error"}[code], func(t *testing.T) {
			var events []string
			output := operationalog.NewOutput(io.Discard,
				func() error { events = append(events, "flush"); return nil },
				func() error { events = append(events, "close"); return nil })
			got, err := runWithLogOutput(output, func() int {
				defer func() { events = append(events, "runtime cleanup") }()
				events = append(events, "runtime stopped")
				return code
			})
			if got != code || err != nil || !reflect.DeepEqual(events, []string{"runtime stopped", "runtime cleanup", "flush", "close"}) {
				t.Fatalf("exit=%d cleanup=%v events=%v", got, err, events)
			}
			if err := output.Close(); err != nil || len(events) != 4 {
				t.Fatalf("repeated cleanup=%v events=%v", err, events)
			}
		})
	}
}

func TestConfiguredGatewayStartupFailureReturnsThroughLogCleanup(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "synthetic.theme.json"), []byte("invalid synthetic theme"), 0600); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	var events []string
	output := operationalog.NewOutput(&logs,
		func() error { events = append(events, "flush"); return nil },
		func() error { events = append(events, "close"); return nil })
	previous := slog.Default()
	slog.SetDefault(operationalog.NewLogger(output, "synthetic-node", "synthetic-session"))
	t.Cleanup(func() { slog.SetDefault(previous) })
	code, err := runWithLogOutput(output, func() int {
		return runConfiguredGateway(config.Config{ConsoleThemeDir: directory}, "synthetic-session", nil)
	})
	if code != 1 || err != nil || !reflect.DeepEqual(events, []string{"flush", "close"}) || !strings.Contains(logs.String(), `"msg":"load Console themes"`) {
		t.Fatalf("startup exit=%d cleanup=%v events=%v logs=%s", code, err, events, logs.String())
	}
}

func TestRuntimeContextCanBeStoppedOnNonSignalExit(t *testing.T) {
	ctx, stop := signalContext()
	t.Cleanup(stop)
	stop()
	stop()
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("runtime cancellation did not reach background operations")
	}
}

func TestRuntimeLogOutputReturnsCleanupFailureWithoutChangingRuntimeExitCode(t *testing.T) {
	flushErr, closeErr := errors.New("synthetic flush failure"), errors.New("synthetic close failure")
	output := operationalog.NewOutput(io.Discard, func() error { return flushErr }, func() error { return closeErr })
	code, err := runWithLogOutput(output, func() int { return 7 })
	if code != 7 || !errors.Is(err, flushErr) || !errors.Is(err, closeErr) {
		t.Fatalf("exit=%d cleanup=%v", code, err)
	}
}

func TestRuntimeLogOutputCleansUpDuringPanicWithoutRecoveringIt(t *testing.T) {
	var events []string
	output := operationalog.NewOutput(io.Discard,
		func() error { events = append(events, "flush"); return nil },
		func() error { events = append(events, "close"); return nil })
	func() {
		defer func() {
			if recovered := recover(); recovered != "synthetic-panic" {
				t.Fatalf("panic = %v", recovered)
			}
		}()
		_, _ = runWithLogOutput(output, func() int { panic("synthetic-panic") })
	}()
	if !reflect.DeepEqual(events, []string{"flush", "close"}) {
		t.Fatalf("panic cleanup=%v", events)
	}
}
