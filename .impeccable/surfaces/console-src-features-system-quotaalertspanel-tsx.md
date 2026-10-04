---
version: 1
slug: "console-src-features-system-quotaalertspanel-tsx"
primary_target: "console/src/features/system/QuotaAlertsPanel.tsx"
related_targets: ["console/src/features/system/QuotaRuleEditor.tsx","console/src/features/system/QuotaRuleDetail.tsx","console/src/features/system/QuotaBadge.tsx","console/src/features/system/quotaAlertPresentation.ts","console/src/features/system/useQuotaAction.ts","console/src/features/system/useQuotaSnapshot.ts","console/src/features/system/System.tsx"]
---

# Repository quota alerts

## Scope and mode

**Operate.** The Quota alerts tab at `/system?tab=alerts` extends the existing System runtime page for platform administrators. It reads and manages finite repository quota rules through the existing administrator API. The signal is logical repository usage; the introduction explicitly distinguishes it from S3, NAS, and local mount physical capacity.

The surface inherits `PRODUCT.md`, `CONTEXT.md`, and the established Console world in `DESIGN.md`. Its implemented composition and behavior are recorded here without changing the global design system.

## Inherited visual language

The existing shell, compact Ant Design tabs, outlined cards, controls, feedback components, and semantic theme roles carry the surface. Cyan marks the active tab and primary save/create/confirm action. Neutral badges describe rule enablement and evidence availability; warning and danger tones identify warning and critical severity, and recovery events use success tone. Every state also has a translated text label, following the incumbent Status Has Words rule.

The System heading uses the established 22px semibold role. The quota heading and repository title use 16px semibold text, card headings and form groups use 14px semibold text, body and consequence explanations use 14px text, and labels, read times, policy summaries, and event metadata use 12px text. Quota badge labels use the primary UI sans family through `QuotaBadge`, even though the shared generic Badge has a monospace wrapper. Status names, help, and actions retain the Console UI type character.

Outlined surfaces use the incumbent theme background, subtle border, structural shadow, and surface corners. This slice adds no independent palette, font, illustration, icon system, or motion language.

## Composition and responsive layout

The System runtime heading and description precede three compact tabs: System diagnostics, Runtime logs, and Quota alerts. The active tab is selected by the `tab` query parameter. Leaving Quota alerts unmounts its panel.

Within the tab, the quota title and scope description share a wrapping toolbar with Refresh and, on the list view, Create rule. Read time, retained-data/authentication warnings, saved/deleted feedback, and unavailable mail prerequisites precede the current work surface. Parent-owned page stacks separate related regions by 16px; the inherited page-heading boundary adds its existing 8px increment. The quota implementation introduces no additional primary-boundary class.

The rule list is one outlined card with divided rows. Each row places the repository name and warning/critical/recovery thresholds before a wrapping group of enablement, severity, and data-state badges and a View action. Empty-list guidance states that creation is disabled by default and sends no test email.

Detail begins with Back to rules, then a Rule state card and a Latest 50 events card. The state card presents repository identity, separate state badges, sample evidence, policy conditions, evaluation/hold timestamps, safe target information, and actions in that order. A target section is separated by a quiet divider. An active confirmation is its own card above Rule state; it does not obscure the supporting state or event evidence.

Editing replaces the current list/detail work surface with a vertically grouped form. Repository and target fields share two columns at the small-screen breakpoint and above. Warning, critical, and recovery each pair threshold with hold duration using the same responsive two-column pattern; sample freshness and save consequences follow. Detail policy facts also use two columns at that breakpoint. Narrow screens use one column, wrapping toolbars and badges, word-wrapped names and prose, and character wrapping for bound/current target versions. Card content and forms use 20px inset padding; field pairs use 16px gaps and form groups use 24px gaps. The existing mobile shell and tab navigation remain in place.

## Rule operations and safe target evidence

