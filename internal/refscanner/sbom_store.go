package refscanner

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type sbomStore struct {
	dir      string
	baseURL  string
	maxBytes int64
	mutex    sync.Mutex
}

func (s *sbomStore) put(content []byte) (string, error) {
	if int64(len(content)) > s.maxBytes {
		return "", errors.New("SBOM exceeds the store capacity")
	}
	digestBytes := sha256.Sum256(content)
	digest := "sha256:" + hex.EncodeToString(digestBytes[:])
	name := s.filename(digest)
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if existing, err := os.ReadFile(name); err == nil && string(existing) == string(content) {
		_ = os.Chtimes(name, time.Now(), time.Now())
		return s.baseURL + "/v1/sboms/" + digest, nil
	}
	if err := s.checkCapacity(int64(len(content)), name); err != nil {
		return "", err
	}
	temporary, err := os.CreateTemp(s.dir, ".sbom-")
	if err != nil {
		return "", err
	}
	temporaryName := temporary.Name()
	defer func() { _ = os.Remove(temporaryName) }()
	if err = temporary.Chmod(0o600); err == nil {
		_, err = temporary.Write(content)
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return "", err
	}
	if err = os.Rename(temporaryName, name); err != nil {
		return "", err
	}
	return s.baseURL + "/v1/sboms/" + digest, nil
}

func (s *sbomStore) get(digest string) ([]byte, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	content, err := os.ReadFile(s.filename(digest))
	if err != nil {
		return nil, err
	}
	actual := sha256.Sum256(content)
	if "sha256:"+hex.EncodeToString(actual[:]) != digest {
		return nil, errors.New("stored SBOM integrity check failed")
	}
	_ = os.Chtimes(s.filename(digest), time.Now(), time.Now())
	return content, nil
}

func (s *sbomStore) filename(digest string) string {
	return filepath.Join(s.dir, digest[len("sha256:"):]+".cdx.json")
}

func (s *sbomStore) checkCapacity(required int64, target string) error {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return err
	}
	var total int64
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		name := filepath.Join(s.dir, entry.Name())
		if name == target {
			continue
		}
		total += info.Size()
	}
	if total+required > s.maxBytes {
		return errors.New("SBOM store capacity is exhausted")
	}
	return nil
}
