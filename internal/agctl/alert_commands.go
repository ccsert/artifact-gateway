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
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

const (
	quotaRulesPath      = managementPrefix + "/repository-quota-alert-rules"
	emailCapabilityPath = managementPrefix + "/email-notifications"
	emailTargetsPath    = managementPrefix + "/email-targets"
	emailDeliveriesPath = managementPrefix + "/email-deliveries"
	emailRole           = "platform admin"
	maxEmailDeliveries  = 100
)

// definiteEmailRefusals are 503 answers that refuse a request before
// anything is queued: the channel is off or cannot encrypt. They are not
// unknown outcomes even though their status is 5xx.
var definiteEmailRefusals = map[string]bool{"email_disabled": true, "encryption_key_unavailable": true, "queue_full": true}

func emailOutcomeUnknown(err error) bool {
	var api *APIError
	if errors.As(err, &api) && definiteEmailRefusals[api.Code] {
		return false
	}
	return outcomeUnknown(err)
}

// readETagged reads a document and the version its ETag carries.
func readETagged[T any](ctx context.Context, client *apiClient, path string) (T, string, error) {
	var value T
	response, err := client.sendJSON(ctx, http.MethodGet, path, requestOptions{idempotent: true}, &value)
	if err != nil {
		return value, "", err
	}
	return value, response.Header.Get("ETag"), nil
}

// quotaRule is the CLI's view of a rule; JSON output passes the server
// document through unchanged.
type quotaRule struct {
	ID            string                                  `json:"id"`
	RepositoryID  string                                  `json:"repositoryId"`
	TargetID      string                                  `json:"targetId"`
	TargetVersion string                                  `json:"targetVersion"`
	Enabled       bool                                    `json:"enabled"`
	Deleted       bool                                    `json:"deleted"`
	Version       string                                  `json:"version"`
	Policy        adminopenapi.RepositoryQuotaAlertPolicy `json:"policy"`
	State         adminopenapi.RepositoryQuotaAlertState  `json:"state"`
	EvaluatedAt   *time.Time                              `json:"evaluatedAt"`
}

func (r quotaRule) complete() bool {
	return r.ID != "" && r.RepositoryID != "" && r.TargetID != "" && r.Version != "" && r.Policy.WarningBasisPoints > 0
}

func renderQuotaRules(w io.Writer, rules []quotaRule) error {
	if len(rules) == 0 {
		return emptyLine(w, "quota alert rules")
	}
	table := [][]string{{"ID", "REPOSITORY", "ENABLED", "SEVERITY", "DATA", "WARN/CRIT/RECOVER", "VERSION"}}
	for _, rule := range rules {
		enabled := yesNo(rule.Enabled)
		if rule.Deleted {
			enabled = "deleted"
		}
		p := rule.Policy
		table = append(table, []string{rule.ID, rule.RepositoryID, enabled, string(rule.State.Severity), string(rule.State.DataState),
			fmt.Sprintf("%s/%s/%s", basisPoints(p.WarningBasisPoints), basisPoints(p.CriticalBasisPoints), basisPoints(p.RecoveryBelowBasisPoints)), rule.Version})
	}
	return writeColumns(w, table)
}

func basisPoints(value int) string {
	return strconv.FormatFloat(float64(value)/100, 'f', -1, 64) + "%"
}

func renderQuotaRule(w io.Writer, rule quotaRule) error {
	p := rule.Policy
	usage := "unknown"
	if rule.State.UsedBytes != nil && rule.State.QuotaBytes != nil {
		usage = fmt.Sprintf("%s of %s", humanBytes(*rule.State.UsedBytes), humanBytes(*rule.State.QuotaBytes))
	}
	return keyValues(w, [][2]string{
		{"id", rule.ID},
		{"repository", rule.RepositoryID},
		{"email target", rule.TargetID + " (version " + rule.TargetVersion + ")"},
		{"enabled", yesNo(rule.Enabled)},
		{"deleted", yesNo(rule.Deleted)},
		{"warning", fmt.Sprintf("at %s for %ds", basisPoints(p.WarningBasisPoints), p.WarningForSeconds)},
		{"critical", fmt.Sprintf("at %s for %ds", basisPoints(p.CriticalBasisPoints), p.CriticalForSeconds)},
		{"recovery", fmt.Sprintf("below %s for %ds", basisPoints(p.RecoveryBelowBasisPoints), p.RecoveryForSeconds)},
		{"max sample age", fmt.Sprintf("%ds", p.MaxSampleAgeSeconds)},
		{"severity", fmt.Sprintf("%s (%s)", rule.State.Severity, rule.State.Phase)},
		{"data", string(rule.State.DataState)},
		{"usage", usage},
		{"evaluated", formatTime(rule.EvaluatedAt)},
		{"version", rule.Version},
	})
}

// policyFlags are the explicit thresholds of a rule. The CLI supplies no
// default policy: a new rule names every value.
type policyFlags struct {
	warning, critical, recovery         int
	warningFor, criticalFor, recoverFor time.Duration
	maxSampleAge                        time.Duration
}

func (f *policyFlags) register(fs *flag.FlagSet) {
	fs.IntVar(&f.warning, "warning-bp", 0, "warning threshold in basis points of the quota (8500 = 85%)")
	fs.IntVar(&f.critical, "critical-bp", 0, "critical threshold in basis points (above the warning)")
	fs.IntVar(&f.recovery, "recovery-below-bp", 0, "recovery threshold in basis points (below the warning)")
	fs.DurationVar(&f.warningFor, "warning-for", 0, "how long usage stays above the warning threshold first (1s-24h)")
	fs.DurationVar(&f.criticalFor, "critical-for", 0, "how long usage stays above the critical threshold first (1s-24h)")
	fs.DurationVar(&f.recoverFor, "recovery-for", 0, "how long usage stays below the recovery threshold first (1s-24h)")
	fs.DurationVar(&f.maxSampleAge, "max-sample-age", 0, "oldest capacity sample the rule trusts (30s-1h)")
}

var policyFlagNames = []string{"warning-bp", "critical-bp", "recovery-below-bp", "warning-for", "critical-for", "recovery-for", "max-sample-age"}

