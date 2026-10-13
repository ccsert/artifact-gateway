package agctl

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestRenderFailureHumanFormat(t *testing.T) {
	var stdout, stderr bytes.Buffer
	renderFailure(&stdout, &stderr, outputTable, &APIError{
		Code:       "access_denied",
		HTTPStatus: 403,
		Message:    "not allowed",
		RequestID:  "req-42",
		Hint:       "ask an administrator for access",
	})
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q", stdout.String())
	}
	for _, want := range []string{"error: not allowed", "code: access_denied", "hint: ask an administrator for access", "request id: req-42"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr missing %q: %s", want, stderr.String())
		}
	}
}

func TestRenderFailureHumanFormatWrappedAPIError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := fmt.Errorf("identity probe: %w", &APIError{Code: "access_denied", HTTPStatus: 403, Message: "not allowed"})
	renderFailure(&stdout, &stderr, outputTable, err)
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "code: access_denied") {
		t.Fatalf("stdout = %q, stderr = %q", stdout.String(), stderr.String())
	}
}

func TestRenderFailureRedactsAllDiagnosticFields(t *testing.T) {
	const secret = "output-diagnostic-secret"
	for _, format := range []outputFormat{outputTable, outputJSON} {
		t.Run(string(format), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			renderFailure(&stdout, &stderr, format, &APIError{
				Code:       "failure_" + secret,
				HTTPStatus: 403,
				Message:    "failed with " + secret,
				Hint:       "replace " + secret,
				RequestID:  "request-" + secret,
			}, secret)
			output := stdout.String() + stderr.String()
			if strings.Contains(output, secret) || !strings.Contains(output, "REDACTED") {
				t.Fatalf("stdout = %q, stderr = %q", stdout.String(), stderr.String())
			}
			if format == outputJSON {
				result := decodeEnvelope(t, stdout.String())
				if result.OK || result.Error == nil || result.Error.HTTPStatus == nil || *result.Error.HTTPStatus != 403 {
					t.Fatalf("envelope = %#v", result)
				}
			}
		})
	}
}

func TestRenderFailureJSONEnvelope(t *testing.T) {
	var stdout, stderr bytes.Buffer
	renderFailure(&stdout, &stderr, outputJSON, &UsageError{Message: "bad input", Hint: "check the flags"})
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
	var envelope struct {
		SchemaVersion int  `json:"schemaVersion"`
		OK            bool `json:"ok"`
		Error         *struct {
			Code       string `json:"code"`
			HTTPStatus *int   `json:"httpStatus"`
			Message    string `json:"message"`
			Hint       string `json:"hint"`
			RequestID  string `json:"requestId"`
		} `json:"error"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout.String())
	}
	if envelope.SchemaVersion != 1 || envelope.OK || envelope.Error == nil {
		t.Fatalf("envelope = %#v", envelope)
	}
	if envelope.Error.Code != "usage_error" || envelope.Error.Message != "bad input" || envelope.Error.Hint != "check the flags" {
		t.Fatalf("error = %#v", envelope.Error)
	}
	if envelope.Error.HTTPStatus != nil || envelope.Error.RequestID != "" {
		t.Fatalf("error = %#v", envelope.Error)
	}
}

func TestRenderSuccessJSONEnvelopeUsesStableKeys(t *testing.T) {
	var stdout bytes.Buffer
	if err := renderSuccess(&stdout, outputJSON, map[string]string{"k": "v"}, &envelopeMeta{Pagination: &paginationMeta{Mode: "none"}}, nil); err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(stdout.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"schemaVersion", "ok", "data", "error", "meta"} {
		if _, present := raw[key]; !present {
			t.Fatalf("envelope missing %q: %s", key, stdout.String())
		}
	}
	if string(raw["error"]) != "null" {
		t.Fatalf("error = %s", raw["error"])
	}
}
