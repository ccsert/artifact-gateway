package agctl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	adminopenapi "github.com/ccsert/artifact-gateway/internal/admin/openapi"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

const groupsPath = managementPrefix + "/groups"

// Group reads and writes accept the platform tier and a caller who
// administers every repository the Group references; a write needs that for
// the members before and after the change. A reader outside that rule gets
// 404. The server decides; these strings only describe it.
const (
	groupReadRole  = "platform admin, or repositories:admin on every member"
	groupWriteRole = "platform admin, or repositories:admin on every member it keeps, drops, or adds"
)

func groupPath(id string) string {
	return groupsPath + "/" + url.PathEscape(id)
}

func renderGroupTable(w io.Writer, items []adminopenapi.Group) error {
	if len(items) == 0 {
		_, err := fmt.Fprintln(w, "no groups")
		return err
	}
	rows := [][]string{{"NAME", "FORMAT", "MEMBERS", "VERSION", "ID"}}
	for _, group := range items {
		rows = append(rows, []string{group.Name, string(group.Format), strconv.Itoa(len(group.Members)), group.Version, group.Id.String()})
	}
	return writeColumns(w, rows)
}

func renderMembers(w io.Writer, members adminopenapi.MemberList) error {
	if len(members) == 0 {
		_, err := fmt.Fprintln(w, "no members")
		return err
	}
	rows := [][]string{{"POSITION", "REPOSITORY ID"}}
	for _, member := range sortedMembers(members) {
		rows = append(rows, []string{strconv.Itoa(member.Position), member.RepositoryId.String()})
	}
	return writeColumns(w, rows)
}

// sortedMembers orders members by configured position without changing the
// server's list, which JSON output reports as returned.
func sortedMembers(members adminopenapi.MemberList) adminopenapi.MemberList {
	ordered := append(adminopenapi.MemberList(nil), members...)
	for i := 1; i < len(ordered); i++ {
		for j := i; j > 0 && ordered[j].Position < ordered[j-1].Position; j-- {
			ordered[j], ordered[j-1] = ordered[j-1], ordered[j]
		}
	}
	return ordered
}

