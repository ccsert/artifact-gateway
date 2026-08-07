package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"hash"
	"io"
	"os"
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

func spoolObjectAppend(ctx context.Context, objects OCIObjectStore, key string, existingSize int64, body io.Reader, maxChunkBytes int64) (*uploadSpool, int64, error) {
	spool, err := newUploadSpool()
	if err != nil {
		return nil, 0, err
	}
	fail := func(err error) (*uploadSpool, int64, error) {
		_ = spool.Close()
		return nil, 0, err
	}
	if existingSize > 0 {
		reader, size, err := objects.Open(ctx, key)
		if err != nil {
			return fail(err)
		}
		written, copyErr := spool.Append(reader, existingSize)
		closeErr := reader.Close()
		if copyErr != nil {
			return fail(copyErr)
		}
		if closeErr != nil {
			return fail(closeErr)
		}
		if size != existingSize || written != existingSize {
			return fail(errors.New("stored upload size does not match offset"))
		}
	}
	chunkSize, err := spool.Append(body, maxChunkBytes)
	if err != nil {
		return fail(err)
	}
	if err := spool.Rewind(); err != nil {
		return fail(err)
	}
	return spool, chunkSize, nil
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
