package raw

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestServeContentMultipartRanges(t *testing.T) {
	body := []byte("0123456789abcdefghijklmnopqrstuvwxyz")
	request := httptest.NewRequest(http.MethodGet, "/raw/downloads/file", nil)
	request.Header.Set("Range", "bytes=0-3,10-14,-3")
	request.Header.Set("If-Range", `"sha256-fixture"`)
	response := httptest.NewRecorder()
	result := ServeContent(response, request, "file", Content{Body: body, Digest: "fixture", ContentType: "application/octet-stream"})
	if response.Code != http.StatusPartialContent {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	mediaType, params, err := mime.ParseMediaType(response.Header().Get("Content-Type"))
	if err != nil || mediaType != "multipart/byteranges" || params["boundary"] == "" {
		t.Fatalf("multipart content-type=%q err=%v", response.Header().Get("Content-Type"), err)
	}
	if response.Header().Get("Content-Range") != "" || response.Header().Get("ETag") != `"sha256-fixture"` {
		t.Fatalf("multipart headers=%v", response.Header())
	}
	if length := response.Header().Get("Content-Length"); length != strconv.Itoa(response.Body.Len()) || result.Bytes != int64(response.Body.Len()) {
		t.Fatalf("length=%s body length=%d result=%+v", length, response.Body.Len(), result)
	}
	reader := multipart.NewReader(response.Body, params["boundary"])
	for i, want := range []struct{ contentRange, body string }{
		{"bytes 0-3/36", "0123"}, {"bytes 10-14/36", "abcde"}, {"bytes 33-35/36", "xyz"},
	} {
		part, err := reader.NextPart()
		if err != nil {
			t.Fatalf("part %d: %v", i, err)
		}
		got, err := io.ReadAll(part)
		if err != nil || string(got) != want.body || part.Header.Get("Content-Range") != want.contentRange || part.Header.Get("Content-Type") != "application/octet-stream" {
			t.Fatalf("part %d header=%v body=%q err=%v", i, part.Header, got, err)
		}
	}
	if _, err := reader.NextPart(); err != io.EOF {
		t.Fatalf("extra multipart data: %v", err)
	}
}

func TestServeContentRangeNormalizationAndBounds(t *testing.T) {
	for _, tc := range []struct {
		name, value, wantBody, contentRange string
		status                              int
	}{
		{"overlap coalesces", "bytes=1-4,3-6", "123456", "bytes 1-6/10", 206},
		{"duplicate coalesces", "bytes=1-3,1-3", "123", "bytes 1-3/10", 206},
		{"adjacent coalesces in order", "bytes=4-6,1-3", "123456", "bytes 1-6/10", 206},
		{"unsatisfiable member skipped", "bytes=100-200,1-3", "123", "bytes 1-3/10", 206},
		{"suffix covers all", "bytes=-99", "0123456789", "bytes 0-9/10", 206},
		{"all unsatisfiable", "bytes=10-20,30-40", "", "bytes */10", 416},
		{"overflow", "bytes=0-1,9223372036854775808-", "", "bytes */10", 416},
		{"zero suffix", "bytes=-0", "", "bytes */10", 416},
		{"reversed", "bytes=4-2,0-1", "", "bytes */10", 416},
		{"empty member", "bytes=0-1,,4-5", "", "bytes */10", 416},
		{"wrong unit", "items=0-1", "", "bytes */10", 416},
		{"too many", "bytes=" + strings.Repeat("0-0,", 16) + "1-1", "", "bytes */10", 416},
		{"field too long", "bytes=" + strings.Repeat(" ", 8192) + "0-1", "", "bytes */10", 416},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/raw/downloads/file", nil)
			request.Header.Set("Range", tc.value)
			response := httptest.NewRecorder()
			ServeContent(response, request, "file", Content{Body: []byte("0123456789"), ContentType: "text/plain"})
			if response.Code != tc.status || response.Body.String() != tc.wantBody || response.Header().Get("Content-Range") != tc.contentRange {
				t.Fatalf("status=%d body=%q range=%q", response.Code, response.Body.String(), response.Header().Get("Content-Range"))
			}
		})
	}
}

type rangeOnlySource struct {
	data                  []byte
	size                  int64
	opens, active, closes int
	maxActive             int
	beforeOpen            func(int)
	failAt, shortAt       int
}

func (*rangeOnlySource) Open(context.Context) (io.ReadCloser, int64, error) {
	return nil, 0, errors.New("must stream only the requested intervals")
}
func (s *rangeOnlySource) OpenRange(ctx context.Context, offset, length int64) (io.ReadCloser, int64, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	s.opens++
	if s.opens == s.failAt {
		return nil, 0, errors.New("object interval unavailable")
	}
	if s.beforeOpen != nil {
		s.beforeOpen(s.opens)
	}
	s.active++
	if s.active > s.maxActive {
		s.maxActive = s.active
	}
	reader := io.Reader(bytes.NewReader(s.data[offset : offset+length]))
	if s.opens == s.shortAt {
		reader = io.LimitReader(reader, length-1)
	}
	return &rangeTestCloser{Reader: reader, close: func() { s.active--; s.closes++ }}, s.size, nil
}

