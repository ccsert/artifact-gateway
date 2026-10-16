package agctl

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	adminopenapi "github.com/ccsert/artifact-gateway/internal/admin/openapi"
)

const (
	auditsPath               = managementPrefix + "/audits"
	auditPagePath            = managementPrefix + "/audits/page"
	diagnosticsPath          = managementPrefix + "/diagnostics"
	runtimeNodesPath         = managementPrefix + "/runtime/nodes"
	overviewPath             = managementPrefix + "/overview-statistics"
	lifecycleJobsPath        = managementPrefix + "/lifecycle-jobs"
	maxAuditLimit            = 500
	maxLifecycleLimit        = 500
	repositoryLifecycleLimit = 100
)

// auditFilters are the filters both audit endpoints share.
type auditFilters struct {
	group, repository, outcome, format, operation, actor, from, to string
}

func (f *auditFilters) register(fs *flag.FlagSet) {
	fs.StringVar(&f.group, "group", "", "only this Group name")
	fs.StringVar(&f.repository, "repository", "", "only this repository name")
	fs.StringVar(&f.outcome, "outcome", "", "only this outcome, for example access_denied")
	fs.StringVar(&f.format, "format", "", "only this format, for example management")
	fs.StringVar(&f.operation, "operation", "", "only this operation")
	fs.StringVar(&f.actor, "actor", "", "only this actor")
	fs.StringVar(&f.from, "from", "", "earliest occurrence, RFC 3339 (cursor pages only)")
	fs.StringVar(&f.to, "to", "", "latest occurrence, RFC 3339 (cursor pages only)")
}

func (f auditFilters) values(withRange bool) (url.Values, error) {
	values := url.Values{}
	for name, value := range map[string]string{
		"group": f.group, "repository": f.repository, "outcome": f.outcome,
		"format": f.format, "operation": f.operation, "actor": f.actor,
	} {
		if value != "" {
			values.Set(name, value)
		}
	}
	for name, value := range map[string]string{"from": f.from, "to": f.to} {
		if value == "" {
			continue
		}
		if !withRange {
			return nil, &UsageError{Message: "--" + name + " only applies to cursor pages", Hint: "drop --bounded to filter by time"}
		}
		if _, err := time.Parse(time.RFC3339, value); err != nil {
			return nil, &UsageError{Message: fmt.Sprintf("--%s %q is not an RFC 3339 time", name, value), Hint: "use a time such as 2026-10-15T00:00:00Z"}
		}
		values.Set(name, value)
	}
	return values, nil
}

var auditColumns = []string{"OCCURRED", "OUTCOME", "ACTOR", "OPERATION", "FORMAT", "REPOSITORY", "STATUS", "RESOURCE"}

func auditRow(record adminopenapi.AuditRecord) []string {
	occurred := record.OccurredAt
	status := ""
	if record.Status != nil {
		status = strconv.Itoa(*record.Status)
	}
	repository := optional(record.Repository)
	if repository == "" {
		repository = optional(record.GroupName)
	}
	return []string{formatTime(&occurred), record.Outcome, optional(record.Actor), optional(record.Operation), optional(record.Format), repository, status, optional(record.Resource)}
}

func renderAuditTable(w io.Writer, records []adminopenapi.AuditRecord) error {
	if len(records) == 0 {
		return emptyLine(w, "matching audit records")
	}
	rows := [][]string{auditColumns}
	for _, record := range records {
		rows = append(rows, auditRow(record))
	}
	return writeColumns(w, rows)
}

// renderAuditCSV writes RFC 4180 CSV with a header row, for spreadsheets and
// tickets. Fields are written as the server reported them.
func renderAuditCSV(w io.Writer, records []adminopenapi.AuditRecord) error {
	writer := csv.NewWriter(w)
	header := []string{"occurredAt", "outcome", "actor", "operation", "format", "repository", "group", "status", "resource", "requestId", "authorizationSource", "authorizationReason"}
	if err := writer.Write(header); err != nil {
		return err
	}
	for _, record := range records {
		status := ""
		if record.Status != nil {
			status = strconv.Itoa(*record.Status)
		}
		if err := writer.Write([]string{
			record.OccurredAt.UTC().Format(time.RFC3339Nano), record.Outcome, optional(record.Actor), optional(record.Operation),
			optional(record.Format), optional(record.Repository), optional(record.GroupName), status, optional(record.Resource),
			optional(record.RequestId), optional(record.AuthorizationSource), optional(record.AuthorizationReason),
		}); err != nil {
			return err
		}
	}
	writer.Flush()
	return writer.Error()
}

