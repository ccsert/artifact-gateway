package agctl

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	adminopenapi "github.com/ccsert/artifact-gateway/internal/admin/openapi"
	"github.com/google/uuid"
)

// These catalogues contain named scope sets and grant recipes. They do not
// change the built-in account roles or any repository's existing grants.
type catalogueInput struct {
	name, description, grantsFile, ifMatch string
	scopes                                 stringList
	grants                                 []adminopenapi.AuthorizationTemplateGrant
	changed                                map[string]bool
}

func (input *catalogueInput) setFlags(fs *flag.FlagSet, kind, operation string) {
	if operation == "update" {
		fs.StringVar(&input.name, "name", "", "new catalogue entry name")
		fs.StringVar(&input.ifMatch, "if-match", "", "update only this version (default: the version read just before)")
	}
	fs.StringVar(&input.description, "description", "", "description; an empty value clears it")
	if kind == "role" {
		fs.Var(&input.scopes, "scope", "repository scope (repeatable); replaces the complete scope set")
	} else {
		fs.StringVar(&input.grantsFile, "grants-file", "", "file containing a JSON grant array; replaces the complete grant recipe (use [] for none)")
	}
}

func validateCatalogueScopes(scopes []string) error {
	seen := map[string]bool{}
	if len(scopes) == 0 {
		return &UsageError{Message: "at least one repository scope is required", Hint: "use repositories:read, repositories:write, repositories:admin, or repositories:intelligence"}
	}
	for _, scope := range scopes {
		if !grantScopes[scope] || seen[scope] {
			return &UsageError{Message: fmt.Sprintf("invalid or repeated repository scope %q", scope), Hint: "use each of repositories:read, repositories:write, repositories:admin, or repositories:intelligence at most once"}
		}
		seen[scope] = true
	}
	return nil
}

func readCatalogueGrants(path string) ([]adminopenapi.AuthorizationTemplateGrant, error) {
	bad := func() error {
		return &UsageError{Message: "--grants-file must contain one JSON array of at most 500 unique principal/resource-prefix grants (at most 128 KiB)", Hint: "each grant needs principal and non-empty, distinct repository scopes; unknown fields and null are rejected"}
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, &UsageError{Message: "cannot open --grants-file", Hint: "pass a readable file containing a JSON grant array"}
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, (128<<10)+1))
	if err != nil || len(data) > 128<<10 {
		return nil, bad()
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	var grants []adminopenapi.AuthorizationTemplateGrant
	if err := decoder.Decode(&grants); err != nil || grants == nil || len(grants) > 500 {
		return nil, bad()
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, bad()
	}
	seen := map[string]bool{}
	for _, row := range templateGrantRows(grants) {
		if validateGrantKey(row.Principal, row.ResourcePrefix) != nil || validateCatalogueScopes(row.Scopes) != nil || seen[row.key()] {
			return nil, bad()
		}
		seen[row.key()] = true
	}
	return grants, nil
}

func (input *catalogueInput) validate(env *commandContext, kind, operation string) error {
	input.changed = env.flagSet
	if operation == "create" {
		input.name = env.args[0]
	} else {
		id, err := uuid.Parse(env.args[0])
		if err != nil || id == uuid.Nil {
			return &UsageError{Message: "ID must be a catalogue entry UUID", Hint: "use agctl " + kind + " list; built-in account roles none/member/admin are not catalogue IDs"}
		}
		if !env.globals.dryRun && !env.globals.yes {
			return &UsageError{Message: "agctl " + kind + " " + operation + " needs --yes", Hint: "review the catalogue change with --dry-run first"}
		}
	}
	if operation == "delete" {
		return nil
	}
	if operation == "update" {
		if !env.flagSet["name"] && !env.flagSet["description"] && !env.flagSet["scope"] && !env.flagSet["grants-file"] {
			return &UsageError{Message: "nothing to update", Hint: "pass --name, --description, or the complete --scope/--grants-file replacement"}
		}
		if env.flagSet["if-match"] && strings.TrimSpace(input.ifMatch) == "" {
			return &UsageError{Message: "--if-match cannot be empty", Hint: "pass the version from agctl " + kind + " show ID"}
		}
	}
	input.name = strings.TrimSpace(input.name)
	if (operation == "create" || env.flagSet["name"]) && (input.name == "" || len(input.name) > 128) {
		return &UsageError{Message: "catalogue names must be 1-128 bytes after trimming whitespace"}
	}
	if len(input.description) > 1000 {
		return &UsageError{Message: "--description must be at most 1000 bytes"}
	}
	if kind == "role" && (operation == "create" || env.flagSet["scope"]) {
		return validateCatalogueScopes(input.scopes)
	}
	if kind == "template" && (operation == "create" || env.flagSet["grants-file"]) {
		if strings.TrimSpace(input.grantsFile) == "" {
			return &UsageError{Message: "--grants-file is required", Hint: "pass a JSON grant array; [] is an explicit empty recipe"}
		}
		grants, err := readCatalogueGrants(input.grantsFile)
		input.grants = grants
		return err
	}
	return nil
}