func renderGroup(w io.Writer, group adminopenapi.Group) error {
	lines := [][2]string{
		{"id", group.Id.String()},
		{"name", group.Name},
		{"format", string(group.Format)},
		{"anonymous read", yesNo(group.AnonymousRead)},
		{"version", group.Version},
	}
	for _, line := range lines {
		if _, err := fmt.Fprintf(w, "%-15s %s\n", line[0]+":", line[1]); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(w, "members:"); err != nil {
		return err
	}
	return renderMembers(w, group.Members)
}

// memberList turns --member values into members in the order given: the
// first is position 0. Repository IDs must be distinct UUIDs.
func memberList(values []string) (adminopenapi.MemberList, error) {
	if len(values) == 0 {
		return nil, &UsageError{Message: "a Group needs at least one --member", Hint: "pass --member REPOSITORY_ID once per member, in resolution order"}
	}
	seen := map[string]bool{}
	members := make(adminopenapi.MemberList, 0, len(values))
	for index, value := range values {
		var id openapi_types.UUID
		if err := id.UnmarshalText([]byte(strings.TrimSpace(value))); err != nil {
			return nil, &UsageError{Message: fmt.Sprintf("--member %q is not a repository ID", value), Hint: "members are repository IDs; agctl repo list shows them"}
		}
		if seen[id.String()] {
			return nil, &UsageError{Message: fmt.Sprintf("repository %s is listed twice", id), Hint: "each repository can be a member once"}
		}
		seen[id.String()] = true
		members = append(members, adminopenapi.Member{RepositoryId: id, Position: index})
	}
	return members, nil
}

// completeGroup reports whether a write response identifies the Group it
// returns. A Group always has at least one member.
func completeGroup(group adminopenapi.Group) bool {
	return group.Id != (openapi_types.UUID{}) && group.Name != "" && group.Format != "" && group.Version != "" && len(group.Members) > 0
}

// sendGroupWrite requires a complete Group before reporting success. Empty,
// null, unreadable, or incomplete JSON keeps the recovery information of an
// unknown outcome instead.
func sendGroupWrite(ctx context.Context, client *apiClient, method, path string, options requestOptions) (adminopenapi.Group, error) {
	var group adminopenapi.Group
	response, err := client.sendJSON(ctx, method, path, options, &group)
	if err != nil {
		return group, err
	}
	if !completeGroup(group) {
		return group, incompleteResponse(response, client, "Group")
	}
	return group, nil
}

func incompleteResponse(response *apiResponse, client *apiClient, resource string) error {
	return &APIError{
		Code:       "invalid_response",
		HTTPStatus: response.StatusCode,
		Message:    fmt.Sprintf("server returned an incomplete %s response", resource),
		RequestID:  redactText(response.Header.Get("X-Request-ID"), client.token),
	}
}

func readGroup(ctx context.Context, client *apiClient, id string) (adminopenapi.Group, error) {
	var group adminopenapi.Group
	response, err := client.sendJSON(ctx, http.MethodGet, groupPath(id), requestOptions{idempotent: true}, &group)
	if err != nil {
		return group, err
	}
	if !completeGroup(group) {
		return group, incompleteResponse(response, client, "Group")
	}
	return group, nil
}

func groupConflictHint(err error, ifMatch, id string) {
	var api *APIError
	if errors.As(err, &api) && api.HTTPStatus == http.StatusPreconditionFailed {
		api.Hint = fmt.Sprintf("the Group changed since version %s and was not updated; run agctl group show %s and retry against its current state", ifMatch, id)
	}
}

func groupListCommand() command {
	list := &cursorList[adminopenapi.Group]{
		path:    groupsPath,
		command: "agctl group list",
		noun:    "groups",
		render:  renderGroupTable,
	}
	return command{
		name:     "group list",
		synopsis: "list Groups",
		usage:    "agctl group list [--page-size N] [--cursor TOKEN | --all [--max-pages N]] [--json]",
		setFlags: list.setFlags,
		meta: commandMeta{
			Resource:     "group",
			Scenarios:    []string{"admin"},
			Stage:        "m1",
			Endpoints:    []string{"GET " + groupsPath},
			MinimumRole:  groupReadRole,
			OutputSchema: "GroupPage",
			Pagination:   true,
			DryRun:       "unsupported",
			Retry:        true,
		},
		run: list.run,
	}
}

func groupShowCommand() command {
	return command{
		name:        "group show",
		synopsis:    "show one Group and its members",
		usage:       "agctl group show ID [--json]",
		positionals: 1,
		argsUsage:   "ID",
		meta: commandMeta{
			Resource:     "group",
			Scenarios:    []string{"admin"},
			Stage:        "m1",
			Endpoints:    []string{"GET " + groupsPath + "/{groupId}"},
			MinimumRole:  groupReadRole,
			OutputSchema: "Group",
			DryRun:       "unsupported",
			Retry:        true,
		},
		run: func(ctx context.Context, env *commandContext) error {
			client, err := env.client()
			if err != nil {
				return err
			}
			group, err := readGroup(ctx, client, env.args[0])
			if err != nil {
				return err
			}
			return env.render(group, nil, func(w io.Writer) error { return renderGroup(w, group) })
		},
	}
}

func groupCreateCommand() command {
	var format, idempotencyKey string
	var members stringList
	var anonymousRead bool
	return command{
		name:        "group create",
		synopsis:    "create a Group from repositories in resolution order",
		usage:       "agctl group create NAME --format FORMAT --member REPOSITORY_ID... [--anonymous-read] [--idempotency-key KEY] [--dry-run] [--json]",
		positionals: 1,
		argsUsage:   "NAME",
		setFlags: func(fs *flag.FlagSet) {
			fs.StringVar(&format, "format", "", "Group format; every member must have it")
			fs.Var(&members, "member", "member repository ID (repeatable; the order given is the configured position)")
			fs.BoolVar(&anonymousRead, "anonymous-read", false, "allow unauthenticated protocol reads where effective policy permits")
			fs.StringVar(&idempotencyKey, "idempotency-key", "", "reuse the key of an earlier attempt whose outcome was unknown (default: a new key)")
		},
		meta: commandMeta{
			Resource:     "group",
			Scenarios:    []string{"admin"},
			Stage:        "m1",
			Endpoints:    []string{"POST " + groupsPath},
			Mutating:     true,
			MinimumRole:  groupWriteRole,
			InputSchema:  "CreateGroup",
			OutputSchema: "Group",
			DryRun:       "client-plan",
			Retry:        true,
		},
		run: func(ctx context.Context, env *commandContext) error {
			name := env.args[0]
			if !repositoryNamePattern.MatchString(name) {
				return &UsageError{Message: fmt.Sprintf("invalid Group name %q", name), Hint: "use 1-63 lowercase letters, digits, and inner hyphens"}
			}
			if format == "" {
				return &UsageError{Message: "missing required flag --format", Hint: "pass --format, for example --format npm"}
			}
			request := adminopenapi.CreateGroup{Name: name, Format: adminopenapi.Format(format)}
			if !request.Format.Valid() {
				return &UsageError{Message: fmt.Sprintf("unknown format %q", format), Hint: "use one of apt, cargo, conan, go, maven, npm, oci, pypi, raw"}
			}
			list, err := memberList(members)
			if err != nil {
				return err
			}
			request.Members = list
			if env.flagSet["anonymous-read"] {
				request.AnonymousRead = &anonymousRead
			}
			key, err := resolveIdempotencyKey(env, idempotencyKey)
			if err != nil {
				return err
			}
			if env.globals.dryRun {
				plan := writePlan{
					Mode:     "client-plan",
					Method:   http.MethodPost,
					Path:     groupsPath,
					Headers:  map[string]string{"Idempotency-Key": key},
					Body:     request,
					Verified: []string{"name pattern and format", "members are distinct repository IDs, positioned in the order given"},
					NotVerified: []string{
						"caller permission (" + groupWriteRole + ")",
						"name uniqueness",
						"that the format supports Groups and every member is an active repository of that format",
					},
				}
				return env.render(plan, nil, func(w io.Writer) error { return renderPlan(w, plan) })
			}
			client, err := env.client()
			if err != nil {
				return err
			}
			body, err := encodeBody(request)
			if err != nil {
				return err
			}
			// The endpoint replays the same response for the same key and body,
			// so every attempt of this call may resend it.
			group, err := sendGroupWrite(ctx, client, http.MethodPost, groupsPath, requestOptions{
				body:       body,
				idempotent: true,
				header:     http.Header{"Idempotency-Key": {key}},
			})
			if err != nil {
				if outcomeUnknown(err) {
					return unknownOutcomeError(err, unknownOutcome{
						Operation:      "group create",
						Name:           name,
						IdempotencyKey: key,
						ReadBack:       "agctl group list",
					}, fmt.Sprintf("the Group may have been created; re-run the same command with --idempotency-key %s to retry safely, or check agctl group list", key))
				}
				return err
			}
			return env.render(group, nil, func(w io.Writer) error { return renderGroup(w, group) })
		},
	}
}

// resolveIdempotencyKey validates --idempotency-key or generates a new key.
func resolveIdempotencyKey(env *commandContext, value string) (string, error) {
	if !env.flagSet["idempotency-key"] {
		return newIdempotencyKey()
	}
	value = strings.TrimSpace(value)
	if value == "" || len(value) > repoIdempotencyKeyMax {
		return "", &UsageError{Message: "--idempotency-key must be 1-128 characters", Hint: "pass the key reported by the earlier attempt"}
	}
	return value, nil
}

func groupUpdateCommand() command {
	var name, ifMatch string
	var anonymousRead bool
	return command{
		name:        "group update",
		synopsis:    "rename a Group or change its anonymous read policy (conditional on its version)",
		usage:       "agctl group update ID [--name NAME] [--anonymous-read=true|false] [--if-match VERSION] [--dry-run] [--json]",
		positionals: 1,
		argsUsage:   "ID",
		setFlags: func(fs *flag.FlagSet) {
			fs.StringVar(&name, "name", "", "new Group name")
			fs.BoolVar(&anonymousRead, "anonymous-read", false, "set the anonymous read policy")
			fs.StringVar(&ifMatch, "if-match", "", "apply only if the Group is still at this version (default: the version read just before the update)")
		},
		meta: commandMeta{
			Resource:     "group",
			Scenarios:    []string{"admin"},
			Stage:        "m1",
			Endpoints:    []string{"GET " + groupsPath + "/{groupId}", "PUT " + groupsPath + "/{groupId}"},
			Mutating:     true,
			MinimumRole:  groupWriteRole,
			InputSchema:  "Group",
			OutputSchema: "Group",
			DryRun:       "client-plan",
		},
		run: func(ctx context.Context, env *commandContext) error {
			id := env.args[0]
			if !env.flagSet["name"] && !env.flagSet["anonymous-read"] {
				return &UsageError{Message: "nothing to update", Hint: "pass --name or --anonymous-read; change members with agctl group members set"}
			}
			if env.flagSet["name"] && !repositoryNamePattern.MatchString(name) {
				return &UsageError{Message: fmt.Sprintf("invalid Group name %q", name), Hint: "use 1-63 lowercase letters, digits, and inner hyphens"}
			}
			if env.flagSet["if-match"] && strings.TrimSpace(ifMatch) == "" {
				return &UsageError{Message: "--if-match cannot be empty", Hint: "pass the Group version from agctl group show"}
			}
			client, err := env.client()
			if err != nil {
				return err
			}
			// The endpoint replaces the whole Group, so the current members and
			// format are read and sent back unchanged. The update is
			// conditional on --if-match, or on the version read here: a change
			// in between fails with 412 instead of being overwritten.
			current, err := readGroup(ctx, client, id)
			if err != nil {
				return err
			}
			if ifMatch == "" {
				ifMatch = current.Version
			}
			replacement := current
			if env.flagSet["name"] {
				replacement.Name = name
			}
			if env.flagSet["anonymous-read"] {
				replacement.AnonymousRead = anonymousRead
			}
			if env.globals.dryRun {
				plan := writePlan{
					Mode:     "client-plan",
					Method:   http.MethodPut,
					Path:     groupPath(id),
					Headers:  map[string]string{"If-Match": ifMatch},
					Body:     replacement,
					Current:  current,
					Verified: []string{"the Group exists and its current members and version were read"},
					NotVerified: []string{
						"caller permission (" + groupWriteRole + ")",
						"name uniqueness",
						"that the version still matches when the update is sent",
					},
				}
				return env.render(plan, nil, func(w io.Writer) error { return renderPlan(w, plan) })
			}
			body, err := encodeBody(replacement)
			if err != nil {
				return err
			}
			// A conditional write is not retried: if a lost attempt applied it,
			// the retry would fail on the new version and misreport a conflict.
			group, err := sendGroupWrite(ctx, client, http.MethodPut, groupPath(id), requestOptions{
				body:   body,
				header: http.Header{"If-Match": {ifMatch}},
			})
			if err != nil {
				groupConflictHint(err, ifMatch, id)
				if outcomeUnknown(err) {
					return unknownOutcomeError(err, unknownOutcome{
						Operation: "group update",
						ID:        id,
						ReadBack:  "agctl group show " + id,
					}, fmt.Sprintf("the update may have been applied; run agctl group show %s and compare its version with %s before retrying", id, ifMatch))
				}
				return err
			}
			return env.render(group, nil, func(w io.Writer) error { return renderGroup(w, group) })
		},
	}
}

// groupDeletion is the data of `group delete`. Group deletion is synchronous:
// a 204 means the Group is gone.
type groupDeletion struct {
	ID      string `json:"id"`
	Deleted bool   `json:"deleted"`
}

func groupDeleteCommand() command {
	return command{
		name:        "group delete",
		synopsis:    "delete a Group (its member repositories are kept)",
		usage:       "agctl group delete ID --yes [--dry-run] [--json]",
		positionals: 1,
		argsUsage:   "ID",
		meta: commandMeta{
			Resource:     "group",
			Scenarios:    []string{"admin"},
			Stage:        "m1",
			Endpoints:    []string{"DELETE " + groupsPath + "/{groupId}"},
			Mutating:     true,
			MinimumRole:  groupWriteRole,
			OutputSchema: "GroupDeletion",
			DryRun:       "client-plan",
		},
		run: func(ctx context.Context, env *commandContext) error {
			id := env.args[0]
			if !env.globals.dryRun && !env.globals.yes {
				return &UsageError{Message: "agctl group delete needs --yes to delete a Group", Hint: "re-run with --yes to confirm, or with --dry-run to see what would happen"}
			}
			client, err := env.client()
			if err != nil {
				return err
			}
			if env.globals.dryRun {
				group, err := readGroup(ctx, client, id)
				if err != nil {
					return err
				}
				plan := writePlan{
					Mode:     "client-plan",
					Method:   http.MethodDelete,
					Path:     groupPath(id),
					Headers:  map[string]string{},
					Current:  group,
					Verified: []string{"the Group exists and is readable", "its member repositories are not deleted with it"},
					NotVerified: []string{
						"caller permission (" + groupWriteRole + ")",
					},
				}
				return env.render(plan, nil, func(w io.Writer) error { return renderPlan(w, plan) })
			}
			// Deletion is not retried: a repeat after a lost 204 answers 404 and
			// would misreport a deletion that succeeded.
			if _, err := client.sendJSON(ctx, http.MethodDelete, groupPath(id), requestOptions{}, nil); err != nil {
				if outcomeUnknown(err) {
					return unknownOutcomeError(err, unknownOutcome{
						Operation: "group delete",
						ID:        id,
						ReadBack:  "agctl group show " + id,
					}, fmt.Sprintf("the Group may have been deleted; agctl group show %s answers 404 (exit 4) once it is gone", id))
				}
				return err
			}
			deletion := groupDeletion{ID: id, Deleted: true}
			return env.render(deletion, nil, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "group %s deleted\n", id)
				return err
			})
		},
	}
}