func auditListCommand() command {
	var filters auditFilters
	var bounded, csvOutput bool
	var limit int
	var follow followOptions
	list := &cursorList[adminopenapi.AuditRecord]{path: auditPagePath, noun: "audit records", render: renderAuditTable}
	return command{
		name:     "audit list",
		synopsis: "list administrative audit records, newest first",
		usage:    "agctl audit list [filters] [--from TIME] [--to TIME] [--page-size N] [--cursor TOKEN | --all] | --bounded [--limit N] | --follow (--duration D | --max-events N) [--csv | --json]",
		setFlags: func(fs *flag.FlagSet) {
			filters.register(fs)
			fs.BoolVar(&bounded, "bounded", false, "read one bounded window from GET /audits instead of cursor pages")
			fs.IntVar(&limit, "limit", 100, "records in the --bounded window (1-500)")
			fs.BoolVar(&csvOutput, "csv", false, "print CSV with a header row instead of a table")
			follow.register(fs)
			list.setFlags(fs)
		},
		meta: commandMeta{
			Resource:     "audit",
			Scenarios:    []string{"ci", "admin"},
			Stage:        "m1",
			Endpoints:    []string{"GET " + auditPagePath, "GET " + auditsPath},
			MinimumRole:  "platform admin",
			OutputSchema: "AuditPage | AuditList | NDJSON stream",
			Pagination:   true,
			DryRun:       "unsupported",
			Retry:        true,
			Follow:       true,
		},
		run: func(ctx context.Context, env *commandContext) error {
			if csvOutput && env.format == outputJSON {
				return &UsageError{Message: "--csv and --json cannot be combined", Hint: "choose one output format"}
			}
			if err := follow.validate(env); err != nil {
				return err
			}
			if follow.follow {
				for _, name := range []string{"bounded", "csv", "cursor", "all", "page-size", "max-pages", "limit", "to"} {
					if env.flagSet[name] {
						return &UsageError{Message: "--" + name + " cannot be combined with --follow", Hint: "--follow streams new records as they arrive; --from replays from a time first"}
					}
				}
				client, err := env.client()
				if err != nil {
					return err
				}
				return followAudits(ctx, env, client, filters, follow)
			}
			if csvOutput {
				list.render = renderAuditCSV
			}
			if !bounded {
				values, err := filters.values(true)
				if err != nil {
					return err
				}
				if env.flagSet["limit"] {
					return &UsageError{Message: "--limit only applies to --bounded", Hint: "cursor pages use --page-size"}
				}
				list.request = func(*commandContext) (string, url.Values, error) { return auditPagePath, values, nil }
				return list.run(ctx, env)
			}
			if env.flagSet["cursor"] || env.flagSet["all"] || env.flagSet["page-size"] || env.flagSet["max-pages"] {
				return &UsageError{Message: "--bounded reads one window and takes no paging flags", Hint: "drop --bounded to page with --cursor or --all"}
			}
			if limit < 1 || limit > maxAuditLimit {
				return &UsageError{Message: fmt.Sprintf("--limit %d is out of range", limit), Hint: "use a limit from 1 to 500"}
			}
			values, err := filters.values(false)
			if err != nil {
				return err
			}
			values.Set("limit", strconv.Itoa(limit))
			client, err := env.client()
			if err != nil {
				return err
			}
			var records []adminopenapi.AuditRecord
			if err := client.getJSON(ctx, auditsPath, values, &records); err != nil {
				return err
			}
			// A full window may hide older records; the endpoint has no cursor.
			meta := &envelopeMeta{Pagination: &paginationMeta{Mode: "bounded", Limit: limit}}
			if len(records) < limit {
				complete := true
				meta.Pagination.Complete = &complete
			}
			return env.render(newListPayload(records), meta, func(w io.Writer) error { return list.render(w, records) })
		},
	}
}

