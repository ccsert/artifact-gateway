package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if len(os.Args) == 2 && os.Args[1] == "public-key" {
		if err := fetchPublicKey(ctx, &http.Client{}, publicKeyEndpoint(os.Getenv("REFERENCE_APT_SIGNER_LISTEN_ADDRESS")), os.Stdout); err != nil {
			os.Exit(1)
		}
		return
	}
	if err := check(ctx, &http.Client{}, healthEndpoint(os.Getenv("REFERENCE_APT_SIGNER_LISTEN_ADDRESS"))); err != nil {
		os.Exit(1)
	}
}

func publicKeyEndpoint(address string) string {
	return strings.TrimSuffix(healthEndpoint(address), "/healthz") + "/v1/public-key"
}

func healthEndpoint(address string) string {
	address = strings.TrimSpace(address)
	if address == "" {
		address = "127.0.0.1:18083"
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return "http://127.0.0.1:18083/healthz"
	}
	return "http://" + net.JoinHostPort(host, port) + "/healthz"
}

func fetchPublicKey(ctx context.Context, client *http.Client, endpoint string, output io.Writer) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "application/pgp-keys" {
		return errors.New("reference APT signer public key is unavailable")
	}
	_, err = io.Copy(output, io.LimitReader(response.Body, 1<<20))
	return err
}

func check(ctx context.Context, client *http.Client, endpoint string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusNoContent {
		return errors.New("reference APT signer is not healthy")
	}
	return nil
}