// catalogueCRUD shares the HTTP lifecycle; resource-specific builders keep
// payloads in generated management types, including full PUT replacements.
type catalogueCRUD[T, W any] struct {
	kind, path, parameter, schema string
	identity                      func(T) (uuid.UUID, string, string)
	complete                      func(T) bool
	build                         func(*T, catalogueInput) W
	matches                       func(T, W) bool
	render                        func(io.Writer, T) error
}

func (catalogue catalogueCRUD[T, W]) read(ctx context.Context, client *apiClient, id string) (T, error) {
	var document T
	response, err := client.sendJSON(ctx, http.MethodGet, catalogue.path+"/"+url.PathEscape(id), requestOptions{idempotent: true}, &document)
	if err == nil {
		actualID, _, _ := catalogue.identity(document)
		expectedID, _ := uuid.Parse(id)
		if !catalogue.complete(document) || actualID != expectedID {
			err = incompleteResponse(response, client, "authorization "+catalogue.kind)
		}
	}
	return document, err
}

func (catalogue catalogueCRUD[T, W]) command(operation string) command {
	input := catalogueInput{}
	method := map[string]string{"create": http.MethodPost, "update": http.MethodPut, "delete": http.MethodDelete}[operation]
	itemPath := catalogue.path + "/{" + catalogue.parameter + "}"
	endpoints := []string{method + " " + itemPath, "GET " + itemPath}
	args, flags := "ID", " --yes"
	if operation == "create" {
		args, flags = "NAME", ""
		endpoints = []string{method + " " + catalogue.path}
	}
	if operation != "delete" {
		flags += " [--description TEXT]"
		if operation == "update" {
			flags += " [--name NAME] [--if-match VERSION]"
		}
		if catalogue.kind == "role" {
			if operation == "create" {
				flags += " --scope SCOPE..."
			} else {
				flags += " [--scope SCOPE...]"
			}
		} else {
			if operation == "create" {
				flags += " --grants-file FILE"
			} else {
				flags += " [--grants-file FILE]"
			}
		}
	}
	inputSchema, outputSchema := catalogue.schema+"Writable", catalogue.schema
	if operation == "delete" {
		inputSchema, outputSchema = "", "AuthorizationCatalogueDeletion"
	}
	return command{
		name: catalogue.kind + " " + operation, synopsis: operation + " an authorization " + catalogue.kind + " catalogue entry",
		usage:       "agctl " + catalogue.kind + " " + operation + " " + args + flags + " [--dry-run] [--json]",
		positionals: 1, argsUsage: args,
		setFlags: func(fs *flag.FlagSet) {
			if operation != "delete" {
				input.setFlags(fs, catalogue.kind, operation)
			}
		},
		meta: commandMeta{
			Resource: "authorization", Scenarios: []string{"admin"}, Stage: "m2", Endpoints: endpoints,
			Mutating: true, MinimumRole: "platform admin", InputSchema: inputSchema,
			OutputSchema: outputSchema, DryRun: "client-plan",
		},
		run: func(ctx context.Context, env *commandContext) error {
			if err := input.validate(env, catalogue.kind, operation); err != nil {
				return err
			}
			path := catalogue.path
			id, name, version := "", input.name, ""
			var current *T
			var client *apiClient
			var err error
			if operation != "create" || !env.globals.dryRun {
				client, err = env.client()
				if err != nil {
					return err
				}
			}
			if operation != "create" {
				id = uuid.MustParse(env.args[0]).String()
				path += "/" + url.PathEscape(id)
				document, err := catalogue.read(ctx, client, id)
				if err != nil {
					return err
				}
				current = &document
				_, name, version = catalogue.identity(document)
			}
			var body any
			var replacement W
			headers := http.Header{}
			plan := writePlan{Mode: "client-plan", Method: method, Path: path,
				Verified:    []string{"local input shape", "catalogue changes do not apply or revoke repository grants"},
				NotVerified: []string{"caller permission (active platform administrator without a password-change requirement)", "name uniqueness and server validation"}}
			if operation != "delete" {
				replacement = catalogue.build(current, input)
				body = replacement
			} else {
				plan.NotVerified = append(plan.NotVerified, "deletion has no If-Match support; the entry can change after this read")
			}
			if operation == "update" {
				if input.ifMatch == "" {
					input.ifMatch = version
				}
				headers.Set("If-Match", input.ifMatch)
				plan.Headers = map[string]string{"If-Match": input.ifMatch}
				plan.NotVerified = append(plan.NotVerified, "that the version still matches when the update is sent")
			}
			if current != nil {
				plan.Current = map[string]any{"id": id, "version": version, "document": *current,
					"scope": "authorization catalogue only; existing repository grants are unchanged"}
				if operation == "update" {
					plan.Current.(map[string]any)["diff"] = map[string]any{"before": catalogue.build(current, catalogueInput{changed: map[string]bool{}}), "after": replacement}
				}
				plan.Verified = append(plan.Verified, "the catalogue entry's identity, content and version were read")
			}
			plan.Body = body
			encoded, err := encodeBody(body)
			if err != nil {
				return err
			}
			limit := 16 << 10
			if catalogue.kind == "template" {
				limit = 128 << 10
			}
			if operation != "delete" && len(encoded) > limit {
				return &UsageError{Message: fmt.Sprintf("authorization %s body exceeds the server's %d-byte limit", catalogue.kind, limit)}
			}
			if env.globals.dryRun {
				return env.render(plan, nil, func(w io.Writer) error {
					if current != nil {
						before, err := json.Marshal(catalogue.build(current, catalogueInput{changed: map[string]bool{}}))
						if err != nil {
							return err
						}
						if _, err := fmt.Fprintf(w, "current catalogue content (version %s; existing repository grants are unchanged): %s\n", version, before); err != nil {
							return err
						}
					}
					return renderPlan(w, plan)
				})
			}
			// None of these writes has an endpoint-declared retry guarantee.
			var document T
			options := requestOptions{header: headers}
			if operation != "delete" {
				options.body = encoded
			}
			response, err := client.sendJSON(ctx, method, path, options, nil)
			if err == nil && operation != "delete" {
				expectedStatus := http.StatusOK
				if operation == "create" {
					expectedStatus = http.StatusCreated
				}
				if json.Unmarshal(response.Body, &document) != nil || !catalogue.complete(document) || !catalogue.matches(document, replacement) || response.StatusCode != expectedStatus {
					err = incompleteResponse(response, client, "authorization "+catalogue.kind)
				} else if returnedID, _, returnedVersion := catalogue.identity(document); operation == "update" && (returnedID.String() != id || returnedVersion == input.ifMatch || returnedVersion == version) {
					err = incompleteResponse(response, client, "authorization "+catalogue.kind)
				}
			}
			if err == nil && operation == "delete" && response.StatusCode != http.StatusNoContent {
				err = incompleteResponse(response, client, "authorization "+catalogue.kind+" deletion")
			}
			if err != nil {
				readBack := "agctl " + catalogue.kind + " show " + id
				if operation == "create" {
					readBack = "agctl " + catalogue.kind + " list"
				}
				var api *APIError
				if errors.As(err, &api) && api.HTTPStatus == http.StatusPreconditionFailed {
					api.Hint = "the catalogue entry changed and was not overwritten; review the current document and repeat --dry-run before updating"
					data := map[string]any{"outcome": "conflict", "id": id, "expectedVersion": input.ifMatch, "proposed": replacement, "readBack": readBack}
					if latest, readErr := catalogue.read(ctx, client, id); readErr == nil {
						data["current"] = latest
					}
					return &resultError{err: err, data: data}
				}
				if outcomeUnknown(err) {
					return unknownOutcomeError(err, unknownOutcome{Operation: catalogue.kind + " " + operation, ID: id, Name: name, Version: input.ifMatch, ReadBack: readBack},
						"the write may have applied; read back the catalogue before deciding whether to repeat it (no automatic retry or idempotency key)")
				}
				return err
			}
			if operation == "delete" {
				return env.render(map[string]any{"id": id, "deleted": true}, nil, func(w io.Writer) error {
					_, err := fmt.Fprintf(w, "authorization %s %s deleted; existing repository grants are unchanged\n", catalogue.kind, id)
					return err
				})
			}
			return env.render(document, nil, func(w io.Writer) error { return catalogue.render(w, document) })
		},
	}
}

