package agctl

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// POSIX record locks are per-process, so writers also serialize within the
// process. The stable sibling lock file is never renamed or removed; closing
// its handle (including process exit) releases the OS lock.
var configMutationMu sync.Mutex

func acquireConfigLock(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, &UsageError{Message: fmt.Sprintf("cannot create config directory: %v", err)}
	}
	file, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, &UsageError{Message: fmt.Sprintf("cannot open config lock: %v", err)}
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		locked, err := tryConfigLock(file)
		if err != nil {
			_ = file.Close()
			return nil, &UsageError{Message: fmt.Sprintf("cannot lock config file: %v", err)}
		}
		if locked {
			return file, nil
		}
		if time.Now().After(deadline) {
			_ = file.Close()
			return nil, &UsageError{Message: "timed out waiting for another config writer; retry the command"}
		}
		time.Sleep(10 * time.Millisecond)
	}
}
