package agctl

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"

	adminopenapi "github.com/ccsert/artifact-gateway/internal/admin/openapi"
	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

const (
	publishSessionsPath = managementPrefix + "/publish-sessions"
	maxRawObjectBytes   = 1 << 30
	maxPublishParallel  = 16
)

// localObject is one file to publish with the digest the server must
// confirm.
type localObject struct {
	file   string
	remote string
	size   int64
	digest string // sha256:<hex>
	raw    []byte // the digest bytes, for the Digest header
}

func digestFile(file string) (localObject, error) {
	handle, err := os.Open(file)
	if err != nil {
		return localObject{}, &UsageError{Message: fmt.Sprintf("cannot read %s", file), Hint: "check the path and its permissions"}
	}
	defer func() { _ = handle.Close() }()
	info, err := handle.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return localObject{}, &UsageError{Message: fmt.Sprintf("%s is not a regular file", file), Hint: "publish regular files; links and devices are not followed"}
	}
	hash := sha256.New()
	size, err := io.Copy(hash, handle)
	if err != nil {
		return localObject{}, &UsageError{Message: fmt.Sprintf("cannot read %s", file), Hint: "check the file"}
	}
	sum := hash.Sum(nil)
	return localObject{file: file, size: size, digest: "sha256:" + hex.EncodeToString(sum), raw: sum}, nil
}

func (o localObject) open() func() (io.ReadCloser, error) {
	return func() (io.ReadCloser, error) { return os.Open(o.file) }
}

// objectResult is the outcome of one object in a batch upload.
type objectResult struct {
	Path string `json:"path"`
	// Coordinate is the canonical Raw coordinate (each segment escaped like
	// Go's url.PathEscape), the form search and browse report.
	Coordinate   string `json:"coordinate,omitempty"`
	File         string `json:"file"`
	Size         int64  `json:"size"`
	Digest       string `json:"digest"`
	Status       string `json:"status"` // uploaded, skipped, planned, failed, unknown
	Error        string `json:"error,omitempty"`
	Code         string `json:"code,omitempty"`
	UploadID     string `json:"uploadId,omitempty"`
	UploadOffset *int64 `json:"uploadOffset,omitempty"`
	ReadBack     string `json:"readBack,omitempty"`
	ConfirmedBy  string `json:"confirmedBy,omitempty"`
}