// statusCheck is one line of `agctl status`.
type statusCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// statusReport is the data of `agctl status`. overall is the worst check:
// unhealthy, then degraded, then unknown, then healthy.
type statusReport struct {
	Server  string        `json:"server"`
	Overall string        `json:"overall"`
	Checks  []statusCheck `json:"checks"`
}

var statusRank = map[string]int{"healthy": 0, "unknown": 1, "degraded": 2, "unhealthy": 3}

func (r *statusReport) add(name, status, detail string) {
	r.Checks = append(r.Checks, statusCheck{Name: name, Status: status, Detail: detail})
	if statusRank[status] > statusRank[r.Overall] {
		r.Overall = status
	}
}

func statusCommand() command {
	var strict bool
	return command{
		name:     "status",
		synopsis: "check whether the Gateway is healthy; exits non-zero when it is not",
		usage:    "agctl status [--strict] [--json]",
		setFlags: func(fs *flag.FlagSet) {
			fs.BoolVar(&strict, "strict", false, "also exit non-zero when the result is degraded or unknown")
		},
		meta: commandMeta{
			Resource:     "runtime",
			Scenarios:    []string{"ci", "admin"},
			Stage:        "m1",
			Endpoints:    []string{"GET /livez", "GET /readyz", "GET " + diagnosticsPath},
			MinimumRole:  "anonymous for liveness and readiness; platform admin for diagnostics",
			OutputSchema: "StatusReport",
			DryRun:       "unsupported",
			Retry:        true,
		},
		run: func(ctx context.Context, env *commandContext) error {
			client, err := env.client()
			if err != nil {
				return err
			}
			report := statusReport{Server: redactText(redactURL(client.baseURL), client.token), Overall: "healthy"}
			for _, probe := range []string{"/livez", "/readyz"} {
				response, err := client.do(ctx, http.MethodGet, probe, requestOptions{idempotent: true})
				if err != nil {
					return err
				}
				if response.StatusCode < 300 {
					report.add(strings.TrimPrefix(probe, "/"), "healthy", "")
				} else {
					report.add(strings.TrimPrefix(probe, "/"), "unhealthy", fmt.Sprintf("HTTP %d", response.StatusCode))
				}
			}
			if err := addDiagnosticChecks(ctx, client, &report, env.settings.Token != ""); err != nil {
				return err
			}
			human := func(w io.Writer) error { return renderStatus(w, report) }
			if report.Overall == "unhealthy" || (strict && report.Overall != "healthy") {
				return &resultError{
					err:   &statusError{overall: report.Overall},
					data:  report,
					human: human,
				}
			}
			return env.render(report, nil, human)
		},
	}
}

// statusError fails `agctl status` with exit 1 and code "unhealthy" or
// "degraded"/"unknown" under --strict; the report is in the envelope data.
type statusError struct{ overall string }

func (e *statusError) Error() string { return "the Gateway is " + e.overall }

