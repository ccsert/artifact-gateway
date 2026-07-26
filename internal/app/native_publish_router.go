package app

import (
	"net/http"
	"strings"
)

type nativePublishRouter struct {
	maven nativeMavenHandler
	conan nativeConanPublishHandler
}

func (h nativePublishRouter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.Contains(r.URL.Path, "/conan-publish-sessions") {
		h.conan.ServeHTTP(w, r)
		return
	}
	h.maven.ServeHTTP(w, r)
}
