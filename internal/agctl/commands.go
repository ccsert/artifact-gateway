package agctl

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	adminopenapi "github.com/ccsert/artifact-gateway/internal/admin/openapi"
)

// managementPrefix is the mount point of the management API on the Gateway.
const managementPrefix = "/api/v2"

// commandMeta describes one command for `agctl commands --json`. Values must
// stay honest: abilities that do not exist are reported as unsupported.
type commandMeta struct {
	Resource     string   `json:"resource"`
	Scenarios    []string `json:"scenarios"`
	Stage        string   `json:"stage"`
	Endpoints    []string `json:"endpoints"`
	Mutating     bool     `json:"mutating"`
	MinimumRole  string   `json:"minimumRole"`
	InputSchema  string   `json:"inputSchema"`
	OutputSchema string   `json:"outputSchema"`
	Pagination   bool     `json:"pagination"`
	DryRun       string   `json:"dryRun"`
	Retry        bool     `json:"retry"`
	Wait         bool     `json:"wait"`
	// CancelSupported is explicit when a command waits on a remote operation
	// whose cancellation ability callers need to distinguish from local exit.
	CancelSupported *bool `json:"cancelSupported,omitempty"`
	// Follow marks a command that streams new entries as bounded NDJSON
	// (--follow with --duration or --max-events).
	Follow bool `json:"follow"`
}

type command struct {
	name        string
	synopsis    string
	usage       string
	positionals int
	argsUsage   string
	setFlags    func(*flag.FlagSet)
	meta        commandMeta
	run         func(context.Context, *commandContext) error
}

func registry() []command {
	return []command{
		shellCommand(),
		versionCommand(),
		infoCommand(),
		commandsCommand(),
		authLoginCommand(),
		authLogoutCommand(),
		authWhoamiCommand(),
		authChangePasswordCommand(),
		configProfileAddCommand(),
		configProfileListCommand(),
		configProfileUseCommand(),
		configProfileRemoveCommand(),
		repoListCommand(),
		repoShowCommand(),
		repoCreateCommand(),
		repoUpdateCommand(),
		repoDeleteCommand(),
		groupListCommand(),
		groupShowCommand(),
		groupCreateCommand(),
		groupUpdateCommand(),
		groupDeleteCommand(),
		groupMembersListCommand(),
		groupMembersSetCommand(),
		groupResolutionCommand(),
		groupBrowseCommand(),
		repoBrowseCommand(),
		repoCapabilitiesCommand(),
		repoConnectCommand(),
		artifactSearchCommand(),
		artifactListCommand(),
		artifactBrowseCommand(),
		artifactShowCommand(),
		artifactDeleteCommand(),
		artifactIdentitiesCommand(),
		artifactUsageCommand(),
		artifactIntelligenceShowCommand(),
		artifactQuarantineShowCommand(),
		ociImagesCommand(),
		ociManifestsCommand(),
		mavenCoordinatesCommand(),
		conanReferencesCommand(),
		conanRecipeRevisionsCommand(),
		conanPackageIDsCommand(),
		conanPackageRevisionsCommand(),
		conanPackageRevisionDeleteCommand(),
		auditListCommand(),
		statusCommand(),
		diagnosticsCommand(),
		runtimeNodesCommand(),
		overviewShowCommand(),
		lifecycleJobListCommand(),
		lifecycleJobShowCommand(),
		grantListCommand(),
		grantUpsertCommand(),
		grantDeleteCommand(),
		roleListCommand(),
		roleShowCommand(),
		roleCatalogue().command("create"),
		roleCatalogue().command("update"),
		roleCatalogue().command("delete"),
		templateListCommand(),
		templateShowCommand(),
		templateCatalogue().command("create"),
		templateCatalogue().command("update"),
		templateCatalogue().command("delete"),
		templateApplyCommand(),
		accessCheckCommand(),
		accessAnonymousShowCommand(),
		accessAnonymousSetCommand(),
		runtimeLogsCommand(),
		serviceAccountListCommand(),
		serviceAccountShowCommand(),
		serviceAccountCreateCommand(),
		serviceAccountUpdateCommand(),
		credentialListCommand(),
		credentialIssueCommand(),
		credentialRevokeCommand(),
		apiKeyListCommand(),
		apiKeyCreateCommand(),
		apiKeyRevokeCommand(),
		userListCommand(),
		userShowCommand(),
		userCreateCommand(),
		userUpdateCommand(),
		userDeleteCommand(),
		userResetPasswordCommand(),
		userSessionsListCommand(),
		userSessionsRevokeCommand(),
		userIdentitiesListCommand(),
		userIdentitiesLinkCommand(),
		userIdentitiesUnlinkCommand(),
		publishRawCommand(),
		publishCreateCommand(),
		publishUploadCommand(),
		publishCommitCommand(),
		publishStatusCommand(),
		publishMavenCommand(),
		quotaAlertListCommand(),
		quotaAlertShowCommand(),
		quotaAlertCreateCommand(),
		quotaAlertUpdateCommand(),
		quotaAlertDeleteCommand(),
		quotaAlertEventsCommand(),
		emailCapabilityCommand(),
		emailTargetListCommand(),
		emailTargetShowCommand(),
		emailTargetCreateCommand(),
		emailTargetUpdateCommand(),
		emailPreviewCommand(),
		emailTestCommand(),
		emailDeliveryListCommand(),
		emailDeliveryShowCommand(),
		emailDeliveryWaitCommand(),
		emailDeliveryReplayCommand(),
	}
}

