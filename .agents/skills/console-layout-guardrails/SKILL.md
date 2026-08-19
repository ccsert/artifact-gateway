---
name: console-layout-guardrails
description: Use when adding or changing Artifact Gateway Console pages, page-level cards, alerts, metrics, tables, loading/error/empty states, or responsive multi-column layouts, especially to prevent sibling surfaces from touching, stacking, overlapping, or overflowing.
---

# Console Layout Guardrails

Use these project-specific rules with Impeccable's `layout` pass. Read `CONTEXT.md`, the page under change, and the shared flow rules in `console/src/styles.css` before editing.

## Establish the page flow

1. State the page's primary task and order its direct surfaces around that task.
2. Use `ag-page-stack` as the page root. Its direct children own no external vertical margin.
3. Let the parent `gap` separate direct siblings. Do not use child `mt-*` classes or component self-margins for page-level separation.
4. `PageHeader` already supplies `ag-page-header`. Mark the first primary work surface with `ag-page-primary` only when the major boundary needs the intentional extra half-step.
5. Keep internal card grids independent from page flow. Use `minmax(0, 1fr)`, `min-width: 0`, and `items-start` for multi-column workspaces so content cannot widen or stretch neighboring cards.
6. Do not add new pairwise rules such as `.ag-card + .ant-alert`. Component order must not be encoded as an exhaustive selector list.

The default rhythm is 16px between related page surfaces and 24px before a primary work surface. A different rhythm needs an explicit page-level reason.

## Model async states before styling

For an initial request, render exactly one of loading, error, empty, or content. Never keep a loading placeholder underneath an initial error.

For refresh or mutation failures with previously loaded data, retain the data and show the error above it. Every API promise must catch rejected requests, convert them to the page's error model, and release loading or busy state in `finally`. Preserve request-id guards where stale responses are possible.

## Prove geometry and behavior

Add or update a focused unit test for state branching and a Playwright layout regression for the real route.

At desktop and narrow mobile widths, assert bounded rhythm rather than only a minimum. An accidentally huge gap is also a layout failure:

- page-level related surfaces stay within the intended 16-18px rhythm tolerance;
- primary boundaries stay within the intended 24-26px rhythm tolerance;
- side-by-side cards neither intersect nor stretch each other;
- the page has no horizontal overflow;
- no `pageerror` or console error occurs in the exercised state.

Use `boundingBox()` or DOM rectangles for these assertions. A clean stylesheet detector, screenshot, or class-name check is supporting evidence, not proof of geometry.

## Validate the change

Run the focused tests first, then the console gates:

```bash
npm --prefix console test -- --run <unit-test>
(cd console && PLAYWRIGHT_EXTERNAL_SERVER=1 npx playwright test <layout-spec>)
npm --prefix console run typecheck
npm --prefix console run lint
npm --prefix console run build
npm --prefix console run format:check
(cd console && antd lint src --format json)
node .agents/skills/impeccable/scripts/detect.mjs --json --scope layout <changed-files>
```

Visually inspect one desktop and one mobile screenshot after the assertions pass. If a shared rule changed, also run the existing theme and representative operations/diagnostics layout specs before committing.