func roleCatalogue() catalogueCRUD[adminopenapi.AuthorizationRole, adminopenapi.AuthorizationRoleWritable] {
	return catalogueCRUD[adminopenapi.AuthorizationRole, adminopenapi.AuthorizationRoleWritable]{
		kind: "role", path: rolesPath, parameter: "roleId", schema: "AuthorizationRole",
		identity: func(role adminopenapi.AuthorizationRole) (uuid.UUID, string, string) {
			return role.Id, role.Name, role.Version
		},
		complete: func(role adminopenapi.AuthorizationRole) bool {
			return role.Id != uuid.Nil && role.Name != "" && role.Version != "" && !role.CreatedAt.IsZero() && !role.UpdatedAt.IsZero() && len(role.Scopes) > 0
		},
		build: func(current *adminopenapi.AuthorizationRole, input catalogueInput) adminopenapi.AuthorizationRoleWritable {
			body := adminopenapi.AuthorizationRoleWritable{Name: input.name, Description: &input.description}
			if current != nil {
				body.Name, body.Description = current.Name, current.Description
				for _, scope := range current.Scopes {
					body.Scopes = append(body.Scopes, adminopenapi.AuthorizationRoleWritableScopes(scope))
				}
			}
			if input.changed["name"] {
				body.Name = input.name
			}
			if input.changed["description"] {
				body.Description = &input.description
			}
			if current == nil || input.changed["scope"] {
				body.Scopes = []adminopenapi.AuthorizationRoleWritableScopes{}
				for _, scope := range input.scopes {
					body.Scopes = append(body.Scopes, adminopenapi.AuthorizationRoleWritableScopes(scope))
				}
			}
			return body
		},
		matches: func(role adminopenapi.AuthorizationRole, body adminopenapi.AuthorizationRoleWritable) bool {
			actual, desired := grantRow{}, grantRow{}
			for _, scope := range role.Scopes {
				actual.Scopes = append(actual.Scopes, string(scope))
			}
			for _, scope := range body.Scopes {
				desired.Scopes = append(desired.Scopes, string(scope))
			}
			return role.Name == body.Name && optional(role.Description) == optional(body.Description) && actual.sameScopes(desired)
		},
		render: func(w io.Writer, role adminopenapi.AuthorizationRole) error {
			return renderRoles(w, []adminopenapi.AuthorizationRole{role})
		},
	}
}