func lookupCommand(name string) (command, bool) {
	for _, cmd := range registry() {
		if cmd.name == name {
			return cmd, true
		}
	}
	return command{}, false
}

type versionInfo struct {
	Version   string `json:"version"`
	Revision  string `json:"revision"`
	GoVersion string `json:"goVersion"`
	Modified  bool   `json:"modified"`
}

func versionCommand() command {
	return command{
		name:     "version",
		synopsis: "print the agctl version",
		usage:    "agctl version [--json]",
		meta: commandMeta{
			Resource:     "local",
			Scenarios:    []string{"ci", "admin"},
			Stage:        "m0",
			Endpoints:    []string{},
			MinimumRole:  "none",
			OutputSchema: "VersionInfo",
			DryRun:       "unsupported",
		},
		run: func(_ context.Context, env *commandContext) error {
			info := versionInfo{
				Version:   env.version.Version,
				Revision:  env.version.Revision,
				GoVersion: env.version.GoVersion,
				Modified:  env.version.Modified,
			}
			return env.render(info, nil, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "agctl %s (revision %s, %s)\n", info.Version, info.Revision, info.GoVersion)
				return err
			})
		},
	}
}

type probeResult struct {
	Status     string `json:"status"`
	HTTPStatus int    `json:"httpStatus,omitempty"`
	Detail     string `json:"detail,omitempty"`
	Code       string `json:"code,omitempty"`
	RequestID  string `json:"requestId,omitempty"`
	Hint       string `json:"hint,omitempty"`
}

type identityResult struct {
	probeResult
	Actor         string `json:"actor,omitempty"`
	Kind          string `json:"kind,omitempty"`
	Role          string `json:"role,omitempty"`
	Administrator *bool  `json:"administrator,omitempty"`
	OIDC          bool   `json:"oidc,omitempty"`
}

type formatsResult struct {
	probeResult
	Count   int      `json:"count,omitempty"`
	Formats []string `json:"formats,omitempty"`
}

type infoReport struct {
	Server   string          `json:"server"`
	Livez    probeResult     `json:"livez"`
	Readyz   probeResult     `json:"readyz"`
	Identity *identityResult `json:"identity"`
	Formats  *formatsResult  `json:"formats"`
}

func infoCommand() command {
	return command{
		name:     "info",
		synopsis: "inspect the Gateway (health, readiness, caller identity, formats)",
		usage:    "agctl info [--json]",
		meta: commandMeta{
			Resource:  "runtime",
			Scenarios: []string{"ci", "admin"},
			Stage:     "m0",
			Endpoints: []string{
				"GET /livez",
				"GET /readyz",
				"GET " + managementPrefix + "/identity",
				"GET " + managementPrefix + "/formats",
			},
			MinimumRole:  "anonymous",
			OutputSchema: "InfoReport",
			DryRun:       "unsupported",
			Retry:        true,
		},
		run: func(ctx context.Context, env *commandContext) error {
			client, err := env.client()
			if err != nil {
				return err
			}
			report := infoReport{Server: redactText(redactURL(client.baseURL), client.token)}

			livez, err := client.do(ctx, http.MethodGet, "/livez", requestOptions{idempotent: true})
			if err != nil {
				return err
			}
			report.Livez = simpleProbe(livez, client.token)

			readyz, err := client.do(ctx, http.MethodGet, "/readyz", requestOptions{idempotent: true})
			if err != nil {
				return err
			}
			report.Readyz = readinessProbe(readyz, client.token)

			report.Identity, err = probeIdentity(ctx, client, env.settings.Token != "")
			if err != nil {
				return err
			}
			report.Formats, err = probeFormats(ctx, client, env.settings.Token != "")
			if err != nil {
				return err
			}
			return env.render(report, nil, func(w io.Writer) error {
				return renderInfoReport(w, report)
			})
		},
	}
}