// apply overlays the flags that were given onto a policy and validates the
// result with the server's ordering and bounds.
func (f policyFlags) apply(env *commandContext, policy adminopenapi.RepositoryQuotaAlertPolicy) (adminopenapi.RepositoryQuotaAlertPolicy, error) {
	seconds := func(d time.Duration) int { return int(d / time.Second) }
	if env.flagSet["warning-bp"] {
		policy.WarningBasisPoints = f.warning
	}
	if env.flagSet["critical-bp"] {
		policy.CriticalBasisPoints = f.critical
	}
	if env.flagSet["recovery-below-bp"] {
		policy.RecoveryBelowBasisPoints = f.recovery
	}
	if env.flagSet["warning-for"] {
		policy.WarningForSeconds = seconds(f.warningFor)
	}
	if env.flagSet["critical-for"] {
		policy.CriticalForSeconds = seconds(f.criticalFor)
	}
	if env.flagSet["recovery-for"] {
		policy.RecoveryForSeconds = seconds(f.recoverFor)
	}
	if env.flagSet["max-sample-age"] {
		policy.MaxSampleAgeSeconds = seconds(f.maxSampleAge)
	}
	if policy.RecoveryBelowBasisPoints <= 0 || policy.RecoveryBelowBasisPoints >= policy.WarningBasisPoints || policy.WarningBasisPoints >= policy.CriticalBasisPoints || policy.CriticalBasisPoints > 10000 {
		return policy, &UsageError{Message: "thresholds must satisfy 0 < recovery-below-bp < warning-bp < critical-bp <= 10000", Hint: "for example --recovery-below-bp 8000 --warning-bp 8500 --critical-bp 9500"}
	}
	for _, value := range []int{policy.WarningForSeconds, policy.CriticalForSeconds, policy.RecoveryForSeconds} {
		if value < 1 || value > 86400 {
			return policy, &UsageError{Message: "--warning-for, --critical-for, and --recovery-for must be 1s-24h", Hint: "for example --warning-for 10m"}
		}
	}
	if policy.MaxSampleAgeSeconds < 30 || policy.MaxSampleAgeSeconds > 3600 {
		return policy, &UsageError{Message: "--max-sample-age must be 30s-1h", Hint: "for example --max-sample-age 10m"}
	}
	return policy, nil
}

// quotaRuleWrite sends a rule write and requires the rule it names.
func quotaRuleWrite(ctx context.Context, client *apiClient, method, path string, body any, header http.Header, id string) (json.RawMessage, quotaRule, error) {
	encoded, err := encodeBody(body)
	if err != nil {
		return nil, quotaRule{}, err
	}
	var raw json.RawMessage
	response, err := client.sendJSON(ctx, method, path, requestOptions{body: encoded, header: header}, &raw)
	if err != nil {
		return nil, quotaRule{}, err
	}
	var rule quotaRule
	if json.Unmarshal(raw, &rule) != nil || !rule.complete() || (id != "" && !strings.EqualFold(rule.ID, id)) {
		return nil, quotaRule{}, incompleteResponse(response, client, "quota alert rule")
	}
	return raw, rule, nil
}

func quotaRulePath(id string) string { return quotaRulesPath + "/" + url.PathEscape(id) }

func quotaAlertListCommand() command {
	return command{
		name:     "quota-alert list",
		synopsis: "list repository quota alert rules with their current state",
		usage:    "agctl quota-alert list [--json]",
		meta: commandMeta{
			Resource: "quota-alert", Scenarios: []string{"admin"}, Stage: "m2",
			Endpoints: []string{"GET " + quotaRulesPath}, MinimumRole: emailRole,
			OutputSchema: "RepositoryQuotaAlertRuleList", DryRun: "unsupported", Retry: true,
		},
		run: func(ctx context.Context, env *commandContext) error {
			client, err := env.client()
			if err != nil {
				return err
			}
			var raw json.RawMessage
			if err := client.getJSON(ctx, quotaRulesPath, nil, &raw); err != nil {
				return err
			}
			var rules []quotaRule
			if err := json.Unmarshal(raw, &rules); err != nil {
				return &APIError{Code: "invalid_response", HTTPStatus: http.StatusOK, Message: "server returned an unreadable rule list"}
			}
			var items []json.RawMessage
			_ = json.Unmarshal(raw, &items)
			meta := &envelopeMeta{Pagination: &paginationMeta{Mode: "none"}}
			return env.render(newListPayload(items), meta, func(w io.Writer) error { return renderQuotaRules(w, rules) })
		},
	}
}

func quotaAlertShowCommand() command {
	return command{
		name:        "quota-alert show",
		synopsis:    "show one quota alert rule",
		usage:       "agctl quota-alert show RULE_ID [--json]",
		positionals: 1,
		argsUsage:   "RULE_ID",
		meta: commandMeta{
			Resource: "quota-alert", Scenarios: []string{"admin"}, Stage: "m2",
			Endpoints: []string{"GET " + quotaRulesPath + "/{ruleId}"}, MinimumRole: emailRole,
			OutputSchema: "RepositoryQuotaAlertRule", DryRun: "unsupported", Retry: true,
		},
		run: documentCommand[quotaRule]{
			request: func(env *commandContext) (string, url.Values, error) { return quotaRulePath(env.args[0]), nil, nil },
			render:  renderQuotaRule,
		}.run,
	}
}

func quotaAlertEventsCommand() command {
	return command{
		name:        "quota-alert events",
		synopsis:    "list the warning, critical, and resolved events of a rule with their delivery state",
		usage:       "agctl quota-alert events RULE_ID [--json]",
		positionals: 1,
		argsUsage:   "RULE_ID",
		meta: commandMeta{
			Resource: "quota-alert", Scenarios: []string{"admin"}, Stage: "m2",
			Endpoints: []string{"GET " + quotaRulesPath + "/{ruleId}/events"}, MinimumRole: emailRole,
			OutputSchema: "RepositoryQuotaAlertEventList", DryRun: "unsupported", Retry: true,
		},
		run: func(ctx context.Context, env *commandContext) error {
			client, err := env.client()
			if err != nil {
				return err
			}
			var events []json.RawMessage
			if err := client.getJSON(ctx, quotaRulePath(env.args[0])+"/events", nil, &events); err != nil {
				return err
			}
			var typed []adminopenapi.RepositoryQuotaAlertEvent
			_ = json.Unmarshal(mustJSON(events), &typed)
			meta := &envelopeMeta{Pagination: &paginationMeta{Mode: "none"}}
			return env.render(newListPayload(events), meta, func(w io.Writer) error {
				if len(typed) == 0 {
					return emptyLine(w, "events")
				}
				table := [][]string{{"SEQ", "SCENARIO", "USED", "QUOTA", "NOTIFICATION", "DELIVERY", "OCCURRED"}}
				for _, event := range typed {
					occurred := event.OccurredAt
					delivery := ""
					if event.DeliveryState != nil {
						delivery = string(*event.DeliveryState)
					}
					table = append(table, []string{strconv.FormatInt(event.Sequence, 10), string(event.Scenario), humanBytes(event.UsedBytes), humanBytes(event.QuotaBytes), string(event.NotificationCode), delivery, formatTime(&occurred)})
				}
				return writeColumns(w, table)
			})
		},
	}
}

func mustJSON(value any) []byte {
	encoded, _ := json.Marshal(value)
	return encoded
}

