package emailnotification

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/mail"
	"path/filepath"
	"strings"
)

var ErrInvalidConfig = errors.New("email relay configuration is invalid")

// Load is offline and returns only fixed errors; SMTP addresses and secret paths
// never become diagnostics. An absent file leaves the channel disabled.
func Load(path string) (Config, error) {
	if path == "" {
		return Config{}, nil
	}
	var cfg Config
	if !filepath.IsAbs(path) || readJSONFile(path, &cfg, false) != nil {
		return Config{}, ErrInvalidConfig
	}
	if !cfg.Enabled {
		return Config{}, nil
	}
	if cfg.Validate() != nil {
		return Config{}, ErrInvalidConfig
	}
	if cfg.CAFile != "" {
		if _, err := relayRoots(cfg.CAFile); err != nil {
			return Config{}, ErrInvalidConfig
		}
	}
	if cfg.AuthFile != "" {
		if _, err := readAuth(cfg.AuthFile); err != nil {
			return Config{}, ErrInvalidConfig
		}
	}
	return cfg, nil
}
func (c Config) Validate() error {
	if !c.Enabled {
		return nil
	}
	if c.Host == "" || len(c.Host) > 253 || strings.ContainsAny(c.Host, " /:@\\\r\n\x00") || c.Port < 1 || c.Port > 65535 || (c.Mode != "implicit_tls" && c.Mode != "starttls_required") || ValidAddress(c.From) != nil || len(c.ApprovedIPs) == 0 || len(c.ApprovedIPs) > 16 {
		return ErrInvalidConfig
	}
	for _, raw := range c.ApprovedIPs {
		if forbiddenIP(net.ParseIP(raw)) {
			return ErrInvalidConfig
		}
	}
	for _, path := range []string{c.CAFile, c.AuthFile} {
		if path != "" && !filepath.IsAbs(path) {
			return ErrInvalidConfig
		}
	}
	if _, err := ConsoleURL(c.ConsoleOrigin); err != nil {
		return ErrInvalidConfig
	}
	return nil
}
func ValidAddress(raw string) error {
	if len(raw) > 254 || strings.ContainsAny(raw, "\r\n\x00") {
		return ErrInvalidMessage
	}
	for _, r := range raw {
		if r < 33 || r > 126 {
			return ErrInvalidMessage
		}
	}
	parsed, err := mail.ParseAddress(raw)
	if err != nil || parsed.Name != "" || parsed.Address != raw || !strings.Contains(raw, "@") {
		return ErrInvalidMessage
	}
	return nil
}
func forbiddenIP(ip net.IP) bool {
	return ip == nil || ip.Equal(net.ParseIP("fd00:ec2::254")) || !ip.IsGlobalUnicast() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified()
}

type relayAuth struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func readAuth(path string) (relayAuth, error) {
	var a relayAuth
	if readJSONFile(path, &a, true) != nil || a.Username == "" || a.Password == "" || len(a.Username) > 256 || len(a.Password) > 1024 || strings.ContainsAny(a.Username+a.Password, "\r\n\x00") {
		return relayAuth{}, ErrInvalidConfig
	}
	return a, nil
}
func readJSONFile(path string, out any, private bool) error {
	file, err := openRegularSetting(path, 16<<10, private)
	if err != nil {
		return ErrInvalidConfig
	}
	defer func() { _ = file.Close() }()
	d := json.NewDecoder(io.LimitReader(file, 16<<10))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil || d.Decode(&struct{}{}) != io.EOF {
		return ErrInvalidConfig
	}
	return nil
}
