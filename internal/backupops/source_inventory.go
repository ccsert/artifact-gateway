package backupops

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"

	"github.com/artifact-gateway/artifact-gateway/internal/backupmanifest"
	"github.com/artifact-gateway/artifact-gateway/internal/opsjson"
)

// Re-read inventory and complete bytes inside the observed stopped interval.
// A matching LIST alone cannot detect a same-size overwrite after export.
func verifySourceObjects(ctx context.Context, source FrozenSource, directory string, observe func(string)) error {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	index, err := root.Open("objects.jsonl")
	if err != nil {
		return err
	}
	defer func() { _ = index.Close() }()
	scanner := bufio.NewScanner(io.LimitReader(index, 64<<20+1))
	scanner.Buffer(make([]byte, 4096), 64<<10)
	err = source.WalkObjects(ctx, func(key string, size int64) error {
		observe(key)
		var item backupmanifest.Object
		if !scanner.Scan() || opsjson.Decode(scanner.Bytes(), &item) != nil || item.Key != key || item.File.Size == nil || *item.File.Size != size {
			return errors.New("inventory changed")
		}
		body, n, e := source.Open(ctx, key)
		if e != nil {
			return e
		}
		sum, count, e := hashReader(io.LimitReader(body, size+1))
		closeErr := body.Close()
		if e != nil || closeErr != nil || n != size || count != size || sum != item.File.SHA256 {
			return errors.New("source bytes changed")
		}
		return nil
	})
	if err != nil || scanner.Scan() || scanner.Err() != nil {
		return errors.New("inventory changed")
	}
	if _, err = index.Seek(0, io.SeekStart); err != nil {
		return err
	}
	scanner = bufio.NewScanner(io.LimitReader(index, 64<<20+1))
	scanner.Buffer(make([]byte, 4096), 64<<10)
	var current backupmanifest.Object
	previous := ""
	return source.WalkReferences(ctx, func(key string) error {
		observe(key)
		if key == "" || key < previous {
			return errors.New("references unordered")
		}
		previous = key
		for current.Key < key {
			if !scanner.Scan() || opsjson.Decode(scanner.Bytes(), &current) != nil {
				return errors.New("referenced object absent")
			}
		}
		if current.Key != key {
			return errors.New("referenced object absent")
		}
		return nil
	})
}