func quotaAlertCreateCommand() command {
	var repo, target string
	var enabled bool
	var policy policyFlags
	return command{
		name:     "quota-alert create",
		synopsis: "create a quota alert rule for a repository (disabled unless --enabled)",
		usage:    "agctl quota-alert create --repo ID --target EMAIL_TARGET_ID --warning-bp N --critical-bp N --recovery-below-bp N --warning-for D --critical-for D --recovery-for D --max-sample-age D [--enabled --yes] [--dry-run] [--json]",
		setFlags: func(fs *flag.FlagSet) {
			fs.StringVar(&repo, "repo", "", "repository ID")
			fs.StringVar(&target, "target", "", "email target ID to notify")
			fs.BoolVar(&enabled, "enabled", false, "enable the rule at once (needs --yes and a ready email channel)")
			policy.register(fs)
		},
		meta: commandMeta{
			Resource: "quota-alert", Scenarios: []string{"admin"}, Stage: "m2",
			Endpoints: []string{"POST " + quotaRulesPath}, Mutating: true, MinimumRole: emailRole,
			InputSchema: "RepositoryQuotaAlertRuleInput", OutputSchema: "RepositoryQuotaAlertRule", DryRun: "client-plan",
		},
		run: func(ctx context.Context, env *commandContext) error {
			if err := requireRepo(repo); err != nil {
				return err
			}
			repoID, err := uuid.Parse(repo)
			if err != nil {
				return &UsageError{Message: "--repo must be a repository ID", Hint: "agctl repo list shows the IDs"}
			}
			targetID, err := uuid.Parse(target)
			if err != nil {
				return &UsageError{Message: "--target must be an email target ID", Hint: "agctl email target list shows the IDs"}
			}
			for _, name := range policyFlagNames {
				if !env.flagSet[name] {
					return &UsageError{Message: "missing required flag --" + name, Hint: "a new rule names every threshold and duration; there is no default policy"}
				}
			}
			values, err := policy.apply(env, adminopenapi.RepositoryQuotaAlertPolicy{})
			if err != nil {
				return err
			}
			if enabled && !env.globals.dryRun && !env.globals.yes {
				return &UsageError{Message: "creating an enabled rule needs --yes; it can send email at once", Hint: "create it disabled and enable it later with agctl quota-alert update RULE_ID --enabled=true --yes"}
			}
			body := adminopenapi.RepositoryQuotaAlertRuleInput{RepositoryId: repoID, TargetId: targetID, Enabled: &enabled, Policy: values}
			if env.globals.dryRun {
				plan := writePlan{Mode: "client-plan", Method: http.MethodPost, Path: quotaRulesPath, Headers: map[string]string{}, Body: body,
					Verified:    []string{"IDs, threshold order, and duration bounds"},
					NotVerified: []string{"caller permission (platform admin)", "that the repository and the email target exist", "that the email channel is ready (needed to enable)", "the rule limit per repository"}}
				return env.render(plan, nil, func(w io.Writer) error { return renderPlan(w, plan) })
			}
			client, err := env.client()
			if err != nil {
				return err
			}
			// Not retried: the endpoint has no idempotency key.
			raw, rule, err := quotaRuleWrite(ctx, client, http.MethodPost, quotaRulesPath, body, nil, "")
			if err == nil && (!strings.EqualFold(rule.RepositoryID, repo) || !strings.EqualFold(rule.TargetID, target) || rule.Enabled != enabled) {
				err = &APIError{Code: "invalid_response", HTTPStatus: http.StatusCreated, Message: "server returned a rule that is not the one requested"}
			}
			if err != nil {
				if emailOutcomeUnknown(err) {
					return unknownOutcomeError(err, unknownOutcome{Operation: "quota-alert create", ID: repo, ReadBack: "agctl quota-alert list"},
						"the rule may have been created; look for it in agctl quota-alert list before creating it again")
				}
				return err
			}
			return env.render(raw, nil, func(w io.Writer) error { return renderQuotaRule(w, rule) })
		},
	}
}

func quotaAlertUpdateCommand() command {
	var target, ifMatch string
	var enabled bool
	var policy policyFlags
	return command{
		name:        "quota-alert update",
		synopsis:    "change a rule's thresholds, target, or enabled state (conditional on its version)",
		usage:       "agctl quota-alert update RULE_ID [--enabled=true|false] [--target ID] [policy flags] [--if-match VERSION] [--yes] [--dry-run] [--json]",
		positionals: 1,
		argsUsage:   "RULE_ID",
		setFlags: func(fs *flag.FlagSet) {
			fs.BoolVar(&enabled, "enabled", false, "enable or disable the rule; enabling needs --yes")
			fs.StringVar(&target, "target", "", "new email target ID")
			fs.StringVar(&ifMatch, "if-match", "", "apply only if the rule is still at this version (default: the version read just before)")
			policy.register(fs)
		},
		meta: commandMeta{
			Resource: "quota-alert", Scenarios: []string{"admin"}, Stage: "m2",
			Endpoints: []string{"PUT " + quotaRulesPath + "/{ruleId}", "GET " + quotaRulesPath + "/{ruleId}"}, Mutating: true, MinimumRole: emailRole,
			InputSchema: "RepositoryQuotaAlertRuleInput", OutputSchema: "RepositoryQuotaAlertRule", DryRun: "client-plan",
		},
		run: func(ctx context.Context, env *commandContext) error {
			id := env.args[0]
			changed := env.flagSet["enabled"] || env.flagSet["target"]
			for _, name := range policyFlagNames {
				changed = changed || env.flagSet[name]
			}
			if !changed {
				return &UsageError{Message: "nothing to update", Hint: "pass --enabled, --target, or a threshold or duration flag"}
			}
			if env.flagSet["if-match"] && strings.TrimSpace(ifMatch) == "" {
				return &UsageError{Message: "--if-match cannot be empty", Hint: "pass the version from agctl quota-alert show"}
			}
			if env.flagSet["enabled"] && enabled && !env.globals.dryRun && !env.globals.yes {
				return &UsageError{Message: "enabling a rule needs --yes; it can send email at once", Hint: "re-run with --yes, or with --dry-run to see the request"}
			}
			client, err := env.client()
			if err != nil {
				return err
			}
			// The endpoint replaces the whole rule, and an omitted "enabled" means
			// false, so the current rule is read and sent back with the changes.
			current, version, err := readETagged[quotaRule](ctx, client, quotaRulePath(id))
			if err != nil {
				return err
			}
			if current.Deleted {
				return &APIError{Code: "rule_deleted", HTTPStatus: http.StatusConflict, Message: "quota alert rule " + id + " is deleted", Hint: "create a new rule"}
			}
			if ifMatch == "" {
				ifMatch = version
				if ifMatch == "" {
					ifMatch = current.Version
				}
			}
			values, err := policy.apply(env, current.Policy)
			if err != nil {
				return err
			}
			repoID, _ := uuid.Parse(current.RepositoryID)
			targetID, _ := uuid.Parse(current.TargetID)
			if env.flagSet["target"] {
				if targetID, err = uuid.Parse(target); err != nil {
					return &UsageError{Message: "--target must be an email target ID", Hint: "agctl email target list shows the IDs"}
				}
			}
			next := current.Enabled
			if env.flagSet["enabled"] {
				next = enabled
			}
			body := adminopenapi.RepositoryQuotaAlertRuleInput{RepositoryId: repoID, TargetId: targetID, Enabled: &next, Policy: values}
			if env.globals.dryRun {
				plan := writePlan{Mode: "client-plan", Method: http.MethodPut, Path: quotaRulePath(id), Headers: map[string]string{"If-Match": ifMatch}, Body: body, Current: current,
					Verified:    []string{"the rule was read; unchanged fields, including enabled, are sent back as they are", "threshold order and duration bounds"},
					NotVerified: []string{"caller permission (platform admin)", "that the version still matches", "that the email channel is ready (needed to enable)"}}
				return env.render(plan, nil, func(w io.Writer) error { return renderPlan(w, plan) })
			}
			// Conditional and therefore not retried.
			raw, rule, err := quotaRuleWrite(ctx, client, http.MethodPut, quotaRulePath(id), body, http.Header{"If-Match": {ifMatch}}, id)
			if err == nil && rule.Enabled != next {
				err = &APIError{Code: "invalid_response", HTTPStatus: http.StatusOK, Message: "server returned a rule with another enabled state"}
			}
			if err != nil {
				var api *APIError
				if errors.As(err, &api) && api.HTTPStatus == http.StatusPreconditionFailed {
					api.Hint = fmt.Sprintf("the rule changed since version %s and was not updated; run agctl quota-alert show %s and retry", ifMatch, id)
				}
				if emailOutcomeUnknown(err) {
					return unknownOutcomeError(err, unknownOutcome{Operation: "quota-alert update", ID: id, ReadBack: "agctl quota-alert show " + id},
						fmt.Sprintf("the update may have been applied; compare the version (%s before) in agctl quota-alert show %s", ifMatch, id))
				}
				return err
			}
			return env.render(raw, nil, func(w io.Writer) error { return renderQuotaRule(w, rule) })
		},
	}
}

