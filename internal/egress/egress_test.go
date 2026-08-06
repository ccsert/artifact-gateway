package egress

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func testHooks(t *testing.T, ips map[string][]net.IP) Hooks {
	t.Helper()
	return Hooks{
		LookupIP: func(_ context.Context, network, host string) ([]net.IP, error) {
			if network != "ip" {
				t.Fatalf("lookup network = %q", network)
			}
			resolved, ok := ips[host]
			if !ok {
				return nil, errors.New("unknown host " + host)
			}
			return resolved, nil
		},
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("dial must not run in this test")
		},
		ProxyFromEnvironment: func(*http.Request) (*url.URL, error) { return nil, nil },
		AllowPrivateProxy:    true,
	}
}

func TestApplyEnvironmentFallsBackToPinnedDirect(t *testing.T) {
	hooks := testHooks(t, map[string][]net.IP{"upstream.example": {net.ParseIP("203.0.113.7")}})
	client, err := Apply(nil, nil, "https://upstream.example/repo", hooks)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok || transport.DialContext == nil || transport.Proxy != nil {
		t.Fatalf("expected pinned direct transport, got %#v", client.Transport)
	}
}

func TestApplyEnvironmentUsesProxyFromEnvironment(t *testing.T) {
	hooks := testHooks(t, nil)
	proxyURL, _ := url.Parse("http://proxy.example:8080")
	hooks.ProxyFromEnvironment = func(*http.Request) (*url.URL, error) { return proxyURL, nil }
	client, err := Apply(nil, nil, "https://upstream.example/repo", hooks)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok || transport.Proxy == nil || transport.DialContext != nil {
		t.Fatalf("expected environment proxy transport, got %#v", client.Transport)
	}
}

func TestApplyDirectRejectsPrivateUpstream(t *testing.T) {
	hooks := testHooks(t, map[string][]net.IP{"internal.example": {net.ParseIP("10.1.2.3")}})
	direct := &repository.EgressProxy{Mode: repository.EgressProxyModeDirect}
	_, err := Apply(nil, direct, "https://internal.example/repo", hooks)
	if err == nil || !strings.Contains(err.Error(), "private address") {
		t.Fatalf("err = %v", err)
	}
}

func TestApplyCustomHTTPBuildsProxyURL(t *testing.T) {
	t.Setenv(KeyEnv, strings.Repeat("ab", 32))
	password, err := EncryptPassword("s3cret")
	if err != nil {
		t.Fatalf("EncryptPassword: %v", err)
	}
	hooks := testHooks(t, map[string][]net.IP{"proxy.corp.example": {net.ParseIP("203.0.113.10")}})
	proxy := &repository.EgressProxy{
		Mode:     repository.EgressProxyModeCustom,
		Protocol: repository.EgressProxyProtocolHTTP,
		Host:     "proxy.corp.example",
		Port:     8080,
		Username: "gateway",
		Password: password,
	}
	client, err := Apply(nil, proxy, "https://upstream.example/repo", hooks)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok || transport.Proxy == nil || transport.DialContext != nil {
		t.Fatalf("expected HTTP proxy transport, got %#v", client.Transport)
	}
	resolved, err := transport.Proxy(&http.Request{URL: &url.URL{Scheme: "https", Host: "upstream.example"}})
	if err != nil {
		t.Fatalf("Proxy: %v", err)
	}
	if resolved.Host != "proxy.corp.example:8080" || resolved.User == nil {
		t.Fatalf("proxy URL = %v", resolved)
	}
	decodedPassword, _ := resolved.User.Password()
	if resolved.User.Username() != "gateway" || decodedPassword != "s3cret" {
		t.Fatalf("proxy credentials = %v", resolved.User)
	}
}

func TestApplyCustomSOCKS5InstallsDialer(t *testing.T) {
	hooks := testHooks(t, map[string][]net.IP{"proxy.corp.example": {net.ParseIP("203.0.113.10")}})
	proxy := &repository.EgressProxy{
		Mode:      repository.EgressProxyModeCustom,
		Protocol:  repository.EgressProxyProtocolSOCKS5,
		Host:      "proxy.corp.example",
		Port:      1080,
		RemoteDNS: true,
	}
	client, err := Apply(nil, proxy, "https://upstream.example/repo", hooks)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok || transport.DialContext == nil || transport.Proxy != nil {
		t.Fatalf("expected socks5 dial transport, got %#v", client.Transport)
	}
}

