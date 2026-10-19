package agctl

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"

	"github.com/google/uuid"
)

type rawUploadProgress struct {
	id     string
	offset int64
}

// readRawUploadProgress verifies the server's native Location against the
// already authorized target. Location never supplies an origin or a new path
// to the HTTP client, so a forged response cannot redirect credentials.
func readRawUploadProgress(response *apiResponse, client *apiClient, target, expectedID string, maxOffset int64) (rawUploadProgress, error) {
	invalid := func() (rawUploadProgress, error) {
		return rawUploadProgress{}, incompleteResponse(response, client, "Raw resumable upload")
	}
	if len(response.Header.Values("Location")) != 1 || len(response.Header.Values("Upload-Offset")) != 1 {
		return invalid()
	}
	location, err := url.Parse(response.Header.Get("Location"))
	if err != nil || location.IsAbs() || location.Host != "" || location.Fragment != "" || location.EscapedPath() != target {
		return invalid()
	}
	query, err := url.ParseQuery(location.RawQuery)
	if err != nil || len(query) != 1 || len(query["uploadId"]) != 1 {
		return invalid()
	}
	id := query.Get("uploadId")
	parsed, err := uuid.Parse(id)
	if err != nil || parsed == uuid.Nil || parsed.String() != id || (expectedID != "" && id != expectedID) {
		return invalid()
	}
	rawOffset := response.Header.Get("Upload-Offset")
	offset, err := strconv.ParseInt(rawOffset, 10, 64)
	if err != nil || strconv.FormatInt(offset, 10) != rawOffset || offset < 0 || offset > maxOffset {
		return invalid()
	}
	return rawUploadProgress{id: id, offset: offset}, nil
}

func rawUploadStatus(ctx context.Context, client *apiClient, target, id string, size int64) (rawUploadProgress, error) {
	response, err := client.do(ctx, http.MethodGet, target, requestOptions{idempotent: true, query: url.Values{"uploadId": {id}}})
	if err != nil {
		return rawUploadProgress{}, err
	}
	if response.StatusCode >= 300 {
		return rawUploadProgress{}, parseAPIError(response, client.token)
	}
	if response.StatusCode != http.StatusNoContent {
		return rawUploadProgress{}, incompleteResponse(response, client, "Raw upload status")
	}
	return readRawUploadProgress(response, client, target, id, size)
}

func recordRawProgress(result *objectResult, target string, progress rawUploadProgress) {
	result.UploadID = progress.id
	offset := progress.offset
	result.UploadOffset = &offset
	result.ReadBack = target + "?" + url.Values{"uploadId": {progress.id}}.Encode()
}

// rawChunk opens a bounded extent of the caller's file for one request.
// The final SHA-256 verification still rejects a file changed between chunks.
func rawChunk(object localObject, offset, length int64) func() (io.ReadCloser, error) {
	return func() (io.ReadCloser, error) {
		file, err := os.Open(object.file)
		if err != nil {
			return nil, err
		}
		return struct {
			io.Reader
			io.Closer
		}{Reader: io.NewSectionReader(file, offset, length), Closer: file}, nil
	}
}

