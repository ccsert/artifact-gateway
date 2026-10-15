package agctl

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// outputSchemaVersion versions the JSON envelope. It only changes on a
// breaking change to the documented schema.
const outputSchemaVersion = 1

type outputFormat string

const (
	outputTable outputFormat = "table"
	outputJSON  outputFormat = "json"
)

type errorReport struct {
	Code       string `json:"code"`
	HTTPStatus *int   `json:"httpStatus"`
	Message    string `json:"message"`
	Hint       string `json:"hint"`
	RequestID  string `json:"requestId"`
}

// paginationMeta states the real pagination of a list. Server-paginated lists
// add the page size requested, the pages read, and whether the listing reached
// the end of the collection.
type paginationMeta struct {
	Mode     string `json:"mode"`
	PageSize int    `json:"pageSize,omitempty"`
	Pages    int    `json:"pages,omitempty"`
	Complete *bool  `json:"complete,omitempty"`
	// Limit and Offset describe an offset window or a bounded read; Total is
	// the server's count when it reports one (null means unknown, never 0).
	Limit  int  `json:"limit,omitempty"`
	Offset *int `json:"offset,omitempty"`
	Total  *int `json:"total,omitempty"`
}

type envelopeMeta struct {
	Pagination *paginationMeta `json:"pagination"`
	// Scope carries what a page reports about the range it covers, such as
	// how many repositories a global search read or which Group a browse page
	// belongs to. Omitted when the source reports nothing.
	Scope map[string]any `json:"scope,omitempty"`
}

type envelope struct {
	SchemaVersion int           `json:"schemaVersion"`
	OK            bool          `json:"ok"`
	Data          any           `json:"data"`
	Error         *errorReport  `json:"error"`
	Meta          *envelopeMeta `json:"meta"`
}

// listPayload is the single list shape: items plus a cursor that is null when
// the source has no server-side pagination.
type listPayload[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"nextCursor"`
}

func newListPayload[T any](items []T) listPayload[T] {
	if items == nil {
		items = []T{}
	}
	return listPayload[T]{Items: items}
}

func writeEnvelope(w io.Writer, value envelope) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}

func renderSuccess(stdout io.Writer, format outputFormat, data any, meta *envelopeMeta, human func(io.Writer) error) error {
	if format == outputJSON {
		return writeEnvelope(stdout, envelope{SchemaVersion: outputSchemaVersion, OK: true, Data: data, Meta: meta})
	}
	if human == nil {
		return nil
	}
	return human(stdout)
}

// renderFailure writes with the selected format. JSON mode prints exactly one
// failure envelope on stdout; table mode prints diagnostics on stderr.
func renderFailure(stdout, stderr io.Writer, format outputFormat, err error, knownSecrets ...string) {
	report := errorReportFrom(err, knownSecrets...)
	var result *resultError
	hasResult := errors.As(err, &result)
	if format == outputJSON {
		value := envelope{SchemaVersion: outputSchemaVersion, OK: false, Error: report}
		if hasResult {
			value.Data = result.data
			value.Meta = result.meta
		}
		_ = writeEnvelope(stdout, value)
		return
	}
	if hasResult && result.human != nil {
		_ = result.human(stdout)
	}
	_, _ = fmt.Fprintf(stderr, "error: %s\n", report.Message)
	var api *APIError
	if errors.As(err, &api) && report.Code != "" {
		_, _ = fmt.Fprintf(stderr, "code: %s\n", report.Code)
	}
	if hint := report.Hint; hint != "" {
		_, _ = fmt.Fprintf(stderr, "hint: %s\n", hint)
	}
	if requestID := report.RequestID; requestID != "" {
		_, _ = fmt.Fprintf(stderr, "request id: %s\n", requestID)
	}
}

func errorReportFrom(err error, knownSecrets ...string) *errorReport {
	return &errorReport{
		Code:       redactText(localErrorCode(err), knownSecrets...),
		HTTPStatus: errorHTTPStatus(err),
		Message:    redactText(err.Error(), knownSecrets...),
		Hint:       redactText(errorHint(err), knownSecrets...),
		RequestID:  redactText(errorRequestID(err), knownSecrets...),
	}
}