func TestApplyCustomSOCKS5LocalResolveGuardsTarget(t *testing.T) {
	hooks := testHooks(t, map[string][]net.IP{
		"proxy.corp.example": {net.ParseIP("203.0.113.10")},
		"upstream.example":   {net.ParseIP("203.0.113.7")},
	})
	proxy := &repository.EgressProxy{
		Mode:     repository.EgressProxyModeCustom,
		Protocol: repository.EgressProxyProtocolSOCKS5,
		Host:     "proxy.corp.example",
		Port:     1080,
	}
	client, err := Apply(nil, proxy, "https://upstream.example/repo", hooks)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	transport := client.Transport.(*http.Transport)
	if _, err := transport.DialContext(context.Background(), "tcp", "other.example:443"); err == nil || !strings.Contains(err.Error(), "dial target changed") {
		t.Fatalf("expected dial target guard, got %v", err)
	}
}

func TestApplyCustomNoProxyBypassesToDirect(t *testing.T) {
	hooks := testHooks(t, map[string][]net.IP{"upstream.internal": {net.ParseIP("203.0.113.7")}})
	proxy := &repository.EgressProxy{
		Mode:     repository.EgressProxyModeCustom,
		Protocol: repository.EgressProxyProtocolHTTP,
		Host:     "proxy.corp.example",
		Port:     8080,
		NoProxy:  []string{"*.internal"},
	}
	client, err := Apply(nil, proxy, "https://upstream.internal/repo", hooks)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok || transport.DialContext == nil || transport.Proxy != nil {
		t.Fatalf("expected noProxy bypass to pinned direct transport, got %#v", client.Transport)
	}
}

func TestApplyCustomRejectsPrivateProxyAddress(t *testing.T) {
	hooks := testHooks(t, map[string][]net.IP{"proxy.internal": {net.ParseIP("192.168.1.10")}})
	hooks.AllowPrivateProxy = false
	proxy := &repository.EgressProxy{
		Mode:     repository.EgressProxyModeCustom,
		Protocol: repository.EgressProxyProtocolHTTP,
		Host:     "proxy.internal",
		Port:     8080,
	}
	_, err := Apply(nil, proxy, "https://upstream.example/repo", hooks)
	if err == nil || !strings.Contains(err.Error(), "private address") {
		t.Fatalf("err = %v", err)
	}
}

func TestApplyRejectsTLSOverride(t *testing.T) {
	client := &http.Client{Transport: &http.Transport{DialTLSContext: func(context.Context, string, string) (net.Conn, error) { return nil, nil }}}
	_, err := Apply(client, nil, "https://upstream.example/repo", testHooks(t, nil))
	if err == nil || !strings.Contains(err.Error(), "must not override TLS dialing") {
		t.Fatalf("err = %v", err)
	}
}

func TestPasswordRoundTrip(t *testing.T) {
	t.Setenv(KeyEnv, strings.Repeat("cd", 32))
	encoded, err := EncryptPassword("pässwörd")
	if err != nil {
		t.Fatalf("EncryptPassword: %v", err)
	}
	decoded, err := DecryptPassword(encoded)
	if err != nil {
		t.Fatalf("DecryptPassword: %v", err)
	}
	if decoded != "pässwörd" {
		t.Fatalf("round trip = %q", decoded)
	}
	empty, err := EncryptPassword("")
	if err != nil || empty != "" {
		t.Fatalf("empty encrypt = %q, %v", empty, err)
	}
}

func TestDecryptPasswordRejectsWrongKey(t *testing.T) {
	t.Setenv(KeyEnv, strings.Repeat("ab", 32))
	encoded, err := EncryptPassword("secret")
	if err != nil {
		t.Fatalf("EncryptPassword: %v", err)
	}
	t.Setenv(KeyEnv, strings.Repeat("ef", 32))
	if _, err := DecryptPassword(encoded); err == nil {
		t.Fatal("expected decryption failure with a different key")
	}
}

func TestEncryptPasswordRequiresKey(t *testing.T) {
	if _, err := EncryptPassword("secret"); !errors.Is(err, ErrKeyNotConfigured) {
		t.Fatalf("err = %v", err)
	}
}
