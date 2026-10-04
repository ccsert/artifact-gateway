---
version: 1
slug: "le-src-features-system-emailnotificationspanel-tsx"
primary_target: "console/src/features/system/EmailNotificationsPanel.tsx"
related_targets: ["console/src/features/system/EmailTargetEditor.tsx","console/src/features/system/EmailTargetAction.tsx","console/src/features/system/EmailTemplatePreview.tsx","console/src/features/system/EmailDeliveryResults.tsx"]
---

# Email notifications

## Scope and mode

**Operate.** The Email notifications tab at `/system?tab=email` extends System runtime for platform administrators. Its task is to manage single-recipient, write-only email targets, inspect fixed synthetic templates, explicitly queue a synthetic test, and read bounded delivery results. SMTP configuration remains a deployment administrator responsibility; the Console accepts no SMTP credentials.

This is a precise local extension of the Verified Control Plane in `DESIGN.md`, inheriting `PRODUCT.md` and `CONTEXT.md`. The built composition is recorded here without establishing a new visual world, global token, or system-wide rule. No concept seed, approved comp, or new shipping raster was needed for this extension.

## Inherited visual language

The existing Console shell, compact Ant Design tabs, outlined cards, controls, notices, empty states, and semantic light/dark roles carry the surface. Cyan identifies the active location and primary create/save/confirm actions; disabling uses semantic danger treatment. Enablement and delivery state always have translated words. Accepted deliveries use success tone, dead deliveries use danger tone, and other delivery states remain neutral.

Inter remains the UI family. The inherited System heading uses the established headline role; the email heading is 16px semibold, target and delivery names and consequence copy use 14px text, and language, read times, attempts, and timestamps use 12px text. Confirmed target ID/version and delivery ID use the existing technical monospace role with character wrapping. QuotaBadge retains the UI sans face for state labels. The slice introduces no independent palette, font, illustration, icon system, or motion language.

## Composition and responsive layout

**State and explicit action precede the template.** The System heading and existing diagnostics, logs, and quota tabs remain, with Email notifications added as the fourth compact tab. The query parameter selects the tab; leaving it unmounts the email panel.

The email title and explanation lead a wrapping toolbar with Refresh. Retained-data feedback, save/queue feedback, and unavailable-channel guidance follow before the work surface. In the normal workspace, Email targets comes first, with New email target as its primary header action. Each divided row shows name and enabled/disabled state, then language and recipient-configured evidence, followed by Edit, Enable/Disable, and Send synthetic test. Template preview, recent delivery results, and read time follow in that order.

Editing or confirmation replaces the list/preview/results workspace while retaining the email heading and relevant top-level feedback. The editor is a vertical field stack. Confirmation names the captured target, identity/version, language, write-only recipient summary, and, for a test, synthetic scenario before its consequence notice and Cancel/Confirm controls. Both test and enable/disable consequence notices are non-closable, so the effect remains readable beside confirmation.

Parent-owned stacks use a 16px gap and `min-width: 0`; the inherited System heading boundary keeps its existing extra 8px. Card content has 20px insets. Editor and confirmation groups use 20px gaps; preview groups and its paired fields use 16px gaps. Scenario/language preview controls occupy two columns at the existing small-screen breakpoint and one column below it. Toolbars, badges, format controls, metadata, and action groups wrap on narrow screens; names and prose wrap by word and technical identities by character. The 320/390px layouts preserve the operations and existing Ant Design tab overflow navigation.

HTML preview uses a full-width, bordered 760px iframe with internal scrolling. This is a finite viewport, not a promise that the entire email is visible at once. Plain text uses preserved line breaks and wrapping rather than horizontal overflow.

## Target and confirmation operations

- Platform administrator capability gates the entire panel. Authentication, permission, or forced-password-change errors replace the whole email workspace with safe guidance and an explicit refresh action. A successful explicit top-level read clears this block only if no newer authorization failure intervened.
- New targets start with blank name and recipient and the UI's language. Saving creates a disabled target and sends nothing. Editing retains the target's enabled flag; enabling and disabling are separate confirmed operations.
- The existing recipient is never returned or displayed. Editing with an empty address retains it; a replacement accepts one bare mailbox. The name must be 1–128 characters without line breaks or NUL, and address validation rejects line breaks and mailbox-list syntax while leaving the server authoritative. Recipient input exists only in the mounted editor and is cleared on success, cancel, conflict, authorization block, and navigation.
- Editing sends the captured version through `If-Match`. A conflict clears the recipient, retains the safe name/language draft, and blocks saving until Refresh latest target and edit again obtains the current version. Polling does not overwrite the local draft.
- Test entry requires an enabled target with a configured recipient, a ready deployment channel, and a writable snapshot. The confirmation keeps the target ID/version and language fixed; it exposes warning, critical, or recovery (`resolved`) as synthetic scenarios and locks the scenario after the first attempt. Confirm enters the real delivery queue with `If-Match` and one UUID idempotency key. Uncertain retries reuse that same target/version/scenario/key; a conflict requires cancelling, refreshing, and confirming again.
- Enable/disable confirmation explains the target version change, quota rules' old-version binding and required route confirmation, and stopped queued old-version delivery. Disabling cannot recall an in-flight message. Test confirmation states global rate limiting, real queue impact, the inability to recall queued/in-flight mail, and SMTP acceptance's limit. No confirmation is inferred from previewing or saving.