func quotaAlertDeleteCommand() command {
	var ifMatch string
	return command{
		name:        "quota-alert delete",
		synopsis:    "delete a quota alert rule (conditional on its version)",
		usage:       "agctl quota-alert delete RULE_ID --yes [--if-match VERSION] [--dry-run] [--json]",
		positionals: 1,
		argsUsage:   "RULE_ID",
		setFlags: func(fs *flag.FlagSet) {
			fs.StringVar(&ifMatch, "if-match", "", "delete only if the rule is still at this version (default: the version read just before)")
		},
		meta: commandMeta{
			Resource: "quota-alert", Scenarios: []string{"admin"}, Stage: "m2",
			Endpoints: []string{"DELETE " + quotaRulesPath + "/{ruleId}", "GET " + quotaRulesPath + "/{ruleId}"}, Mutating: true, MinimumRole: emailRole,
			OutputSchema: "QuotaRuleDeletion", DryRun: "client-plan",
		},
		run: func(ctx context.Context, env *commandContext) error {
			id := env.args[0]
			if !env.globals.dryRun && !env.globals.yes {
				return &UsageError{Message: "agctl quota-alert delete needs --yes", Hint: "re-run with --yes, or with --dry-run; disabling with --enabled=false keeps the rule"}
			}
			if env.flagSet["if-match"] && strings.TrimSpace(ifMatch) == "" {
				return &UsageError{Message: "--if-match cannot be empty", Hint: "pass the version from agctl quota-alert show"}
			}
			client, err := env.client()
			if err != nil {
				return err
			}
			if ifMatch == "" || env.globals.dryRun {
				current, version, err := readETagged[quotaRule](ctx, client, quotaRulePath(id))
				if err != nil {
					return err
				}
				if ifMatch == "" {
					ifMatch = version
					if ifMatch == "" {
						ifMatch = current.Version
					}
				}
				if env.globals.dryRun {
					plan := writePlan{Mode: "client-plan", Method: http.MethodDelete, Path: quotaRulePath(id), Headers: map[string]string{"If-Match": ifMatch}, Current: current,
						Verified: []string{"the rule exists"}, NotVerified: []string{"caller permission (platform admin)", "that the version still matches"}}
					return env.render(plan, nil, func(w io.Writer) error { return renderPlan(w, plan) })
				}
			}
			// Conditional and therefore not retried.
			response, err := client.sendJSON(ctx, http.MethodDelete, quotaRulePath(id), requestOptions{header: http.Header{"If-Match": {ifMatch}}}, nil)
			if err != nil {
				var api *APIError
				if errors.As(err, &api) && api.HTTPStatus == http.StatusPreconditionFailed {
					api.Hint = fmt.Sprintf("the rule changed since version %s and was not deleted; run agctl quota-alert show %s", ifMatch, id)
				}
				if emailOutcomeUnknown(err) {
					return unknownOutcomeError(err, unknownOutcome{Operation: "quota-alert delete", ID: id, ReadBack: "agctl quota-alert show " + id},
						fmt.Sprintf("the rule may have been deleted; agctl quota-alert show %s reports deleted once it is", id))
				}
				return err
			}
			result := map[string]any{"id": id, "deleted": true, "version": response.Header.Get("ETag")}
			return env.render(result, nil, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "quota alert rule %s deleted\n", id)
				return err
			})
		},
	}
}

func emailCapabilityCommand() command {
	return command{
		name:     "email capability",
		synopsis: "show whether this Gateway process can queue email (not whether mail is delivered)",
		usage:    "agctl email capability [--json]",
		meta: commandMeta{
			Resource: "email", Scenarios: []string{"admin"}, Stage: "m2",
			Endpoints: []string{"GET " + emailCapabilityPath}, MinimumRole: emailRole,
			OutputSchema: "EmailCapability", DryRun: "unsupported", Retry: true,
		},
		run: documentCommand[adminopenapi.EmailCapability]{
			request: func(*commandContext) (string, url.Values, error) { return emailCapabilityPath, nil, nil },
			render: func(w io.Writer, capability adminopenapi.EmailCapability) error {
				_, err := fmt.Fprintf(w, "email: %s (%s); this is the answering process's configuration, not proof of delivery\n",
					map[bool]string{true: "can queue", false: "cannot queue"}[capability.Enabled], capability.Reason)
				return err
			},
		}.run,
	}
}

func emailTargetPath(id string) string { return emailTargetsPath + "/" + url.PathEscape(id) }

func renderEmailTargets(w io.Writer, targets []adminopenapi.EmailTarget) error {
	if len(targets) == 0 {
		return emptyLine(w, "email targets")
	}
	table := [][]string{{"NAME", "LOCALE", "ENABLED", "RECIPIENT", "VERSION", "ID"}}
	for _, target := range targets {
		table = append(table, []string{target.Name, string(target.Locale), yesNo(target.Enabled), map[bool]string{true: "configured", false: "missing"}[target.RecipientConfigured], target.Version, target.Id.String()})
	}
	return writeColumns(w, table)
}

// recipientInput reads a recipient address from bounded stdin or a private
// file. The address is registered for redaction and never printed.
type recipientInput struct {
	stdin bool
	file  string
}

func (r *recipientInput) register(fs *flag.FlagSet) {
	fs.BoolVar(&r.stdin, "recipient-stdin", false, "read the recipient address from stdin (bounded, never echoed)")
	fs.StringVar(&r.file, "recipient-file", "", "read the recipient address from this private file (first line)")
}

func (r recipientInput) given() bool { return r.stdin || r.file != "" }