// addDiagnosticChecks reads /api/v2/diagnostics. Without credentials, or when
// the caller may not read it, the diagnostic checks are unknown, never
// healthy.
func addDiagnosticChecks(ctx context.Context, client *apiClient, report *statusReport, hasToken bool) error {
	if !hasToken {
		report.add("diagnostics", "unknown", "no credentials configured")
		return nil
	}
	var diagnostics adminopenapi.Diagnostics
	if err := client.getJSON(ctx, diagnosticsPath, nil, &diagnostics); err != nil {
		var api *APIError
		if !errors.As(err, &api) {
			return err
		}
		report.add("diagnostics", "unknown", fmt.Sprintf("HTTP %d (%s)", api.HTTPStatus, api.Code))
		return nil
	}
	for _, dependency := range diagnostics.Dependencies {
		name := "dependency " + dependency.Name
		switch dependency.Status {
		case adminopenapi.DiagnosticDependencyStatusReachable:
			report.add(name, "healthy", "")
		case adminopenapi.DiagnosticDependencyStatusNotConfigured:
			report.add(name, "healthy", "not configured")
		default:
			report.add(name, "unhealthy", dependencyDetail(dependency))
		}
	}
	nodes := diagnostics.Nodes
	nodeDetail := fmt.Sprintf("%d online, %d stale, %d offline", nodes.Online, nodes.Stale, nodes.Offline)
	if len(nodes.Issues) > 0 {
		codes := make([]string, 0, len(nodes.Issues))
		for _, issue := range nodes.Issues {
			codes = append(codes, issue.Code)
		}
		nodeDetail += "; issues: " + strings.Join(codes, ", ")
	}
	switch nodes.Status {
	case adminopenapi.RuntimeNodeHealthStatusHealthy:
		report.add("nodes", "healthy", nodeDetail)
	case adminopenapi.RuntimeNodeHealthStatusDegraded:
		report.add("nodes", "degraded", nodeDetail)
	default:
		report.add("nodes", "unhealthy", nodeDetail)
	}
	if scanner := diagnostics.Scanner; scanner != nil {
		detail := strings.TrimSpace(fmt.Sprintf("%s; vulnerability database %s", scanner.Status, scanner.DatabaseFreshness))
		switch {
		case scanner.Status == adminopenapi.DiagnosticScannerStatusNotConfigured:
			report.add("scanner", "healthy", "not configured")
		case scanner.Status == adminopenapi.DiagnosticScannerStatusUnhealthy || scanner.Status == adminopenapi.DiagnosticScannerStatusUnreachable:
			report.add("scanner", "unhealthy", detail)
		case scanner.Status == adminopenapi.DiagnosticScannerStatusUnknown:
			report.add("scanner", "unknown", detail)
		case scanner.Status == adminopenapi.DiagnosticScannerStatusHealthy && scanner.DatabaseFreshness == adminopenapi.DiagnosticScannerDatabaseFreshnessFresh:
			report.add("scanner", "healthy", detail)
		default:
			report.add("scanner", "degraded", detail)
		}
	}
	failed := 0
	for _, queue := range diagnostics.Queues {
		if queue.State == adminopenapi.DiagnosticQueueStatStateFailed {
			failed += queue.Count
		}
	}
	if failed > 0 {
		report.add("queues", "degraded", fmt.Sprintf("%d failed jobs", failed))
	} else {
		report.add("queues", "healthy", "")
	}
	return nil
}

// dependencyDetail is the status followed by the detail when the detail adds
// something.
func dependencyDetail(dependency adminopenapi.DiagnosticDependency) string {
	detail := strings.TrimSpace(optional(dependency.Detail))
	if detail == "" || detail == string(dependency.Status) {
		return string(dependency.Status)
	}
	return string(dependency.Status) + " (" + detail + ")"
}

func renderStatus(w io.Writer, report statusReport) error {
	if _, err := fmt.Fprintf(w, "%s: %s\n", report.Server, report.Overall); err != nil {
		return err
	}
	rows := [][]string{{"CHECK", "STATUS", "DETAIL"}}
	for _, check := range report.Checks {
		rows = append(rows, []string{check.Name, check.Status, check.Detail})
	}
	return writeColumns(w, rows)
}

func diagnosticsCommand() command {
	return command{
		name:     "diagnostics",
		synopsis: "show build, runtime, dependency, scanner, capacity, and queue diagnostics",
		usage:    "agctl diagnostics [--json]",
		meta: commandMeta{
			Resource:     "runtime",
			Scenarios:    []string{"admin"},
			Stage:        "m1",
			Endpoints:    []string{"GET " + diagnosticsPath},
			MinimumRole:  "platform admin",
			OutputSchema: "Diagnostics",
			DryRun:       "unsupported",
			Retry:        true,
		},
		run: documentCommand[adminopenapi.Diagnostics]{
			request: func(*commandContext) (string, url.Values, error) { return diagnosticsPath, nil, nil },
			render:  renderDiagnostics,
		}.run,
	}
}