## Preview, safe results, and bounded refresh

Preview selects warning, critical, or recovery in Simplified Chinese or English, then returns subject, template version, HTML, and equivalent plain text. Changing scenario or language clears the prior preview. This fixed synthetic preview reads no recipient, creates no queue entry, and sends no message.

`emailPreviewDocument` bounds the API HTML input and rebuilds a finite inert layout from an allowlist before inserting it. Anchors become non-interactive text; scripts, forms, nested frames, and resource elements are excluded. The iframe has an empty sandbox and no-referrer policy, with a first-position CSP denying scripts, URLs, external images/fonts/connections, frames, objects, base URLs, and form actions. No external assets or tracking resources are used. These browser restrictions are distinct from the delivered MIME's HTML/plain alternatives and do not establish mail-client rendering compatibility.

Results show at most 50 deliveries. Each row presents safe target name or ID, textual state, notification kind, scenario, language, attempts, delivery ID, UTC update/next-attempt/acceptance times when available, finite error/cancellation labels, and any possible-duplicate warning. The surface does not expose recipient addresses, SMTP replies, hosts, credentials, or message bodies. **Accepted means the SMTP server accepted the message; it does not confirm inbox delivery or reading.** Uncertain retries may duplicate mail; disabling cannot recall mail already in flight. Replay remains an administrator API operation rather than a Console action.

Initial loading, failure, empty, and content states are explicit. A failed ordinary refresh retains prior data with a warning; reads in progress or failed reads gate dependent writes and preview requests. Targets, capability, and latest deliveries share the top-level snapshot. Visible documents refresh every 30 seconds, hidden documents pause, and visibility return refreshes. Replacement reads invalidate earlier generations, and unmount aborts/ignores obsolete reads and actions. Actions use a synchronous duplicate-request lock. Safe fixed error text avoids displaying arbitrary response messages.

## Recorded evidence and acceptance boundary

This record is grounded in the five email components, `emailPresentation.ts`, `console/src/styles.css`, shared snapshot/action helpers, `System.tsx`, the incumbent product/design documents, and the completed review report at `../ui-review.zh-CN.md`. The documenter read saved implementation and reports without opening a browser, rerunning a detector, or repeating tests.

The review inspected all 32 required captures under `.impeccable/review/`: 12 warning workspaces across 320/390/1440px, both UI locales and themes; eight desktop critical/recovery workspaces; and 12 Chinese dark/English light editor and test-confirmation captures. Its initial sole material fix was the two closable confirmation notices. After `closable={false}` landed, the reviewer scored that fix resolved and found no regression within the correction's stated scope. These fixtures demonstrate layout and states, not production recipients, live capacity, or external delivery.

`/tmp/email-console-real-browser-head.log` records a separate passing Chromium run against a real API, isolated PostgreSQL, and owned TLS SMTP sinks that cannot relay mail: create/edit/CAS retained safe drafts and cleared recipients, two explicit synthetic tests, one accepted multipart MIME, one permanent recipient failure, and 36 preview combinations across the three widths, two locales, two themes, and three scenarios. It reports no browser errors or external requests. The final Console aggregate reports 487 passing unit tests, 149 passing Playwright tests, and one skipped Keycloak browser test; these are read execution records, not documenter reruns.

Screenshots and the owned SMTP fixture do not establish Gmail, Outlook, or Apple Mail rendering, external inbox delivery, reading, or exactly-once delivery. Keyboard/screen-reader/real-touch behavior and other browsers were not newly tested by this documentation pass. The global `DESIGN.md`, design sidecar, `PRODUCT.md`, `CONTEXT.md`, and other surface briefs remain unchanged; iframe dimensions, this workflow, and its evidence boundary stay local to this surface.
