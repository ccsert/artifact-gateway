package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := check(ctx, &http.Client{}, healthEndpoint(os.Getenv("REFERENCE_SCANNER_LISTEN_ADDRESS"))); err != nil {
		os.Exit(1)
	}
}

func healthEndpoint(address string) string {
	address = strings.TrimSpace(address)
	if address == "" {
		address = "127.0.0.1:18082"
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return "http://127.0.0.1:18082/livez"
	}
	return "http://" + net.JoinHostPort(host, port) + "/livez"
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
		return errors.New("reference scanner is not live")
	}
	return nil
}
