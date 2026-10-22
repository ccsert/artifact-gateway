package refaptsigner

import (
	"bytes"
	"context"
	"crypto"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/clearsign"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
	"github.com/ccsert/artifact-gateway/internal/aptpublication"
)

type Service struct {
	entity      *openpgp.Entity
	identity    string
	fingerprint string
	algorithm   string
	publicKey   []byte
	mu          sync.Mutex
}

func NewService(entity *openpgp.Entity, identity string) (*Service, error) {
	if entity == nil || entity.PrimaryKey == nil || entity.PrivateKey == nil || identity == "" {
		return nil, errors.New("APT signing entity is invalid")
	}
	bits, err := entity.PrimaryKey.BitLength()
	if err != nil {
		return nil, err
	}
	var public bytes.Buffer
	armored, err := armor.Encode(&public, openpgp.PublicKeyType, nil)
	if err != nil {
		return nil, err
	}
	if err = entity.Serialize(armored); err != nil {
		_ = armored.Close()
		return nil, err
	}
	if err = armored.Close(); err != nil {
		return nil, err
	}
	return &Service{
		entity: entity, identity: identity,
		fingerprint: stringsUpperHex(entity.PrimaryKey.Fingerprint[:]),
		algorithm:   fmt.Sprintf("rsa%d-sha256", bits), publicKey: public.Bytes(),
	}, nil
}

func stringsUpperHex(value []byte) string {
	return string(bytes.ToUpper([]byte(hex.EncodeToString(value))))
}

func (s *Service) SignRelease(ctx context.Context, release []byte) (aptpublication.SignReleaseResult, error) {
	if s == nil || len(release) == 0 {
		return aptpublication.SignReleaseResult{}, errors.New("APT Release is empty")
	}
	if err := ctx.Err(); err != nil {
		return aptpublication.SignReleaseResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	config := &packet.Config{DefaultHash: crypto.SHA256}
	var inRelease bytes.Buffer
	cleartext, err := clearsign.Encode(&inRelease, s.entity.PrivateKey, config)
	if err != nil {
		return aptpublication.SignReleaseResult{}, err
	}
	if _, err = cleartext.Write(release); err != nil {
		_ = cleartext.Close()
		return aptpublication.SignReleaseResult{}, err
	}
	if err = cleartext.Close(); err != nil {
		return aptpublication.SignReleaseResult{}, err
	}
	inReleaseBytes := inRelease.Bytes()
	if s.entity.PrivateKey.Version == 4 {
		inReleaseBytes, err = withV4InReleaseChecksum(inReleaseBytes)
		if err != nil {
			return aptpublication.SignReleaseResult{}, err
		}
	}
	var detached bytes.Buffer
	if err = openpgp.ArmoredDetachSign(&detached, s.entity, bytes.NewReader(release), config); err != nil {
		return aptpublication.SignReleaseResult{}, err
	}
	return aptpublication.SignReleaseResult{
		InRelease: inReleaseBytes, Detached: detached.Bytes(), SignerIdentity: s.identity,
		KeyFingerprint: s.fingerprint, Algorithm: s.algorithm,
	}, nil
}

// withV4InReleaseChecksum keeps the generated cleartext and signature packets
// intact. Legacy GnuPG requires CRC24 when signature armor has no base64 padding.
func withV4InReleaseChecksum(inRelease []byte) ([]byte, error) {
	block, rest := clearsign.Decode(inRelease)
	if block == nil || len(rest) != 0 {
		return nil, errors.New("invalid generated APT clear signature")
	}
	start := bytes.LastIndex(inRelease, []byte("\n-----BEGIN PGP SIGNATURE-----\n"))
	if start < 0 {
		return nil, errors.New("generated APT signature armor is missing")
	}
	var output bytes.Buffer
	output.Write(inRelease[:start+1])
	armored, err := armor.EncodeWithChecksumOption(&output, block.ArmoredSignature.Type, block.ArmoredSignature.Header, true)
	if err != nil {
		return nil, err
	}
	if _, err = io.Copy(armored, block.ArmoredSignature.Body); err != nil {
		_ = armored.Close()
		return nil, err
	}
	if err = armored.Close(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func (s *Service) PublicKey() []byte {
	if s == nil {
		return nil
	}
	return append([]byte(nil), s.publicKey...)
}

func (s *Service) Fingerprint() string {
	if s == nil {
		return ""
	}
	return s.fingerprint
}