func (r recipientInput) read(env *commandContext) (string, error) {
	hint := `pass it privately, for example: printf '%s\n' "$ALERT_RECIPIENT" | agctl email target create NAME --locale en --recipient-stdin`
	if r.stdin && r.file != "" {
		return "", &UsageError{Message: "pass only one of --recipient-stdin or --recipient-file", Hint: hint}
	}
	var value string
	if r.stdin {
		read, err := readBoundedSecret(env.stdin, secretInputLimit, "recipient", hint)
		if err != nil {
			return "", err
		}
		value = read
	} else {
		file, err := os.Open(r.file)
		if err != nil {
			return "", &UsageError{Message: "cannot read --recipient-file " + r.file, Hint: hint}
		}
		defer func() { _ = file.Close() }()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || info.Size() > secretInputLimit {
			return "", &UsageError{Message: "--recipient-file must be a private regular file of at most 4096 bytes", Hint: "chmod 600 the file, or use --recipient-stdin"}
		}
		data, err := io.ReadAll(io.LimitReader(file, secretInputLimit+1))
		if err != nil || len(data) > secretInputLimit {
			return "", &UsageError{Message: "cannot read --recipient-file " + r.file, Hint: hint}
		}
		value, _, _ = strings.Cut(string(data), "\n")
		value = strings.TrimSpace(value)
	}
	env.addKnownSecret(value)
	if value == "" || utf8.RuneCountInString(value) > 254 || !strings.Contains(value, "@") || strings.ContainsAny(value, " \t\r\n") {
		return "", &UsageError{Message: "the recipient is not an email address of at most 254 characters", Hint: hint}
	}
	return value, nil
}

const recipientPlaceholder = "<read from --recipient-stdin or --recipient-file; never shown>"

func completeEmailTarget(target adminopenapi.EmailTarget) bool {
	return target.Id != (openapi_types.UUID{}) && target.Name != "" && target.Locale.Valid() && target.Version != ""
}

func sendEmailTarget(ctx context.Context, client *apiClient, method, path string, body adminopenapi.EmailTargetInput, header http.Header) (adminopenapi.EmailTarget, error) {
	encoded, err := encodeBody(body)
	if err != nil {
		return adminopenapi.EmailTarget{}, err
	}
	var target adminopenapi.EmailTarget
	response, err := client.sendJSON(ctx, method, path, requestOptions{body: encoded, header: header}, &target)
	if err == nil && (!completeEmailTarget(target) || target.Name != body.Name || target.Locale != adminopenapi.EmailTargetLocale(body.Locale) ||
		(body.Enabled != nil && target.Enabled != *body.Enabled) || (body.Recipient != nil && !target.RecipientConfigured)) {
		err = incompleteResponse(response, client, "email target")
	}
	return target, err
}

func emailTargetListCommand() command {
	return command{
		name:     "email target list",
		synopsis: "list email targets (recipients are never shown)",
		usage:    "agctl email target list [--json]",
		meta: commandMeta{
			Resource: "email", Scenarios: []string{"admin"}, Stage: "m2",
			Endpoints: []string{"GET " + emailTargetsPath}, MinimumRole: emailRole,
			OutputSchema: "EmailTargetList", DryRun: "unsupported", Retry: true,
		},
		run: func(ctx context.Context, env *commandContext) error {
			client, err := env.client()
			if err != nil {
				return err
			}
			var targets []adminopenapi.EmailTarget
			if err := client.getJSON(ctx, emailTargetsPath, nil, &targets); err != nil {
				return err
			}
			meta := &envelopeMeta{Pagination: &paginationMeta{Mode: "none"}}
			return env.render(newListPayload(targets), meta, func(w io.Writer) error { return renderEmailTargets(w, targets) })
		},
	}
}

func emailTargetShowCommand() command {
	return command{
		name:        "email target show",
		synopsis:    "show one email target",
		usage:       "agctl email target show ID [--json]",
		positionals: 1,
		argsUsage:   "ID",
		meta: commandMeta{
			Resource: "email", Scenarios: []string{"admin"}, Stage: "m2",
			Endpoints: []string{"GET " + emailTargetsPath + "/{targetId}"}, MinimumRole: emailRole,
			OutputSchema: "EmailTarget", DryRun: "unsupported", Retry: true,
		},
		run: documentCommand[adminopenapi.EmailTarget]{
			request: func(env *commandContext) (string, url.Values, error) { return emailTargetPath(env.args[0]), nil, nil },
			render: func(w io.Writer, target adminopenapi.EmailTarget) error {
				return renderEmailTargets(w, []adminopenapi.EmailTarget{target})
			},
		}.run,
	}
}

func emailTargetCreateCommand() command {
	var locale string
	var enabled bool
	var recipient recipientInput
	return command{
		name:        "email target create",
		synopsis:    "create an email target; the recipient comes only from stdin or a private file",
		usage:       "agctl email target create NAME --locale en|zh-CN (--recipient-stdin | --recipient-file FILE) [--enabled] [--dry-run] [--json]",
		positionals: 1,
		argsUsage:   "NAME",
		setFlags: func(fs *flag.FlagSet) {
			fs.StringVar(&locale, "locale", "", "message language: en or zh-CN")
			fs.BoolVar(&enabled, "enabled", false, "enable the target (default: disabled)")
			recipient.register(fs)
		},
		meta: commandMeta{
			Resource: "email", Scenarios: []string{"admin"}, Stage: "m2",
			Endpoints: []string{"POST " + emailTargetsPath}, Mutating: true, MinimumRole: emailRole,
			InputSchema: "EmailTargetInput", OutputSchema: "EmailTarget", DryRun: "client-plan",
		},
		run: func(ctx context.Context, env *commandContext) error {
			name := strings.TrimSpace(env.args[0])
			if name == "" || utf8.RuneCountInString(name) > 128 {
				return &UsageError{Message: "the target name must be 1-128 characters", Hint: "for example agctl email target create ops --locale en --recipient-stdin"}
			}
			if !adminopenapi.EmailTargetInputLocale(locale).Valid() {
				return &UsageError{Message: "--locale must be en or zh-CN", Hint: "messages are rendered in this language"}
			}
			if !recipient.given() {
				return &UsageError{Message: "a target needs a recipient from --recipient-stdin or --recipient-file", Hint: "the address is never passed on the command line"}
			}
			address, err := recipient.read(env)
			if err != nil {
				return err
			}
			body := adminopenapi.EmailTargetInput{Name: name, Locale: adminopenapi.EmailTargetInputLocale(locale), Enabled: &enabled, Recipient: &address}
			if env.globals.dryRun {
				shown := body
				placeholder := recipientPlaceholder
				shown.Recipient = &placeholder
				plan := writePlan{Mode: "client-plan", Method: http.MethodPost, Path: emailTargetsPath, Headers: map[string]string{}, Body: shown,
					Verified: []string{"name, locale, and recipient shape"}, NotVerified: []string{"caller permission (platform admin)", "that the settings encryption key is configured"}}
				return env.render(plan, nil, func(w io.Writer) error { return renderPlan(w, plan) })
			}
			client, err := env.client()
			if err != nil {
				return err
			}
			// Not retried: the endpoint has no idempotency key.
			target, err := sendEmailTarget(ctx, client, http.MethodPost, emailTargetsPath, body, nil)
			if err != nil {
				if emailOutcomeUnknown(err) {
					return unknownOutcomeError(err, unknownOutcome{Operation: "email target create", Name: name, ReadBack: "agctl email target list"},
						fmt.Sprintf("the target may have been created; look for %q in agctl email target list before creating it again", name))
				}
				return err
			}
			return env.render(target, nil, func(w io.Writer) error { return renderEmailTargets(w, []adminopenapi.EmailTarget{target}) })
		},
	}
}

