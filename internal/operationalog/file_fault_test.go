package operationalog

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

type faultLogFile struct {
	*os.File
	partial     bool
	writeErr    error
	syncErr     error
	closeErr    error
	truncateErr error
	closeCount  int
}

func (f *faultLogFile) Write(data []byte) (int, error) {
	if f.partial {
		n, err := f.File.Write(data[:1])
		return n, errors.Join(err, f.writeErr)
	}
	return f.File.Write(data)
}
func (f *faultLogFile) Sync() error { return errors.Join(f.File.Sync(), f.syncErr) }
func (f *faultLogFile) Close() error {
	f.closeCount++
	return errors.Join(f.File.Close(), f.closeErr)
}
func (f *faultLogFile) Truncate(size int64) error {
	if f.truncateErr != nil {
		return f.truncateErr
	}
	return f.File.Truncate(size)
}

func faultOutput(t *testing.T, fault *faultLogFile) (*Output, FileOptions) {
	t.Helper()
	options := fileOptions(t)
	operations := defaultFileOperations()
	operations.open = func(path string) (logFile, error) {
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		fault.File = file
		return fault, err
	}
	output, err := newFileOutput(options, operations)
	if err != nil {
		t.Fatal(err)
	}
	return output, options
}

func TestFileOutputRollsBackPartialAndShortWritesBeforeRecovery(t *testing.T) {
	for _, failure := range []error{errors.New("synthetic-ENOSPC"), nil} {
		fault := &faultLogFile{}
		output, options := faultOutput(t, fault)
		if _, err := output.Write([]byte("{}\n")); err != nil {
			t.Fatal(err)
		}
		fault.partial, fault.writeErr = true, failure
		if n, err := output.Write([]byte("{\"n\":2}\n")); n != 0 || err == nil || (failure != nil && !errors.Is(err, failure)) || (failure == nil && !errors.Is(err, io.ErrShortWrite)) {
			t.Fatalf("partial event result = %d, %v", n, err)
		}
		fault.partial = false
		if _, err := output.Write([]byte("{\"n\":3}\n")); err != nil {
			t.Fatal(err)
		}
		if err := output.Close(); err != nil {
			t.Fatal(err)
		}
		if got := string(readFile(t, managedFiles(t, options.Directory)[0])); got != "{}\n{\"n\":3}\n" {
			t.Fatalf("partial prefix remained: %q", got)
		}
	}
}

func TestFileOutputStopsAfterRollbackFailureAndPreservesCleanupErrors(t *testing.T) {
	writeFailure := errors.New("synthetic-write-failure")
	rollbackFailure := errors.New("synthetic-rollback-failure")
	fault := &faultLogFile{partial: true, writeErr: writeFailure, truncateErr: rollbackFailure}
	output, _ := faultOutput(t, fault)
	if _, err := output.Write([]byte("{}\n")); !errors.Is(err, writeFailure) || !errors.Is(err, rollbackFailure) {
		t.Fatalf("rollback errors = %v", err)
	}
	fault.partial, fault.truncateErr = false, nil
	if _, err := output.Write([]byte("{}\n")); !errors.Is(err, rollbackFailure) {
		t.Fatalf("continued a damaged segment: %v", err)
	}
	if err := output.Close(); !errors.Is(err, writeFailure) || !errors.Is(err, rollbackFailure) || fault.closeCount != 1 {
		t.Fatalf("damaged output cleanup = %v, closes %d", err, fault.closeCount)
	}
}

func TestFileOutputReportsSyncAndCloseErrorsAndStillClosesOnce(t *testing.T) {
	syncFailure, closeFailure := errors.New("synthetic-sync"), errors.New("synthetic-close")
	fault := &faultLogFile{syncErr: syncFailure, closeErr: closeFailure}
	output, _ := faultOutput(t, fault)
	if n, err := output.Write([]byte("{}\n")); n != 3 || !errors.Is(err, syncFailure) {
		t.Fatalf("sync error = %d, %v", n, err)
	}
	for i := 0; i < 2; i++ {
		if err := output.Close(); !errors.Is(err, syncFailure) || !errors.Is(err, closeFailure) {
			t.Fatalf("cleanup errors = %v", err)
		}
	}
	if fault.closeCount != 1 {
		t.Fatalf("close count = %d", fault.closeCount)
	}
}