func renderDiagnostics(w io.Writer, diagnostics adminopenapi.Diagnostics) error {
	build := diagnostics.Build
	lines := [][2]string{
		{"generated", diagnostics.GeneratedAt.UTC().Format(time.RFC3339)},
		{"build", fmt.Sprintf("%s (%s, %s)", build.Version, build.Revision, build.GoVersion)},
		{"instance", diagnostics.Runtime.InstanceId},
		{"nodes", fmt.Sprintf("%s: %d online, %d stale, %d offline", diagnostics.Nodes.Status, diagnostics.Nodes.Online, diagnostics.Nodes.Stale, diagnostics.Nodes.Offline)},
	}
	for _, dependency := range diagnostics.Dependencies {
		lines = append(lines, [2]string{"dependency " + dependency.Name, dependencyDetail(dependency)})
	}
	if scanner := diagnostics.Scanner; scanner != nil {
		lines = append(lines, [2]string{"scanner", fmt.Sprintf("%s (%s), database %s", scanner.Name, scanner.Status, scanner.DatabaseFreshness)})
	} else {
		lines = append(lines, [2]string{"scanner", "not reported"})
	}
	if rate := diagnostics.HttpErrorRate; rate != nil {
		value := string(rate.State)
		if rate.Ratio != nil && rate.Requests != nil {
			value = fmt.Sprintf("%.2f%% of %d requests (%s)", *rate.Ratio*100, *rate.Requests, rate.State)
		} else if rate.Reason != nil {
			value += " (" + string(*rate.Reason) + ")"
		}
		lines = append(lines, [2]string{"HTTP 5xx rate", value})
	}
	if capacity := diagnostics.LocalCapacity; capacity == nil {
		lines = append(lines, [2]string{"local capacity", "not configured"})
	} else {
		lines = append(lines, [2]string{"local capacity", fmt.Sprintf("%s, scope %s, unit %s; checked at %s", capacity.Source, capacity.Scope, capacity.Unit, capacity.CheckedAt.UTC().Format(time.RFC3339))})
		for _, mount := range capacity.Mounts {
			parts := []string{string(mount.Status)}
			if mount.Reason != nil {
				parts = append(parts, "reason "+string(*mount.Reason))
			}
			total, available := "unknown", "unknown"
			if mount.TotalBytes != nil {
				total = fmt.Sprintf("%d %s", *mount.TotalBytes, capacity.Unit)
			}
			if mount.AvailableBytes != nil {
				available = fmt.Sprintf("%d %s", *mount.AvailableBytes, capacity.Unit)
			}
			parts = append(parts, "total "+total, "available "+available)
			sample := formatTime(mount.SampleAt)
			if sample == "" {
				sample = "unknown"
			}
			parts = append(parts, "sample "+sample)
			if len(mount.SharedWith) > 0 {
				aliases := make([]string, 0, len(mount.SharedWith))
				for _, alias := range mount.SharedWith {
					aliases = append(aliases, string(alias))
				}
				parts = append(parts, "shared with "+strings.Join(aliases, ", "))
			}
			lines = append(lines, [2]string{"mount " + string(mount.Alias), strings.Join(parts, ", ")})
		}
	}
	for _, queue := range diagnostics.Queues {
		lines = append(lines, [2]string{fmt.Sprintf("queue %s/%s", queue.Kind, queue.Format), fmt.Sprintf("%d %s", queue.Count, queue.State)})
	}
	return keyValues(w, lines)
}

