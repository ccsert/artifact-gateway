package nexussource

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"time"
)

// sourceTimedOut uses both transport evidence and the operation budget. The
// client body and context timers may report their result in either order.
func sourceTimedOut(ctx context.Context, err error) bool {
	var timeout net.Error
	if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || errors.As(err, &timeout) && timeout.Timeout() {
		return true
	}
	deadline, ok := ctx.Deadline()
	return ok && !time.Now().Before(deadline)
}

// sourceRequestError returns only bounded public codes; an upstream URL,
// credential or raw transport error must never escape the source boundary.
func sourceRequestError(ctx context.Context, err error) error {
	if sourceTimedOut(ctx, err) {
		return fail("source_timeout")
	}
	var certificate *tls.CertificateVerificationError
	var unknown x509.UnknownAuthorityError
	if errors.As(err, &certificate) || errors.As(err, &unknown) {
		return fail("source_tls_failed")
	}
	return fail("source_unreachable")
}