func groupMembersListCommand() command {
	return command{
		name:        "group members list",
		synopsis:    "list a Group's members with their configured positions",
		usage:       "agctl group members list ID [--json]",
		positionals: 1,
		argsUsage:   "ID",
		meta: commandMeta{
			Resource:     "group",
			Scenarios:    []string{"admin"},
			Stage:        "m1",
			Endpoints:    []string{"GET " + groupsPath + "/{groupId}/members"},
			MinimumRole:  groupReadRole,
			OutputSchema: "MemberList",
			DryRun:       "unsupported",
			Retry:        true,
		},
		run: func(ctx context.Context, env *commandContext) error {
			client, err := env.client()
			if err != nil {
				return err
			}
			var members adminopenapi.MemberList
			if err := client.getJSON(ctx, groupPath(env.args[0])+"/members", nil, &members); err != nil {
				return err
			}
			payload := newListPayload(members)
			meta := &envelopeMeta{Pagination: &paginationMeta{Mode: "none"}}
			return env.render(payload, meta, func(w io.Writer) error { return renderMembers(w, members) })
		},
	}
}

func groupMembersSetCommand() command {
	var members stringList
	var ifMatch string
	return command{
		name:        "group members set",
		synopsis:    "replace a Group's members in resolution order (conditional on its version)",
		usage:       "agctl group members set ID --member REPOSITORY_ID... [--if-match VERSION] [--dry-run] [--json]",
		positionals: 1,
		argsUsage:   "ID",
		setFlags: func(fs *flag.FlagSet) {
			fs.Var(&members, "member", "member repository ID (repeatable; the order given is the configured position)")
			fs.StringVar(&ifMatch, "if-match", "", "apply only if the Group is still at this version (default: the version read just before the update)")
		},
		meta: commandMeta{
			Resource:     "group",
			Scenarios:    []string{"admin"},
			Stage:        "m1",
			Endpoints:    []string{"GET " + groupsPath + "/{groupId}", "PUT " + groupsPath + "/{groupId}/members"},
			Mutating:     true,
			MinimumRole:  groupWriteRole,
			InputSchema:  "MemberList",
			OutputSchema: "Group",
			DryRun:       "client-plan",
		},
		run: func(ctx context.Context, env *commandContext) error {
			id := env.args[0]
			list, err := memberList(members)
			if err != nil {
				return err
			}
			if env.flagSet["if-match"] && strings.TrimSpace(ifMatch) == "" {
				return &UsageError{Message: "--if-match cannot be empty", Hint: "pass the Group version from agctl group show"}
			}
			client, err := env.client()
			if err != nil {
				return err
			}
			var current *adminopenapi.Group
			if ifMatch == "" {
				group, err := readGroup(ctx, client, id)
				if err != nil {
					return err
				}
				current = &group
				ifMatch = group.Version
			}
			if env.globals.dryRun {
				plan := writePlan{
					Mode:     "client-plan",
					Method:   http.MethodPut,
					Path:     groupPath(id) + "/members",
					Headers:  map[string]string{"If-Match": ifMatch},
					Body:     list,
					Verified: []string{"members are distinct repository IDs, positioned in the order given"},
					NotVerified: []string{
						"caller permission (" + groupWriteRole + ")",
						"that every member is an active repository of the Group's format",
						"that the version still matches when the update is sent",
					},
				}
				if current != nil {
					plan.Current = current
					plan.Verified = append(plan.Verified, "the Group exists and its current version was read")
				}
				return env.render(plan, nil, func(w io.Writer) error { return renderPlan(w, plan) })
			}
			body, err := encodeBody(list)
			if err != nil {
				return err
			}
			group, err := replaceGroupMembers(ctx, client, id, body, ifMatch)
			if err != nil {
				var applied *resultError
				if errors.As(err, &applied) {
					return err
				}
				groupConflictHint(err, ifMatch, id)
				if outcomeUnknown(err) {
					return unknownOutcomeError(err, unknownOutcome{
						Operation: "group members set",
						ID:        id,
						ReadBack:  "agctl group members list " + id,
					}, fmt.Sprintf("the members may have been replaced; run agctl group show %s and compare its version with %s before retrying", id, ifMatch))
				}
				return err
			}
			return env.render(group, nil, func(w io.Writer) error { return renderGroup(w, group) })
		},
	}
}

