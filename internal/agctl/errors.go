// Package agctl implements the agctl command line client: command
// registration, configuration and credential resolution, the HTTP client
// layer, and the exit-code and JSON output contracts. The contract is
// recorded in docs/adr/0007-agctl-cli-shape.md.
package agctl

import (
	"errors"
	"net/http"
)

// Exit codes are part of the documented contract in docs/cli.md.
const (
	ExitOK              = 0
	ExitFailure         = 1
	ExitUsage           = 2
	ExitPartial         = 3
	ExitNotFound        = 4
	ExitUnauthenticated = 5
	ExitForbidden       = 6
	ExitConflict        = 7
	ExitConnection      = 8
)

// UsageError marks invalid invocation, configuration, or missing required
// input. It exits with code 2 before any request is sent.
type UsageError struct {
	Message string
	Hint    string
}

func (e *UsageError) Error() string { return e.Message }

// CredentialsError marks a command that requires credentials when none are
// configured. It exits with code 5.
type CredentialsError struct {
	Message string
	Hint    string
}

func (e *CredentialsError) Error() string { return e.Message }

// ConnectionError marks a failed or timed-out connection to the Gateway. It
// exits with code 8.
type ConnectionError struct {
	Message string
	Hint    string
}

func (e *ConnectionError) Error() string { return e.Message }

// PartialError marks a batch that finished with failures in some items. It
// exits with code 3.
type PartialError struct {
	Message string
	Hint    string
}

func (e *PartialError) Error() string { return e.Message }

// APIError is a structured refusal or failure returned by the management API.
type APIError struct {
	Code       string
	HTTPStatus int
	Message    string
	RequestID  string
	Hint       string
}

func (e *APIError) Error() string { return e.Message }

func exitCodeFor(err error) int {
	if err == nil {
		return ExitOK
	}
	var usage *UsageError
	if errors.As(err, &usage) {
		return ExitUsage
	}
	var credentials *CredentialsError
	if errors.As(err, &credentials) {
		return ExitUnauthenticated
	}
	var partial *PartialError
	if errors.As(err, &partial) {
		return ExitPartial
	}
	var api *APIError
	if errors.As(err, &api) {
		switch api.HTTPStatus {
		case http.StatusNotFound:
			return ExitNotFound
		case http.StatusUnauthorized:
			return ExitUnauthenticated
		case http.StatusForbidden:
			return ExitForbidden
		case http.StatusConflict, http.StatusPreconditionFailed:
			return ExitConflict
		}
		return ExitFailure
	}
	var connection *ConnectionError
	if errors.As(err, &connection) {
		return ExitConnection
	}
	return ExitFailure
}

// localErrorCode maps an error to the stable envelope code. API errors carry
// the server problem code when the response included one.
func localErrorCode(err error) string {
	var usage *UsageError
	if errors.As(err, &usage) {
		return "usage_error"
	}
	var credentials *CredentialsError
	if errors.As(err, &credentials) {
		return "credentials_missing"
	}
	var partial *PartialError
	if errors.As(err, &partial) {
		return "partial_failure"
	}
	var api *APIError
	if errors.As(err, &api) {
		return api.Code
	}
	var connection *ConnectionError
	if errors.As(err, &connection) {
		return "connection_error"
	}
	return "internal_error"
}

func errorHint(err error) string {
	var usage *UsageError
	if errors.As(err, &usage) {
		return usage.Hint
	}
	var credentials *CredentialsError
	if errors.As(err, &credentials) {
		return credentials.Hint
	}
	var partial *PartialError
	if errors.As(err, &partial) {
		return partial.Hint
	}
	var api *APIError
	if errors.As(err, &api) {
		return api.Hint
	}
	var connection *ConnectionError
	if errors.As(err, &connection) {
		return connection.Hint
	}
	return ""
}

func errorHTTPStatus(err error) *int {
	var api *APIError
	if errors.As(err, &api) && api.HTTPStatus != 0 {
		return &api.HTTPStatus
	}
	return nil
}

func errorRequestID(err error) string {
	var api *APIError
	if errors.As(err, &api) {
		return api.RequestID
	}
	return ""
}

// hintForStatus derives the actionable hint attached to API errors.
func hintForStatus(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "the request was rejected; check the inputs for this command"
	case http.StatusUnauthorized:
		return "provide a token with --token, AGCTL_TOKEN, or a profile in ~/.config/agctl/config.yaml"
	case http.StatusForbidden:
		return "the credential is valid but lacks the required permission for this operation"
	case http.StatusNotFound:
		return "the requested resource does not exist or is not visible to this credential"
	case http.StatusConflict, http.StatusPreconditionFailed:
		return "the resource changed or is in a conflicting state; re-read it and retry"
	case http.StatusTooManyRequests:
		return "the server is rate limiting; retry after the server-provided delay"
	case http.StatusServiceUnavailable:
		return "the server reported a dependency failure; retry when it is ready"
	}
	if status >= 500 {
		return "the server failed; include the request id when reporting this"
	}
	return ""
}
