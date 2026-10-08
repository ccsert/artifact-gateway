package app

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

var proxyRedirectModes = []string{
	"default", "environment-no-proxy", "direct", "custom-no-proxy",
	"environment-connect", "custom-http", "socks-local", "socks-remote",
}

func configureProxyRedirectMode(t *testing.T, fixture *proxyRedirectFixture, mode string) *repository.EgressProxy {
	t.Helper()
	switch mode {
	case "default":
		return nil
	case "environment-no-proxy":
		return &repository.EgressProxy{Mode: repository.EgressProxyModeEnvironment}
	case "direct":
		return &repository.EgressProxy{Mode: repository.EgressProxyModeDirect}
	case "custom-no-proxy":
		return &repository.EgressProxy{Mode: repository.EgressProxyModeCustom, Protocol: repository.EgressProxyProtocolHTTP,
			Host: redirectProxy, Port: 8080, NoProxy: []string{"*.redirect-test.example"}}
	case "environment-connect", "custom-http":
		address := newProxyRedirectCONNECTProxy(t, fixture)
		// The separate trusted hook can reach only this test's proxy listener.
		// Production DefaultHooks leaves it nil and clears inherited dial hooks.
		fixture.hooks.ProxyDialContext = func(ctx context.Context, network, target string) (net.Conn, error) {
			if target != redirectProxy+":8080" {
				return nil, errors.New("unexpected synthetic CONNECT proxy hop")
			}
			return (&net.Dialer{}).DialContext(ctx, network, address)
		}
		if mode == "environment-connect" {
			proxyURL := &url.URL{Scheme: "http", Host: redirectProxy + ":8080"}
			fixture.hooks.ProxyFromEnvironment = func(*http.Request) (*url.URL, error) { return proxyURL, nil }
			return &repository.EgressProxy{Mode: repository.EgressProxyModeEnvironment}
		}
		return &repository.EgressProxy{Mode: repository.EgressProxyModeCustom, Protocol: repository.EgressProxyProtocolHTTP,
			Host: redirectProxy, Port: 8080}
	case "socks-local", "socks-remote":
		address := newProxyRedirectSOCKSProxy(t, fixture)
		fixture.hooks.DialContext = func(ctx context.Context, network, target string) (net.Conn, error) {
			if target != redirectProxy+":8080" {
				return nil, errors.New("unexpected synthetic SOCKS proxy hop")
			}
			return (&net.Dialer{}).DialContext(ctx, network, address)
		}
		return &repository.EgressProxy{Mode: repository.EgressProxyModeCustom, Protocol: repository.EgressProxyProtocolSOCKS5,
			Host: redirectProxy, Port: 8080, RemoteDNS: mode == "socks-remote"}
	default:
		t.Fatalf("unknown fixture mode %q", mode)
		return nil
	}
}

type proxyRedirectConnections struct {
	mu     sync.Mutex
	conns  []net.Conn
	closed bool
}

func (c *proxyRedirectConnections) add(connection net.Conn) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		_ = connection.Close()
		return
	}
	c.conns = append(c.conns, connection)
}

func (c *proxyRedirectConnections) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	for _, connection := range c.conns {
		_ = connection.Close()
	}
}

func (f *proxyRedirectFixture) recordProxyTarget(target string) bool {
	f.mu.Lock()
	f.proxyTargets = append(f.proxyTargets, target)
	rejected := f.proxyRejected[target]
	f.mu.Unlock()
	if rejected {
		return false
	}
	host, _, err := net.SplitHostPort(target)
	if err != nil {
		return false
	}
	// Both hostname and locally resolved SOCKS address forms map exclusively to
	// the owned TLS fixture. All other targets fail without an outbound dial.
	return host == redirectOrigin || host == redirectCDN || host == redirectDenied ||
		host == "93.184.216.10" || host == "93.184.216.11" || host == "93.184.216.12"
}

func (f *proxyRedirectFixture) proxySnapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.proxyTargets...)
}

func (f *proxyRedirectFixture) dialSnapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.dials...)
}

func relayProxyRedirectConnection(client, upstream net.Conn) {
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(upstream, client)
		_ = upstream.Close()
		close(done)
	}()
	_, _ = io.Copy(client, upstream)
	_ = client.Close()
	<-done
}

func newProxyRedirectCONNECTProxy(t *testing.T, fixture *proxyRedirectFixture) string {
	t.Helper()
	connections := &proxyRedirectConnections{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		allowed := fixture.recordProxyTarget(r.Host)
		if r.Method != http.MethodConnect || !allowed {
			http.Error(w, "synthetic proxy target rejected", http.StatusForbidden)
			return
		}
		upstream, err := (&net.Dialer{}).DialContext(r.Context(), "tcp", fixture.upstreamAddress)
		if err != nil {
			http.Error(w, "synthetic upstream unavailable", http.StatusBadGateway)
			return
		}
		connections.add(upstream)
		client, buffer, err := w.(http.Hijacker).Hijack()
		if err != nil {
			_ = upstream.Close()
			return
		}
		connections.add(client)
		_, _ = buffer.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		if err := buffer.Flush(); err != nil {
			_ = client.Close()
			_ = upstream.Close()
			return
		}
		relayProxyRedirectConnection(client, upstream)
	}))
	t.Cleanup(func() { connections.close(); server.Close() })
	return strings.TrimPrefix(server.URL, "http://")
}

func newProxyRedirectSOCKSProxy(t *testing.T, fixture *proxyRedirectFixture) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	connections := &proxyRedirectConnections{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			client, err := listener.Accept()
			if err != nil {
				return
			}
			connections.add(client)
			go serveProxyRedirectSOCKS(client, fixture, connections)
		}
	}()
	t.Cleanup(func() { _ = listener.Close(); connections.close(); <-done })
	return listener.Addr().String()
}

func serveProxyRedirectSOCKS(client net.Conn, fixture *proxyRedirectFixture, connections *proxyRedirectConnections) {
	defer func() { _ = client.Close() }()
	var greeting [2]byte
	if _, err := io.ReadFull(client, greeting[:]); err != nil || greeting[0] != 5 || greeting[1] == 0 {
		return
	}
	methods := make([]byte, int(greeting[1]))
	if _, err := io.ReadFull(client, methods); err != nil {
		return
	}
	_, _ = client.Write([]byte{5, 0})
	var request [4]byte
	if _, err := io.ReadFull(client, request[:]); err != nil || request[0] != 5 || request[1] != 1 {
		return
	}
	var host string
	switch request[3] {
	case 1, 4:
		size := 4
		if request[3] == 4 {
			size = 16
		}
		address := make([]byte, size)
		if _, err := io.ReadFull(client, address); err != nil {
			return
		}
		host = net.IP(address).String()
	case 3:
		var length [1]byte
		if _, err := io.ReadFull(client, length[:]); err != nil {
			return
		}
		name := make([]byte, int(length[0]))
		if _, err := io.ReadFull(client, name); err != nil {
			return
		}
		host = string(name)
	default:
		return
	}
	var port [2]byte
	if _, err := io.ReadFull(client, port[:]); err != nil {
		return
	}
	target := net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(port[:]))))
	if !fixture.recordProxyTarget(target) {
		_, _ = client.Write([]byte{5, 2, 0, 1, 127, 0, 0, 1, 0, 0})
		return
	}
	upstream, err := net.Dial("tcp", fixture.upstreamAddress)
	if err != nil {
		return
	}
	connections.add(upstream)
	_, _ = client.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0})
	relayProxyRedirectConnection(client, upstream)
}