func runtimeNodesCommand() command {
	return command{
		name:     "runtime nodes",
		synopsis: "list the Gateway processes that reported in, with their roles and health",
		usage:    "agctl runtime nodes [--json]",
		meta: commandMeta{
			Resource:     "runtime",
			Scenarios:    []string{"admin"},
			Stage:        "m1",
			Endpoints:    []string{"GET " + runtimeNodesPath},
			MinimumRole:  "platform admin",
			OutputSchema: "RuntimeNodeList",
			DryRun:       "unsupported",
			Retry:        true,
		},
		run: documentCommand[adminopenapi.RuntimeNodeList]{
			request: func(*commandContext) (string, url.Values, error) { return runtimeNodesPath, nil, nil },
			render: func(w io.Writer, nodes adminopenapi.RuntimeNodeList) error {
				health := nodes.Health
				if _, err := fmt.Fprintf(w, "health: %s (%d online, %d stale, %d offline)\n", health.Status, health.Online, health.Stale, health.Offline); err != nil {
					return err
				}
				if len(nodes.Items) == 0 {
					return emptyLine(w, "nodes")
				}
				current := optional(nodes.CurrentSessionId)
				rows := [][]string{{"INSTANCE", "STATUS", "ROLES", "VERSION", "LAST SEEN", "SESSION"}}
				for _, node := range nodes.Items {
					seen := node.LastSeenAt
					session := node.SessionId
					if session == current {
						session += " (this response)"
					}
					rows = append(rows, []string{node.InstanceId, string(node.Status), strings.Join(node.Roles, ","), optional(node.Version), formatTime(&seen), session})
				}
				return writeColumns(w, rows)
			},
		}.run,
	}
}

func overviewShowCommand() command {
	return command{
		name:     "overview show",
		synopsis: "show request, denial, and capacity totals for the repositories the caller administers",
		usage:    "agctl overview show [--json]",
		meta: commandMeta{
			Resource:     "runtime",
			Scenarios:    []string{"admin"},
			Stage:        "m1",
			Endpoints:    []string{"GET " + overviewPath},
			MinimumRole:  "repositories:admin (filtered to administered repositories)",
			OutputSchema: "OverviewStatistics",
			DryRun:       "unsupported",
			Retry:        true,
		},
		run: documentCommand[adminopenapi.OverviewStatistics]{
			request: func(*commandContext) (string, url.Values, error) { return overviewPath, nil, nil },
			render: func(w io.Writer, overview adminopenapi.OverviewStatistics) error {
				totals := overview.Totals
				if _, err := fmt.Fprintf(w, "as of %s, over the repositories this credential administers:\nrequests 24h/7d/30d: %d / %d / %d\ndenied   24h/7d/30d: %d / %d / %d\nobjects: %d, %s used\n",
					overview.GeneratedAt.UTC().Format(time.RFC3339),
					totals.Requests.OneDay, totals.Requests.SevenDays, totals.Requests.ThirtyDays,
					totals.Denied.OneDay, totals.Denied.SevenDays, totals.Denied.ThirtyDays,
					totals.ObjectCount, humanBytes(totals.UsedBytes)); err != nil {
					return err
				}
				if len(overview.Repositories) == 0 {
					return nil
				}
				rows := [][]string{{"REPOSITORY", "FORMAT", "REQUESTS 24H", "DENIED 24H", "OBJECTS", "USED"}}
				for _, repo := range overview.Repositories {
					rows = append(rows, []string{repo.Name, string(repo.Format), strconv.FormatInt(repo.Requests.OneDay, 10), strconv.FormatInt(repo.Denied.OneDay, 10), strconv.FormatInt(repo.ObjectCount, 10), humanBytes(repo.UsedBytes)})
				}
				_, _ = fmt.Fprintln(w)
				return writeColumns(w, rows)
			},
		}.run,
	}
}

// lifecycleJobRow is one job of `lifecycle-job list`, with its repository
// when the list spans repositories.
type lifecycleJobRow struct {
	RepositoryID   string                    `json:"repositoryId,omitempty"`
	RepositoryName string                    `json:"repositoryName,omitempty"`
	Job            adminopenapi.LifecycleJob `json:"job"`
}

