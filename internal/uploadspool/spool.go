package uploadspool

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/ccsert/artifact-gateway/internal/objectstore"
)

var ErrTooLarge = errors.New("upload exceeds size limit")

const CopyBufferSize = 128 << 10

type Spool struct {
	file   *os.File
	hasher hash.Hash
	size   int64
}

func New() (*Spool, error) {
	file, err := os.CreateTemp("", "artifact-gateway-upload-*")
	if err != nil {
		return nil, err
	}
	return &Spool{file: file, hasher: sha256.New()}, nil
}

func Read(reader io.Reader, maxBytes int64) (*Spool, error) {
	spool, err := New()
	if err != nil {
		return nil, err
	}
	if _, err = spool.Append(reader, maxBytes); err != nil {
		_ = spool.Close()
		return nil, err
	}
	if err = spool.Rewind(); err != nil {
		_ = spool.Close()
		return nil, err
	}
	return spool, nil
}

func (s *Spool) Append(reader io.Reader, maxBytes int64) (int64, error) {
	if maxBytes < 0 {
		return 0, ErrTooLarge
	}
	limit := maxBytes
	if maxBytes < int64(^uint64(0)>>1) {
		limit++
	}
	limited := &io.LimitedReader{R: reader, N: limit}
	written, err := io.CopyBuffer(io.MultiWriter(s.file, s.hasher), limited, make([]byte, CopyBufferSize))
	s.size += written
	if err != nil {
		return written, err
	}
	if written > maxBytes {
		return written, ErrTooLarge
	}
	return written, nil
}

func (s *Spool) Rewind() error {
	_, err := s.file.Seek(0, io.SeekStart)
	return err
}

func (s *Spool) Reader() io.Reader { return s.file }

// File permits bounded ZIP/section inspection of a staged upload. The caller
// must not close it; Close retains ownership of cleanup.
func (s *Spool) File() *os.File { return s.file }

func (s *Spool) Size() int64 { return s.size }

func (s *Spool) Digest() string {
	return "sha256:" + hex.EncodeToString(s.hasher.Sum(nil))
}

func (s *Spool) DigestBytes() []byte {
	return append([]byte(nil), s.hasher.Sum(nil)...)
}

func (s *Spool) Close() error {
	name := s.file.Name()
	closeErr := s.file.Close()
	removeErr := os.Remove(name)
	if closeErr != nil {
		return closeErr
	}
	if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return removeErr
	}
	return nil
}

func AppendObject(_ context.Context, _ objectstore.Store, _ string, _ int64, body io.Reader, maxChunkBytes int64) (*Spool, int64, error) {
	spool, err := Read(body, maxChunkBytes)
	if err != nil {
		return nil, 0, err
	}
	return spool, spool.Size(), nil
}

func PartPrefix(key string) string { return key + ".parts/" }

func PartKey(key string, offset int64) string {
	return PartPrefix(key) + fmt.Sprintf("%020d", offset)
}

func partOffset(key, partKey string) (int64, bool) {
	value := strings.TrimPrefix(partKey, PartPrefix(key))
	if value == partKey || len(value) != 20 {
		return 0, false
	}
	offset, err := strconv.ParseInt(value, 10, 64)
	return offset, err == nil && offset >= 0
}

// ReadUpload assembles immutable upload chunks once at completion.
// Legacy cumulative upload objects are accepted as the initial prefix so
// in-flight sessions created before this representation can still complete.
func ReadUpload(ctx context.Context, objects objectstore.Store, key string, expectedSize int64) (*Spool, error) {
	spool, err := New()
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*Spool, error) {
		_ = spool.Close()
		return nil, err
	}
	assembled := int64(0)
	if reader, size, openErr := objects.Open(ctx, key); openErr == nil {
		written, copyErr := spool.Append(reader, size)
		closeErr := reader.Close()
		if copyErr != nil || closeErr != nil {
			return fail(errors.Join(copyErr, closeErr))
		}
		if written != size {
			return fail(errors.New("stored upload prefix size changed while spooling"))
		}
		assembled = size
	} else if !errors.Is(openErr, objectstore.ErrNotFound) {
		return fail(openErr)
	}
	parts, err := objects.List(ctx, PartPrefix(key))
	if err != nil {
		return fail(err)
	}
	sort.Strings(parts)
	for _, partKey := range parts {
		offset, ok := partOffset(key, partKey)
		if !ok || offset != assembled {
			return fail(errors.New("stored upload chunks are not contiguous"))
		}
		reader, size, err := objects.Open(ctx, partKey)
		if err != nil {
			return fail(err)
		}
		written, copyErr := spool.Append(reader, size)
		closeErr := reader.Close()
		if copyErr != nil || closeErr != nil {
			return fail(errors.Join(copyErr, closeErr))
		}
		if written != size {
			return fail(errors.New("stored upload chunk size changed while spooling"))
		}
		assembled += size
	}
	if assembled != expectedSize {
		return fail(fmt.Errorf("stored upload size does not match offset: got %d want %d", assembled, expectedSize))
	}
	if err := spool.Rewind(); err != nil {
		return fail(err)
	}
	return spool, nil
}

func DeleteUpload(ctx context.Context, objects objectstore.Store, key string) error {
	parts, err := objects.List(ctx, PartPrefix(key))
	deleteErr := err
	if err == nil {
		for _, partKey := range parts {
			deleteErr = errors.Join(deleteErr, objects.Delete(ctx, partKey))
		}
	}
	return errors.Join(deleteErr, objects.Delete(ctx, key))
}

func ReadObject(ctx context.Context, objects objectstore.Store, key string) (*Spool, error) {
	reader, size, err := objects.Open(ctx, key)
	if err != nil {
		return nil, err
	}
	spool, copyErr := Read(reader, size)
	closeErr := reader.Close()
	if copyErr != nil {
		return nil, copyErr
	}
	if closeErr != nil {
		_ = spool.Close()
		return nil, closeErr
	}
	if spool.Size() != size {
		_ = spool.Close()
		return nil, errors.New("stored upload size changed while spooling")
	}
	return spool, nil
}
