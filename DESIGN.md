---
name: Artifact Gateway Console
description: A refined, protocol-native engineering console for trusted artifact operations.
colors:
  action-dark: "#fafafa"
  action-dark-hover: "#ffffff"
  action-dark-active: "#e4e4e7"
  action-light: "#09090b"
  action-light-hover: "#27272a"
  action-light-active: "#3f3f46"
  dark-text-on-action: "#09090b"
  light-text-on-action: "#ffffff"
  signal-dark: "#22d3ee"
  signal-dark-hover: "#67e8f9"
  signal-light: "#0e7490"
  signal-light-hover: "#164e63"
  dark-bg: "#09090b"
  dark-rail: "#0c0c0f"
  dark-surface: "#111114"
  dark-elevated: "#18181c"
  dark-hover: "rgba(255, 255, 255, 0.045)"
  dark-border: "rgba(255, 255, 255, 0.10)"
  dark-text: "#ededef"
  dark-text-strong: "#fafafa"
  dark-text-secondary: "#a1a1aa"
  dark-text-muted: "#8b8b94"
  light-bg: "#f7f7f8"
  light-rail: "#fbfbfc"
  light-surface: "#ffffff"
  light-hover: "#f2f2f4"
  light-border: "#e4e4e7"
  light-text: "#18181b"
  light-text-strong: "#09090b"
  light-text-secondary: "#52525b"
  light-text-muted: "#63636c"
  success-dark: "#4ade80"
  warning-dark: "#fbbf24"
  error-dark: "#f87171"
  success-light: "#147a3b"
  warning-light: "#a94e09"
  error-light: "#b91c1c"
typography:
  headline:
    fontFamily: "'Geist Variable', ui-sans-serif, system-ui, 'PingFang SC', 'Noto Sans SC', sans-serif"
    fontSize: "24px"
    fontWeight: 600
    lineHeight: "32px"
    letterSpacing: "-0.02em"
  title:
    fontFamily: "'Geist Variable', ui-sans-serif, system-ui, 'PingFang SC', 'Noto Sans SC', sans-serif"
    fontSize: "14px"
    fontWeight: 600
    lineHeight: "20px"
    letterSpacing: "-0.005em"
  body:
    fontFamily: "'Geist Variable', ui-sans-serif, system-ui, 'PingFang SC', 'Noto Sans SC', sans-serif"
    fontSize: "14px"
    fontWeight: 400
    lineHeight: "20px"
    letterSpacing: "normal"
  label:
    fontFamily: "'Geist Variable', ui-sans-serif, system-ui, 'PingFang SC', 'Noto Sans SC', sans-serif"
    fontSize: "12.5px"
    fontWeight: 500
    lineHeight: "18px"
    letterSpacing: "normal"
  technical:
    fontFamily: "'Geist Mono Variable', ui-monospace, SFMono-Regular, Menlo, monospace"
    fontSize: "12.5px"
    fontWeight: 400
    lineHeight: "18px"
    letterSpacing: "normal"
rounded:
  tooltip: "6px"
  control: "8px"
  surface: "12px"
  overlay: "14px"
  pill: "9999px"
spacing:
  xs: "4px"
  sm: "8px"
  md: "16px"
  lg: "24px"
  xl: "40px"
components:
  button-primary:
    backgroundColor: "{colors.action-dark}"
    textColor: "{colors.dark-text-on-action}"
    typography: "{typography.body}"
    rounded: "{rounded.control}"
    height: "34px"
    padding: "0 14px"
  button-primary-hover:
    backgroundColor: "{colors.action-dark-hover}"
  button-primary-active:
    backgroundColor: "{colors.action-dark-active}"
  button-default:
    backgroundColor: "{colors.dark-surface}"
    textColor: "{colors.dark-text}"
    typography: "{typography.body}"
    rounded: "{rounded.control}"
    height: "34px"
    padding: "0 14px"
  input-field:
    backgroundColor: "{colors.dark-surface}"
    textColor: "{colors.dark-text}"
    typography: "{typography.body}"
    rounded: "{rounded.control}"
    height: "34px"
    padding: "4px 11px"
  card:
    backgroundColor: "{colors.dark-surface}"
    textColor: "{colors.dark-text}"
    rounded: "{rounded.surface}"
    padding: "16px"
  menu-item-selected:
    backgroundColor: "{colors.dark-hover}"
    textColor: "{colors.dark-text-strong}"
    indicatorColor: "{colors.signal-dark}"
    typography: "{typography.body}"
    rounded: "{rounded.control}"
    height: "38px"
  table-header:
    backgroundColor: "transparent"
    textColor: "{colors.dark-text-muted}"
    typography: "{typography.label}"
    padding: "12px 16px"
  format-identity:
    swatchSize: "8px"
    swatchRounded: "2px"
    textColor: "{colors.dark-text-secondary}"
    typography: "{typography.technical}"