func emailTargetUpdateCommand() command {
	var name, locale, ifMatch string
	var enabled bool
	var recipient recipientInput
	return command{
		name:        "email target update",
		synopsis:    "rename, relocalize, enable, disable, or re-address an email target (conditional on its version)",
		usage:       "agctl email target update ID [--name NAME] [--locale en|zh-CN] [--enabled=true|false] [--recipient-stdin | --recipient-file FILE] [--if-match VERSION] [--dry-run] [--json]",
		positionals: 1,
		argsUsage:   "ID",
		setFlags: func(fs *flag.FlagSet) {
			fs.StringVar(&name, "name", "", "new name")
			fs.StringVar(&locale, "locale", "", "new language: en or zh-CN")
			fs.BoolVar(&enabled, "enabled", false, "enable or disable the target")
			fs.StringVar(&ifMatch, "if-match", "", "apply only if the target is still at this version (default: the version read just before)")
			recipient.register(fs)
		},
		meta: commandMeta{
			Resource: "email", Scenarios: []string{"admin"}, Stage: "m2",
			Endpoints: []string{"PUT " + emailTargetsPath + "/{targetId}", "GET " + emailTargetsPath + "/{targetId}"}, Mutating: true, MinimumRole: emailRole,
			InputSchema: "EmailTargetInput", OutputSchema: "EmailTarget", DryRun: "client-plan",
		},
		run: func(ctx context.Context, env *commandContext) error {
			id := env.args[0]
			if !env.flagSet["name"] && !env.flagSet["locale"] && !env.flagSet["enabled"] && !recipient.given() {
				return &UsageError{Message: "nothing to update", Hint: "pass --name, --locale, --enabled, or a new recipient"}
			}
			if env.flagSet["locale"] && !adminopenapi.EmailTargetInputLocale(locale).Valid() {
				return &UsageError{Message: "--locale must be en or zh-CN", Hint: "messages are rendered in this language"}
			}
			if env.flagSet["name"] && (strings.TrimSpace(name) == "" || utf8.RuneCountInString(strings.TrimSpace(name)) > 128) {
				return &UsageError{Message: "--name must be 1-128 characters", Hint: "pass a short name"}
			}
			if env.flagSet["if-match"] && strings.TrimSpace(ifMatch) == "" {
				return &UsageError{Message: "--if-match cannot be empty", Hint: "pass the version from agctl email target show"}
			}
			var address *string
			if recipient.given() {
				value, err := recipient.read(env)
				if err != nil {
					return err
				}
				address = &value
			}
			client, err := env.client()
			if err != nil {
				return err
			}
			// The endpoint replaces the target and an omitted "enabled" means
			// false, so unchanged fields are read and sent back as they are.
			current, version, err := readETagged[adminopenapi.EmailTarget](ctx, client, emailTargetPath(id))
			if err != nil {
				return err
			}
			if ifMatch == "" {
				ifMatch = version
				if ifMatch == "" {
					ifMatch = current.Version
				}
			}
			body := adminopenapi.EmailTargetInput{Name: current.Name, Locale: adminopenapi.EmailTargetInputLocale(current.Locale), Recipient: address}
			next := current.Enabled
			if env.flagSet["enabled"] {
				next = enabled
			}
			body.Enabled = &next
			if env.flagSet["name"] {
				body.Name = strings.TrimSpace(name)
			}
			if env.flagSet["locale"] {
				body.Locale = adminopenapi.EmailTargetInputLocale(locale)
			}
			if env.globals.dryRun {
				shown := body
				if shown.Recipient != nil {
					placeholder := recipientPlaceholder
					shown.Recipient = &placeholder
				}
				plan := writePlan{Mode: "client-plan", Method: http.MethodPut, Path: emailTargetPath(id), Headers: map[string]string{"If-Match": ifMatch}, Body: shown, Current: current,
					Verified:    []string{"the target was read; unchanged fields, including enabled, are sent back as they are"},
					NotVerified: []string{"caller permission (platform admin)", "that the version still matches"}}
				return env.render(plan, nil, func(w io.Writer) error { return renderPlan(w, plan) })
			}
			// Conditional and therefore not retried.
			target, err := sendEmailTarget(ctx, client, http.MethodPut, emailTargetPath(id), body, http.Header{"If-Match": {ifMatch}})
			if err == nil && !strings.EqualFold(target.Id.String(), id) {
				err = &APIError{Code: "invalid_response", HTTPStatus: http.StatusOK, Message: "server returned another email target"}
			}
			if err != nil {
				var api *APIError
				if errors.As(err, &api) && api.HTTPStatus == http.StatusPreconditionFailed {
					api.Hint = fmt.Sprintf("the target changed since version %s and was not updated; rules bound to its old version keep it until updated; run agctl email target show %s and retry", ifMatch, id)
				}
				if emailOutcomeUnknown(err) {
					return unknownOutcomeError(err, unknownOutcome{Operation: "email target update", ID: id, ReadBack: "agctl email target show " + id},
						fmt.Sprintf("the update may have been applied; compare the version (%s before) in agctl email target show %s", ifMatch, id))
				}
				return err
			}
			return env.render(target, nil, func(w io.Writer) error { return renderEmailTargets(w, []adminopenapi.EmailTarget{target}) })
		},
	}
}

func emailPreviewCommand() command {
	var scenario, locale, htmlOutput, textOutput string
	return command{
		name:     "email preview",
		synopsis: "render a synthetic alert email without queueing anything",
		usage:    "agctl email preview --scenario warning|critical|resolved --locale en|zh-CN [--html-output FILE] [--text-output FILE] [--json]",
		setFlags: func(fs *flag.FlagSet) {
			fs.StringVar(&scenario, "scenario", "", "warning, critical, or resolved")
			fs.StringVar(&locale, "locale", "", "en or zh-CN")
			fs.StringVar(&htmlOutput, "html-output", "", "write the HTML part to this new file")
			fs.StringVar(&textOutput, "text-output", "", "write the plain-text part to this new file")
		},
		meta: commandMeta{
			Resource: "email", Scenarios: []string{"admin"}, Stage: "m2",
			Endpoints: []string{"POST " + emailCapabilityPath + ":preview"}, MinimumRole: emailRole,
			InputSchema: "EmailPreviewInput", OutputSchema: "EmailPreview", DryRun: "unsupported", Retry: true,
		},
		run: func(ctx context.Context, env *commandContext) error {
			if !adminopenapi.EmailPreviewInputScenario(scenario).Valid() || !adminopenapi.EmailPreviewInputLocale(locale).Valid() {
				return &UsageError{Message: "--scenario must be warning, critical, or resolved and --locale en or zh-CN", Hint: "previews render fixed synthetic data"}
			}
			for _, path := range []string{htmlOutput, textOutput} {
				if path == "" {
					continue
				}
				if _, err := os.Lstat(path); err == nil {
					return &UsageError{Message: path + " already exists", Hint: "preview files are never overwritten; choose a new path"}
				}
			}
			client, err := env.client()
			if err != nil {
				return err
			}
			body, err := encodeBody(adminopenapi.EmailPreviewInput{Scenario: adminopenapi.EmailPreviewInputScenario(scenario), Locale: adminopenapi.EmailPreviewInputLocale(locale)})
			if err != nil {
				return err
			}
			// Rendering changes nothing, so the request is retried.
			var preview adminopenapi.EmailPreview
			if _, err := client.sendJSON(ctx, http.MethodPost, emailCapabilityPath+":preview", requestOptions{body: body, idempotent: true}, &preview); err != nil {
				return err
			}
			for path, content := range map[string]string{htmlOutput: preview.Html, textOutput: preview.Text} {
				if path == "" {
					continue
				}
				file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
				if err != nil {
					return &UsageError{Message: "cannot create " + path, Hint: "choose a new path in a writable directory"}
				}
				_, writeErr := file.WriteString(content)
				if closeErr := file.Close(); writeErr != nil || closeErr != nil {
					return fmt.Errorf("cannot write %s", path)
				}
			}
			return env.render(preview, nil, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "subject: %s\ntemplate: %s\n\n%s\n", preview.Subject, preview.TemplateVersion, preview.Text)
				return err
			})
		},
	}
}

