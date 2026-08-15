package refaptsigner

import (
	"context"
	"crypto"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
)

type KeyOptions struct {
	Path    string
	Name    string
	Comment string
	Email   string
	RSABits int
}

func LoadOrCreateEntity(ctx context.Context, options KeyOptions) (*openpgp.Entity, error) {
	if !filepath.IsAbs(options.Path) || options.Name == "" || options.Email == "" || options.RSABits < 1024 || options.RSABits > 4096 {
		return nil, errors.New("reference APT signer key options are invalid")
	}
	if entity, err := LoadEntity(options.Path); err == nil {
		return entity, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	parent := filepath.Dir(options.Path)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return nil, fmt.Errorf("create APT signer key directory: %w", err)
	}
	if err := os.Chmod(parent, 0o700); err != nil {
		return nil, fmt.Errorf("restrict APT signer key directory: %w", err)
	}
	entity, err := openpgp.NewEntity(options.Name, options.Comment, options.Email, &packet.Config{RSABits: options.RSABits, DefaultHash: crypto.SHA256})
	if err != nil {
		return nil, fmt.Errorf("generate APT signer key: %w", err)
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	temporary, err := os.CreateTemp(parent, ".apt-release-key-*")
	if err != nil {
		return nil, err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	failed := true
	defer func() {
		if failed {
			_ = temporary.Close()
		}
	}()
	if err = temporary.Chmod(0o600); err != nil {
		return nil, err
	}
	if err = entity.SerializePrivate(temporary, &packet.Config{DefaultHash: crypto.SHA256}); err != nil {
		return nil, err
	}
	if err = temporary.Sync(); err != nil {
		return nil, err
	}
	if err = temporary.Close(); err != nil {
		return nil, err
	}
	failed = false
	if err = os.Link(temporaryPath, options.Path); err != nil {
		if errors.Is(err, os.ErrExist) {
			return LoadEntity(options.Path)
		}
		return nil, fmt.Errorf("install APT signer key: %w", err)
	}
	return entity, nil
}

// LoadEntity opens an operator-custodied private key without mutating its
// permissions, which allows a signer deployment to mount the key read-only.
func LoadEntity(path string) (*openpgp.Entity, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("APT signer key file permissions are too broad")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	entities, err := openpgp.ReadKeyRing(file)
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("read APT signer key: %w", err)
	}
	if err = file.Close(); err != nil {
		return nil, fmt.Errorf("close APT signer key: %w", err)
	}
	if len(entities) != 1 || entities[0].PrivateKey == nil {
		return nil, errors.New("APT signer key file must contain exactly one private key")
	}
	return entities[0], nil
}
