// Package artifacthttp serves verified immutable byte objects without format coordinates.
package artifacthttp

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/ccsert/artifact-gateway/internal/objectstore"
)

type Object struct {
	Digest, ObjectKey, ContentType string
	Size                           int64
}
type Result struct {
	Status int
	Bytes  int64
}

func ContentDisposition(path string) string {
	name := path
	if index := strings.LastIndex(name, "/"); index >= 0 {
		name = name[index+1:]
	}
	decoded, err := url.PathUnescape(name)
	if err != nil || decoded == "" || strings.ContainsAny(decoded, "\r\n") {
		return ""
	}
	return mime.FormatMediaType("inline", map[string]string{"filename": decoded})
}

func ServeObject(w http.ResponseWriter, r *http.Request, name string, asset Object, objects objectstore.Store) Result {
	statusWriter := &statusWriter{ResponseWriter: w}
	digest := strings.TrimPrefix(asset.Digest, "sha256:")
	w.Header().Set("Content-Type", asset.ContentType)
	if disposition := ContentDisposition(name); disposition != "" {
		w.Header().Set("Content-Disposition", disposition)
	}
	etag := `"sha256-` + digest + `"`
	w.Header().Set("ETag", etag)
	if decoded, err := hex.DecodeString(digest); err == nil {
		w.Header().Set("Digest", "sha-256="+base64.StdEncoding.EncodeToString(decoded))
	}
	if r.Header.Get("If-None-Match") == etag {
		statusWriter.WriteHeader(http.StatusNotModified)
		return statusWriter.result()
	}
	if r.Method == http.MethodGet && r.Header.Get("Range") != "" {
		start, end, ok := parseNativeRange(statusWriter, r, asset.Size)
		if !ok {
			return statusWriter.result()
		}
		length := end - start + 1
		reader, _, err := objects.OpenRange(r.Context(), asset.ObjectKey, start, length)
		if err != nil {
			http.Error(statusWriter, "raw object unavailable", http.StatusInternalServerError)
			return statusWriter.result()
		}
		defer func() { _ = reader.Close() }()
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Range", "bytes "+formatUint(uint64(start))+"-"+formatUint(uint64(end))+"/"+formatUint(uint64(asset.Size)))
		w.Header().Set("Content-Length", formatUint(uint64(length)))
		statusWriter.WriteHeader(http.StatusPartialContent)
		_, _ = io.CopyN(statusWriter, reader, length)
		return statusWriter.result()
	}
	reader, size, err := objects.Open(r.Context(), asset.ObjectKey)
	if err != nil {
		http.Error(statusWriter, "raw object unavailable", http.StatusInternalServerError)
		return statusWriter.result()
	}
	defer func() { _ = reader.Close() }()
	if asset.Size > 0 {
		size = asset.Size
	}
	w.Header().Set("Content-Length", formatUint(uint64(size)))
	statusWriter.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = io.Copy(statusWriter, reader)
	}
	return statusWriter.result()
}

type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(body)
	w.bytes += int64(n)
	return n, err
}

func (w *statusWriter) result() Result {
	status := w.status
	if status == 0 {
		status = http.StatusOK
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return Result{Status: status}
	}
	return Result{Status: status, Bytes: w.bytes}
}
func parseNativeRange(w http.ResponseWriter, request *http.Request, size int64) (int64, int64, bool) {
	rangeHeader := strings.TrimPrefix(request.Header.Get("Range"), "bytes=")
	parts := strings.SplitN(rangeHeader, "-", 2)
	if len(parts) != 2 || strings.Contains(rangeHeader, ",") {
		w.Header().Set("Content-Range", "bytes */"+formatUint(uint64(size)))
		w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
		return 0, 0, false
	}
	start, end := int64(0), size-1
	if parts[0] == "" {
		suffix, err := parseNativeByte(parts[1])
		if err != nil || suffix <= 0 {
			w.Header().Set("Content-Range", "bytes */"+formatUint(uint64(size)))
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return 0, 0, false
		}
		if suffix < size {
			start = size - suffix
		}
	} else {
		value, err := parseNativeByte(parts[0])
		if err != nil || value >= size {
			w.Header().Set("Content-Range", "bytes */"+formatUint(uint64(size)))
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return 0, 0, false
		}
		start = value
		if parts[1] != "" {
			end, err = parseNativeByte(parts[1])
			if err != nil || end < start {
				w.Header().Set("Content-Range", "bytes */"+formatUint(uint64(size)))
				w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
				return 0, 0, false
			}
			if end >= size {
				end = size - 1
			}
		}
	}
	return start, end, true
}

func parseNativeByte(value string) (int64, error) {
	if value == "" {
		return 0, errors.New("empty range")
	}
	var result int64
	for _, char := range value {
		if char < '0' || char > '9' {
			return 0, errors.New("invalid range")
		}
		if result > (1<<63-1)/10 {
			return 0, errors.New("range overflow")
		}
		result = result*10 + int64(char-'0')
	}
	return result, nil
}

func formatUint(value uint64) string { return strconv.FormatUint(value, 10) }