func emailDeliveryPath(id string) string { return emailDeliveriesPath + "/" + url.PathEscape(id) }

func renderDeliveries(w io.Writer, deliveries []adminopenapi.EmailDelivery) error {
	if len(deliveries) == 0 {
		return emptyLine(w, "email deliveries")
	}
	table := [][]string{{"STATE", "KIND", "SCENARIO", "ATTEMPTS", "ERROR", "DUPLICATE?", "CREATED", "ID"}}
	for _, delivery := range deliveries {
		created := delivery.CreatedAt
		kind := ""
		if delivery.Kind != nil {
			kind = string(*delivery.Kind)
		}
		table = append(table, []string{string(delivery.State), kind, string(delivery.Scenario), strconv.Itoa(delivery.Attempts), optional(delivery.ErrorCode), yesNo(delivery.PossibleDuplicate), formatTime(&created), delivery.Id.String()})
	}
	return writeColumns(w, table)
}

func terminalDelivery(state adminopenapi.EmailDeliveryState) bool {
	return state == adminopenapi.EmailDeliveryStateAccepted || state == adminopenapi.EmailDeliveryStateDead
}

func emailTestCommand() command {
	var target, scenario, key string
	return command{
		name:     "email test",
		synopsis: "queue a synthetic test email to a target (sends real mail; needs --yes)",
		usage:    "agctl email test --target ID --scenario warning|critical|resolved --yes [--idempotency-key UUID] [--dry-run] [--json]",
		setFlags: func(fs *flag.FlagSet) {
			fs.StringVar(&target, "target", "", "email target ID")
			fs.StringVar(&scenario, "scenario", "", "warning, critical, or resolved")
			fs.StringVar(&key, "idempotency-key", "", "UUID that makes a repeat answer the same delivery (default: a new one)")
		},
		meta: commandMeta{
			Resource: "email", Scenarios: []string{"admin"}, Stage: "m2",
			Endpoints: []string{"POST " + emailCapabilityPath + ":test", "GET " + emailTargetsPath + "/{targetId}"}, Mutating: true, MinimumRole: emailRole,
			InputSchema: "EmailTestInput", OutputSchema: "EmailDelivery", DryRun: "client-plan", Retry: true,
		},
		run: func(ctx context.Context, env *commandContext) error {
			targetID, err := uuid.Parse(target)
			if err != nil {
				return &UsageError{Message: "--target must be an email target ID", Hint: "agctl email target list shows the IDs"}
			}
			if !adminopenapi.EmailTestInputScenario(scenario).Valid() {
				return &UsageError{Message: "--scenario must be warning, critical, or resolved", Hint: "the test renders that synthetic scenario"}
			}
			if env.flagSet["idempotency-key"] {
				if _, err := uuid.Parse(key); err != nil {
					return &UsageError{Message: "--idempotency-key must be a UUID", Hint: "reuse the key an earlier attempt reported"}
				}
			} else {
				key = uuid.NewString()
			}
			if !env.globals.dryRun && !env.globals.yes {
				return &UsageError{Message: "agctl email test sends real email and needs --yes", Hint: "preview the message with agctl email preview first"}
			}
			client, err := env.client()
			if err != nil {
				return err
			}
			current, version, err := readETagged[adminopenapi.EmailTarget](ctx, client, emailTargetPath(target))
			if err != nil {
				return err
			}
			if version == "" {
				version = current.Version
			}
			body := adminopenapi.EmailTestInput{TargetId: targetID, Scenario: adminopenapi.EmailTestInputScenario(scenario)}
			if env.globals.dryRun {
				plan := writePlan{Mode: "client-plan", Method: http.MethodPost, Path: emailCapabilityPath + ":test", Headers: map[string]string{"If-Match": version, "Idempotency-Key": key}, Body: body, Current: current,
					Verified:    []string{"the target exists and its version was read"},
					NotVerified: []string{"that the email channel is ready", "that the target is enabled", "delivery to the inbox"}}
				return env.render(plan, nil, func(w io.Writer) error { return renderPlan(w, plan) })
			}
			encoded, err := encodeBody(body)
			if err != nil {
				return err
			}
			// The Idempotency-Key makes a repeat answer the same delivery, so the
			// request is retried with the same key.
			var delivery adminopenapi.EmailDelivery
			response, err := client.sendJSON(ctx, http.MethodPost, emailCapabilityPath+":test", requestOptions{
				body: encoded, idempotent: true, header: http.Header{"If-Match": {version}, "Idempotency-Key": {key}},
			}, &delivery)
			if err == nil && (delivery.Id == (openapi_types.UUID{}) || delivery.State == "" || delivery.TargetId != targetID) {
				err = incompleteResponse(response, client, "email delivery")
			}
			if err != nil {
				if emailOutcomeUnknown(err) {
					return unknownOutcomeError(err, unknownOutcome{Operation: "email test", ID: target, IdempotencyKey: key, ReadBack: "agctl email delivery list"},
						fmt.Sprintf("a test may have been queued; re-run with --idempotency-key %s to get the same delivery instead of a second email", key))
				}
				return err
			}
			result := struct {
				adminopenapi.EmailDelivery
				IdempotencyKey string `json:"idempotencyKey"`
			}{delivery, key}
			return env.render(result, nil, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "test email queued as delivery %s (state %s); queued is not delivered: wait with agctl email delivery wait %s\n", delivery.Id, delivery.State, delivery.Id)
				return err
			})
		},
	}
}

