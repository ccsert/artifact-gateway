package operationalog

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// FileOptions bounds one output session. Previous sessions and untracked files
// are never adopted or deleted. Directory must be an absolute deployment path.
type FileOptions struct {
	Directory    string
	MaxSizeBytes int64
	MaxBackups   int
	MaxAge       time.Duration
}

type logFile interface {
	io.Writer
	Sync() error
	Close() error
	Truncate(int64) error
	Seek(int64, int) (int64, error)
	Stat() (os.FileInfo, error)
}

type fileOperations struct {
	open   func(string) (logFile, error)
	rename func(string, string) error
	remove func(string) error
	lstat  func(string) (os.FileInfo, error)
	now    func() time.Time
}

func defaultFileOperations() fileOperations {
	return fileOperations{
		open: func(path string) (logFile, error) {
			return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		},
		rename: os.Rename, remove: os.Remove, lstat: os.Lstat, now: time.Now,
	}
}

type sealedFile struct {
	path     string
	sealedAt time.Time
	identity os.FileInfo
}

type fileWriter struct {
	options  FileOptions
	ops      fileOperations
	dir      string
	file     logFile
	size     int64
	sequence uint64
	sealed   []sealedFile
	failure  error
}

// NewFileOutput accepts one complete, already redacted JSON object plus newline
// per Write. It creates a private session directory, syncs each accepted event,
// and rotates before an event would exceed MaxSizeBytes. Close uses Output's
// serialized, idempotent lifecycle. It does not read or redact raw slog records.
func NewFileOutput(options FileOptions) (*Output, error) {
	return newFileOutput(options, defaultFileOperations())
}

func newFileOutput(options FileOptions, operations fileOperations) (*Output, error) {
	if !filepath.IsAbs(options.Directory) || options.MaxSizeBytes <= 0 || options.MaxSizeBytes > 1<<30 || options.MaxBackups < 0 || options.MaxBackups > 100 || options.MaxAge <= 0 || options.MaxAge > 365*24*time.Hour {
		return nil, errors.New("invalid runtime log file options")
	}
	if err := os.MkdirAll(options.Directory, 0o700); err != nil {
		return nil, err
	}
	directory, err := os.MkdirTemp(options.Directory, "gateway-")
	if err != nil {
		return nil, err
	}
	writer := &fileWriter{options: options, ops: operations, dir: directory}
	writer.file, err = operations.open(writer.activePath())
	if err != nil {
		// Only remove this newly created, still empty session directory.
		return nil, errors.Join(err, os.Remove(directory))
	}
	return NewOutput(writer, writer.flush, writer.close), nil
}

func (f *fileWriter) activePath() string { return filepath.Join(f.dir, "active.ndjson") }

func (f *fileWriter) Write(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}
	if data[len(data)-1] != '\n' || bytes.ContainsAny(data[:len(data)-1], "\r\n") || len(bytes.TrimSpace(data)) == 0 || bytes.TrimSpace(data)[0] != '{' || !json.Valid(data) {
		return 0, errors.New("runtime log file requires one complete JSON object per write")
	}
	if int64(len(data)) > f.options.MaxSizeBytes {
		return 0, errors.New("runtime log event exceeds file size limit")
	}
	if f.failure != nil {
		return 0, f.failure
	}
	if f.size+int64(len(data)) > f.options.MaxSizeBytes {
		if err := f.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := f.file.Write(data)
	if err != nil || n != len(data) {
		if err == nil {
			err = io.ErrShortWrite
		}
		// A failed event must not leave a prefix that the next event can join.
		rollbackErr := f.file.Truncate(f.size)
		_, seekErr := f.file.Seek(f.size, io.SeekStart)
		if rollbackErr != nil || seekErr != nil {
			f.failure = errors.Join(err, rollbackErr, seekErr)
		}
		return 0, errors.Join(err, rollbackErr, seekErr)
	}
	f.size += int64(n)
	return n, errors.Join(f.file.Sync(), f.prune())
}

func (f *fileWriter) rotate() error {
	path := filepath.Join(f.dir, fmt.Sprintf("%020d.ndjson", f.sequence+1))
	if _, err := f.ops.lstat(path); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			err = errors.New("runtime log rotation destination already exists")
		}
		return err
	}
	identity, err := f.file.Stat()
	if err != nil {
		return err
	}
	if err := f.file.Sync(); err != nil {
		return err
	}
	err = f.file.Close()
	f.file = nil
	if err != nil {
		f.failure = err
		return err
	}
	if err := f.ops.rename(f.activePath(), path); err != nil {
		f.failure = err
		return err
	}
	f.sequence++
	f.sealed = append(f.sealed, sealedFile{path: path, sealedAt: f.ops.now(), identity: identity})
	f.file, err = f.ops.open(f.activePath())
	if err != nil {
		f.failure = err
		return err
	}
	f.size = 0
	return nil
}

func (f *fileWriter) prune() error {
	for len(f.sealed) > 0 {
		oldest := f.sealed[0]
		if len(f.sealed) <= f.options.MaxBackups && f.ops.now().Sub(oldest.sealedAt) < f.options.MaxAge {
			break
		}
		identity, err := f.ops.lstat(oldest.path)
		if errors.Is(err, os.ErrNotExist) {
			f.sealed = f.sealed[1:]
			continue
		}
		if err != nil {
			return err
		}
		if !identity.Mode().IsRegular() || !os.SameFile(identity, oldest.identity) {
			return errors.New("runtime log retention refused a replaced segment")
		}
		if err := f.ops.remove(oldest.path); err != nil {
			return err
		}
		f.sealed = f.sealed[1:]
	}
	return nil
}

func (f *fileWriter) flush() error {
	if f.file == nil {
		return f.failure
	}
	return errors.Join(f.failure, f.file.Sync(), f.prune())
}

func (f *fileWriter) close() error {
	if f.file == nil {
		return nil
	}
	err := f.file.Close()
	f.file = nil
	return err
}