func templateCatalogue() catalogueCRUD[adminopenapi.AuthorizationTemplate, adminopenapi.AuthorizationTemplateWritable] {
	return catalogueCRUD[adminopenapi.AuthorizationTemplate, adminopenapi.AuthorizationTemplateWritable]{
		kind: "template", path: templatesPath, parameter: "templateId", schema: "AuthorizationTemplate",
		identity: func(template adminopenapi.AuthorizationTemplate) (uuid.UUID, string, string) {
			return template.Id, template.Name, template.Version
		},
		complete: func(template adminopenapi.AuthorizationTemplate) bool {
			return template.Id != uuid.Nil && template.Name != "" && template.Version != "" && !template.CreatedAt.IsZero() && !template.UpdatedAt.IsZero() && template.Grants != nil
		},
		build: func(current *adminopenapi.AuthorizationTemplate, input catalogueInput) adminopenapi.AuthorizationTemplateWritable {
			body := adminopenapi.AuthorizationTemplateWritable{Name: input.name, Description: &input.description, Grants: input.grants}
			if current != nil {
				body.Name, body.Description, body.Grants = current.Name, current.Description, current.Grants
			}
			if input.changed["name"] {
				body.Name = input.name
			}
			if input.changed["description"] {
				body.Description = &input.description
			}
			if input.changed["grants-file"] {
				body.Grants = input.grants
			}
			return body
		},
		matches: func(template adminopenapi.AuthorizationTemplate, body adminopenapi.AuthorizationTemplateWritable) bool {
			diff := diffGrants(templateGrantRows(template.Grants), templateGrantRows(body.Grants))
			return template.Name == body.Name && optional(template.Description) == optional(body.Description) && len(template.Grants) == len(body.Grants) && len(diff.Added)+len(diff.Removed)+len(diff.Changed) == 0
		},
		render: func(w io.Writer, template adminopenapi.AuthorizationTemplate) error {
			return renderTemplates(w, []adminopenapi.AuthorizationTemplate{template})
		},
	}
}
