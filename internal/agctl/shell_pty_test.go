//go:build darwin || linux

package agctl

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
)

type shellPTY struct {
	t      *testing.T
	cmd    *exec.Cmd
	file   *os.File
	chunks chan string
	done   chan error
	text   string
}

func startShellPTY(t *testing.T, binary, home string, args ...string) *shellPTY {
	t.Helper()
	cmd := exec.Command(binary, append([]string{"shell"}, args...)...)
	cmd.Env = append(os.Environ(), "HOME="+home, "AGCTL_PROFILE=", "AGCTL_SERVER=", "AGCTL_TOKEN=", "TERM=xterm")
	file, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 24, Cols: 100})
	if err != nil {
		t.Fatal(err)
	}
	// File.Fd may restore blocking mode on Linux. Duplicate first, then set
	// O_NONBLOCK on the raw descriptor before NewFile registers Go's poller.
	fd, err := syscall.Dup(int(file.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	syscall.CloseOnExec(fd)
	if err := syscall.SetNonblock(fd, true); err != nil {
		_ = syscall.Close(fd)
		t.Fatal(err)
	}
	file = os.NewFile(uintptr(fd), "shell-pty")
	p := &shellPTY{t: t, cmd: cmd, file: file, chunks: make(chan string, 1024), done: make(chan error, 1)}
	go func() {
		defer close(p.chunks)
		buf := make([]byte, 4096)
		for {
			n, err := file.Read(buf)
			if n > 0 {
				p.chunks <- string(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	go func() { p.done <- cmd.Wait() }()
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = file.Close() })
	p.expect("]> ")
	return p
}
func (p *shellPTY) expect(value string) {
	p.t.Helper()
	deadline := time.NewTimer(8 * time.Second)
	defer deadline.Stop()
	for !strings.Contains(p.text, value) {
		select {
		case text, ok := <-p.chunks:
			if !ok {
				p.t.Fatalf("PTY closed waiting for %q: %s", value, p.text)
			}
			p.text += text
		case <-deadline.C:
			p.t.Fatalf("PTY timed out waiting for %q: %s", value, p.text)
		}
	}
	p.text = p.text[strings.Index(p.text, value)+len(value):]
}
func (p *shellPTY) send(value string) {
	p.t.Helper()
	if _, err := io.WriteString(p.file, value); err != nil {
		p.t.Fatal(err)
	}
}
func (p *shellPTY) wait(success bool) {
	p.t.Helper()
	select {
	case err := <-p.done:
		if success && err != nil {
			p.t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		p.t.Fatal("shell did not exit within five seconds")
	}
}

func TestShellPTY(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "agctl")
	build := exec.Command("go", "build", "-o", binary, "../../cmd/agctl")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	t.Run("commands-history-and-exit", func(t *testing.T) {
		home := t.TempDir()
		p := startShellPTY(t, binary, home)
		p.send("ver\t\n")
		p.expect("(revision ")
		p.expect("]> ")
		p.send("set json on\n")
		p.expect("]> ")
		p.send("version\n")
		p.expect(`"schemaVersion": 1`)
		p.expect("]> ")
		p.send("version --token SYNTHETIC_TOKEN_136\n")
		p.expect(`"schemaVersion": 1`)
		p.expect("]> ")
		p.send("auth change-password --password-stdin\nversion\n")
		p.expect("stdin")
		p.expect(`"revision":`)
		p.expect("]> ")
		p.send("config profile add session --server https://example.com --token-stdin\nversion\n")
		p.expect("stdin")
		p.expect(`"revision":`)
		p.expect("]> ")
		p.send("repo create SYNTHETIC_BODY_136 --description SYNTHETIC_PRIVATE_136 --dry-run\n")
		p.expect("]> ")
		p.send("exit\n")
		p.wait(true)
		data, err := os.ReadFile(filepath.Join(home, ".config", "agctl", "history"))
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{"SYNTHETIC_", "--token", "--description", "schemaVersion"} {
			if bytes.Contains(data, []byte(secret)) {
				t.Fatalf("history leaked %q: %s", secret, data)
			}
		}
		if !bytes.Contains(data, []byte("version\n")) {
			t.Fatal("history missing command")
		}
	})
	t.Run("eof", func(t *testing.T) { p := startShellPTY(t, binary, t.TempDir()); p.send("\x04"); p.wait(true) })
	t.Run("closed-terminal", func(t *testing.T) { p := startShellPTY(t, binary, t.TempDir()); _ = p.file.Close(); p.wait(false) })
	t.Run("non-tty", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, "shell")
		cmd.Env = append(os.Environ(), "HOME="+t.TempDir(), "AGCTL_PROFILE=")
		cmd.Stdin = strings.NewReader("version\nexit\n")
		output, err := cmd.CombinedOutput()
		exit, ok := err.(*exec.ExitError)
		if !ok || exit.ExitCode() != 2 || !strings.Contains(string(output), "TTY") {
			t.Fatalf("non-TTY: %s, %v", output, err)
		}
	})
	t.Run("interrupt-request-then-continue", func(t *testing.T) {
		started := make(chan struct{}, 1)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case started <- struct{}{}:
			default:
			}
			<-r.Context().Done()
		}))
		defer server.Close()
		p := startShellPTY(t, binary, t.TempDir(), "--server", server.URL, "--token", "synthetic")
		p.send("auth whoami\n")
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("request never started")
		}
		p.send("\x03")
		p.expect("]> ")
		p.send("ver\t\n")
		p.expect("(revision ")
		p.expect("]> ")
		// Ctrl-C at the prompt must clear the input and preserve the session.
		p.send("discarded\x03")
		p.expect("]> ")
		p.send("ver\t\n")
		p.expect("(revision ")
		p.expect("]> ")
		p.send("exit\n")
		p.wait(true)
	})
	t.Run("termination-at-prompt", func(t *testing.T) {
		p := startShellPTY(t, binary, t.TempDir())
		if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
			t.Fatal(err)
		}
		p.wait(false)
	})
}