func TestServeContentMultipartDoesNotCompleteAfterSourceFailure(t *testing.T) {
	for _, tc := range []struct {
		name            string
		failAt, shortAt int
		status, closes  int
	}{
		{"first open", 1, 0, 500, 0},
		{"later open", 2, 0, 206, 1},
		{"short first interval", 0, 1, 206, 1},
		{"short later interval", 0, 2, 206, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := &rangeOnlySource{data: []byte("0123456789"), size: 10, failAt: tc.failAt, shortAt: tc.shortAt}
			r := httptest.NewRequest(http.MethodGet, "/raw/downloads/file", nil)
			r.Header.Set("Range", "bytes=0-1,8-9")
			w := httptest.NewRecorder()
			ServeContent(w, r, "file", Content{Size: 10, Source: source, ContentType: "text/plain"})
			if w.Code != tc.status || source.active != 0 || source.closes != tc.closes {
				t.Fatalf("status=%d source=%+v", w.Code, source)
			}
			if w.Code == 206 {
				length, err := strconv.Atoi(w.Header().Get("Content-Length"))
				if err != nil || length <= w.Body.Len() {
					t.Fatalf("failed stream advertised success length=%d got=%d error=%v", length, w.Body.Len(), err)
				}
				_, params, _ := mime.ParseMediaType(w.Header().Get("Content-Type"))
				if strings.HasSuffix(w.Body.String(), "--"+params["boundary"]+"--\r\n") {
					t.Fatal("failed stream got a successful multipart closing delimiter")
				}
			}
		})
	}
}

type rangeTestCloser struct {
	io.Reader
	close func()
}

func (r *rangeTestCloser) Close() error { r.close(); return nil }

func TestServeContentMultipartStreamsSequentiallyAndStopsOnCancellation(t *testing.T) {
	for _, cancelAfterFirst := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		source := &rangeOnlySource{data: []byte("0123456789"), size: 10}
		request := httptest.NewRequest(http.MethodGet, "/raw/downloads/file", nil).WithContext(ctx)
		request.Header.Set("Range", "bytes=0-1,8-9")
		response := httptest.NewRecorder()
		writer := http.ResponseWriter(response)
		if cancelAfterFirst {
			writer = &cancelRangeWriter{ResponseWriter: response, cancel: cancel}
		}
		ServeContent(writer, request, "file", Content{Size: source.size, Source: source, ContentType: "text/plain"})
		cancel()
		wantOpens := 2
		if cancelAfterFirst {
			wantOpens = 1
		}
		if source.opens != wantOpens || source.active != 0 || source.maxActive != 1 || source.closes != source.opens {
			t.Fatalf("cancel=%t source=%+v", cancelAfterFirst, source)
		}
	}
}

type cancelRangeWriter struct {
	http.ResponseWriter
	cancel context.CancelFunc
}

func (w *cancelRangeWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	if bytes.Equal(p, []byte("01")) {
		w.cancel()
	}
	return n, err
}

func TestServeContentRangeHeadValidatorsAndLimits(t *testing.T) {
	for _, tc := range []struct {
		name, method, rangeValue, ifRange string
		want                              int
	}{
		{"head ignores range", http.MethodHead, "bytes=0-1,8-9", "", 200},
		{"stale if-range", http.MethodGet, "bytes=0-1,8-9", `"stale"`, 200},
		{"weak if-range", http.MethodGet, "bytes=0-1,8-9", `W/"sha256-fixture"`, 200},
		{"date if-range without modified time", http.MethodGet, "bytes=0-1,8-9", "Wed, 21 Oct 2015 07:28:00 GMT", 200},
		{"empty object", http.MethodGet, "bytes=0-1,8-9", "", 416},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte("0123456789")
			if tc.name == "empty object" {
				body = nil
			}
			request := httptest.NewRequest(tc.method, "/raw/downloads/file", nil)
			request.Header.Set("Range", tc.rangeValue)
			request.Header.Set("If-Range", tc.ifRange)
			response := httptest.NewRecorder()
			ServeContent(response, request, "file", Content{Body: body, Digest: "fixture", ContentType: "text/plain"})
			if response.Code != tc.want || (tc.method == http.MethodHead && response.Body.Len() != 0) {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
	// Range limits must be checked before opening any stream, even for a very
	// large source that cannot be buffered in the process.
	source := &rangeOnlySource{size: 1 << 40}
	request := httptest.NewRequest(http.MethodGet, "/raw/downloads/file", nil)
	request.Header.Set("Range", "bytes=0-67108863,70000000-70000001")
	response := httptest.NewRecorder()
	ServeContent(response, request, "file", Content{Size: source.size, Source: source, ContentType: "text/plain"})
	if response.Code != 416 || source.opens != 0 {
		t.Fatalf("unbounded multipart response=%d opens=%d", response.Code, source.opens)
	}
}
