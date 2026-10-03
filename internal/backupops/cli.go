package backupops

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/backupmanifest"
	"github.com/artifact-gateway/artifact-gateway/internal/opsjson"
)

// RunCLI executes real transfers only with an explicit private operational spec.
// It does not load the server .env, mint credentials, or use default endpoints.
func RunCLI(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "usage: gateway backup <export|restore> --spec private.json --bundle directory")
		return 2
	}
	action := args[0]
	if action != "export" && action != "restore" {
		_, _ = fmt.Fprintln(stderr, "invalid backup action")
		return 2
	}
	flags := flag.NewFlagSet("backup "+action, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	specPath := flags.String("spec", "", "explicit private source or new-target JSON")
	directory := flags.String("bundle", "", "new export directory or existing backup directory")
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, _ = fmt.Fprintln(stdout, "usage: gateway backup <export|restore> --spec private.json --bundle directory")
			return 0
		}
		_, _ = fmt.Fprintln(stderr, "invalid backup arguments")
		return 2
	}
	if *specPath == "" || *directory == "" || flags.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "backup requires explicit --spec and --bundle")
		return 2
	}
	data, err := privateSpec(*specPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backup spec must be a private bounded regular JSON file")
		return 2
	}
	var result TransferReport
	if action == "export" {
		var spec SourceSpec
		if opsjson.Decode(data, &spec) != nil {
			_, _ = fmt.Fprintln(stderr, "invalid source spec")
			return 2
		}
		source, sourceErr := newSource(ctx, spec)
		if sourceErr != nil {
			result = report("")
			result.Reason = "source_spec_or_release_invalid"
			err = sourceErr
		} else {
			result, err = Export(ctx, source, *directory, source.spec.Release, source.writers(), spec.BackupID)
		}
	} else {
		var spec TargetSpec
		if opsjson.Decode(data, &spec) != nil {
			_, _ = fmt.Fprintln(stderr, "invalid isolated target spec")
			return 2
		}
		result, err = RestoreNew(ctx, *directory, spec)
	}
	if json.NewEncoder(stdout).Encode(result) != nil {
		_, _ = fmt.Fprintln(stderr, "backup report could not be written")
		return 2
	}
	if err != nil {
		return 1
	}
	return 0
}

func privateSpec(path string) ([]byte, error) {
	dir, name := filepath.Dir(path), filepath.Base(path)
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	return readBounded(root, name, 1<<20)
}

// RestoreNew validates integrity, actual approved software/schema and PG dump
// before creating any target data resource. Successful targets remain isolated;
// failures clean only resources created and proven owned by this invocation.
func RestoreNew(ctx context.Context, directory string, spec TargetSpec) (TransferReport, error) {
	m, err := LoadManifest(directory)
	r := report(m.BackupID)
	if err != nil {
		return fail(r, "bundle_input_invalid")
	}
	checked := backupmanifest.Verify(ctx, directory, m)
	if checked.Integrity != backupmanifest.Verified || checked.WriterEvidence != "declared" || m.Metadata == nil {
		return fail(r, "bundle_input_invalid")
	}
	if validateTargetSpec(spec) != nil || spec.Docker.Validate(ctx) != nil {
		return fail(r, "explicit_local_isolated_target_required")
	}
	release, err := spec.Docker.ObserveRelease(ctx, spec.Release)
	if err != nil {
		return fail(r, "release_schema_mismatch")
	}
	spec.Release = release
	root, err := os.OpenRoot(directory)
	if err != nil {
		return fail(r, "bundle_input_invalid")
	}
	defer func() { _ = root.Close() }()
	ledger, err := readBounded(root, m.Schema.File.Path, 64<<20)
	if err != nil || VerifyRelease(m, release, strings.NewReader(string(ledger))) != nil {
		return fail(r, "release_schema_mismatch")
	}
	dump, err := checkedFile(root, m.Database.File)
	if err != nil {
		return fail(r, "database_input_invalid")
	}
	err = spec.Docker.ValidateDump(ctx, dump)
	_ = dump.Close()
	if err != nil {
		if errors.Is(err, errCleanupIncomplete) {
			return fail(r, "archive_validation_cleanup_incomplete")
		}
		return fail(r, "database_input_invalid")
	}
	target, err := NewTarget(ctx, spec)
	if err != nil {
		if errors.Is(err, errCleanupIncomplete) {
			return fail(r, "target_creation_cleanup_incomplete")
		}
		return fail(r, "isolated_target_creation_failed")
	}
	result, restoreErr := Restore(ctx, directory, release, target)
	if restoreErr == nil {
		endpoint, startErr := target.StartGateway(ctx)
		if startErr != nil || waitGateway(ctx, endpoint) != nil {
			result.Status = "failed"
			result.Reason = "restored_gateway_not_ready"
			restoreErr = errors.New(result.Reason)
		} else {
			result.Readiness = "verified"
			result.TargetProject = spec.Project
			restoreErr = runReadChecks(ctx, endpoint, spec, &result)
			if restoreErr != nil {
				result.Status = "failed"
				result.Reason = "semantic_readback_failed"
			}
		}
	}
	if restoreErr != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if target.Cleanup(cleanupCtx) != nil {
			result.Reason = "restore_failed_cleanup_incomplete"
		}
		return result, restoreErr
	}
	return result, nil
}

func waitGateway(ctx context.Context, endpoint string) error {
	client := &http.Client{Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	deadline := time.Now().Add(30 * time.Second)
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/readyz", nil)
		if err != nil {
			return err
		}
		response, err := client.Do(request)
		if err == nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
			_ = response.Body.Close()
			if response.StatusCode == 204 {
				return nil
			}
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Now().After(deadline) {
			return errors.New("readiness deadline exceeded")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}