func putResumableRawObject(ctx context.Context, client *apiClient, target string, object localObject, chunkSize int64, id string, result *objectResult) error {
	progress := rawUploadProgress{id: id}
	if id != "" {
		result.UploadID = id
		result.ReadBack = target + "?" + url.Values{"uploadId": {id}}.Encode()
	}
	if id == "" {
		// Creation has no idempotency key. Never repeat a lost start response.
		response, err := client.do(ctx, http.MethodPost, target, requestOptions{query: url.Values{"resumable": {"1"}}})
		if err != nil {
			return err
		}
		if response.StatusCode >= 300 {
			return parseAPIError(response, client.token)
		}
		if response.StatusCode != http.StatusCreated {
			return incompleteResponse(response, client, "Raw upload creation")
		}
		progress, err = readRawUploadProgress(response, client, target, "", object.size)
		if err != nil {
			return err
		}
		recordRawProgress(result, target, progress)
		if progress.offset != 0 {
			return incompleteResponse(response, client, "new Raw upload offset")
		}
	} else {
		var err error
		progress, err = rawUploadStatus(ctx, client, target, id, object.size)
		if err != nil {
			return err
		}
	}
	recordRawProgress(result, target, progress)
	for progress.offset < object.size {
		length := min(chunkSize, object.size-progress.offset)
		next, err := appendRawChunk(ctx, client, target, object, progress, length)
		if err != nil {
			return err
		}
		progress = next
		recordRawProgress(result, target, progress)
	}
	result.ReadBack = target
	response, err := client.do(ctx, http.MethodPut, target, requestOptions{
		query:  url.Values{"uploadId": {progress.id}, "complete": {"1"}},
		header: http.Header{"Digest": {"sha-256=" + base64.StdEncoding.EncodeToString(object.raw)}},
	})
	if err == nil && response.StatusCode >= 300 {
		err = parseAPIError(response, client.token)
	}
	if err == nil && (response.StatusCode != http.StatusCreated || normalizeRawETag(response.Header.Get("ETag")) != object.digest || response.Header.Get("Digest") != "sha-256="+base64.StdEncoding.EncodeToString(object.raw)) {
		err = incompleteResponse(response, client, "Raw upload completion")
	}
	if err != nil {
		// A closed upload may answer 404 on replay. Read the immutable target
		// bytes' identity instead of blindly committing or creating again.
		if outcomeUnknown(err) && rawTargetMatches(ctx, client, target, object) {
			result.ConfirmedBy = "readBack"
			return nil
		}
		return err
	}
	result.ConfirmedBy = "response"
	return nil
}

func appendRawChunk(ctx context.Context, client *apiClient, target string, object localObject, progress rawUploadProgress, length int64) (rawUploadProgress, error) {
	for attempt := 0; ; attempt++ {
		response, err := client.do(ctx, http.MethodPatch, target, requestOptions{
			query: url.Values{"uploadId": {progress.id}}, stream: rawChunk(object, progress.offset, length), length: length,
			header: http.Header{"Upload-Offset": {strconv.FormatInt(progress.offset, 10)}},
		})
		if err == nil && response.StatusCode >= 300 {
			err = parseAPIError(response, client.token)
		}
		if err == nil {
			next, parseErr := readRawUploadProgress(response, client, target, progress.id, object.size)
			if response.StatusCode == http.StatusNoContent && parseErr == nil && next.offset == progress.offset+length {
				return next, nil
			}
			err = incompleteResponse(response, client, "Raw chunk offset")
		}
		if !outcomeUnknown(err) {
			return progress, err
		}
		observed, readErr := rawUploadStatus(ctx, client, target, progress.id, object.size)
		if readErr != nil {
			return progress, err // preserve the original uncertain write
		}
		if observed.offset == progress.offset+length {
			return observed, nil
		}
		if observed.offset != progress.offset || attempt >= client.maxRetries {
			return progress, err
		}
		// A fresh status has serialized behind the preceding PATCH. Retrying
		// the same chunk with the same offset cannot append it twice.
		if err := client.sleep(ctx, clientRetryBaseWait); err != nil {
			return progress, &ConnectionError{Message: "Raw upload stopped before the next confirmed chunk", Hint: "resume using the returned uploadId and the unchanged source file"}
		}
	}
}

func rawTargetMatches(ctx context.Context, client *apiClient, target string, object localObject) bool {
	response, err := client.do(ctx, http.MethodHead, target, requestOptions{idempotent: true})
	return err == nil && response.StatusCode == http.StatusOK && normalizeRawETag(response.Header.Get("ETag")) == object.digest &&
		response.Header.Get("Content-Length") == strconv.FormatInt(object.size, 10) && response.Header.Get("Digest") == "sha-256="+base64.StdEncoding.EncodeToString(object.raw)
}
