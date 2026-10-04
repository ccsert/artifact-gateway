package emailnotification

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/smtp"
	"net/textproto"
	"strconv"
	"time"
)

type Config struct {
	Enabled       bool     `json:"enabled"`
	Host          string   `json:"host"`
	Port          int      `json:"port"`
	Mode          string   `json:"mode"`
	From          string   `json:"from"`
	ApprovedIPs   []string `json:"approvedIPs"`
	CAFile        string   `json:"caFile"`
	AuthFile      string   `json:"authFile"`
	ConsoleOrigin string   `json:"consoleOrigin"`
}

type Result struct {
	Code                      string
	Permanent, OutcomeUnknown bool
}

// Sender's optional network hooks let tests route an approved synthetic address
// to an owned local fixture. Deployment wiring uses the defaults exclusively.
type Sender struct {
	Config      Config
	LookupHost  func(context.Context, string) ([]string, error)
	DialContext func(context.Context, string, string) (net.Conn, error)
}

func (s Sender) Send(ctx context.Context, recipient, eventID string, message []byte) Result {
	if !s.Config.Enabled {
		return Result{Code: "email_disabled", Permanent: true}
	}
	if s.Config.Validate() != nil || ValidAddress(recipient) != nil || len(message) > 128<<10 {
		return Result{Code: "invalid_message", Permanent: true}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	lookup := s.LookupHost
	if lookup == nil {
		lookup = net.DefaultResolver.LookupHost
	}
	addresses, err := lookup(ctx, s.Config.Host)
	if err != nil || len(addresses) == 0 {
		return Result{Code: "relay_resolution_failed"}
	}
	approved := make(map[string]bool, len(s.Config.ApprovedIPs))
	for _, v := range s.Config.ApprovedIPs {
		approved[net.ParseIP(v).String()] = true
	}
	for _, address := range addresses {
		ip := net.ParseIP(address)
		if forbiddenIP(ip) || !approved[ip.String()] {
			return Result{Code: "relay_address_denied", Permanent: true}
		}
	}
	dial := s.DialContext
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	conn, err := dial(ctx, "tcp", net.JoinHostPort(addresses[0], strconv.Itoa(s.Config.Port)))
	if err != nil {
		return Result{Code: "relay_connect_failed"}
	}
	defer func() { _ = conn.Close() }()
	deadline, _ := ctx.Deadline()
	_ = conn.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	roots, err := relayRoots(s.Config.CAFile)
	if err != nil {
		return Result{Code: "tls_configuration_failed", Permanent: true}
	}
	tlsConfig := &tls.Config{ServerName: s.Config.Host, RootCAs: roots, MinVersion: tls.VersionTLS12}
	smtpConn := conn
	if s.Config.Mode == "implicit_tls" {
		secured := tls.Client(conn, tlsConfig)
		if secured.HandshakeContext(ctx) != nil {
			return Result{Code: "tls_verification_failed"}
		}
		smtpConn = secured
	}
	client, err := smtp.NewClient(smtpConn, s.Config.Host)
	if err != nil {
		return smtpResult(err, false)
	}
	defer func() { _ = client.Close() }()
	if s.Config.Mode == "starttls_required" {
		if available, _ := client.Extension("STARTTLS"); !available {
			return Result{Code: "tls_required", Permanent: true}
		}
		if client.StartTLS(tlsConfig) != nil {
			return Result{Code: "tls_verification_failed"}
		}
	}
	if state, ok := client.TLSConnectionState(); !ok || !state.HandshakeComplete || len(state.VerifiedChains) == 0 {
		return Result{Code: "tls_verification_failed"}
	}
	if s.Config.AuthFile != "" {
		auth, e := readAuth(s.Config.AuthFile)
		if e != nil {
			return Result{Code: "authentication_configuration_failed", Permanent: true}
		}
		if client.Auth(smtp.PlainAuth("", auth.Username, auth.Password, s.Config.Host)) != nil {
			return Result{Code: "authentication_failed"}
		}
	}
	if err = client.Mail(s.Config.From); err != nil {
		return smtpResult(err, false)
	}
	if err = client.Rcpt(recipient); err != nil {
		return smtpResult(err, false)
	}
	data, err := client.Data()
	if err != nil {
		return smtpResult(err, false)
	}
	if _, err = data.Write(message); err != nil {
		return smtpResult(err, true)
	}
	if err = data.Close(); err != nil {
		return smtpResult(err, true)
	}
	// The final positive DATA reply is acceptance. QUIT/network errors after it
	// cannot turn a successful acceptance into another send attempt.
	return Result{}
}
func smtpResult(err error, afterData bool) Result {
	var reply *textproto.Error
	if errors.As(err, &reply) {
		if reply.Code >= 500 {
			return Result{Code: "smtp_permanent_rejection", Permanent: true}
		}
		if reply.Code >= 400 {
			return Result{Code: "smtp_temporary_rejection"}
		}
	}
	if afterData {
		return Result{Code: "outcome_unknown", OutcomeUnknown: true}
	}
	return Result{Code: "smtp_transport_failed"}
}
func relayRoots(path string) (*x509.CertPool, error) {
	if path == "" {
		return nil, nil
	}
	file, err := openRegularSetting(path, 1<<20, false)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return nil, ErrInvalidConfig
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		roots = x509.NewCertPool()
	}
	if !roots.AppendCertsFromPEM(data) {
		return nil, ErrInvalidConfig
	}
	return roots, nil
}