// simpleProbe preserves structured diagnostics without exposing raw bodies.
func simpleProbe(response *apiResponse, secrets ...string) probeResult {
	if response.StatusCode < 300 {
		return probeResult{Status: "ok"}
	}
	return errorProbe(parseAPIError(response, secrets...))
}

func readinessProbe(response *apiResponse, secrets ...string) probeResult {
	result := simpleProbe(response, secrets...)
	if response.StatusCode == http.StatusServiceUnavailable {
		result.Status = "not_ready"
	}
	return result
}

func errorProbe(api *APIError) probeResult {
	status := "error"
	switch api.HTTPStatus {
	case http.StatusUnauthorized:
		status = "unauthorized"
	case http.StatusForbidden:
		status = "forbidden"
	}
	return probeResult{
		Status:     status,
		HTTPStatus: api.HTTPStatus,
		Detail:     api.Message,
		Code:       api.Code,
		RequestID:  api.RequestID,
		Hint:       api.Hint,
	}
}

func probeIdentity(ctx context.Context, client *apiClient, hasToken bool) (*identityResult, error) {
	result := &identityResult{}
	if !hasToken {
		result.Status = "unauthenticated"
		result.Detail = "no credentials configured"
		return result, nil
	}
	var identity adminopenapi.CurrentIdentity
	if err := client.getJSON(ctx, managementPrefix+"/identity", nil, &identity); err != nil {
		var api *APIError
		if !errors.As(err, &api) {
			return nil, err
		}
		result.probeResult = errorProbe(api)
		return result, nil
	}
	result.Status = "ok"
	result.Actor = identity.Actor
	result.Kind = string(identity.Kind)
	if identity.Role != nil {
		result.Role = string(*identity.Role)
	}
	administrator := identity.Administrator
	result.Administrator = &administrator
	result.OIDC = identity.Oidc != nil
	return result, nil
}

func probeFormats(ctx context.Context, client *apiClient, hasToken bool) (*formatsResult, error) {
	result := &formatsResult{}
	if !hasToken {
		result.Status = "unauthenticated"
		result.Detail = "no credentials configured"
		return result, nil
	}
	var list adminopenapi.FormatProfileList
	if err := client.getJSON(ctx, managementPrefix+"/formats", nil, &list); err != nil {
		var api *APIError
		if !errors.As(err, &api) {
			return nil, err
		}
		result.probeResult = errorProbe(api)
		return result, nil
	}
	result.Status = "ok"
	result.Count = len(list.Items)
	for _, profile := range list.Items {
		result.Formats = append(result.Formats, string(profile.Format))
	}
	return result, nil
}

func renderInfoReport(w io.Writer, report infoReport) error {
	lines := []struct {
		label string
		value string
	}{
		{"server", report.Server},
		{"livez", formatProbe(report.Livez)},
		{"readyz", formatProbe(report.Readyz)},
		{"identity", formatIdentity(report.Identity)},
		{"formats", formatFormats(report.Formats)},
	}
	for _, line := range lines {
		if _, err := fmt.Fprintf(w, "%-9s %s\n", line.label+":", line.value); err != nil {
			return err
		}
	}
	return nil
}

func formatProbe(probe probeResult) string {
	line := probe.Status
	if probe.HTTPStatus != 0 {
		line += fmt.Sprintf(" (HTTP %d)", probe.HTTPStatus)
	}
	if probe.Code != "" {
		line += " [" + probe.Code + "]"
	}
	if probe.Detail != "" {
		line += " — " + probe.Detail
	}
	if probe.Hint != "" {
		line += "; hint: " + probe.Hint
	}
	if probe.RequestID != "" {
		line += "; request id: " + probe.RequestID
	}
	return line
}