// replaceGroupMembers sends the conditional member replacement. The server
// answers with the updated Group. A server older than that contract answers
// with a bare member list, which carries no version, so the Group is read
// back to report its new version. The write is not retried.
func replaceGroupMembers(ctx context.Context, client *apiClient, id string, body []byte, ifMatch string) (adminopenapi.Group, error) {
	response, err := client.sendJSON(ctx, http.MethodPut, groupPath(id)+"/members", requestOptions{
		body:   body,
		header: http.Header{"If-Match": {ifMatch}},
	}, nil)
	if err != nil {
		return adminopenapi.Group{}, err
	}
	trimmed := bytes.TrimSpace(response.Body)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		var members adminopenapi.MemberList
		if json.Unmarshal(trimmed, &members) != nil || len(members) == 0 {
			return adminopenapi.Group{}, incompleteResponse(response, client, "member list")
		}
		group, err := readGroup(ctx, client, id)
		if err != nil {
			return adminopenapi.Group{}, &resultError{
				err:  err,
				data: newListPayload(members),
				hint: fmt.Sprintf("the members were replaced, but the Group could not be read back; run agctl group show %s", id),
			}
		}
		return group, nil
	}
	var group adminopenapi.Group
	if json.Unmarshal(trimmed, &group) != nil || !completeGroup(group) {
		return adminopenapi.Group{}, incompleteResponse(response, client, "Group")
	}
	return group, nil
}

