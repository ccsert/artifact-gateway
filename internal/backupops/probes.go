package backupops

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"regexp"
	"strings"
)

// ReadCheck is a fixed, bounded, read-only recovery assertion. Tokens are
// supplied separately in the private target spec; bundles never mint them.
type ReadCheck struct {
	Kind       string `json:"kind"`
	Format     string `json:"format"`
	Path       string `json:"path"`
	Credential string `json:"credential"`
	Principal  string `json:"principal,omitempty"`
	Status     int    `json:"status"`
	Size       *int64 `json:"size,omitempty"`
	SHA256     string `json:"sha256,omitempty"`
}

func validateReadChecks(spec TargetSpec) error {
	if len(spec.ReadChecks) > 64 {
		return errors.New("too many read checks")
	}
	for _, check := range spec.ReadChecks {
		if (check.Format != "oci" || !strings.HasPrefix(check.Path, "/v2/")) && (check.Format != "raw" || !strings.HasPrefix(check.Path, "/raw/")) {
			return errors.New("unsupported protocol probe")
		}
		if len(check.Path) > 8192 || strings.ContainsAny(check.Path, "\r\n#") {
			return errors.New("invalid probe path")
		}
		if check.Kind != "protocol" && check.Kind != "grant-allow" && check.Kind != "grant-deny" {
			return errors.New("unsupported semantic probe")
		}
		if check.Kind == "grant-deny" {
			if check.Credential != "denied" || check.Principal == "" || (check.Status != 401 && check.Status != 403) {
				return errors.New("invalid denial assertion")
			}
		} else if check.Status != 200 || check.Size == nil || *check.Size < 0 || *check.Size == math.MaxInt64 || !regexp.MustCompile(`^sha256:[a-f0-9]{64}$`).MatchString(check.SHA256) {
			return errors.New("invalid byte assertion")
		}
		switch check.Credential {
		case "admin", "none":
		case "reader":
			if spec.ReaderToken == "" {
				return errors.New("reader credential absent")
			}
		case "denied":
			if spec.DeniedToken == "" {
				return errors.New("denied credential absent")
			}
		default:
			return errors.New("unsupported probe credential")
		}
		if check.Kind == "grant-allow" && (check.Credential != "reader" || check.Principal == "") {
			return errors.New("invalid grant assertion")
		}
	}
	for key := range spec.RuntimeEnvironment {
		if !allowedRuntimeKey(key) {
			return errors.New("unsupported private runtime setting")
		}
	}
	return nil
}
func runReadChecks(ctx context.Context, endpoint string, spec TargetSpec, r *TransferReport) error {
	client := &http.Client{Timeout: 30e9, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	protocol := false
	allow := false
	deny := false
	for _, check := range spec.ReadChecks {
		token := ""
		switch check.Credential {
		case "admin":
			token = spec.AdminToken
		case "reader":
			token = spec.ReaderToken
		case "denied":
			token = spec.DeniedToken
		}
		request := func(path string) (*http.Response, error) {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+path, nil)
			if err != nil {
				return nil, err
			}
			if token != "" {
				req.Header.Set("Authorization", "Bearer "+token)
			}
			return client.Do(req)
		}
		if check.Kind == "grant-allow" || check.Kind == "grant-deny" {
			response, err := request("/api/v2/identity")
			if err != nil {
				return errors.New("probe identity unavailable")
			}
			var identity struct {
				Actor string `json:"actor"`
			}
			decodeErr := json.NewDecoder(io.LimitReader(response.Body, 16384)).Decode(&identity)
			_ = response.Body.Close()
			if response.StatusCode != 200 || decodeErr != nil || identity.Actor != check.Principal {
				return errors.New("probe identity does not match")
			}
		}
		response, err := request(check.Path)
		if err != nil {
			return errors.New("probe response unavailable")
		}
		if response.StatusCode != check.Status {
			_ = response.Body.Close()
			return errors.New("probe status differs")
		}
		if check.Kind == "grant-deny" {
			_, err = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
			_ = response.Body.Close()
			if err != nil {
				return errors.New("probe read failed")
			}
			deny = true
			continue
		}
		digest, size, readErr := hashReader(io.LimitReader(response.Body, *check.Size+1))
		_ = response.Body.Close()
		if readErr != nil || size != *check.Size || digest != check.SHA256 {
			return errors.New("probe bytes differ")
		}
		protocol = true
		if check.Kind == "grant-allow" {
			allow = true
		}
	}
	if protocol {
		r.Protocol = "verified"
	}
	if allow && deny {
		r.Authorization = "verified"
	}
	return nil
}