func formatIdentity(identity *identityResult) string {
	if identity == nil {
		return "unknown"
	}
	if identity.Status != "ok" {
		return formatProbe(identity.probeResult)
	}
	line := identity.Actor
	details := []string{}
	if identity.Kind != "" {
		details = append(details, "kind="+identity.Kind)
	}
	if identity.Role != "" {
		details = append(details, "role="+identity.Role)
	}
	if identity.Administrator != nil {
		details = append(details, fmt.Sprintf("administrator=%t", *identity.Administrator))
	}
	if identity.OIDC {
		details = append(details, "oidc=true")
	}
	if len(details) > 0 {
		line += " (" + strings.Join(details, ", ") + ")"
	}
	return line
}

func formatFormats(formats *formatsResult) string {
	if formats == nil {
		return "unknown"
	}
	if formats.Status != "ok" {
		return formatProbe(formats.probeResult)
	}
	return fmt.Sprintf("%d formats: %s", formats.Count, strings.Join(formats.Formats, ", "))
}

type flagInfo struct {
	Name    string `json:"name"`
	Usage   string `json:"usage"`
	Type    string `json:"type"`
	Default string `json:"default"`
	Scope   string `json:"scope"`
}

type commandInfo struct {
	Name     string      `json:"name"`
	Synopsis string      `json:"synopsis"`
	Usage    string      `json:"usage"`
	Flags    []flagInfo  `json:"flags"`
	Meta     commandMeta `json:"meta"`
}

func commandFlagInfos(cmd command, timeout time.Duration) []flagInfo {
	flags := &globalFlags{timeout: timeout, output: string(outputTable)}
	fs := flag.NewFlagSet("agctl "+cmd.name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	registerGlobalFlags(fs, flags)
	var infos []flagInfo
	collect := func(scope string) func(*flag.Flag) {
		return func(f *flag.Flag) {
			infos = append(infos, flagInfo{
				Name:    f.Name,
				Usage:   f.Usage,
				Type:    flagType(f),
				Default: f.DefValue,
				Scope:   scope,
			})
		}
	}
	fs.VisitAll(collect("global"))
	if cmd.setFlags != nil {
		local := flag.NewFlagSet("agctl "+cmd.name, flag.ContinueOnError)
		local.SetOutput(io.Discard)
		cmd.setFlags(local)
		local.VisitAll(collect("command"))
	}
	return infos
}

// flagType names the value behind a flag for the command tree. The stdlib
// flag package does not expose a type name, so the Getter interface decides.
func flagType(f *flag.Flag) string {
	getter, ok := f.Value.(flag.Getter)
	if !ok {
		return "string"
	}
	switch getter.Get().(type) {
	case bool:
		return "bool"
	case time.Duration:
		return "duration"
	case int:
		return "int"
	case string:
		return "string"
	}
	return "string"
}

func commandInfos(timeout time.Duration) []commandInfo {
	commands := registry()
	items := make([]commandInfo, 0, len(commands))
	for _, cmd := range commands {
		items = append(items, commandInfo{
			Name:     cmd.name,
			Synopsis: cmd.synopsis,
			Usage:    cmd.usage,
			Flags:    commandFlagInfos(cmd, timeout),
			Meta:     cmd.meta,
		})
	}
	return items
}

func commandsCommand() command {
	return command{
		name:     "commands",
		synopsis: "print the command tree for humans and agents",
		usage:    "agctl commands [--json]",
		meta: commandMeta{
			Resource:     "local",
			Scenarios:    []string{"ci", "admin"},
			Stage:        "m0",
			Endpoints:    []string{},
			MinimumRole:  "none",
			OutputSchema: "CommandTree",
			DryRun:       "unsupported",
		},
		run: func(_ context.Context, env *commandContext) error {
			items := commandInfos(env.globals.timeout)
			payload := newListPayload(items)
			meta := &envelopeMeta{Pagination: &paginationMeta{Mode: "none"}}
			return env.render(payload, meta, func(w io.Writer) error {
				width := 0
				for _, item := range items {
					if len(item.Name) > width {
						width = len(item.Name)
					}
				}
				for _, item := range items {
					if _, err := fmt.Fprintf(w, "  %-*s  %s\n", width, item.Name, item.Synopsis); err != nil {
						return err
					}
				}
				_, err := fmt.Fprintf(w, "\nrun `agctl <command> --help` for flags; `agctl commands --json` prints the machine-readable tree\n")
				return err
			})
		},
	}
}
