package agctl

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// secretDestination delivers a one-time secret (an API key or a service
// account credential) to a new private file. The secret never reaches
// stdout, stderr, JSON, verbose output, or error text.
//
// prepare runs before the request: the destination must not exist, and a
// private (0600) temporary file is created next to it, which proves the
// directory is writable and keeps the final step on one filesystem. deliver
// runs after the server answered: it writes the secret to the temporary
// file, syncs it, and links it to the destination, which fails instead of
// overwriting a file that appeared in the meantime.
type secretDestination struct {
	path string
	temp *os.File
}

const secretOutputHint = "pass --secret-output with a path to a new file, for example --secret-output ./ci-token; the secret is written there with mode 0600 and never printed"

func prepareSecretDestination(path string) (*secretDestination, error) {
	if strings.TrimSpace(path) == "" {
		return nil, &UsageError{Message: "this command issues a one-time secret and needs --secret-output FILE", Hint: secretOutputHint}
	}
	clean := filepath.Clean(path)
	if absolute, err := filepath.Abs(clean); err == nil {
		clean = absolute
	}
	if _, err := os.Lstat(clean); err == nil {
		return nil, &UsageError{Message: fmt.Sprintf("--secret-output %s already exists", clean), Hint: "choose a new path; an existing file or link is never overwritten"}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, &UsageError{Message: fmt.Sprintf("cannot check --secret-output %s", clean), Hint: secretOutputHint}
	}
	dir := filepath.Dir(clean)
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return nil, &UsageError{Message: fmt.Sprintf("the directory of --secret-output %s does not exist", clean), Hint: "create the directory first"}
	}
	temp, err := os.CreateTemp(dir, ".agctl-secret-*")
	if err != nil {
		return nil, &UsageError{Message: fmt.Sprintf("cannot write a private file in %s", dir), Hint: "choose a directory you can write to"}
	}
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		_ = os.Remove(temp.Name())
		return nil, &UsageError{Message: fmt.Sprintf("cannot make a private file in %s", dir), Hint: "choose a directory on a filesystem with POSIX permissions"}
	}
	return &secretDestination{path: clean, temp: temp}, nil
}

// discard removes the temporary file when the secret was never issued.
func (d *secretDestination) discard() {
	if d == nil || d.temp == nil {
		return
	}
	_ = d.temp.Close()
	_ = os.Remove(d.temp.Name())
	d.temp = nil
}

// deliver writes the secret. Its errors never contain the secret.
func (d *secretDestination) deliver(secret string) error {
	defer d.discard()
	if _, err := d.temp.WriteString(secret + "\n"); err != nil {
		return fmt.Errorf("cannot write the secret to a private file next to %s", d.path)
	}
	if err := d.temp.Sync(); err != nil {
		return fmt.Errorf("cannot flush the secret to disk next to %s", d.path)
	}
	if err := d.temp.Close(); err != nil {
		return fmt.Errorf("cannot close the private file next to %s", d.path)
	}
	if err := os.Link(d.temp.Name(), d.path); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("%s appeared while the secret was issued and was not overwritten", d.path)
		}
		return fmt.Errorf("cannot place the secret at %s", d.path)
	}
	return nil
}

// secretDelivery is the safe summary of a one-time secret: where it went,
// never what it is.
type secretDelivery struct {
	Path      string `json:"path"`
	Mode      string `json:"mode"`
	Delivered bool   `json:"delivered"`
}

// undeliveredSecret reports an issued secret that could not be written. The
// object exists on the server; the caller must revoke it and issue again.
// Nothing is re-issued automatically.
func undeliveredSecret(cause error, data any, revoke string) error {
	return &resultError{
		err:  &deliveryError{message: cause.Error()},
		data: data,
		hint: "the secret was issued but not saved and cannot be shown again; revoke it with " + revoke + " and issue a new one",
	}
}

// deliveryError fails a command whose secret was issued but not delivered.
// It exits 1 with code secret_not_delivered.
type deliveryError struct{ message string }

func (e *deliveryError) Error() string { return e.message }