// readLifecycleJobs reads the newest jobs of one repository, or of every
// repository the caller administers. Neither endpoint pages: the global one
// limits before permission filtering; the repository one returns at most 100.
func readLifecycleJobs(ctx context.Context, client *apiClient, repo string, limit int) ([]lifecycleJobRow, *paginationMeta, error) {
	if repo != "" {
		var jobs []adminopenapi.LifecycleJob
		if err := client.getJSON(ctx, repositorySubPath(repo, "lifecycle-jobs"), nil, &jobs); err != nil {
			return nil, nil, err
		}
		rows := make([]lifecycleJobRow, 0, len(jobs))
		for _, job := range jobs {
			rows = append(rows, lifecycleJobRow{RepositoryID: repo, Job: job})
		}
		meta := &paginationMeta{Mode: "bounded", Limit: repositoryLifecycleLimit}
		if len(rows) < repositoryLifecycleLimit {
			complete := true
			meta.Complete = &complete
		}
		return rows, meta, nil
	}
	var jobs []adminopenapi.RepositoryLifecycleJob
	if err := client.getJSON(ctx, lifecycleJobsPath, url.Values{"limit": {strconv.Itoa(limit)}}, &jobs); err != nil {
		return nil, nil, err
	}
	rows := make([]lifecycleJobRow, 0, len(jobs))
	for _, item := range jobs {
		rows = append(rows, lifecycleJobRow{RepositoryID: item.RepositoryId.String(), RepositoryName: item.RepositoryName, Job: item.Job})
	}
	meta := &paginationMeta{Mode: "bounded", Limit: limit}
	// Fewer visible rows do not prove that older visible jobs do not exist:
	// the server applies its limit before filtering repository permissions.
	return rows, meta, nil
}

func renderLifecycleJobs(w io.Writer, rows []lifecycleJobRow) error {
	if len(rows) == 0 {
		return emptyLine(w, "lifecycle jobs")
	}
	table := [][]string{{"ID", "KIND", "STATE", "PROGRESS", "ATTEMPTS", "REPOSITORY", "CREATED"}}
	for _, row := range rows {
		job := row.Job
		repository := row.RepositoryName
		if repository == "" {
			repository = row.RepositoryID
		}
		created := job.CreatedAt
		table = append(table, []string{job.Id, string(job.Kind), string(job.State),
			fmt.Sprintf("%d/%d", job.ProgressCurrent, job.ProgressTotal), fmt.Sprintf("%d/%d", job.Attempts, job.MaxAttempts),
			repository, formatTime(&created)})
	}
	return writeColumns(w, table)
}

func lifecycleJobFlags(repo *string, limit *int) func(*flag.FlagSet) {
	return func(fs *flag.FlagSet) {
		fs.StringVar(repo, "repo", "", "only this repository (needs repositories:admin on it)")
		fs.IntVar(limit, "limit", maxLifecycleLimit, "newest jobs across repositories to read (1-500; without --repo)")
	}
}

func validLifecycleLimit(env *commandContext, repo string, limit int) error {
	if repo != "" && env.flagSet["limit"] {
		return &UsageError{Message: "--limit only applies without --repo", Hint: "the repository endpoint returns its newest jobs without a limit"}
	}
	if limit < 1 || limit > maxLifecycleLimit {
		return &UsageError{Message: fmt.Sprintf("--limit %d is out of range", limit), Hint: "use a limit from 1 to 500"}
	}
	return nil
}

var lifecycleJobEndpoints = []string{"GET " + lifecycleJobsPath, "GET " + repositoriesPath + "/{repositoryId}/lifecycle-jobs"}

