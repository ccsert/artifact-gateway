package snapshotimport

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/artifact-gateway/artifact-gateway/internal/capacityplan"
	"github.com/artifact-gateway/artifact-gateway/internal/objectstore"
	"github.com/artifact-gateway/artifact-gateway/internal/opsjson"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

// TargetSpec names existing credentials through environment references. It
// has no default runtime config, source endpoints or credential discovery.
type TargetSpec struct {
	TargetID       string `json:"targetId"`
	RepositoryID   string `json:"repositoryId"`
	Actor          string `json:"actor"`
	DatabaseURLEnv string `json:"databaseUrlEnv"`
	S3Endpoint     string `json:"s3Endpoint"`
	S3Bucket       string `json:"s3Bucket"`
	S3AccessKeyEnv string `json:"s3AccessKeyEnv"`
	S3SecretKeyEnv string `json:"s3SecretKeyEnv"`
}

var envName = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,127}$`)

func privateJSON(file string, max int64) ([]byte, error) {
	root, err := os.OpenRoot(filepath.Dir(file))
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	info, err := root.Lstat(filepath.Base(file))
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("private_spec_required")
	}
	return readBounded(root, filepath.Base(file), max)
}
func RunCLI(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	usage := "usage: gateway snapshot-import <verify|dry-run|apply> --bundle directory --manifest-sha256 sha256:... [--spec private-target.json] [--capacity-plan private-capacity.json]"
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, usage)
		return 2
	}
	action := args[0]
	if action != "verify" && action != "dry-run" && action != "apply" {
		_, _ = fmt.Fprintln(stderr, usage)
		return 2
	}
	flags := flag.NewFlagSet("snapshot-import", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	bundle := flags.String("bundle", "", "frozen offline bundle")
	digest := flags.String("manifest-sha256", "", "exact manifest digest")
	specPath := flags.String("spec", "", "explicit existing target")
	capPath := flags.String("capacity-plan", "", "bound capacity evidence")
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, _ = fmt.Fprintln(stdout, usage)
			return 0
		}
		_, _ = fmt.Fprintln(stderr, "invalid snapshot import arguments")
		return 2
	}
	if flags.NArg() != 0 || *bundle == "" || *digest == "" || (action != "verify" && *specPath == "") || (action == "apply" && *capPath == "") || (action == "verify" && (*specPath != "" || *capPath != "")) {
		_, _ = fmt.Fprintln(stderr, usage)
		return 2
	}
	prepared, rejections, err := Prepare(ctx, *bundle, *digest)
	report := NewReport(prepared)
	if prepared != nil {
		defer func() { _ = prepared.Close() }()
	}
	report.Rejected = rejections
	emit := func(code int) int {
		if json.NewEncoder(stdout).Encode(report.withCounts()) != nil {
			_, _ = fmt.Fprintln(stderr, "snapshot import report could not be written")
			return 2
		}
		return code
	}
	if err != nil {
		report.Status = "rejected"
		if len(rejections) == 0 {
			report.Rejected = []Rejection{{Reason: err.Error()}}
		}
		return emit(1)
	}
	if action == "verify" {
		report.Status = "source-verified"
		return emit(0)
	}
	data, err := privateJSON(*specPath, 1<<20)
	var spec TargetSpec
	if err != nil || opsjson.Decode(data, &spec) != nil || !opaque.MatchString(spec.TargetID) || !opaque.MatchString(spec.Actor) || !envName.MatchString(spec.DatabaseURLEnv) || !envName.MatchString(spec.S3AccessKeyEnv) || !envName.MatchString(spec.S3SecretKeyEnv) {
		_, _ = fmt.Fprintln(stderr, "invalid private target spec")
		return 2
	}
	if _, err := uuid.Parse(spec.RepositoryID); err != nil {
		_, _ = fmt.Fprintln(stderr, "invalid target repository identity")
		return 2
	}
	report.TargetID = spec.TargetID
	report.RepositoryID = spec.RepositoryID
	// Complete source validation precedes connecting; bound capacity validation
	// and target preflight precede any target mutation.
	databaseURL := os.Getenv(spec.DatabaseURLEnv)
	access := os.Getenv(spec.S3AccessKeyEnv)
	secret := os.Getenv(spec.S3SecretKeyEnv)
	if databaseURL == "" || access == "" || secret == "" {
		_, _ = fmt.Fprintln(stderr, "existing target credential references unavailable")
		return 2
	}
	objects, err := objectstore.NewRustFSStore(spec.S3Endpoint, access, secret, spec.S3Bucket)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "invalid target object store spec")
		return 2
	}
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		report.Status = "failed"
		report.Rejected = []Rejection{{Reason: "target_database_unavailable"}}
		return emit(1)
	}
	defer func() { _ = store.Close() }()
	databaseIdentity, err := store.MavenImportDatabaseIdentity(ctx)
	if err != nil {
		report.Status = "failed"
		report.Rejected = []Rejection{{Reason: "target_database_identity_unavailable"}}
		return emit(1)
	}
	binding, err := TargetBinding(databaseIdentity, spec.S3Endpoint, spec.S3Bucket)
	if err != nil {
		return 2
	}
	report.TargetBinding = binding
	if err := objects.CheckBucket(ctx); err != nil {
		report.Status = "failed"
		report.Rejected = []Rejection{{Reason: "target_bucket_unavailable"}}
		return emit(1)
	}
	plans, err := prepared.References(spec.RepositoryID, spec.TargetID, spec.Actor, binding)
	if err != nil {
		return 2
	}
	var capacity []capacityplan.Plan
	if *capPath != "" {
		data, err := privateJSON(*capPath, 8<<20)
		var plan capacityplan.Plan
		if err != nil || opsjson.Decode(data, &plan) != nil || ValidateCapacity(plan, plans, prepared.Digest, binding) != nil {
			report.Status = "rejected"
			report.Rejected = []Rejection{{Reason: "capacity_preflight_not_sufficient_or_bound"}}
			return emit(1)
		}
		capacity = append(capacity, plan)
	}
	report, err = Run(ctx, prepared, store, objects, spec.RepositoryID, spec.TargetID, spec.Actor, binding, action == "apply", capacity...)
	if action == "dry-run" && err == nil {
		report.CapacityReferences = CapacityReferences(plans)
	}
	if err != nil {
		return emit(1)
	}
	return emit(0)
}

// TargetBinding pins the actual PG cluster/database and normalized S3 pair.
// It contains no DSN, key, username or secret; credential rotation is allowed.
func TargetBinding(databaseIdentity, endpoint, bucket string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.User != nil || u.Host == "" || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") || (u.Path != "" && u.Path != "/") || databaseIdentity == "" || bucket == "" {
		return "", errors.New("invalid_target_binding")
	}
	u.Host = strings.ToLower(u.Host)
	u.Path = ""
	b, _ := json.Marshal([]string{databaseIdentity, u.String(), bucket})
	return sum(b), nil
}