// validRawPath rejects paths a Raw repository cannot address safely.
func validRawPath(value string) bool {
	if value == "" || strings.HasPrefix(value, "/") || strings.HasSuffix(value, "/") || strings.ContainsAny(value, "\x00\r\n\\") {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func rawObjectPath(repository, objectPath string) string {
	return "/raw/" + url.PathEscape(repository) + "/" + rawCoordinate(objectPath)
}

// rawCoordinate is a Raw path's canonical coordinate: every segment escaped
// like Go's url.PathEscape, as the Gateway stores and reports it.
func rawCoordinate(objectPath string) string {
	segments := strings.Split(objectPath, "/")
	for index, segment := range segments {
		segments[index] = url.PathEscape(segment)
	}
	return strings.Join(segments, "/")
}

// collectRawObjects maps a file or a directory tree to repository paths.
// Directories are walked without following links.
func collectRawObjects(source, remote string) ([]localObject, error) {
	info, err := os.Lstat(source)
	if err != nil {
		return nil, &UsageError{Message: fmt.Sprintf("cannot read %s", source), Hint: "pass an existing file or directory"}
	}
	if info.Mode().IsRegular() {
		if remote == "" {
			remote = filepath.Base(source)
		}
		if !validRawPath(remote) {
			return nil, &UsageError{Message: fmt.Sprintf("invalid repository path %q", remote), Hint: "use a relative path such as releases/app.tar.gz without '..' segments"}
		}
		object, err := digestFile(source)
		if err != nil {
			return nil, err
		}
		object.remote = remote
		return []localObject{object}, nil
	}
	if !info.IsDir() {
		return nil, &UsageError{Message: fmt.Sprintf("%s is neither a file nor a directory", source), Hint: "links are not followed"}
	}
	if remote != "" && !validRawPath(remote) {
		return nil, &UsageError{Message: fmt.Sprintf("invalid repository prefix %q", remote), Hint: "use a relative prefix such as releases/1.0"}
	}
	var objects []localObject
	err = filepath.WalkDir(source, func(file string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !entry.Type().IsRegular() {
			return nil
		}
		relative, err := filepath.Rel(source, file)
		if err != nil {
			return err
		}
		objectPath := filepath.ToSlash(relative)
		if remote != "" {
			objectPath = path.Join(remote, objectPath)
		}
		if !validRawPath(objectPath) {
			return &UsageError{Message: fmt.Sprintf("%s maps to an invalid repository path %q", file, objectPath), Hint: "rename the file"}
		}
		object, err := digestFile(file)
		if err != nil {
			return err
		}
		object.remote = objectPath
		objects = append(objects, object)
		return nil
	})
	if err != nil {
		var usage *UsageError
		if errors.As(err, &usage) {
			return nil, err
		}
		return nil, &UsageError{Message: fmt.Sprintf("cannot walk %s", source), Hint: "check the directory permissions"}
	}
	if len(objects) == 0 {
		return nil, &UsageError{Message: fmt.Sprintf("%s contains no regular files", source), Hint: "nothing to publish"}
	}
	sort.Slice(objects, func(i, j int) bool { return objects[i].remote < objects[j].remote })
	return objects, nil
}

// remoteDigest reads a Raw object's digest without its body. ok is false
// when the object does not exist.
func remoteDigest(ctx context.Context, client *apiClient, objectPath string) (string, bool, error) {
	response, err := client.do(ctx, http.MethodHead, objectPath, requestOptions{idempotent: true})
	if err != nil {
		return "", false, err
	}
	switch {
	case response.StatusCode == http.StatusNotFound:
		return "", false, nil
	case response.StatusCode >= 300:
		return "", false, parseAPIError(response, client.token)
	}
	return normalizeRawETag(response.Header.Get("ETag")), true, nil
}

// normalizeRawETag reads the digest out of a Raw ETag. Reads answer
// "sha256-<hex>" while uploads answer "sha256:<hex>"; both name the same
// digest.
func normalizeRawETag(etag string) string {
	value := strings.Trim(strings.TrimPrefix(strings.TrimSpace(etag), "W/"), `"`)
	if strings.HasPrefix(value, "sha256-") {
		value = "sha256:" + strings.TrimPrefix(value, "sha256-")
	}
	return value
}

// putRawObject uploads one object. The Digest header makes the server
// verify the bytes, and the ETag it answers must name the same digest; the
// same bytes may be sent again, so the request is retried.
func putRawObject(ctx context.Context, client *apiClient, objectPath string, object localObject) error {
	response, err := client.do(ctx, http.MethodPut, objectPath, requestOptions{
		idempotent: true,
		stream:     object.open(),
		length:     object.size,
		header:     http.Header{"Digest": {"sha-256=" + base64.StdEncoding.EncodeToString(object.raw)}},
	})
	if err != nil {
		return err
	}
	if response.StatusCode >= 300 {
		return parseAPIError(response, client.token)
	}
	if normalizeRawETag(response.Header.Get("ETag")) != object.digest {
		return incompleteResponse(response, client, "Raw upload")
	}
	return nil
}

func publishRawCommand() command {
	var repo, to, uploadID string
	var parallel int
	var force, resumable bool
	var chunkSize int64
	return command{
		name:        "publish raw",
		synopsis:    "upload a file or a directory tree to a Raw hosted repository",
		usage:       "agctl publish raw SOURCE --repo ID [--to PATH_OR_PREFIX] [--parallel N] [--resumable] [--upload-id UUID] [--chunk-size BYTES] [--force] [--dry-run] [--json]",
		positionals: 1,
		argsUsage:   "SOURCE",
		setFlags: func(fs *flag.FlagSet) {
			fs.StringVar(&repo, "repo", "", "Raw hosted repository ID")
			fs.StringVar(&to, "to", "", "repository path for a file, or prefix for a directory (default: the file name, or the repository root)")
			fs.IntVar(&parallel, "parallel", 4, "uploads in flight (1-16)")
			fs.BoolVar(&force, "force", false, "upload objects whose repository copy already has the same digest")
			fs.BoolVar(&resumable, "resumable", false, "upload in bounded offset-addressed chunks")
			fs.StringVar(&uploadID, "upload-id", "", "resume one file's existing upload (implies --resumable)")
			fs.Int64Var(&chunkSize, "chunk-size", 4<<20, "resumable chunk bytes (1-16777216, default 4194304)")
		},
		meta: commandMeta{
			Resource:  "artifact",
			Scenarios: []string{"ci"},
			Stage:     "m2",
			Endpoints: []string{
				"GET " + repositoriesPath + "/{repositoryId}",
				"GET " + repositoriesPath + "/{repositoryId}/capabilities",
				"HEAD /raw/{repository}/{path}",
				"PUT /raw/{repository}/{path}",
				"GET /raw/{repository}/{path}",
				"POST /raw/{repository}/{path}",
				"PATCH /raw/{repository}/{path}",
			},
			Mutating:     true,
			MinimumRole:  "repositories:write",
			OutputSchema: "ObjectResults",
			DryRun:       "client-plan",
			Retry:        true,
		},
		run: func(ctx context.Context, env *commandContext) error {
			if err := requireRepo(repo); err != nil {
				return err
			}
			if parallel < 1 || parallel > maxPublishParallel {
				return &UsageError{Message: "--parallel must be 1-16", Hint: "for example --parallel 4"}
			}
			objects, err := collectRawObjects(env.args[0], to)
			if err != nil {
				return err
			}
			if uploadID != "" {
				id, err := uuid.Parse(uploadID)
				if err != nil || id == uuid.Nil || len(objects) != 1 {
					return &UsageError{Message: "--upload-id requires a nonzero UUID and exactly one source file", Hint: "resume the returned uploadId with the same file, --repo and --to"}
				}
				uploadID, resumable = id.String(), true
			}
			if chunkSize < 1 || chunkSize > 16<<20 || (!resumable && env.flagSet["chunk-size"]) {
				return &UsageError{Message: "--chunk-size requires --resumable and must be 1-16777216", Hint: "for example --resumable --chunk-size 4194304"}
			}
			for _, object := range objects {
				if object.size > maxRawObjectBytes {
					return &UsageError{Message: fmt.Sprintf("%s is larger than the 1 GiB a Raw upload accepts", object.file), Hint: "split the file"}
				}
			}
			client, err := env.client()
			if err != nil {
				return err
			}
			// Capability reads, a directory's bounded parallelism and all resumed
			// chunks share one deadline, including retries and read-back.
			ctx, cancel := context.WithTimeout(ctx, env.globals.timeout)
			defer cancel()
			repository, err := publicationTarget(ctx, client, repo, adminopenapi.FormatRaw)
			if err != nil {
				return err
			}
			results := make([]objectResult, len(objects))
			var wait sync.WaitGroup
			slots := make(chan struct{}, parallel)
			for index, object := range objects {
				results[index] = objectResult{Path: object.remote, Coordinate: rawCoordinate(object.remote), File: object.file, Size: object.size, Digest: object.digest}
				wait.Add(1)
				go func(result *objectResult, object localObject) {
					defer wait.Done()
					slots <- struct{}{}
					defer func() { <-slots }()
					target := rawObjectPath(repository.Name, object.remote)
					if !force {
						digest, exists, err := remoteDigest(ctx, client, target)
						if err != nil {
							result.Status, result.Error, result.Code = "failed", redactText(err.Error(), client.token), localErrorCode(err)
							return
						}
						if exists && digest == object.digest {
							result.Status = "skipped"
							return
						}
					}
					if env.globals.dryRun {
						result.Status = "planned"
						return
					}
					var uploadErr error
					if resumable {
						uploadErr = putResumableRawObject(ctx, client, target, object, chunkSize, uploadID, result)
					} else {
						uploadErr = putRawObject(ctx, client, target, object)
					}
					if err := uploadErr; err != nil {
						result.Status, result.Error, result.Code = "failed", redactText(err.Error(), client.token), localErrorCode(err)
						if outcomeUnknown(err) {
							result.Status = "unknown"
						}
						return
					}
					result.Status = "uploaded"
				}(&results[index], object)
			}
			wait.Wait()
			hint := "re-run the original publish raw command with the same SOURCE, --repo and --to values; objects already in place are skipped by digest"
			if resumable {
				hint += "; for an item with uploadId, resume that single file using --upload-id ID; an unconfirmed start without an ID is unknown and must not be blindly repeated"
			}
			return renderObjectResults(env, results, hint)
		},
	}
}

func publicationTarget(ctx context.Context, client *apiClient, id string, format adminopenapi.Format) (adminopenapi.Repository, error) {
	var repo adminopenapi.Repository
	if err := client.getJSON(ctx, repositoryPath(id), nil, &repo); err != nil {
		return repo, err
	}
	if !strings.EqualFold(repo.Id, id) || repo.Name == "" {
		return repo, &APIError{Code: "invalid_response", HTTPStatus: http.StatusOK, Message: "repository response does not identify the requested target"}
	}
	if repo.Format != format || repositoryType(repo) != string(adminopenapi.RepositoryTypeHosted) || repo.State != adminopenapi.RepositoryStateActive {
		return repo, &UsageError{Message: fmt.Sprintf("repository %s is not an active %s Hosted repository", repo.Name, format), Hint: "use a publish-capable Hosted repository; other formats use their native clients (agctl repo connect)"}
	}
	var capabilities adminopenapi.RepositoryCapabilities
	if err := client.getJSON(ctx, repositorySubPath(id, "capabilities"), nil, &capabilities); err != nil {
		return repo, err
	}
	if capabilities.Format != format || string(capabilities.Type) != string(adminopenapi.RepositoryTypeHosted) {
		return repo, &APIError{Code: "invalid_response", HTTPStatus: http.StatusOK, Message: "publication capabilities do not match the requested repository"}
	}
	if !slices.Contains(capabilities.Operations, adminopenapi.RepositoryOperationPublish) {
		return repo, &APIError{Code: "not_supported", HTTPStatus: http.StatusNotImplemented, Message: "the target does not advertise the publish operation"}
	}
	return repo, nil
}

// renderObjectResults prints per-object outcomes. Any failed or unknown
// object makes the batch partial (exit 3); re-running the same command
// skips the objects already in place.
func renderObjectResults(env *commandContext, results []objectResult, rerun string) error {
	counts := map[string]int{}
	for _, result := range results {
		counts[result.Status]++
	}
	human := func(w io.Writer) error {
		table := [][]string{{"STATUS", "PATH", "SIZE", "DIGEST"}}
		resumable := false
		for _, result := range results {
			resumable = resumable || result.UploadID != ""
		}
		if resumable {
			table[0] = append(table[0], "UPLOAD", "OFFSET")
		}
		for _, result := range results {
			size := result.Size
			status := result.Status
			if result.Error != "" {
				status += ": " + result.Error
			}
			row := []string{status, result.Path, formatBytes(&size), shortDigest(result.Digest)}
			if resumable {
				offset := "unknown"
				if result.UploadOffset != nil {
					offset = fmt.Sprint(*result.UploadOffset)
				}
				row = append(row, result.UploadID, offset)
			}
			table = append(table, row)
		}
		return writeColumns(w, table)
	}
	failed := counts["failed"] + counts["unknown"]
	if failed == 0 {
		return env.render(newListPayload(results), &envelopeMeta{Pagination: &paginationMeta{Mode: "none"}}, human)
	}
	return &resultError{
		err: &PartialError{
			Message: fmt.Sprintf("%d of %d objects were not confirmed (%d failed, %d unknown)", failed, len(results), counts["failed"], counts["unknown"]),
			Hint:    rerun,
		},
		data:  newListPayload(results),
		meta:  &envelopeMeta{Pagination: &paginationMeta{Mode: "none"}},
		human: human,
	}
}

// mavenObjects digests the POM and the other files of one Maven coordinate.
// Object names are file names and must be distinct.
func mavenObjects(pom string, files []string) ([]localObject, string, error) {
	if pom == "" {
		return nil, "", &UsageError{Message: "missing required flag --pom", Hint: "pass the POM of the coordinate, for example --pom target/app-1.0.pom"}
	}
	all := append([]string{pom}, files...)
	seen := map[string]bool{}
	objects := make([]localObject, 0, len(all))
	for _, file := range all {
		name := filepath.Base(file)
		if seen[name] {
			return nil, "", &UsageError{Message: fmt.Sprintf("two files are named %s", name), Hint: "object names are file names and must be distinct"}
		}
		seen[name] = true
		object, err := digestFile(file)
		if err != nil {
			return nil, "", err
		}
		object.remote = name
		objects = append(objects, object)
	}
	return objects, filepath.Base(pom), nil
}

var mavenCoordinatePattern = func(value string) bool {
	parts := strings.Split(value, ":")
	if len(parts) < 3 || len(parts) > 5 {
		return false
	}
	for _, part := range parts {
		if part == "" || strings.Trim(part, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_.-") != "" {
			return false
		}
	}
	return true
}

type declaredObject struct {
	Name   string `json:"name"`
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
}

type createMavenSession struct {
	Format     string           `json:"format"`
	Coordinate string           `json:"coordinate"`
	PomObject  string           `json:"pomObject"`
	Objects    []declaredObject `json:"objects"`
}

func sessionRequest(coordinate, pomName string, objects []localObject) createMavenSession {
	request := createMavenSession{Format: "maven", Coordinate: coordinate, PomObject: pomName}
	for _, object := range objects {
		request.Objects = append(request.Objects, declaredObject{Name: object.remote, Digest: object.digest, Size: object.size})
	}
	return request
}

func completeSession(session adminopenapi.PublishSession) bool {
	if session.Id == (openapi_types.UUID{}) || session.RepositoryId == (openapi_types.UUID{}) || session.Coordinate == "" || !session.State.Valid() || session.ExpiresAt.IsZero() || len(session.Objects) == 0 {
		return false
	}
	seen := make(map[string]bool, len(session.Objects))
	for _, object := range session.Objects {
		if object.Name == "" || object.Digest == "" || object.Size < 0 || seen[object.Name] {
			return false
		}
		seen[object.Name] = true
	}
	return true
}

func sessionMatchesRequest(session adminopenapi.PublishSession, repo string, request createMavenSession) bool {
	if !completeSession(session) || !strings.EqualFold(session.RepositoryId.String(), repo) || session.Coordinate != request.Coordinate || len(session.Objects) != len(request.Objects) {
		return false
	}
	declared := make(map[string]declaredObject, len(request.Objects))
	for _, object := range request.Objects {
		declared[object.Name] = object
	}
	for _, object := range session.Objects {
		want, ok := declared[object.Name]
		if !ok || object.Digest != want.Digest || int64(object.Size) != want.Size {
			return false
		}
	}
	return true
}

func renderSession(w io.Writer, session adminopenapi.PublishSession) error {
	if err := keyValues(w, [][2]string{
		{"session", session.Id.String()},
		{"repository", session.RepositoryId.String()},
		{"coordinate", session.Coordinate},
		{"state", string(session.State)},
		{"expires", session.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z")},
	}); err != nil {
		return err
	}
	table := [][]string{{"OBJECT", "SIZE", "DIGEST"}}
	for _, object := range session.Objects {
		size := int64(object.Size)
		table = append(table, []string{object.Name, formatBytes(&size), shortDigest(object.Digest)})
	}
	return writeColumns(w, table)
}

// createSession creates a Maven publish session. The Idempotency-Key makes
// a repeat with the same declaration answer the same session, so the
// request is retried.
func createSession(ctx context.Context, client *apiClient, repo, key string, request createMavenSession) (adminopenapi.PublishSession, error) {
	body, err := encodeBody(request)
	if err != nil {
		return adminopenapi.PublishSession{}, err
	}
	var session adminopenapi.PublishSession
	response, err := client.sendJSON(ctx, http.MethodPost, repositorySubPath(repo, "publish-sessions"), requestOptions{
		body: body, idempotent: true, header: http.Header{"Idempotency-Key": {key}},
	}, &session)
	if err == nil && !sessionMatchesRequest(session, repo, request) {
		err = incompleteResponse(response, client, "publish session")
	}
	return session, err
}

func uploadSessionObjects(ctx context.Context, client *apiClient, sessionID string, objects []localObject, parallel int) []objectResult {
	results := make([]objectResult, len(objects))
	var wait sync.WaitGroup
	slots := make(chan struct{}, parallel)
	for index, object := range objects {
		results[index] = objectResult{Path: object.remote, File: object.file, Size: object.size, Digest: object.digest}
		wait.Add(1)
		go func(result *objectResult, object localObject) {
			defer wait.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			// Objects are content-addressed by their declared digest: sending
			// the same bytes again stages the same object, so it is retried.
			response, err := client.do(ctx, http.MethodPut, publishSessionsPath+"/"+url.PathEscape(sessionID)+"/objects/"+url.PathEscape(object.remote), requestOptions{
				idempotent: true, stream: object.open(), length: object.size,
			})
			if err == nil && response.StatusCode >= 300 {
				err = parseAPIError(response, client.token)
			}
			if err != nil {
				result.Status, result.Error, result.Code = "failed", redactText(err.Error(), client.token), localErrorCode(err)
				if outcomeUnknown(err) {
					result.Status = "unknown"
				}
				return
			}
			result.Status = "uploaded"
		}(&results[index], object)
	}
	wait.Wait()
	return results
}

// commitSession commits a session. Commit has no idempotency key and a
// repeat answers 409 session_closed, so it is not retried; when the answer
// is lost the session is read back, and a committed session proves the
// commit.
func commitSession(ctx context.Context, client *apiClient, sessionID string) (adminopenapi.Artifact, bool, error) {
	var artifact adminopenapi.Artifact
	response, err := client.sendJSON(ctx, http.MethodPost, publishSessionsPath+"/"+url.PathEscape(sessionID)+":commit", requestOptions{}, &artifact)
	if err == nil && (artifact.Id == (openapi_types.UUID{}) || artifact.Coordinate == "" || artifact.Digest == "") {
		err = incompleteResponse(response, client, "committed artifact")
	}
	if err == nil || !outcomeUnknown(err) {
		return artifact, false, err
	}
	var session adminopenapi.PublishSession
	if readErr := client.getJSON(ctx, publishSessionsPath+"/"+url.PathEscape(sessionID), nil, &session); readErr == nil && completeSession(session) && strings.EqualFold(session.Id.String(), sessionID) && session.State == adminopenapi.PublishSessionStateCommitted {
		return adminopenapi.Artifact{}, true, nil
	}
	return artifact, false, err
}

// publishResult is the data of a Maven publish.
type publishResult struct {
	SessionID string                 `json:"sessionId"`
	State     string                 `json:"state"`
	Artifact  *adminopenapi.Artifact `json:"artifact"`
	Confirmed string                 `json:"confirmedBy"`
	Objects   []objectResult         `json:"objects,omitempty"`
}

func mavenFlags(repo, coordinate, pom *string, files *stringList, key *string) func(*flag.FlagSet) {
	return func(fs *flag.FlagSet) {
		fs.StringVar(repo, "repo", "", "Maven hosted repository ID")
		fs.StringVar(coordinate, "coordinate", "", "groupId:artifactId:version[:classifier[:extension]]")
		fs.StringVar(pom, "pom", "", "the POM file of the coordinate")
		fs.Var(files, "file", "another file of the coordinate, such as the jar (repeatable)")
		fs.StringVar(key, "idempotency-key", "", "reuse the key of an earlier attempt (default: a new key)")
	}
}

func validMavenInput(repo, coordinate string) error {
	if err := requireRepo(repo); err != nil {
		return err
	}
	if !mavenCoordinatePattern(coordinate) {
		return &UsageError{Message: fmt.Sprintf("invalid --coordinate %q", coordinate), Hint: "use groupId:artifactId:version, for example com.example:app:1.0.0"}
	}
	return nil
}

func publishCreateCommand() command {
	var repo, coordinate, pom, key string
	var files stringList
	return command{
		name:     "publish create",
		synopsis: "open a Maven publish session declaring the files of one coordinate",
		usage:    "agctl publish create --repo ID --coordinate G:A:V --pom FILE [--file FILE]... [--idempotency-key KEY] [--dry-run] [--json]",
		setFlags: mavenFlags(&repo, &coordinate, &pom, &files, &key),
		meta: commandMeta{
			Resource: "publish-session", Scenarios: []string{"ci"}, Stage: "m1",
			Endpoints: []string{"GET " + repositoriesPath + "/{repositoryId}", "GET " + repositoriesPath + "/{repositoryId}/capabilities", "POST " + repositoriesPath + "/{repositoryId}/publish-sessions"}, Mutating: true,
			MinimumRole: "repositories:write", InputSchema: "CreatePublishSession", OutputSchema: "PublishSession", DryRun: "client-plan", Retry: true,
		},
		run: func(ctx context.Context, env *commandContext) error {
			if err := validMavenInput(repo, coordinate); err != nil {
				return err
			}
			objects, pomName, err := mavenObjects(pom, files)
			if err != nil {
				return err
			}
			key, err = resolveIdempotencyKey(env, key)
			if err != nil {
				return err
			}
			request := sessionRequest(coordinate, pomName, objects)
			if env.globals.dryRun {
				plan := writePlan{Mode: "client-plan", Method: http.MethodPost, Path: repositorySubPath(repo, "publish-sessions"), Headers: map[string]string{"Idempotency-Key": key}, Body: request,
					Verified: []string{"coordinate shape, distinct file names, and local sizes and digests"}, NotVerified: []string{"caller permission (repositories:write)", "that the repository is an active Maven hosted repository", "the POM contents"}}
				return env.render(plan, nil, func(w io.Writer) error { return renderPlan(w, plan) })
			}
			client, err := env.client()
			if err != nil {
				return err
			}
			if _, err := publicationTarget(ctx, client, repo, adminopenapi.FormatMaven); err != nil {
				return err
			}
			session, err := createSession(ctx, client, repo, key, request)
			if err != nil {
				if outcomeUnknown(err) {
					return unknownOutcomeError(err, unknownOutcome{Operation: "publish create", ID: repo, Name: coordinate, IdempotencyKey: key, ReadBack: "re-run with --idempotency-key " + key},
						fmt.Sprintf("the session may have been created; re-run the same command with --idempotency-key %s to get it back", key))
				}
				return err
			}
			return env.render(session, nil, func(w io.Writer) error { return renderSession(w, session) })
		},
	}
}

func publishUploadCommand() command {
	var files stringList
	var parallel int
	return command{
		name:        "publish upload",
		synopsis:    "upload the declared files of an open Maven publish session",
		usage:       "agctl publish upload SESSION --file FILE... [--parallel N] [--json]",
		positionals: 1,
		argsUsage:   "SESSION",
		setFlags: func(fs *flag.FlagSet) {
			fs.Var(&files, "file", "a declared file, matched by file name (repeatable; include the POM)")
			fs.IntVar(&parallel, "parallel", 4, "uploads in flight (1-16)")
		},
		meta: commandMeta{
			Resource: "publish-session", Scenarios: []string{"ci"}, Stage: "m1",
			Endpoints: []string{"PUT " + publishSessionsPath + "/{sessionId}/objects/{objectName}"}, Mutating: true,
			MinimumRole: "repositories:write", OutputSchema: "ObjectResults", DryRun: "unsupported", Retry: true,
		},
		run: func(ctx context.Context, env *commandContext) error {
			if len(files) == 0 {
				return &UsageError{Message: "pass the files to upload with --file", Hint: "include every declared file; re-uploading one is harmless"}
			}
			if parallel < 1 || parallel > maxPublishParallel {
				return &UsageError{Message: "--parallel must be 1-16", Hint: "for example --parallel 4"}
			}
			var objects []localObject
			seen := map[string]bool{}
			for _, file := range files {
				object, err := digestFile(file)
				if err != nil {
					return err
				}
				object.remote = filepath.Base(file)
				if seen[object.remote] {
					return &UsageError{Message: fmt.Sprintf("two files are named %s", object.remote), Hint: "object names are file names and must be distinct"}
				}
				seen[object.remote] = true
				objects = append(objects, object)
			}
			client, err := env.client()
			if err != nil {
				return err
			}
			results := uploadSessionObjects(ctx, client, env.args[0], objects, parallel)
			return renderObjectResults(env, results, "re-run the original publish upload command with the same SESSION and --file values; re-uploading the same bytes is safe")
		},
	}
}

func publishCommitCommand() command {
	return command{
		name:        "publish commit",
		synopsis:    "commit a Maven publish session, making its coordinate visible",
		usage:       "agctl publish commit SESSION [--json]",
		positionals: 1,
		argsUsage:   "SESSION",
		meta: commandMeta{
			Resource: "publish-session", Scenarios: []string{"ci"}, Stage: "m1",
			Endpoints: []string{"POST " + publishSessionsPath + "/{sessionId}:commit", "GET " + publishSessionsPath + "/{sessionId}"}, Mutating: true,
			MinimumRole: "repositories:write", OutputSchema: "PublishResult", DryRun: "unsupported",
		},
		run: func(ctx context.Context, env *commandContext) error {
			client, err := env.client()
			if err != nil {
				return err
			}
			return finishCommit(ctx, env, client, env.args[0], nil)
		},
	}
}

func finishCommit(ctx context.Context, env *commandContext, client *apiClient, sessionID string, objects []objectResult) error {
	artifact, readBack, err := commitSession(ctx, client, sessionID)
	if err != nil {
		if outcomeUnknown(err) {
			return unknownOutcomeError(err, unknownOutcome{Operation: "publish commit", ID: sessionID, ReadBack: "agctl publish status " + sessionID},
				fmt.Sprintf("the commit may have been applied; agctl publish status %s shows state committed once it is (a repeated commit answers 409)", sessionID))
		}
		return err
	}
	result := publishResult{SessionID: sessionID, State: "committed", Confirmed: "commit response", Objects: objects}
	if readBack {
		result.Confirmed = "session read-back after a lost commit response"
	} else {
		result.Artifact = &artifact
	}
	return env.render(result, nil, func(w io.Writer) error {
		if result.Artifact != nil {
			_, err := fmt.Fprintf(w, "published %s (%s) as artifact %s\n", artifact.Coordinate, shortDigest(artifact.Digest), artifact.Id)
			return err
		}
		_, err := fmt.Fprintf(w, "session %s is committed (confirmed by reading it back)\n", sessionID)
		return err
	})
}

func publishStatusCommand() command {
	return command{
		name:        "publish status",
		synopsis:    "show a publish session and its state",
		usage:       "agctl publish status SESSION [--json]",
		positionals: 1,
		argsUsage:   "SESSION",
		meta: commandMeta{
			Resource: "publish-session", Scenarios: []string{"ci"}, Stage: "m1",
			Endpoints: []string{"GET " + publishSessionsPath + "/{sessionId}"}, MinimumRole: "repositories:read",
			OutputSchema: "PublishSession", DryRun: "unsupported", Retry: true,
		},
		run: func(ctx context.Context, env *commandContext) error {
			client, err := env.client()
			if err != nil {
				return err
			}
			var session adminopenapi.PublishSession
			if err := client.getJSON(ctx, publishSessionsPath+"/"+url.PathEscape(env.args[0]), nil, &session); err != nil {
				return err
			}
			return env.render(session, nil, func(w io.Writer) error { return renderSession(w, session) })
		},
	}
}

func publishMavenCommand() command {
	var repo, coordinate, pom, key string
	var files stringList
	var parallel int
	return command{
		name:     "publish maven",
		synopsis: "publish one Maven coordinate: open a session, upload its files, and commit",
		usage:    "agctl publish maven --repo ID --coordinate G:A:V --pom FILE [--file FILE]... [--parallel N] [--idempotency-key KEY] [--dry-run] [--json]",
		setFlags: func(fs *flag.FlagSet) {
			mavenFlags(&repo, &coordinate, &pom, &files, &key)(fs)
			fs.IntVar(&parallel, "parallel", 4, "uploads in flight (1-16)")
		},
		meta: commandMeta{
			Resource:  "publish-session",
			Scenarios: []string{"ci"},
			Stage:     "m1",
			Endpoints: []string{
				"GET " + repositoriesPath + "/{repositoryId}",
				"GET " + repositoriesPath + "/{repositoryId}/capabilities",
				"POST " + repositoriesPath + "/{repositoryId}/publish-sessions",
				"PUT " + publishSessionsPath + "/{sessionId}/objects/{objectName}",
				"POST " + publishSessionsPath + "/{sessionId}:commit",
				"GET " + publishSessionsPath + "/{sessionId}",
			},
			Mutating: true, MinimumRole: "repositories:write", OutputSchema: "PublishResult", DryRun: "client-plan", Retry: true,
		},
		run: func(ctx context.Context, env *commandContext) error {
			if err := validMavenInput(repo, coordinate); err != nil {
				return err
			}
			if parallel < 1 || parallel > maxPublishParallel {
				return &UsageError{Message: "--parallel must be 1-16", Hint: "for example --parallel 4"}
			}
			objects, pomName, err := mavenObjects(pom, files)
			if err != nil {
				return err
			}
			key, err = resolveIdempotencyKey(env, key)
			if err != nil {
				return err
			}
			request := sessionRequest(coordinate, pomName, objects)
			if env.globals.dryRun {
				plan := writePlan{Mode: "client-plan", Method: http.MethodPost, Path: repositorySubPath(repo, "publish-sessions"), Headers: map[string]string{"Idempotency-Key": key}, Body: request,
					Verified:    []string{"coordinate shape, distinct file names, and local sizes and digests"},
					NotVerified: []string{"caller permission (repositories:write)", "that the repository is an active Maven hosted repository", "the POM contents and that the coordinate is new"}}
				return env.render(plan, nil, func(w io.Writer) error { return renderPlan(w, plan) })
			}
			client, err := env.client()
			if err != nil {
				return err
			}
			if _, err := publicationTarget(ctx, client, repo, adminopenapi.FormatMaven); err != nil {
				return err
			}
			session, err := createSession(ctx, client, repo, key, request)
			if err != nil {
				if outcomeUnknown(err) {
					return unknownOutcomeError(err, unknownOutcome{Operation: "publish maven", ID: repo, Name: coordinate, IdempotencyKey: key, ReadBack: "re-run with --idempotency-key " + key},
						fmt.Sprintf("the session may have been created; re-run the same command with --idempotency-key %s, which resumes it", key))
				}
				return err
			}
			sessionID := session.Id.String()
			if session.State != adminopenapi.PublishSessionStateOpen {
				return &APIError{Code: "session_closed", HTTPStatus: http.StatusConflict,
					Message: fmt.Sprintf("publish session %s for this key is %s", sessionID, session.State),
					Hint:    "a committed session cannot be reused; check agctl publish status " + sessionID + ", or publish with a new --idempotency-key"}
			}
			results := uploadSessionObjects(ctx, client, sessionID, objects, parallel)
			for _, result := range results {
				if result.Status != "uploaded" {
					stopped := publishResult{SessionID: sessionID, State: "open", Objects: results}
					return &resultError{
						err:  &PartialError{Message: fmt.Sprintf("not every file of %s was uploaded; the session stays open", coordinate), Hint: fmt.Sprintf("re-run the same command with --idempotency-key %s to resume session %s", key, sessionID)},
						data: stopped,
					}
				}
			}
			return finishCommit(ctx, env, client, sessionID, results)
		},
	}
}