func emailDeliveryListCommand() command {
	var limit int
	return command{
		name:     "email delivery list",
		synopsis: "list the newest email deliveries",
		usage:    "agctl email delivery list [--limit N] [--json]",
		setFlags: func(fs *flag.FlagSet) {
			fs.IntVar(&limit, "limit", 50, "newest deliveries to read (1-100; there is no cursor)")
		},
		meta: commandMeta{
			Resource: "email", Scenarios: []string{"admin"}, Stage: "m2",
			Endpoints: []string{"GET " + emailDeliveriesPath}, MinimumRole: emailRole,
			OutputSchema: "EmailDeliveryList", Pagination: true, DryRun: "unsupported", Retry: true,
		},
		run: func(ctx context.Context, env *commandContext) error {
			if limit < 1 || limit > maxEmailDeliveries {
				return &UsageError{Message: "--limit must be 1-100", Hint: "the endpoint reads the newest deliveries only"}
			}
			client, err := env.client()
			if err != nil {
				return err
			}
			var deliveries []adminopenapi.EmailDelivery
			if err := client.getJSON(ctx, emailDeliveriesPath, url.Values{"limit": {strconv.Itoa(limit)}}, &deliveries); err != nil {
				return err
			}
			meta := &envelopeMeta{Pagination: &paginationMeta{Mode: "bounded", Limit: limit}}
			if len(deliveries) < limit {
				complete := true
				meta.Pagination.Complete = &complete
			}
			return env.render(newListPayload(deliveries), meta, func(w io.Writer) error { return renderDeliveries(w, deliveries) })
		},
	}
}

func emailDeliveryShowCommand() command {
	return command{
		name:        "email delivery show",
		synopsis:    "show one email delivery and its attempts",
		usage:       "agctl email delivery show ID [--json]",
		positionals: 1,
		argsUsage:   "ID",
		meta: commandMeta{
			Resource: "email", Scenarios: []string{"admin"}, Stage: "m2",
			Endpoints: []string{"GET " + emailDeliveriesPath + "/{deliveryId}"}, MinimumRole: emailRole,
			OutputSchema: "EmailDelivery", DryRun: "unsupported", Retry: true,
		},
		run: documentCommand[adminopenapi.EmailDelivery]{
			request: func(env *commandContext) (string, url.Values, error) { return emailDeliveryPath(env.args[0]), nil, nil },
			render: func(w io.Writer, delivery adminopenapi.EmailDelivery) error {
				return renderDeliveries(w, []adminopenapi.EmailDelivery{delivery})
			},
		}.run,
	}
}

// emailDeliveryWaitCommand polls a delivery until it is accepted by the SMTP
// relay or dead. There is no cancel endpoint: a timeout leaves the delivery
// queued on the server.
func emailDeliveryWaitCommand() command {
	return command{
		name:        "email delivery wait",
		synopsis:    "wait until a delivery is accepted by the relay or dead, within --timeout",
		usage:       "agctl email delivery wait ID [--timeout DURATION] [--json]",
		positionals: 1,
		argsUsage:   "ID",
		meta: commandMeta{
			Resource: "email", Scenarios: []string{"admin"}, Stage: "m2",
			Endpoints: []string{"GET " + emailDeliveriesPath + "/{deliveryId}"}, MinimumRole: emailRole,
			OutputSchema: "EmailDelivery", DryRun: "unsupported", Retry: true, Wait: true,
		},
		run: func(parent context.Context, env *commandContext) error {
			id := env.args[0]
			client, err := env.client()
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(parent, env.globals.timeout)
			defer cancel()
			var last *adminopenapi.EmailDelivery
			for {
				var delivery adminopenapi.EmailDelivery
				err := client.getJSON(ctx, emailDeliveryPath(id), nil, &delivery)
				if err == nil {
					last = &delivery
					if terminalDelivery(delivery.State) {
						return env.render(delivery, nil, func(w io.Writer) error {
							_, err := fmt.Fprintf(w, "delivery %s is %s after %d attempts (accepted means the relay took it, not that it reached an inbox)\n", id, delivery.State, delivery.Attempts)
							return err
						})
					}
				} else if ctx.Err() == nil {
					return err
				}
				if ctx.Err() == nil && sleepOrDone(ctx, repoWaitInterval) {
					continue
				}
				state := "not read"
				if last != nil {
					state = string(last.State)
				}
				return &resultError{
					err: &WaitTimeoutError{
						Message: fmt.Sprintf("delivery %s was not accepted or dead within the wait budget (last state: %s)", id, state),
						Hint:    "the delivery stays queued; there is no cancel. Wait again with a longer --timeout or check agctl email delivery show " + id,
					},
					data: last,
				}
			}
		},
	}
}

func emailDeliveryReplayCommand() command {
	var ifMatch string
	return command{
		name:        "email delivery replay",
		synopsis:    "queue a dead delivery again (may send a duplicate; needs --yes)",
		usage:       "agctl email delivery replay ID --yes [--if-match VERSION] [--dry-run] [--json]",
		positionals: 1,
		argsUsage:   "ID",
		setFlags: func(fs *flag.FlagSet) {
			fs.StringVar(&ifMatch, "if-match", "", "replay only if the delivery is still at this version (default: the version read just before)")
		},
		meta: commandMeta{
			Resource: "email", Scenarios: []string{"admin"}, Stage: "m2",
			Endpoints: []string{"POST " + emailDeliveriesPath + "/{deliveryId}:replay", "GET " + emailDeliveriesPath + "/{deliveryId}"}, Mutating: true, MinimumRole: emailRole,
			OutputSchema: "EmailDelivery", DryRun: "client-plan",
		},
		run: func(ctx context.Context, env *commandContext) error {
			id := env.args[0]
			if !env.globals.dryRun && !env.globals.yes {
				return &UsageError{Message: "agctl email delivery replay can send email again and needs --yes", Hint: "check agctl email delivery show first"}
			}
			if env.flagSet["if-match"] && strings.TrimSpace(ifMatch) == "" {
				return &UsageError{Message: "--if-match cannot be empty", Hint: "pass the version from agctl email delivery show"}
			}
			client, err := env.client()
			if err != nil {
				return err
			}
			if ifMatch == "" || env.globals.dryRun {
				current, version, err := readETagged[adminopenapi.EmailDelivery](ctx, client, emailDeliveryPath(id))
				if err != nil {
					return err
				}
				if ifMatch == "" {
					ifMatch = version
					if ifMatch == "" {
						ifMatch = current.Version
					}
				}
				if env.globals.dryRun {
					plan := writePlan{Mode: "client-plan", Method: http.MethodPost, Path: emailDeliveryPath(id) + ":replay", Headers: map[string]string{"If-Match": ifMatch}, Current: current,
						Verified:    []string{"the delivery exists and its version was read"},
						NotVerified: []string{"that the delivery may be replayed (only dead deliveries can)", "that the email channel is ready"}}
					return env.render(plan, nil, func(w io.Writer) error { return renderPlan(w, plan) })
				}
			}
			// Conditional and therefore not retried.
			var delivery adminopenapi.EmailDelivery
			response, err := client.sendJSON(ctx, http.MethodPost, emailDeliveryPath(id)+":replay", requestOptions{header: http.Header{"If-Match": {ifMatch}}}, &delivery)
			if err == nil && (!strings.EqualFold(delivery.Id.String(), id) || delivery.State == "") {
				err = incompleteResponse(response, client, "email delivery")
			}
			if err != nil {
				if emailOutcomeUnknown(err) {
					return unknownOutcomeError(err, unknownOutcome{Operation: "email delivery replay", ID: id, ReadBack: "agctl email delivery show " + id},
						fmt.Sprintf("the replay may have been queued; compare the version (%s before) in agctl email delivery show %s before replaying again", ifMatch, id))
				}
				return err
			}
			return env.render(delivery, nil, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "delivery %s queued again (state %s)\n", id, delivery.State)
				return err
			})
		},
	}
}
