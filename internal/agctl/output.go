package agctl

import (
	"encoding/json"
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

type paginationMeta struct {
	Mode string `json:"mode"`
}

type envelopeMeta struct {
	Pagination *paginationMeta `json:"pagination"`
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
func renderFailure(stdout, stderr io.Writer, format outputFormat, err error) {
	if format == outputJSON {
		_ = writeEnvelope(stdout, envelope{SchemaVersion: outputSchemaVersion, OK: false, Error: errorReportFrom(err)})
		return
	}
	_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
	if hint := errorHint(err); hint != "" {
		_, _ = fmt.Fprintf(stderr, "hint: %s\n", hint)
	}
	if requestID := errorRequestID(err); requestID != "" {
		_, _ = fmt.Fprintf(stderr, "request id: %s\n", requestID)
	}
}

func errorReportFrom(err error) *errorReport {
	return &errorReport{
		Code:       localErrorCode(err),
		HTTPStatus: errorHTTPStatus(err),
		Message:    err.Error(),
		Hint:       errorHint(err),
		RequestID:  errorRequestID(err),
	}
}