- Platform capability gates the whole panel. A non-administrator sees explicit access guidance rather than rule controls.
- New creation starts with blank repository, target, thresholds, hold durations, and sample-age values. Its save request always uses `enabled: false`. An existing email target is required to enter creation; target web management is outside this surface.
- Repository scope is fixed after creation. The repository picker exposes active repositories and can load further catalogue pages. An existing rule retains its repository identity if it is absent from the loaded catalogue.
- The target picker shows name, language, and readiness. Detail separately shows name/language, recipient-configured flag, target enabled state, and bound target version. When the current version differs, it also shows that version and explains that Edit and save rebinds the rule without catching up previously unqueued events. Recipient addresses are not read or shown.
- Policy fields accept decimal percentages with at most two decimal places and convert them exactly to integer basis points. Recovery is strictly above zero and below warning, warning is below critical, and critical is at most 100%. Hold durations are integer seconds from 1–86400; maximum sample age is 30–3600 seconds. Validation failure preserves the entered draft.
- Saving an existing rule uses its version and preserves its enabled flag. The consequence text explains rebinding, hold-timer reset, retained severity, cancellation of unclaimed old automatic deliveries, and the possibility that a claimed delivery finishes once. An enabled rule additionally says evaluation and possible notifications continue after save. Cancel returns to the prior detail or list without writing.
- Enable, disable, and delete first open a confirmation card naming the repository and safe target name/language. A second explicit Confirm action performs the captured versioned request. Enable requires available channel/encryption, an enabled target with a configured recipient, and equality between current and bound target versions. Disable retains severity and history; soft deletion retains history and does not create a recovery event. Both explain automatic-delivery cancellation and the claimed-delivery caveat. Delete uses semantic danger treatment.

## Evidence, delivery reads, and feedback states

The server supplies severity and data state. Disabled, unknown, stale, and configuration-changed states are distinct from recovery. When evidence is unavailable, detail says severity is retained and cannot establish recovery. The sample is labelled Latest sample only for available evidence; otherwise it is Historical sample. Missing samples have explicit guidance. Byte values outside JavaScript's safe integer range carry an approximation mark and a statement that severity comes from the server.

The ordered event list retains each immutable event's sequence, occurrence time, scenario, delivery/notification state, sampled usage/quota and sample time, policy thresholds, and evidence start time. It does not reconstruct a timeline from current state or equate manual disable/delete with recovery. A delivery ID exposes a read-only View/Hide delivery details action inside its event row. Expanded detail shows delivery state, attempts, finite error/cancellation explanations, possible-duplicate warning, next attempt, acceptance time, template version, and language when present. Accepted means the SMTP server accepted the message; it does not confirm inbox delivery.

Initial loading, failure, empty, and content states are explicit. A failed refresh retains previously read data and places a warning above it. Refresh and retry actions remain available, while dependent rule writes are gated during top-level reads or failed top-level reads. Authentication, forced-password-change, and permission failures establish a panel-wide write block; only an explicit successful top-level read clears it. Version conflicts preserve the draft and require returning, refreshing, and editing again rather than overwriting newer configuration.

Reads refresh every 30 seconds while the document is visible, pause in hidden documents, and refresh on visibility return. Unmount and replacement requests invalidate earlier generations. Actions use a synchronous duplicate-request lock and abort/ignore completion after their owner unmounts. Polling updates do not replace the editor's local draft.

## Recorded evidence and acceptance boundary

This record was derived from the final quota components, `System.tsx`, shared Console layout/badge components, `console/src/styles.css`, and the incumbent product/design/domain documents. The saved full-page matrix is `.impeccable/review/quota-editor-{1440,390,320}-{zh-CN,en-US}.png` in dark theme and `.impeccable/review/quota-detail-{1440,390,320}-{zh-CN,en-US}.png` in light theme. The documenter sampled the saved desktop editor and narrow detail captures without launching a browser or recapturing evidence.

Screenshot repositories, rules, policy values, targets, and timestamps are synthetic fixtures. Their critical event sample and evidence start precede its occurrence consistently, while the displayed historical rule sample remains a separate retained observation. These fixture values demonstrate states and layout; they are not default policy, real capacity, production health, or actual mail delivery evidence. The existing real API/PostgreSQL acceptance remains separate from the visual matrix.

Email-target writes and address forms, template preview, test sending, replay, SMTP configuration or real inbox delivery, and deployment/release remain outside this slice. The record preserves the existing global DESIGN.md, design sidecar, PRODUCT.md, CONTEXT.md, and other surface briefs. No new global rule or visual-world decision is established by this extension.
