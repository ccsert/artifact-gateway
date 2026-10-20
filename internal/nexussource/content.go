package nexussource

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
)

// AssetRequest contains a fixed manifest identity, never a caller-selected URL.
type AssetRequest struct {
	Format    string
	Path      string
	Size      int64
	Validator string
}
type Content struct {
	Body      io.ReadCloser
	Validator string
	MediaType string
}
type contentBody struct {
	io.Reader
	io.Closer
	ctx  context.Context
	done context.CancelFunc
}

func (b *contentBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	if sourceTimedOut(b.ctx, err) {
		return n, fail("source_timeout")
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return n, fail("source_download_failed")
	}
	return n, err
}

func (b *contentBody) Close() error { err := b.Closer.Close(); b.done(); return err }

// OpenAsset uses exactly the same encrypted source, HTTPS, pinned egress and
// redirect boundary as discovery. Each request is bounded by size and time.
func (s *Service) OpenAsset(ctx context.Context, id, version, repository string, in AssetRequest) (*Content, error) {
	canonical, wire, reason := assetRoute(in.Format, in.Path)
	if !sourceName.MatchString(repository) || reason != "" || canonical != in.Path || in.Size < 0 || in.Size > 1<<30 {
		return nil, fail("invalid_request")
	}
	remote, source, done, err := s.connect(ctx, id)
	if err != nil {
		return nil, err
	}
	bad := func(code string) (*Content, error) { done(); return nil, fail(code) }
	if source.Version != version {
		return bad("source_version_changed")
	}
	u := url.URL{Scheme: remote.base.Scheme, Host: remote.base.Host, Path: remote.base.Path + "/repository/" + repository + "/" + wire}
	req, err := http.NewRequestWithContext(remote.ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return bad("source_invalid_response")
	}
	if strings.HasPrefix(in.Validator, "etag:") {
		req.Header.Set("If-Match", strings.TrimPrefix(in.Validator, "etag:"))
	}
	if in.Format == "oci" && strings.Contains(wire, "/manifests/") {
		req.Header.Set("Accept", "application/vnd.oci.image.index.v1+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.docker.distribution.manifest.v2+json")
	}
	if remote.credential.Username != "" {
		req.SetBasicAuth(remote.credential.Username, remote.credential.Password)
	}
	resp, err := remote.client.Do(req)
	if err != nil {
		requestErr := sourceRequestError(remote.ctx, err)
		done()
		return nil, requestErr
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		switch resp.StatusCode {
		case 404:
			return bad("source_missing")
		case 412:
			return bad("source_changed")
		case 401, 403:
			return bad("source_unauthorized")
		}
		return bad("source_download_failed")
	}
	if resp.ContentLength >= 0 && resp.ContentLength != in.Size {
		_ = resp.Body.Close()
		return bad("source_changed")
	}
	validator := ""
	etag := resp.Header.Get("ETag")
	if strings.HasPrefix(etag, "\"") && strings.HasSuffix(etag, "\"") && len(etag) <= 512 && !strings.ContainsAny(etag, "\r\n\x00") {
		validator = "etag:" + etag
	}
	if strings.HasPrefix(in.Validator, "etag:") && validator != in.Validator {
		_ = resp.Body.Close()
		return bad("source_changed")
	}
	mediaType := resp.Header.Get("Content-Type")
	if in.Format == "oci" && mediaType != "" {
		parsed, _, parseErr := mime.ParseMediaType(mediaType)
		if parseErr != nil {
			_ = resp.Body.Close()
			return bad("source_invalid_response")
		}
		mediaType = strings.ToLower(parsed)
	}
	return &Content{Body: &contentBody{Reader: io.LimitReader(resp.Body, in.Size+1), Closer: resp.Body, ctx: remote.ctx, done: done}, Validator: validator, MediaType: mediaType}, nil
}
