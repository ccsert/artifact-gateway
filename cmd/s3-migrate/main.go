package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/objectstore"
)

const usage = "usage: s3-migrate <inventory|copy|mirror|verify> [options]"

type endpointConfig struct {
	endpoint  string
	bucket    string
	accessKey string
	secretKey string
}

type endpointFactory func(endpointConfig) (objectstore.BucketMigrationEndpoint, error)

type commandEvidence struct {
	Operation string `json:"operation"`
	objectstore.BucketMigrationReport
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr, nil); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "s3-migrate: %v\n", err)
		os.Exit(1)
	}
}

func run(
	ctx context.Context,
	args []string,
	getenv func(string) string,
	stdout io.Writer,
	stderr io.Writer,
	newEndpoint endpointFactory,
) error {
	if len(args) == 0 {
		return errors.New(usage)
	}
	operation := args[0]
	if operation != "inventory" && operation != "copy" && operation != "mirror" && operation != "verify" {
		return fmt.Errorf("unknown operation %q", operation)
	}

	flags := flag.NewFlagSet("s3-migrate "+operation, flag.ContinueOnError)
	flags.SetOutput(stderr)
	sourceEndpoint := flags.String("source-endpoint", "", "source S3-compatible endpoint URL")
	sourceBucket := flags.String("source-bucket", "", "source bucket")
	targetEndpoint := flags.String("target-endpoint", "", "target S3-compatible endpoint URL")
	targetBucket := flags.String("target-bucket", "", "target bucket")
	deleteTargetExtras := flags.Bool("delete-target-extras", false, "confirm deletion of target-only keys during a frozen mirror")
	timeout := flags.Duration("timeout", 2*time.Hour, "maximum migration duration")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments: %s", strings.Join(flags.Args(), " "))
	}
	if *sourceEndpoint == "" || *sourceBucket == "" {
		return errors.New("--source-endpoint and --source-bucket are required")
	}
	needsTarget := operation == "copy" || operation == "mirror" || operation == "verify"
	if needsTarget && (*targetEndpoint == "" || *targetBucket == "") {
		return errors.New("--target-endpoint and --target-bucket are required for copy and verify")
	}
	if needsTarget && *sourceEndpoint == *targetEndpoint && *sourceBucket == *targetBucket {
		return errors.New("source and target must be different")
	}
	if *timeout <= 0 {
		return errors.New("--timeout must be greater than zero")
	}
	if operation == "mirror" && !*deleteTargetExtras {
		return errors.New("mirror requires --delete-target-extras and both write paths must be frozen")
	}
	if operation != "mirror" && *deleteTargetExtras {
		return errors.New("--delete-target-extras is only valid for mirror")
	}

	sourceAccessKey := getenv("S3_MIGRATION_SOURCE_ACCESS_KEY")
	sourceSecretKey := getenv("S3_MIGRATION_SOURCE_SECRET_KEY")
	targetAccessKey := getenv("S3_MIGRATION_TARGET_ACCESS_KEY")
	targetSecretKey := getenv("S3_MIGRATION_TARGET_SECRET_KEY")
	if sourceAccessKey == "" || sourceSecretKey == "" ||
		(needsTarget && (targetAccessKey == "" || targetSecretKey == "")) {
		if needsTarget {
			return errors.New("S3_MIGRATION_SOURCE_ACCESS_KEY, S3_MIGRATION_SOURCE_SECRET_KEY, S3_MIGRATION_TARGET_ACCESS_KEY and S3_MIGRATION_TARGET_SECRET_KEY are required")
		}
		return errors.New("S3_MIGRATION_SOURCE_ACCESS_KEY and S3_MIGRATION_SOURCE_SECRET_KEY are required")
	}

	if newEndpoint == nil {
		newEndpoint = func(config endpointConfig) (objectstore.BucketMigrationEndpoint, error) {
			return objectstore.NewS3BucketMigrationEndpoint(config.endpoint, config.accessKey, config.secretKey, config.bucket)
		}
	}
	source, err := newEndpoint(endpointConfig{
		endpoint:  *sourceEndpoint,
		bucket:    *sourceBucket,
		accessKey: sourceAccessKey,
		secretKey: sourceSecretKey,
	})
	if err != nil {
		return fmt.Errorf("configure source: %w", err)
	}

	operationCtx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	var report objectstore.BucketMigrationReport
	switch operation {
	case "inventory":
		report, err = objectstore.InventoryBucket(operationCtx, source)
	case "copy", "mirror", "verify":
		var target objectstore.BucketMigrationEndpoint
		target, err = newEndpoint(endpointConfig{
			endpoint:  *targetEndpoint,
			bucket:    *targetBucket,
			accessKey: targetAccessKey,
			secretKey: targetSecretKey,
		})
		if err == nil {
			switch operation {
			case "copy":
				report, err = objectstore.CopyBucket(operationCtx, source, target)
			case "mirror":
				report, err = objectstore.MirrorBucket(operationCtx, source, target)
			case "verify":
				report, err = objectstore.VerifyBucket(operationCtx, source, target)
			}
		}
	}
	if err != nil {
		return fmt.Errorf("%s: %w", operation, err)
	}
	if err := json.NewEncoder(stdout).Encode(commandEvidence{Operation: operation, BucketMigrationReport: report}); err != nil {
		return fmt.Errorf("write evidence: %w", err)
	}
	return nil
}
