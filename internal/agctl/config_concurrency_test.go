package agctl

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestProfileMutationProcess(t *testing.T) {
	if os.Getenv("AGCTL_TEST_PROFILE_WORKER") != "1" {
		return
	}
	ready := os.Getenv("AGCTL_TEST_PROFILE_READY")
	if err := os.WriteFile(ready, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(os.Getenv("AGCTL_TEST_PROFILE_START")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("worker start deadline exceeded")
		}
		time.Sleep(time.Millisecond)
	}
	for i := 0; i < 4; i++ {
		name := os.Getenv("AGCTL_TEST_PROFILE_PREFIX") + "-" + strconv.Itoa(i)
		args := []string{"config", "profile", "add", name, "--server", "https://gateway.example", "--json"}
		var stdout, stderr bytes.Buffer
		if code := RunCLI(context.Background(), args, strings.NewReader(""), &stdout, &stderr); code != ExitOK {
			t.Fatalf("profile add code=%d stderr=%s", code, &stderr)
		}
	}
}

func TestProfileConcurrentProcessesPreserveUpdates(t *testing.T) {
	isolatedCLI(t)
	var seed strings.Builder
	seed.WriteString("profiles:\n")
	for i := 0; i < 64; i++ {
		fmt.Fprintf(&seed, "  seed-%d:\n    server: https://gateway.example\n", i)
	}
	configPath := writeHomeConfig(t, seed.String())
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	gateDir := t.TempDir()
	start := filepath.Join(gateDir, "start")
	const workers = 8
	children := make([]*exec.Cmd, workers)
	outputs := make([]bytes.Buffer, workers)
	for i := range children {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		t.Cleanup(cancel)
		child := exec.CommandContext(ctx, executable, "-test.run=^TestProfileMutationProcess$")
		child.Env = append(os.Environ(), "AGCTL_TEST_PROFILE_WORKER=1",
			"AGCTL_TEST_PROFILE_START="+start,
			"AGCTL_TEST_PROFILE_READY="+filepath.Join(gateDir, strconv.Itoa(i)),
			"AGCTL_TEST_PROFILE_PREFIX=worker-"+strconv.Itoa(i))
		child.Stdout = &outputs[i]
		child.Stderr = &outputs[i]
		children[i] = child
		if err := child.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if child.Process != nil {
				_ = child.Process.Kill()
			}
		})
	}
	deadline := time.Now().Add(10 * time.Second)
	for i := 0; i < workers; i++ {
		for {
			if _, err := os.Stat(filepath.Join(gateDir, strconv.Itoa(i))); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("workers did not reach the start barrier")
			}
			time.Sleep(time.Millisecond)
		}
	}
	if err := os.WriteFile(start, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for i, child := range children {
		if err := child.Wait(); err != nil {
			t.Fatalf("worker %d: %v\n%s", i, err, &outputs[i])
		}
	}
	content := readConfigContent(t, configPath)
	for i := 0; i < workers; i++ {
		for j := 0; j < 4; j++ {
			name := fmt.Sprintf("worker-%d-%d:", i, j)
			if !strings.Contains(content, name) {
				t.Fatalf("successful concurrent update %s was lost", name)
			}
		}
	}
	for i := 0; i < 64; i++ {
		if !strings.Contains(content, fmt.Sprintf("seed-%d:", i)) {
			t.Fatal("existing profile was lost")
		}
	}
}
