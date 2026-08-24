package main

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/buildinfo"
)

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "version" || os.Args[1] == "--version") {
		build := buildinfo.Read()
		fmt.Printf("artifact-gateway-healthcheck %s (revision %s, %s)\n", build.Version, build.Revision, build.GoVersion)
		return
	}
	client := http.Client{Timeout: 2 * time.Second}
	response, err := client.Get("http://127.0.0.1:8080/readyz")
	if err != nil {
		os.Exit(1)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusNoContent {
		os.Exit(1)
	}
}