---

# Design System: Artifact Gateway Console

[简体中文](DESIGN.zh-CN.md) | [Documentation index](docs/README.md)

## Overview

**Creative North Star: "The Refined Engineering Console"** (design language v2, [#319](https://github.com/ccsert/artifact-gateway/issues/319))

Artifact Gateway Console should feel like the control surface for a system whose claims can be proved, built with the care of a well-made developer tool. The visual language is calm, precise, and generous: neutral layers separated by hairline borders, open spacing, and a single foreground-colored primary action. Real status, immutable identity, policy boundaries, and recoverable actions receive attention before decoration. The default dark theme is a quiet, near-black workspace rather than a glowing dashboard; the light theme preserves the same hierarchy instead of becoming a separate product.

The [v2 design canvas](https://claude.ai/artifact/V5Hyr915rnoPv477jcc8Xr) shows the target dashboard, repository list, repository detail, command palette, and tokens.

The interface is an **Operate** surface. Density, scanability, predictable Ant Design behavior, keyboard access, and fast state feedback outrank spectacle. Product character comes from protocol-aware objects, monospaced identities, the source-to-distribution lifecycle, and restrained signal cyan—not from gradients, oversized marketing type, or ornamental motion.

**Key Characteristics:**

- Evidence-led hierarchy: health, risk, blocked work, and required action appear before decorative metrics.
- Protocol-native specificity: coordinates, digests, Repository types, and lifecycle stages remain visible and copyable.
- Restrained signal color: cyan identifies links, current location, and focus only; the primary action is a solid foreground fill; semantic colors retain their status meanings.
- Layered but flat surfaces: hairline borders and tonal separation establish structure; shadows are reserved for overlays.
- Identity as a swatch: formats and states are a small colored mark beside neutral, readable text.
- Crisp motion: short, interruptible feedback supports state change and never delays frequent navigation.

## Colors

The palette is neutral-first with a single cool signal family and explicit semantic status colors.

Runtime themes follow [ADR 0005](docs/adr/0005-console-semantic-theme-system.md): a constrained Theme Package is resolved once into typed surface, content, border, action, link, focus, selection, navigation, status, visualization, identity, and effect roles. Ant Design component tokens and custom CSS variables consume that same projection. Page CSS must name a semantic role rather than a palette color. Markup does the same through the semantic utilities defined in `console/src/styles.css` (`text-fg-strong`, `text-fg`, `text-fg-secondary`, `text-fg-tertiary`, `text-fg-disabled`, `border-line`, `divide-line`, `bg-surface`, `bg-surface-translucent`, `bg-surface-hover`, `bg-canvas`, `text-on-identity`); a unit test rejects raw palette utilities such as `text-zinc-500` or `bg-white/10`.

An explicit foreground role applied directly to an Ant Design button or icon uses the Tailwind important modifier (for example, `text-fg-tertiary!`) to preserve that role over Ant Design’s unlayered component styles. Verify the actual foreground in both themes and relevant interaction states.

### Action and Signal

- **Foreground Action** (`action-dark`, `action-light`): the one primary action per decision surface is a solid fill in the strongest text color, with `dark-text-on-action` / `light-text-on-action` on top. It is `content.strong`, deliberately distinct from body text so derived selection stays neutral.
- **Signal Cyan** (`signal-dark`, `signal-light`): links, keyboard focus, the current-location indicator, and selected controls such as switches and tabs. It is never a button fill in the built-in themes.
- Extension Theme Packages keep their own primary color for actions; the foreground action is a built-in role override, not a package schema change.

### Neutral

- **Night Ledger** (`dark-bg`): the dark page canvas and strongest visual recess.
- **Rail** (`dark-rail`, `light-rail`): the navigation rail, one step off the canvas.
- **Verified Surface** (`dark-surface`): cards, tables, controls, and primary work areas.
- **Raised Instrument** (`dark-elevated`): modals, drawers, popovers, and other genuine overlays.
- **Operational Text** (`dark-text`, `light-text`): default readable content.
- **Decisive Text** (`dark-text-strong`, `light-text-strong`): page titles, values, and the strongest labels.
- **Supporting Text** (`dark-text-secondary`, `light-text-secondary`): explanations that still carry actionable meaning.
- **Muted Metadata** (`dark-text-muted`): timestamps, technical labels, and secondary metadata; never the only carrier of a state.
- **Daylight Canvas** (`light-bg`, `light-surface`): light-theme page and work surfaces with the same hierarchy as the dark theme.

### Named Rules

**The Signal Rarity Rule.** Cyan is reserved for location, focus, and trusted system links; it should not become a button fill or a general decorative fill.

**The Neutral Selection Rule.** Native text selection is a content state. Its background is derived from foreground and container neutrals, never from the primary action color.

**The Accent Budget Rule.** Generic icons, neutral badges, empty states, disabled controls, ordinary hover, card borders, and large decorative washes do not spend the primary accent. Actual identity marks are the only decorative exception.

**The Status Has Words Rule.** Success, warning, and error colors must be paired with text, an icon, or both. Color alone never communicates operational state. A state with no data is neutral, never success.

**The Identity Is a Swatch Rule.** Artifact formats are an 8px categorical swatch beside neutral monospace text (`FormatBadge`); states are a status-colored dot beside status-colored text (`StatusText`); Hosted/Proxy is neutral monospace text (`RepositoryTypeBadge`). Colored pills are reserved for counts and filters, never for identity, so labels stay readable in every theme.

## Typography

**Display Font:** Geist (self-hosted variable font) with PingFang SC / Noto Sans SC for Chinese
**Body Font:** Geist with the same fallbacks
**Label/Mono Font:** Geist Mono (self-hosted) with platform monospace fallbacks

Both families ship with the Console bundle through Fontsource under the SIL Open Font License; no font is fetched from a third-party host.

**Character:** The primary stack is neutral and compact so dense management tasks remain legible. The monospace stack gives protocol coordinates, digests, principals, request IDs, and commands a distinct evidentiary voice without turning general copy into code.

### Hierarchy

- **Headline** (`headline`): page identity only; one per surface.
- **Title** (`title`): cards, drawers, modals, and focused work sections.
- **Body** (`body`): controls, tables, explanatory copy, and task instructions.
- **Label** (`label`): field labels, table headings, metrics, and short metadata; uppercase is allowed only for compact categorical labels.
- **Technical** (`technical`): immutable identifiers, commands, digests, principals, protocol paths, and request IDs.

### Named Rules

**The Evidence Is Monospace Rule.** Use monospace only where exact character identity matters; names, explanations, statuses, and actions stay in the primary UI font.

**The Twelve-Pixel Floor Rule.** Ten- or eleven-pixel text is reserved for non-essential marks only. Operational guidance and status metadata use the label or body role.

## Layout

The system uses a 4px base grid, a maximum content width of 1440px, and a page stack owned by the parent rather than by sibling margins. Related page surfaces have 16px separation. A primary work boundary may use 24px separation through `ag-page-primary`; components must not encode page order with pairwise sibling selectors.

Authenticated navigation uses a fixed desktop rail and an accessible Drawer below the desktop breakpoint. Multi-column workspaces use `minmax(0, 1fr)`, `min-width: 0`, and start alignment so long coordinates or tables cannot stretch neighboring panels. Tables may scroll within their surface, but the page itself must not gain horizontal overflow.

At narrow widths, filters and headings stack, metric grids reduce columns, touch targets reach 44px on coarse pointers, and lifecycle stages become a readable vertical sequence. Mobile adaptation preserves governance capabilities; it does not hide the controls that make a task complete.

**The Parent Owns Rhythm Rule.** Direct page children have no external vertical margin. `ag-page-stack` and the intentional primary boundary own page spacing.

**The Geometry Is Evidence Rule.** Responsive acceptance uses DOM rectangles, overflow assertions, and desktop/mobile browser screenshots—not class-name inspection alone.

## Elevation & Depth

The system is layered and flat by default. Background, surface, border, and overlay tones carry the hierarchy. Cards and buttons have no shadow at rest; popovers, dropdowns, drawers, and modals use the raised shadow with a hairline ring. Blur is limited to shell chrome and modal scrims where it clarifies layering.

### Shadow Vocabulary

- **Structural Card:** `--ag-shadow-structural` resolves to `none` in the built-in themes; the border separates work surfaces.
- **Raised Instrument:** `--ag-shadow-elevated` belongs to modals, drawers, dropdowns, popovers, and the command palette.

**The Flat-Until-Raised Rule.** Hover may adjust border or tonal background. Large shadows appear only when the component has actually moved above its context.

## Shapes

Controls use gently curved 8px corners, work surfaces use 12px corners, and overlays use 14px corners. Tooltips are tighter at 6px. Full pills and circles are reserved for compact status dots, avatars, and truly circular controls. Borders are quiet and semi-transparent in dark mode, becoming crisp neutral dividers in light mode.

Adjacent elements should share a compatible radius family. A nested control must not look softer and more decorative than the surface that contains it.

## Components

### Buttons

- **Shape:** compact and stable (`control` radius, 34px default height; 44px minimum on coarse pointers).
- **Primary:** one dominant action per decision surface, filled with the foreground action color (light on dark, dark on light). No glow or colored shadow.
- **Hover / Focus / Active:** explicit Ant Design state tokens, visible focus, and subtle press scale. Transitions name exact properties and stay within the fast interaction range.
- **Secondary / Text:** secondary work remains neutral; destructive actions use the semantic danger treatment instead of becoming a second primary color.

### Cards / Containers

- **Corner Style:** grounded surface corners (`surface`).
- **Background:** semantic surface tokens, never a one-off near-black or near-white that breaks theme parity.
- **Shadow Strategy:** structural at rest; stronger only when elevated.
- **Border:** a quiet semantic boundary that becomes slightly clearer on hover when the whole card is interactive.
- **Internal Padding:** 16px for standard work surfaces; dense lists may use 12px vertical rhythm while preserving readable targets.

### Inputs / Fields

- **Style:** Ant Design outlined controls with 34px desktop height and shared surface, text, border, and radius tokens.
- **Focus:** Signal Cyan outline or shadow with a visible `focus-visible` fallback.
- **Error / Disabled:** semantic status and explicit explanatory copy; disabled contrast is never reused for ordinary help text.

### Navigation

- Navigation is grouped by runtime, governance, and management tasks.
- The selected item uses a neutral wash, strong text, and a narrow Signal Cyan location indicator.
- Desktop collapse is immediate and stable. Mobile navigation uses a Drawer with labelled close behavior and 44px targets.

### Metric Strip

Metric strips are compact summaries, not a replacement for the page's primary task. Values use tabular numbers and status tone only when the metric is genuinely actionable. At small widths the strip reflows without clipping or hiding labels.

### Lifecycle Stages

The source-to-distribution sequence—source, scan, quarantine decision, promotion, distribution—is the signature product object. It distinguishes conditional governance from lifecycle state, remains readable without color, and links each stage to real operational evidence.

### Feedback States

Loading, error, empty, stale-data warning, and content are mutually exclusive for an initial request. Refresh failures keep previously loaded data and place the error above it. Loading and action results are announced to assistive technology; retryable reads expose a Retry action.

### Motion

- Fast press feedback uses approximately 120ms; ordinary component state transitions use approximately 180ms with the established strong ease-out curve.
- Frequent route navigation and keyboard-initiated actions do not receive decorative entrance animation.
- Entering overlays and feedback may use a short opacity/transform transition; nothing enters from `scale(0)`.
- Dynamic repeated UI prefers interruptible transitions. Only `transform`, `opacity`, and deliberately chosen color or shadow properties animate.
- `prefers-reduced-motion` removes movement while preserving useful opacity or color state feedback.

## Do's and Don'ts

### Do:

- **Do** preserve real API data, protocol deep links, copy commands, immutable identities, and format-specific operations when refining a surface.
- **Do** use Ant Design components and tokens for controls, tables, overlays, messages, and semantic states before creating a custom primitive.
- **Do** keep public, authenticated, loading, error, empty, partial-data, and disabled states explicit.
- **Do** use `ag-page-stack`, 16px related spacing, and the intentional 24px primary boundary for page flow.
- **Do** verify desktop, mobile, light, dark, keyboard, and reduced-motion behavior in proportion to the changed surface.
- **Do** keep animation crisp, purposeful, and interruptible.

### Don't:

- **Don't** turn the Console into a generic card-and-table admin template; expose Artifact Gateway's protocol, identity, trust, and lifecycle semantics.
- **Don't** fabricate capacity, health, scan, vulnerability, release, or availability data.
- **Don't** use `transition-all`, animate layout properties, or replay staggered page entrances during frequent administration.
- **Don't** use low-contrast muted text for required guidance or encode state through color alone.
- **Don't** add page-level spacing through child margins or pairwise sibling selectors.
- **Don't** introduce a second component, toast, or motion library when Ant Design and CSS already cover the interaction.
- **Don't** weaken protocol, OpenAPI, deep-link, browser, upgrade, or recovery gates to make a visual change pass.
