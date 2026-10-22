package refaptsigner

import (
	"bytes"
	"context"
	"crypto"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"testing"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/clearsign"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
)

func TestSignReleaseIncludesArmorChecksumsForLegacyGPGV(t *testing.T) {
	entity, err := openpgp.NewEntity("APT compatibility", "synthetic test key", "apt@example.test", &packet.Config{RSABits: 2048, DefaultHash: crypto.SHA256})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(entity, "APT compatibility")
	if err != nil {
		t.Fatal(err)
	}
	release := []byte("Suite: stable\nAcquire-By-Hash: yes\nSHA256:\n")
	signed, err := service.SignRelease(context.Background(), release)
	if err != nil {
		t.Fatal(err)
	}
	block, rest := clearsign.Decode(signed.InRelease)
	if block == nil || len(rest) != 0 {
		t.Fatal("invalid generated InRelease")
	}
	if !hasArmorChecksum(signed.InRelease) {
		t.Fatal("InRelease signature omits CRC24 required by legacy gpgv for unpadded signature packets")
	}
	raw, err := io.ReadAll(block.ArmoredSignature.Body)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := packet.Read(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	signature, ok := parsed.(*packet.Signature)
	if !ok || signature.Version != 4 || signature.PubKeyAlgo != packet.PubKeyAlgoRSA || signature.Hash != crypto.SHA256 || signature.IssuerKeyId == nil || *signature.IssuerKeyId != entity.PrimaryKey.KeyId || !bytes.Equal(signature.IssuerFingerprint, entity.PrimaryKey.Fingerprint) {
		t.Fatalf("signature identity or algorithm changed: %#v", signature)
	}
	if _, err = openpgp.CheckDetachedSignature(openpgp.EntityList{entity}, bytes.NewReader(block.Bytes), bytes.NewReader(raw), nil); err != nil {
		t.Fatalf("verify InRelease: %v", err)
	}
	for name, data := range map[string][]byte{"Release.gpg": signed.Detached, "public key": service.PublicKey()} {
		if !hasArmorChecksum(data) {
			t.Fatalf("%s omits its existing CRC24", name)
		}
		armored, err := armor.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = io.ReadAll(armored.Body); err != nil {
			t.Fatalf("%s checksum: %v", name, err)
		}
	}
	if _, err = openpgp.CheckArmoredDetachedSignature(openpgp.EntityList{entity}, bytes.NewReader(release), bytes.NewReader(signed.Detached), nil); err != nil {
		t.Fatalf("verify Release.gpg: %v", err)
	}
}

func TestV4InReleaseChecksumPreservesSignedContent(t *testing.T) {
	entity, err := openpgp.NewEntity("APT content preservation", "", "apt@example.test", &packet.Config{RSABits: 2048, DefaultHash: crypto.SHA256})
	if err != nil {
		t.Fatal(err)
	}
	for name, release := range map[string][]byte{
		"normal":                   []byte("Suite: stable\nSHA256:\n"),
		"no final newline":         []byte("Suite: stable"),
		"dash and armor-like text": []byte("Origin: APT\n-- dash\n-----BEGIN PGP SIGNATURE-----\n=AAAA\n\n"),
		"CRLF and trailing spaces": []byte("Suite: stable \t\r\nSHA256:\r\n"),
	} {
		t.Run(name, func(t *testing.T) {
			var original bytes.Buffer
			w, err := clearsign.Encode(&original, entity.PrivateKey, &packet.Config{DefaultHash: crypto.SHA256})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = w.Write(release); err != nil {
				t.Fatal(err)
			}
			if err = w.Close(); err != nil {
				t.Fatal(err)
			}
			encoded, err := withV4InReleaseChecksum(original.Bytes())
			if err != nil {
				t.Fatal(err)
			}
			before, _ := clearsign.Decode(original.Bytes())
			after, rest := clearsign.Decode(encoded)
			if before == nil || after == nil || len(rest) != 0 {
				t.Fatal("clear signature failed to decode")
			}
			rawBefore, err := io.ReadAll(before.ArmoredSignature.Body)
			if err != nil {
				t.Fatal(err)
			}
			rawAfter, err := io.ReadAll(after.ArmoredSignature.Body)
			if err != nil {
				t.Fatal(err)
			}
			prefix, _, found := bytes.Cut(original.Bytes(), []byte("\n-----BEGIN PGP SIGNATURE-----\n"))
			if !found || !bytes.Equal(original.Bytes()[:len(prefix)+1], encoded[:len(prefix)+1]) || !bytes.Equal(before.Bytes, after.Bytes) || !bytes.Equal(rawBefore, rawAfter) {
				t.Fatal("checksum encoding changed cleartext or original signature packet")
			}
			if !hasArmorChecksum(encoded) {
				t.Fatal("encoded signature omits CRC24")
			}
			if _, err = openpgp.CheckDetachedSignature(openpgp.EntityList{entity}, bytes.NewReader(after.Bytes), bytes.NewReader(rawAfter), nil); err != nil {
				t.Fatalf("signature no longer verifies: %v", err)
			}
		})
	}
}

func TestSignReleasePreservesV6InReleaseArmor(t *testing.T) {
	entity, err := openpgp.NewEntity("APT v6 output", "synthetic test key", "apt@example.test", &packet.Config{RSABits: 2048, DefaultHash: crypto.SHA256, V6Keys: true})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(entity, "APT v6 output")
	if err != nil {
		t.Fatal(err)
	}
	signed, err := service.SignRelease(context.Background(), []byte("Suite: stable\nSHA256:\n"))
	if err != nil {
		t.Fatal(err)
	}
	if hasArmorChecksum(signed.InRelease) {
		t.Fatal("v6 InRelease must keep the library's checksum-free armor")
	}
	block, rest := clearsign.Decode(signed.InRelease)
	if block == nil || len(rest) != 0 {
		t.Fatal("invalid generated v6 InRelease")
	}
	raw, err := io.ReadAll(block.ArmoredSignature.Body)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := packet.Read(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	signature, ok := parsed.(*packet.Signature)
	if !ok || signature.Version != 6 {
		t.Fatalf("v6 signature changed: %#v", signature)
	}
	if _, err = openpgp.CheckDetachedSignature(openpgp.EntityList{entity}, bytes.NewReader(block.Bytes), bytes.NewReader(raw), nil); err != nil {
		t.Fatalf("verify v6 InRelease: %v", err)
	}
}

func TestV4InReleaseChecksumPreservesUnpaddedLegacyFixture(t *testing.T) {
	// Produced by the real 4c37180a Service.SignRelease with a disposable RSA2048
	// key. Fixed Debian bookworm gpgv exits 2 for this unpadded, checksum-free
	// InRelease and exits 0 after only CRC24 is added. No private key is retained.
	read := func(name string) []byte {
		t.Helper()
		data, err := os.ReadFile("testdata/legacy-gpgv-unpadded." + name)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	original, release, publicKey := read("InRelease"), read("Release"), read("asc")
	keys, err := openpgp.ReadArmoredKeyRing(bytes.NewReader(publicKey))
	if err != nil || len(keys) != 1 || keys[0].PrivateKey != nil {
		t.Fatalf("invalid public fixture key: %v", err)
	}
	if hex.EncodeToString(keys[0].PrimaryKey.Fingerprint) != "159a4f4bdd0539402ce603453cd4f15331318ecc" {
		t.Fatal("fixture signing identity changed")
	}
	before, rest := clearsign.Decode(original)
	if before == nil || len(rest) != 0 || !bytes.Equal(before.Plaintext, release) || hasArmorChecksum(original) {
		t.Fatal("fixture no longer contains the original checksum-free Release")
	}
	rawBefore, err := io.ReadAll(before.ArmoredSignature.Body)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(rawBefore)
	if len(rawBefore) != 363 || len(rawBefore)%3 != 0 || hex.EncodeToString(sum[:]) != "a676ccdcc8a5d9201c11f07d234dff710275d67c97c5dda589b25b41473b7266" {
		t.Fatal("fixture no longer has the fixed 363-byte unpadded signature")
	}
	prefix, signatureArmor, found := bytes.Cut(original, []byte("\n-----BEGIN PGP SIGNATURE-----\n"))
	if !found || bytes.ContainsRune(signatureArmor, '=') {
		t.Fatal("fixture signature unexpectedly has base64 padding or CRC24")
	}
	encoded, err := withV4InReleaseChecksum(original)
	if err != nil {
		t.Fatal(err)
	}
	after, rest := clearsign.Decode(encoded)
	if after == nil || len(rest) != 0 || !hasArmorChecksum(encoded) {
		t.Fatal("fixture signature was not encoded with CRC24")
	}
	rawAfter, err := io.ReadAll(after.ArmoredSignature.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(original[:len(prefix)+1], encoded[:len(prefix)+1]) || !bytes.Equal(before.Bytes, after.Bytes) || !bytes.Equal(rawBefore, rawAfter) {
		t.Fatal("CRC24 changed the fixture cleartext, canonical bytes or signature packet")
	}
	if _, err = openpgp.CheckDetachedSignature(keys, bytes.NewReader(after.Bytes), bytes.NewReader(rawAfter), nil); err != nil {
		t.Fatalf("fixture signature no longer verifies with its original public key: %v", err)
	}
}

func hasArmorChecksum(data []byte) bool {
	if start := bytes.LastIndex(data, []byte("\n-----BEGIN PGP SIGNATURE-----\n")); start >= 0 {
		data = data[start:]
	}
	for _, line := range bytes.Split(data, []byte("\n")) {
		if len(line) == 5 && line[0] == '=' {
			return true
		}
	}
	return false
}
