package raw

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestParsePathRequiresCanonicalRawResource(t *testing.T) {
	group, resource, ok := ParsePath("/raw/releases/com/example/app.jar")
	if !ok || group != "releases" || resource != "com/example/app.jar" {
		t.Fatalf("parsed group=%q resource=%q ok=%t", group, resource, ok)
	}
	if _, _, ok := ParsePath("/raw/releases/../private"); ok {
		t.Fatal("non-canonical resource was accepted")
	}
}

func TestValidChecksumAcceptsOnlyLowercaseCanonicalHex(t *testing.T) {
	valid := make([]byte, 64)
	for index := range valid {
		valid[index] = 'a'
	}
	if !ValidChecksum("artifact.sha256", valid) {
		t.Fatal("valid sha256 checksum was rejected")
	}
	valid[0] = 'A'
	if ValidChecksum("artifact.sha256", valid) {
		t.Fatal("uppercase checksum was accepted")
	}
}

func TestServeContentAppliesSingleRange(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/raw/releases/artifact", nil)
	request.Header.Set("Range", "bytes=2-4")
	response := httptest.NewRecorder()

	result := ServeContent(response, request, "artifact", Content{Body: []byte("abcdef"), Digest: "bef57ec7f53a6d40beb640a780a639c83bc29ac8a9816f1f6c5c6dcd93c4721", ContentType: "application/octet-stream"})
	if response.Code != http.StatusPartialContent || response.Body.String() != "cde" || result.Status != http.StatusPartialContent || result.Bytes != 3 {
		t.Fatalf("response=%d body=%q result=%+v", response.Code, response.Body.String(), result)
	}
}

func TestServeContentRejectsOverflowingRange(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/raw/releases/artifact", nil)
	request.Header.Set("Range", "bytes=9223372036854775808-")
	response := httptest.NewRecorder()

	result := ServeContent(response, request, "artifact", Content{Body: []byte("abcdef"), ContentType: "application/octet-stream"})
	if response.Code != http.StatusRequestedRangeNotSatisfiable || response.Header().Get("Content-Range") != "bytes */6" || result.Status != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("response=%d content-range=%q result=%+v", response.Code, response.Header().Get("Content-Range"), result)
	}
}

func TestServeContentIgnoresRangeWhenIfRangeDoesNotMatch(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/raw/releases/artifact", nil)
	request.Header.Set("Range", "bytes=2-4")
	request.Header.Set("If-Range", `"different"`)
	response := httptest.NewRecorder()

	result := ServeContent(response, request, "artifact", Content{Body: []byte("abcdef"), Digest: "bef57ec7f53a6d40beb640a780a639c83bc29ac8a9816f1f6c5c6dcd93c4721", ContentType: "application/octet-stream"})
	if response.Code != http.StatusOK || response.Body.String() != "abcdef" || result.Status != http.StatusOK || result.Bytes != 6 {
		t.Fatalf("response=%d body=%q result=%+v", response.Code, response.Body.String(), result)
	}
}

func TestMemberProxyAllowedRejectsUnconfiguredOrPrivateTargets(t *testing.T) {
	if !MemberProxyAllowed(repository.Member{Endpoint: "https://proxy.example", AllowedHosts: []string{"proxy.example"}}) {
		t.Fatal("configured public proxy was rejected")
	}
	if MemberProxyAllowed(repository.Member{Endpoint: "https://127.0.0.1", AllowedHosts: []string{"127.0.0.1"}}) {
		t.Fatal("private proxy target was accepted")
	}
}
