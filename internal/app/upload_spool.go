package app

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
)

var errUploadTooLarge = errors.New("upload exceeds size limit")

const uploadCopyBufferSize = 128 << 10

type uploadSpool struct {
	file   *os.File
	hasher hash.Hash
	size   int64
}

func newUploadSpool() (*uploadSpool, error) {
	file, err := os.CreateTemp("", "artifact-gateway-upload-*")
	if err != nil {
		return nil, err
	}
	return &uploadSpool{file: file, hasher: sha256.New()}, nil
}

func spoolUpload(reader io.Reader, maxBytes int64) (*uploadSpool, error) {
	spool, err := newUploadSpool()
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

func (s *uploadSpool) Append(reader io.Reader, maxBytes int64) (int64, error) {
	if maxBytes < 0 {
		return 0, errUploadTooLarge
	}
	limit := maxBytes
	if maxBytes < int64(^uint64(0)>>1) {
		limit++
	}
	limited := &io.LimitedReader{R: reader, N: limit}
	written, err := io.CopyBuffer(io.MultiWriter(s.file, s.hasher), limited, make([]byte, uploadCopyBufferSize))
	s.size += written
	if err != nil {
		return written, err
	}
	if written > maxBytes {
		return written, errUploadTooLarge
	}
	return written, nil
}

func (s *uploadSpool) Rewind() error {
	_, err := s.file.Seek(0, io.SeekStart)
	return err
}

func (s *uploadSpool) Reader() io.Reader { return s.file }

func (s *uploadSpool) Size() int64 { return s.size }

func (s *uploadSpool) Digest() string {
	return "sha256:" + hex.EncodeToString(s.hasher.Sum(nil))
}

func (s *uploadSpool) DigestBytes() []byte {
	return append([]byte(nil), s.hasher.Sum(nil)...)
}

func (s *uploadSpool) Close() error {
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

func spoolObjectAppend(_ context.Context, _ OCIObjectStore, _ string, _ int64, body io.Reader, maxChunkBytes int64) (*uploadSpool, int64, error) {
	spool, err := spoolUpload(body, maxChunkBytes)
	if err != nil {
		return nil, 0, err
	}
	return spool, spool.Size(), nil
}

func uploadPartPrefix(key string) string { return key + ".parts/" }

func uploadPartKey(key string, offset int64) string {
	return uploadPartPrefix(key) + fmt.Sprintf("%020d", offset)
}

func uploadPartOffset(key, partKey string) (int64, bool) {
	value := strings.TrimPrefix(partKey, uploadPartPrefix(key))
	if value == partKey || len(value) != 20 {
		return 0, false
	}
	offset, err := strconv.ParseInt(value, 10, 64)
	return offset, err == nil && offset >= 0
}

// spoolStoredUpload assembles immutable upload chunks once at completion.
// Legacy cumulative upload objects are accepted as the initial prefix so
// in-flight sessions created before this representation can still complete.
func spoolStoredUpload(ctx context.Context, objects OCIObjectStore, key string, expectedSize int64) (*uploadSpool, error) {
	spool, err := newUploadSpool()
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*uploadSpool, error) {
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
	} else if !errors.Is(openErr, errOCICacheMiss) {
		return fail(openErr)
	}
	parts, err := objects.List(ctx, uploadPartPrefix(key))
	if err != nil {
		return fail(err)
	}
	sort.Strings(parts)
	for _, partKey := range parts {
		offset, ok := uploadPartOffset(key, partKey)
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

func deleteUploadObjects(ctx context.Context, objects OCIObjectStore, key string) error {
	parts, err := objects.List(ctx, uploadPartPrefix(key))
	deleteErr := err
	if err == nil {
		for _, partKey := range parts {
			deleteErr = errors.Join(deleteErr, objects.Delete(ctx, partKey))
		}
	}
	return errors.Join(deleteErr, objects.Delete(ctx, key))
}

func spoolStoredObject(ctx context.Context, objects OCIObjectStore, key string) (*uploadSpool, error) {
	reader, size, err := objects.Open(ctx, key)
	if err != nil {
		return nil, err
	}
	spool, copyErr := spoolUpload(reader, size)
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
