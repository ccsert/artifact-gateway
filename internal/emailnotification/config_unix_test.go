//go:build unix

package emailnotification

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestConfigurationFIFOIsRejectedWithoutBlocking(t *testing.T) {
	for _, kind := range []string{"auth", "ca", "config"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "synthetic-fifo")
			if err := syscall.Mkfifo(path, 0600); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				var err error
				switch kind {
				case "auth":
					_, err = readAuth(path)
				case "ca":
					_, err = relayRoots(path)
				case "config":
					_, err = Load(path)
				}
				done <- err
			}()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("nonregular setting accepted")
				}
			case <-time.After(200 * time.Millisecond):
				t.Fatal("setting read blocked before regular-file validation")
			}
		})
	}
}
