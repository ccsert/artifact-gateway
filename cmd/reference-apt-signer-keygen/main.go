package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/artifact-gateway/artifact-gateway/internal/refaptsigner"
)

func main() {
	os.Exit(run(os.Getenv, os.Stdout, os.Stderr))
}

func run(getenv func(string) string, stdout, stderr io.Writer) int {
	path := strings.TrimSpace(getenv("REFERENCE_APT_SIGNER_KEY_FILE"))
	name := strings.TrimSpace(getenv("REFERENCE_APT_SIGNER_NAME"))
	comment := strings.TrimSpace(getenv("REFERENCE_APT_SIGNER_COMMENT"))
	email := strings.TrimSpace(getenv("REFERENCE_APT_SIGNER_EMAIL"))
	bits := 4096
	if raw := strings.TrimSpace(getenv("REFERENCE_APT_SIGNER_RSA_BITS")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "reference APT signer key configuration is invalid")
			return 2
		}
		bits = parsed
	}
	if strings.ContainsAny(name+comment+email, "\x00\r\n") || name == "" || email == "" || bits < 2048 || bits > 4096 {
		_, _ = fmt.Fprintln(stderr, "reference APT signer key configuration is invalid")
		return 2
	}
	entity, err := refaptsigner.LoadOrCreateEntity(context.Background(), refaptsigner.KeyOptions{
		Path: path, Name: name, Comment: comment, Email: email, RSABits: bits,
	})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "reference APT signer key could not be initialized")
		return 1
	}
	service, err := refaptsigner.NewService(entity, name+" <"+email+">")
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "reference APT signer public key could not be derived")
		return 1
	}
	if _, err = stdout.Write(service.PublicKey()); err != nil {
		_, _ = fmt.Fprintln(stderr, "reference APT signer public key output failed")
		return 1
	}
	return 0
}
