package testsupport

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// SMTPFixture never forwards mail. All certificates and recipients are synthetic.
type SMTPFixture struct {
	Address, CAFile string
	ImplicitTLS     bool
	RejectRCPT      int
	NoSTARTTLS      bool
	RejectAuth      bool
	DropFinalReply  bool
	AfterAccept     func()
	mu              sync.Mutex
	Messages        [][]byte
	Commands        []string
	listener        net.Listener
	certificate     tls.Certificate
	wg              sync.WaitGroup
}

func NewSMTPFixture(t *testing.T, implicit bool) *SMTPFixture {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "smtp.example.test"}, DNSNames: []string{"smtp.example.test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := tls.X509KeyPair(certPEM, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "synthetic-ca.pem")
	if err = os.WriteFile(path, certPEM, 0600); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &SMTPFixture{Address: l.Addr().String(), CAFile: path, ImplicitTLS: implicit, listener: l, certificate: pair}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			c, e := l.Accept()
			if e != nil {
				return
			}
			s.wg.Add(1)
			go func() { defer s.wg.Done(); s.serve(c) }()
		}
	}()
	t.Cleanup(func() { _ = l.Close(); s.wg.Wait() })
	return s
}
func (s *SMTPFixture) Snapshot() ([][]byte, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][]byte(nil), s.Messages...), append([]string(nil), s.Commands...)
}
func (s *SMTPFixture) serve(c net.Conn) {
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(15 * time.Second))
	if s.ImplicitTLS {
		c = tls.Server(c, &tls.Config{Certificates: []tls.Certificate{s.certificate}, MinVersion: tls.VersionTLS12})
	}
	p := textproto.NewConn(c)
	_ = p.PrintfLine("220 synthetic fixture")
	secured := s.ImplicitTLS
	for {
		line, err := p.ReadLine()
		if err != nil {
			return
		}
		s.mu.Lock()
		s.Commands = append(s.Commands, line)
		s.mu.Unlock()
		switch {
		case strings.HasPrefix(line, "EHLO"):
			if !secured && !s.NoSTARTTLS {
				_ = p.PrintfLine("250-fixture\r\n250 STARTTLS")
			} else {
				_ = p.PrintfLine("250-fixture\r\n250 AUTH PLAIN")
			}
		case line == "STARTTLS":
			if s.NoSTARTTLS {
				_ = p.PrintfLine("502 unavailable")
				continue
			}
			_ = p.PrintfLine("220 TLS ready")
			c = tls.Server(c, &tls.Config{Certificates: []tls.Certificate{s.certificate}, MinVersion: tls.VersionTLS12})
			p = textproto.NewConn(c)
			secured = true
		case strings.HasPrefix(line, "AUTH"):
			if !secured || s.RejectAuth {
				_ = p.PrintfLine("535 synthetic auth rejected")
			} else {
				_ = p.PrintfLine("235 authenticated")
			}
		case strings.HasPrefix(line, "MAIL FROM"):
			_ = p.PrintfLine("250 sender accepted")
		case strings.HasPrefix(line, "RCPT TO"):
			if s.RejectRCPT != 0 {
				_ = p.PrintfLine("%d synthetic recipient rejection", s.RejectRCPT)
			} else {
				_ = p.PrintfLine("250 recipient accepted")
			}
		case line == "DATA":
			_ = p.PrintfLine("354 send data")
			data, e := p.ReadDotBytes()
			if e != nil {
				return
			}
			s.mu.Lock()
			s.Messages = append(s.Messages, data)
			s.mu.Unlock()
			if s.AfterAccept != nil {
				s.AfterAccept()
			}
			if s.DropFinalReply {
				return
			}
			_ = p.PrintfLine("250 queued")
		case line == "QUIT":
			_ = p.PrintfLine("221 bye")
			return
		default:
			_ = p.PrintfLine("250 ok")
		}
	}
}
