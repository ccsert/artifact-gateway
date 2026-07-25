package authorization

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"
)

const oidcJWKSCacheTTL = 5 * time.Minute

// OIDCConfig supplies the immutable issuer trust boundary. Only RS256 is
// accepted so the configured JWKS cannot negotiate an unexpected algorithm.
type OIDCConfig struct {
	Issuer        string
	Audience      string
	JWKSURL       string
	AdminSubjects []string
}

type OIDCIdentity struct {
	Subject string
	Admin   bool
}

type OIDCValidator struct {
	config OIDCConfig
	client *http.Client

	mu      sync.Mutex
	keys    map[string]*rsa.PublicKey
	expires time.Time
}

func NewOIDCValidator(config OIDCConfig) *OIDCValidator {
	if config.Issuer == "" {
		return nil
	}
	admins := make([]string, 0, len(config.AdminSubjects))
	for _, subject := range config.AdminSubjects {
		if subject = strings.TrimSpace(subject); subject != "" {
			admins = append(admins, subject)
		}
	}
	config.AdminSubjects = admins
	return &OIDCValidator{config: config, client: &http.Client{Timeout: 5 * time.Second}}
}

func (v *OIDCValidator) Validate(ctx context.Context, token string) (OIDCIdentity, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return OIDCIdentity{}, false
	}
	var header struct {
		Algorithm string `json:"alg"`
		KeyID     string `json:"kid"`
	}
	var claims struct {
		Issuer   string          `json:"iss"`
		Subject  string          `json:"sub"`
		Audience json.RawMessage `json:"aud"`
		Expires  int64           `json:"exp"`
	}
	if !decodeJWTPart(parts[0], &header) || !decodeJWTPart(parts[1], &claims) || header.Algorithm != "RS256" || header.KeyID == "" || claims.Subject == "" || claims.Issuer != v.config.Issuer || claims.Expires == 0 || !time.Now().UTC().Before(time.Unix(claims.Expires, 0)) || !audienceContains(claims.Audience, v.config.Audience) {
		return OIDCIdentity{}, false
	}
	key, ok := v.key(ctx, header.KeyID)
	if !ok {
		return OIDCIdentity{}, false
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return OIDCIdentity{}, false
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature) != nil {
		return OIDCIdentity{}, false
	}
	identity := OIDCIdentity{Subject: claims.Subject}
	for _, subject := range v.config.AdminSubjects {
		if subject == identity.Subject {
			identity.Admin = true
			break
		}
	}
	return identity, true
}

func (v *OIDCValidator) key(ctx context.Context, keyID string) (*rsa.PublicKey, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if time.Now().UTC().Before(v.expires) {
		if key, ok := v.keys[keyID]; ok {
			return key, true
		}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, v.config.JWKSURL, nil)
	if err != nil {
		return nil, false
	}
	response, err := v.client.Do(request)
	if err != nil || response.StatusCode != http.StatusOK {
		if response != nil {
			_ = response.Body.Close()
		}
		return nil, false
	}
	defer func() { _ = response.Body.Close() }()
	var document struct {
		Keys []struct {
			KeyID string `json:"kid"`
			Type  string `json:"kty"`
			Use   string `json:"use"`
			N     string `json:"n"`
			E     string `json:"e"`
		} `json:"keys"`
	}
	if json.NewDecoder(response.Body).Decode(&document) != nil {
		return nil, false
	}
	keys := make(map[string]*rsa.PublicKey)
	for _, jwk := range document.Keys {
		if jwk.Type != "RSA" || (jwk.Use != "" && jwk.Use != "sig") || jwk.KeyID == "" {
			continue
		}
		modulus, err := base64.RawURLEncoding.DecodeString(jwk.N)
		if err != nil || len(modulus) == 0 {
			continue
		}
		exponent, err := base64.RawURLEncoding.DecodeString(jwk.E)
		if err != nil || len(exponent) == 0 || len(exponent) > 4 {
			continue
		}
		e := 0
		for _, b := range exponent {
			e = e<<8 | int(b)
		}
		if e < 3 || e%2 == 0 {
			continue
		}
		keys[jwk.KeyID] = &rsa.PublicKey{N: new(big.Int).SetBytes(modulus), E: e}
	}
	v.keys = keys
	v.expires = time.Now().UTC().Add(oidcJWKSCacheTTL)
	key, ok := v.keys[keyID]
	return key, ok
}

func decodeJWTPart(part string, target any) bool {
	decoded, err := base64.RawURLEncoding.DecodeString(part)
	return err == nil && json.Unmarshal(decoded, target) == nil
}

func audienceContains(raw json.RawMessage, expected string) bool {
	var audience string
	if json.Unmarshal(raw, &audience) == nil {
		return audience == expected
	}
	var audiences []string
	if json.Unmarshal(raw, &audiences) != nil {
		return false
	}
	for _, audience := range audiences {
		if audience == expected {
			return true
		}
	}
	return false
}
