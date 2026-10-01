---
name: issue-authoring
description: Use when creating, splitting, or triaging Artifact Gateway GitHub issues — choosing type/area/format labels, drafting background and acceptance criteria from verified evidence, linking series and dependencies, and setting up the issue-to-merge trace. Covers feature requests, defects, design decisions, console, CLI, and format-specific work.
---

# Issue Authoring

Write an issue so a developer or an agent can pick it up without a follow-up
conversation. Classification vocabulary: `CONTRIBUTING.md` → "Issue
classification" (authoritative) and `gh label list` (live). Section structure:
`.github/ISSUE_TEMPLATE/` forms. Issue text is written in Chinese, matching the
existing corpus.

Stay within the user's authorized scope. Read-only triage returns findings and
links; a draft request returns a draft. Comment or create only when the request
authorizes that action, without asking again for authorization already given.

## Steps

1. **查重（search first）.** `gh issue list --state all --search "<keywords>"`,
   then scan open series and roadmaps. Reuse an existing home; when an update is
   authorized, comment there rather than create a duplicate — the log-viewer ask duplicated an existing
   series this way: the duplicate was closed and its non-overlapping content
   moved into comments on the surviving issues. Done when no open issue covers
   the same outcome.

2. **Classify.** One `type:*` (feature / bug / design / docs / chore), one
   primary `area:*` (console / api / cli / auth / audit / platform / ops), plus
   `format:*` when one package protocol is at stake — the format set includes
   preview protocols, so list it with `gh label list` rather than recalling it.
   Cross-area process/tooling work may omit the area label and title prefix,
   as permitted by CONTRIBUTING; other Issues require one primary area.

3. **Draft with evidence.** Every claim about current behavior cites its source
   — file path, contract path, or a command that was run. Follow the matching
   form and state background, scope, acceptance, explicit exclusions and any
   dependencies using its type-specific fields. For bugs, expected behavior and
   contract evidence define acceptance, and impact states scope/exclusions.
   For designs, problem/constraints state scope/exclusions and the decision plus
   its recorded outcome define acceptance. An issue
   without acceptance criteria is not ready to file.

4. **Create when authorized.** `gh issue create --label "type: …" --label "area: …"`
   for Issues with a primary area; omit the area flag for the process/tooling
   exception above. The
   title is `<area>: <结论>` — the finding or required outcome, not the task
   ("Console: the audit strip counts only the loaded page"). A series gets a
   parent issue whose body lists children as a task list, with per-child
   dependencies and a same-file ordering note: two issues that edit one file
   merge in order, never in parallel.

5. **Trace to merge.** One issue per mergeable PR, per `CONTRIBUTING.md` →
   "Issue-to-merge traceability": branch `codex/issue-N-<slug>`, `Closes #N`
   in the PR body, checks and results recorded against the head SHA.

## Acceptance criteria by area

- **console** — grep `console/e2e/` for any copy deleted or renamed, then run
  the full Playwright suite locally, not the "relevant" spec; assert layout at
  desktop and narrow widths per `console-layout-guardrails`.
- **api / contract** — change the `api/openapi` source first; `make
  openapi-check` regenerates and verifies; contract tests name the endpoint
  behavior.
- **data / migrations** — migrations append only; Postgres integration tests
  cover the lifecycle, not just the handler.
- **cli** — assert exit codes and `--json` schemas; E2E runs against a real
  gateway.
- **docs** — keep the bilingual pair in sync; `make docs-check`.

## Writing rules

- A design issue presents at least two options with their trade-offs and names
  the decision point; the conclusion is recorded back in the issue.
- A breaking change names its rollout rhythm and blast radius (for example: an
  explicit switch in this release, the default flips in the next).
- State the target behavior; keep prohibitions for hard guardrails only.