func groupResolutionCommand() command {
	return command{
		name:        "group resolution",
		synopsis:    "show the order the protocol resolver reads a Group's members in",
		usage:       "agctl group resolution ID [--json]",
		positionals: 1,
		argsUsage:   "ID",
		meta: commandMeta{
			Resource:     "group",
			Scenarios:    []string{"admin"},
			Stage:        "m1",
			Endpoints:    []string{"GET " + groupsPath + "/{groupId}/resolution"},
			MinimumRole:  groupReadRole,
			OutputSchema: "GroupResolution",
			DryRun:       "unsupported",
			Retry:        true,
		},
		run: func(ctx context.Context, env *commandContext) error {
			client, err := env.client()
			if err != nil {
				return err
			}
			var resolution adminopenapi.GroupResolution
			if err := client.getJSON(ctx, groupPath(env.args[0])+"/resolution", nil, &resolution); err != nil {
				return err
			}
			return env.render(resolution, nil, func(w io.Writer) error { return renderResolution(w, resolution) })
		},
	}
}

func renderResolution(w io.Writer, resolution adminopenapi.GroupResolution) error {
	if _, err := fmt.Fprintf(w, "group %s (%s, version %s), strategy %s\n", resolution.GroupId, resolution.Format, resolution.GroupVersion, resolution.Strategy); err != nil {
		return err
	}
	if len(resolution.Members) > 0 {
		rows := [][]string{{"ORDER", "REPOSITORY", "TYPE", "POSITION", "REPOSITORY ID"}}
		for _, member := range resolution.Members {
			rows = append(rows, []string{
				strconv.Itoa(member.ResolutionOrder), member.RepositoryName, string(member.Type),
				strconv.Itoa(member.ConfiguredPosition), member.RepositoryId.String(),
			})
		}
		if err := writeColumns(w, rows); err != nil {
			return err
		}
	} else if _, err := fmt.Fprintln(w, "no member is resolved"); err != nil {
		return err
	}
	if resolution.ExcludedMemberCount > 0 {
		_, err := fmt.Fprintf(w, "%d members excluded (missing, inactive, or of another format)\n", resolution.ExcludedMemberCount)
		return err
	}
	return nil
}
