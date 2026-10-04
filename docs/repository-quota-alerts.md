# Repository logical quota alerts

[简体中文](repository-quota-alerts.zh-CN.md) | [Documentation index](README.md)

This default-off finite evaluator connects sustained repository logical quota
breaches to PostgreSQL state/events and the [existing TLS email channel](email-notification-delivery.md).
[#237](https://github.com/ccsert/artifact-gateway/issues/237) delivers part of
[#211](https://github.com/ccsert/artifact-gateway/issues/211),
[#212](https://github.com/ccsert/artifact-gateway/issues/212),
[#229](https://github.com/ccsert/artifact-gateway/issues/229) and
[#230](https://github.com/ccsert/artifact-gateway/issues/230).
It does not evaluate local mounts or 5xx. S3/NAS physical pool capacity stays
unknown. Platform administrators can manage rules and read safe delivery evidence
in Console under **System → Quota alerts** (`/system?tab=alerts`).

For small private deployments, reuse required PostgreSQL, Scheduler and Worker.
This avoids another alert service, but the product owns rule/mail lifecycle and
cannot notify when Gateway/PostgreSQL is completely down. Use an existing external
availability watcher for independent outage detection. Prometheus/Alertmanager
offers external observation/routing with additional installation, upgrade and
configuration work; it is not a prerequisite for this finite quota rule.

## Console workflow

Configure the email channel, encryption key and recipient target through the
existing deployment configuration and administrator API first. Console shows only
the target name, locale, enabled state, recipient-configured flag and bound version;
it never reads back an address, credential or SMTP setting.

Create a rule with explicit warning/critical/recovery percentages, their hold times
and maximum sample age. Percentages allow two decimal places and are converted to
exact integer basis points. New rules are always disabled; creation and saving send
no test email. Enable, disable and delete require a separate explicit confirmation.
An unavailable channel/key can still permit saving disabled rules. A target version
change requires editing and saving to rebind before enabling.

Conflicting saves retain the draft and require a fresh configuration read. Permission
or forced-password denials block writes across cancel/back navigation; only an
explicit successful refresh with no newer authorization denial restores writes.
Reads refresh every 30 seconds while visible, pause in the background and stop when
leaving the tab. Failed reads retain their last snapshot with an outdated notice.

Rule severity and unknown/stale evidence remain separate. The latest 50 immutable
events preserve their policy and sample evidence; delivery details show attempts,
retry time, finite failure/cancellation codes and possible duplicates. **Accepted by
SMTP server** does not confirm inbox delivery. No client-side severity evaluation,
recipient persistence, automatic enabling or sending is introduced. Email target
web management, template preview, explicit synthetic tests and replay remain later
slices; no production relay, recipient or deployment configuration is changed.

## Explicit configuration and permission

Platform administrators only; forced password change blocks every operation.
All responses, including early errors, have `Cache-Control: no-store`. Each rule
binds a real repository UUID and an existing email target. One undeleted rule per
repository, at most 100 total rules including retained soft-deleted history.
Recipients, SMTP, credentials, object paths and log contents are absent from
rule/event responses.

| API | Behavior |
| --- | --- |
| `GET/POST /api/v2/repository-quota-alert-rules` | List / create disabled by default |
| `GET/PUT /api/v2/repository-quota-alert-rules/{ruleId}` | State / configuration CAS with `If-Match` |
| `DELETE /api/v2/repository-quota-alert-rules/{ruleId}` | CAS soft deletion retaining state/events |
| `GET /api/v2/repository-quota-alert-rules/{ruleId}/events` | Newest 50 events and safe mail status |

PUT replaces configuration; repository scope is immutable. Identical saves do
not increment configuration version or record a change audit. Enabling requires
enabled deployment email configuration, working encryption and an enabled target;
validation is offline and never probes SMTP. The target version is bound to the
rule. After target changes, an administrator must CAS-update the rule to bind the
new version.

Every threshold, hold and freshness value is explicit; there is no production
default policy. Basis points use 10000 for 100%, requiring
`0 < recovery < warning < critical <= 10000`. Each hold is 1–86400 seconds;
freshness is 30–3600 seconds. This is a synthetic example, never automatically
applied or enabled:

```json
{
  "repositoryId": "00000000-0000-4000-8000-000000000001",
  "targetId": "00000000-0000-4000-8000-000000000002",
  "enabled": false,
  "policy": {
    "warningBasisPoints": 8500,
    "criticalBasisPoints": 9500,
    "recoveryBelowBasisPoints": 8000,
    "warningForSeconds": 300,
    "criticalForSeconds": 120,
    "recoveryForSeconds": 180,
    "maxSampleAgeSeconds": 60
  }
}
```

## State and evidence

Logical capacity keeps existing format semantics; collected Cargo objects are
excluded. Positive quota and usage come from one PostgreSQL statement snapshot.
Exact integer comparison avoids overflow, rounding and mixed quota/usage reads.
Mail describes this logical scope, never physical free space.

Warning and critical have independent continuous evidence. Critical can fire
before a longer warning hold. Normal → warning → critical; critical latches until
usage is strictly below recovery threshold for the entire recovery hold, then
resolved returns severity to normal. Merely falling below warning does not
downgrade critical. Each scenario occurs at most once per episode. Periodic
reminders are disabled; a new episode after recovery needs full breach evidence.

Zero/missing quota is unlimited/unconfigured (`not_configured`) and is not
evaluated. Inactive/deleted repositories, read failures, stale, future or invalid
samples clear continuous timers while retaining severity, episode and last valid
values; they never resolve an alert. Scheduler samples every 15 seconds; gaps
over 30 seconds reset evidence, so restart cannot count downtime. GET projects
staleness without persisting, evaluating or sending. Configuration changes,
disable and soft deletion preserve active severity rather than claiming recovery.

Normal/pending/firing is separate from dataState. Byte values are last valid
evidence and are not current usage when unknown/stale. Configuration version and
evaluation stateVersion are separate. Events retain rule version, repository
name/ID, bytes/quota, policy, UTC event/sample/evidence times, event/episode/previous
event IDs and global rule sequence, plus an existing email delivery reference.

## Consistency and delivery

Scheduler nodes evaluate; Worker nodes run the existing `email` worker. Each run
has a 100-rule/5-second budget. PostgreSQL row locks, SKIP LOCKED and due times
prevent duplicate advancement across instances. State, episode, immutable event
and mail snapshot commit together, with no external network I/O in the transaction.
Configuration changes use the same rule lock; database time determines holds.

Disabled/changed targets, unavailable encryption or disabled deployment email produce explicit
non-delivery codes. Restoring configuration does not silently send old transitions
or repeat a still-firing alert after rebinding a target. Each evaluation run checks
encryption offline without opening target ciphertext or probing SMTP. New events
with `encryption_key_unavailable` create no delivery; previously queued mail keeps
its existing bounded retry semantics. Valid routes share the
1000-active-mail cap. A full queue retains a dead `queue_full` delivery for explicit
administrator CAS replay once space is available. Automatic transitions do not
consume the synthetic-test five-per-minute budget.

Global rule sequence preserves order across episodes: pending/retrying/delivering
predecessors block later claims; accepted/dead predecessors permit progress.
Configuration changes, disable and deletion cancel unclaimed pending/retrying
quota mail as dead with `rule_changed`, `rule_disabled` or `rule_deleted`.
Already claimed SMTP permission may finish once; network sends cannot be revoked.
A durable cancellation marker prevents automatic retry after failure or lease
expiry. Explicit administrator replay clears that marker and renews retry
permission while retaining historical evidence and target/relay gates.

Existing leases, fencing, eight bounded attempts and sticky possibleDuplicate
apply. Accepted means SMTP relay acceptance, not inbox delivery. Disconnect after
DATA or restart can duplicate mail; delivery is not exactly-once. From, Console
origin, encrypted recipient, locale and event evidence are immutable on retries.

Version `quota-1` supplies bilingual warning/critical/resolved text/color styles,
responsive HTML, equivalent plain text and fixed Outlook wrappers. No external
images, fonts, tracking pixels or attachments. Only a validated HTTPS origin
produces the fixed diagnostics link. New synthetic previews use version 2;
queued version 1 retains its original historical wording/rendering on replay.

## Validation and follow-up

Synthetic public HTTP/store/TLS tests exercise actual Raw HTTP writes → logical
capacity → PG state/events → reopened mail worker → owned TLS sink → actual
localized multipart MIME. Fixed timelines cover holds, hysteresis, direct
critical, unknown/stale, restart gaps and large integers. Isolated PostgreSQL
tests cover concurrent evaluators, configuration fencing, queue overflow, read
failures and atomic rollback when event persistence fails.

This does not certify real Gmail/Outlook/Apple Mail clients or production rollout.
Terminal retention/archive, Console manual email replay, explicit local mount alerts
and process 5xx alerts remain separate slices. Real relay, recipients and production
thresholds require separate deployment authorization. This slice creates no
credentials, sends no real email and changes no production state.