func TestFileOutputRotationFailuresKeepPreviouslyAcceptedEvents(t *testing.T) {
	for _, phase := range []string{"rename", "reopen"} {
		t.Run(phase, func(t *testing.T) {
			options := fileOptions(t)
			options.MaxSizeBytes = 3
			operations := defaultFileOperations()
			failure := errors.New("synthetic-rotation-failure")
			if phase == "rename" {
				operations.rename = func(string, string) error { return failure }
			} else {
				open, count := operations.open, 0
				operations.open = func(path string) (logFile, error) {
					count++
					if count > 1 {
						return nil, failure
					}
					return open(path)
				}
			}
			output, err := newFileOutput(options, operations)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := output.Write([]byte("{}\n")); err != nil {
				t.Fatal(err)
			}
			if n, err := output.Write([]byte("{}\n")); n != 0 || !errors.Is(err, failure) {
				t.Fatalf("rotation result = %d, %v", n, err)
			}
			if err := output.Close(); !errors.Is(err, failure) {
				t.Fatalf("rotation cleanup error = %v", err)
			}
			files := managedFiles(t, options.Directory)
			if len(files) != 1 || string(readFile(t, files[0])) != "{}\n" {
				t.Fatalf("accepted event lost on %s: %v", phase, files)
			}
		})
	}
}

func TestFileOutputRefusesRetentionOfReplacedOrUntrackedFiles(t *testing.T) {
	options := fileOptions(t)
	options.MaxSizeBytes, options.MaxBackups = 3, 1
	output, err := NewFileOutput(options)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := output.Close(); err == nil {
			t.Error("cleanup did not report the replaced segment")
		}
	}()
	for i := 0; i < 2; i++ {
		if _, err := output.Write([]byte("{}\n")); err != nil {
			t.Fatal(err)
		}
	}
	files := managedFiles(t, options.Directory)
	replacement := filepath.Join(options.Directory, "replacement.ndjson")
	if err := os.WriteFile(replacement, []byte("synthetic-user-file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, files[0]); err != nil {
		t.Fatal(err)
	}
	if n, err := output.Write([]byte("{}\n")); n != 3 || err == nil {
		t.Fatalf("replacement retention = %d, %v", n, err)
	}
	if string(readFile(t, files[0])) != "synthetic-user-file" {
		t.Fatal("retention deleted a replaced segment")
	}
}

func TestFileOutputRetriesFailedRetentionWithoutLosingCurrentEvent(t *testing.T) {
	options := fileOptions(t)
	options.MaxSizeBytes, options.MaxBackups = 3, 0
	operations := defaultFileOperations()
	failure := errors.New("synthetic-remove-failure")
	failRemove := true
	operations.remove = func(path string) error {
		if failRemove {
			return failure
		}
		return os.Remove(path)
	}
	output, err := newFileOutput(options, operations)
	if err != nil {
		t.Fatal(err)
	}
	defer closeFileOutput(t, output)
	if _, err := output.Write([]byte("{}\n")); err != nil {
		t.Fatal(err)
	}
	if n, err := output.Write([]byte("{}\n")); n != 3 || !errors.Is(err, failure) {
		t.Fatalf("retention failure = %d, %v", n, err)
	}
	failRemove = false
	if err := output.Flush(); err != nil || len(managedFiles(t, options.Directory)) != 1 {
		t.Fatalf("retention retry = %v", err)
	}
}

func TestFileOutputRefusesToOverwriteAnUntrackedRotationDestination(t *testing.T) {
	options := fileOptions(t)
	options.MaxSizeBytes = 3
	output, err := NewFileOutput(options)
	if err != nil {
		t.Fatal(err)
	}
	defer closeFileOutput(t, output)
	if _, err := output.Write([]byte("{}\n")); err != nil {
		t.Fatal(err)
	}
	active := managedFiles(t, options.Directory)[0]
	foreign := filepath.Join(filepath.Dir(active), "00000000000000000001.ndjson")
	if err := os.WriteFile(foreign, []byte("synthetic-untracked-segment"), 0o600); err != nil {
		t.Fatal(err)
	}
	if n, err := output.Write([]byte("{}\n")); n != 0 || err == nil {
		t.Fatalf("rotation collision = %d, %v", n, err)
	}
	if string(readFile(t, foreign)) != "synthetic-untracked-segment" || string(readFile(t, active)) != "{}\n" {
		t.Fatal("rotation overwrote an untracked or active file")
	}
}

func TestFileOutputInitializationFailureLeavesExistingFilesUntouched(t *testing.T) {
	options := fileOptions(t)
	existing := filepath.Join(options.Directory, "existing.ndjson")
	if err := os.WriteFile(existing, []byte("synthetic-existing-marker"), 0o600); err != nil {
		t.Fatal(err)
	}
	operations := defaultFileOperations()
	failure := errors.New("synthetic-open-failure")
	operations.open = func(string) (logFile, error) { return nil, failure }
	if _, err := newFileOutput(options, operations); !errors.Is(err, failure) {
		t.Fatalf("initialization failure = %v", err)
	}
	entries, err := os.ReadDir(options.Directory)
	if err != nil || len(entries) != 1 || string(readFile(t, existing)) != "synthetic-existing-marker" {
		t.Fatalf("initialization changed existing files: %v, %v", entries, err)
	}
}