func lifecycleJobListCommand() command {
	var repo string
	var limit int
	return command{
		name:     "lifecycle-job list",
		synopsis: "list the newest lifecycle jobs (retention, promotion, replication, reclaim, scans)",
		usage:    "agctl lifecycle-job list [--repo ID | --limit N] [--json]",
		setFlags: lifecycleJobFlags(&repo, &limit),
		meta: commandMeta{
			Resource:     "lifecycle-job",
			Scenarios:    []string{"admin"},
			Stage:        "m1",
			Endpoints:    lifecycleJobEndpoints,
			MinimumRole:  "repositories:admin (filtered to administered repositories)",
			OutputSchema: "LifecycleJobRows",
			Pagination:   true,
			DryRun:       "unsupported",
			Retry:        true,
		},
		run: func(ctx context.Context, env *commandContext) error {
			if err := validLifecycleLimit(env, repo, limit); err != nil {
				return err
			}
			client, err := env.client()
			if err != nil {
				return err
			}
			rows, pagination, err := readLifecycleJobs(ctx, client, repo, limit)
			if err != nil {
				return err
			}
			scope := map[string]any{"limitAppliedBeforeAuthorization": true}
			window := fmt.Sprintf("up to %d newest global jobs, filtered to administered repositories", limit)
			if repo != "" {
				scope = map[string]any{"repositoryId": repo}
				window = fmt.Sprintf("up to %d newest jobs of repository %s", repositoryLifecycleLimit, repo)
			}
			return env.render(newListPayload(rows), &envelopeMeta{Pagination: pagination, Scope: scope}, func(w io.Writer) error {
				if _, err := fmt.Fprintln(w, "window: "+window); err != nil {
					return err
				}
				return renderLifecycleJobs(w, rows)
			})
		},
	}
}

// lifecycleJobShowCommand finds one job in the same lists; there is no
// endpoint for a single job.
func lifecycleJobShowCommand() command {
	var repo string
	var limit int
	return command{
		name:        "lifecycle-job show",
		synopsis:    "show one lifecycle job, found among the newest jobs",
		usage:       "agctl lifecycle-job show JOB_ID [--repo ID | --limit N] [--json]",
		positionals: 1,
		argsUsage:   "JOB_ID",
		setFlags:    lifecycleJobFlags(&repo, &limit),
		meta: commandMeta{
			Resource:     "lifecycle-job",
			Scenarios:    []string{"admin"},
			Stage:        "m1",
			Endpoints:    lifecycleJobEndpoints,
			MinimumRole:  "repositories:admin (filtered to administered repositories)",
			OutputSchema: "LifecycleJobRow",
			DryRun:       "unsupported",
			Retry:        true,
		},
		run: func(ctx context.Context, env *commandContext) error {
			if err := validLifecycleLimit(env, repo, limit); err != nil {
				return err
			}
			client, err := env.client()
			if err != nil {
				return err
			}
			rows, _, err := readLifecycleJobs(ctx, client, repo, limit)
			if err != nil {
				return err
			}
			for _, row := range rows {
				if row.Job.Id != env.args[0] {
					continue
				}
				return env.render(row, nil, func(w io.Writer) error { return renderLifecycleJob(w, row) })
			}
			scope := fmt.Sprintf("the global window of %d newest jobs after repository-permission filtering", limit)
			if repo != "" {
				scope = fmt.Sprintf("the %d newest jobs of repository %s", repositoryLifecycleLimit, repo)
			}
			return &APIError{
				Code:       "not_found",
				HTTPStatus: http.StatusNotFound,
				Message:    fmt.Sprintf("lifecycle job %s is not among %s", env.args[0], scope),
				Hint:       "an older job may fall outside the list; pass --repo to search one repository's jobs",
			}
		},
	}
}

func renderLifecycleJob(w io.Writer, row lifecycleJobRow) error {
	job := row.Job
	created := job.CreatedAt
	repository := row.RepositoryName
	if repository == "" {
		repository = row.RepositoryID
	}
	details := ""
	if job.Details != nil {
		if encoded, err := json.Marshal(job.Details); err == nil && string(encoded) != "{}" {
			details = string(encoded)
		}
	}
	return keyValues(w, [][2]string{
		{"id", job.Id},
		{"kind", string(job.Kind)},
		{"state", string(job.State)},
		{"repository", repository},
		{"progress", fmt.Sprintf("%d/%d %s", job.ProgressCurrent, job.ProgressTotal, optional(job.ProgressMessage))},
		{"attempts", fmt.Sprintf("%d/%d", job.Attempts, job.MaxAttempts)},
		{"created", formatTime(&created)},
		{"started", formatTime(job.StartedAt)},
		{"completed", formatTime(job.CompletedAt)},
		{"next attempt", formatTime(job.NextAttemptAt)},
		{"last error", optional(job.LastError)},
		{"details", details},
	})
}
