---
name: Artifact Gateway Console
description: A precise, protocol-native control plane for trusted artifact operations.
colors:
  primary-dark: "#06b6d4"
  primary-dark-hover: "#22d3ee"
  primary-dark-active: "#0891b2"
  primary-light: "#0891b2"
  primary-soft-dark: "rgba(6, 182, 212, 0.12)"
  primary-soft-light: "rgba(8, 145, 178, 0.10)"
  dark-bg: "#08090b"
  dark-surface: "#141417"
  dark-elevated: "#1b1b1f"
  dark-text: "#e4e4e7"
  dark-text-strong: "#fafafa"
  dark-text-secondary: "#b4b4bd"
  dark-text-muted: "#8f8f9a"
  light-bg: "#f6f7f9"
  light-surface: "#ffffff"
  light-text: "#27272a"
  light-text-strong: "#18181b"
  light-text-secondary: "#52525b"
  success-dark: "#34d399"
  warning-dark: "#fbbf24"
  error-dark: "#fb7185"
typography:
  headline:
    fontFamily: "Inter, ui-sans-serif, system-ui, -apple-system, 'Segoe UI', sans-serif"
    fontSize: "22px"
    fontWeight: 600
    lineHeight: "30px"
    letterSpacing: "-0.01em"
  title:
    fontFamily: "Inter, ui-sans-serif, system-ui, -apple-system, 'Segoe UI', sans-serif"
    fontSize: "16px"
    fontWeight: 600
    lineHeight: "24px"
    letterSpacing: "-0.01em"
  body:
    fontFamily: "Inter, ui-sans-serif, system-ui, -apple-system, 'Segoe UI', sans-serif"
    fontSize: "14px"
    fontWeight: 400
    lineHeight: "22px"
    letterSpacing: "normal"
  label:
    fontFamily: "Inter, ui-sans-serif, system-ui, -apple-system, 'Segoe UI', sans-serif"
    fontSize: "12px"
    fontWeight: 500
    lineHeight: "20px"
    letterSpacing: "0.04em"
  technical:
    fontFamily: "'JetBrains Mono', ui-monospace, SFMono-Regular, Menlo, monospace"
    fontSize: "12px"
    fontWeight: 400
    lineHeight: "20px"
    letterSpacing: "normal"
rounded:
  tooltip: "6px"
  control: "8px"
  surface: "10px"
  overlay: "12px"
  pill: "9999px"
spacing:
  xs: "4px"
  sm: "8px"
  md: "16px"
  lg: "24px"
  xl: "32px"
components:
  button-primary:
    backgroundColor: "{colors.primary-dark}"
    textColor: "{colors.dark-text-strong}"
    typography: "{typography.body}"
    rounded: "{rounded.control}"
    height: "34px"
    padding: "0 15px"
  button-primary-hover:
    backgroundColor: "{colors.primary-dark-hover}"
  button-primary-active:
    backgroundColor: "{colors.primary-dark-active}"
  button-default:
    backgroundColor: "{colors.dark-surface}"
    textColor: "{colors.dark-text}"
    typography: "{typography.body}"
    rounded: "{rounded.control}"
    height: "34px"
    padding: "0 15px"
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
    backgroundColor: "{colors.primary-soft-dark}"
    textColor: "{colors.primary-dark-hover}"
    typography: "{typography.body}"
    rounded: "{rounded.control}"
    height: "38px"
  table-header:
    backgroundColor: "{colors.dark-bg}"
    textColor: "{colors.dark-text-muted}"
    typography: "{typography.label}"
    padding: "12px 16px"
---

# Design System: Artifact Gateway Console

[简体中文](DESIGN.zh-CN.md) | [Documentation index](docs/README.md)

## Overview

**Creative North Star: "The Verified Control Plane"**

Artifact Gateway Console should feel like the control surface for a system whose claims can be proved. The visual language is dark, precise, operational, and quiet: real status, immutable identity, policy boundaries, and recoverable actions receive attention before decoration. The default dark theme resembles a carefully lit engineering console rather than a generic black dashboard; the light theme preserves the same hierarchy instead of becoming a separate product.

The interface is an **Operate** surface. Density, scanability, predictable Ant Design behavior, keyboard access, and fast state feedback outrank spectacle. Product character comes from protocol-aware objects, monospaced identities, the source-to-distribution lifecycle, and restrained signal cyan—not from gradients, oversized marketing type, or ornamental motion.

**Key Characteristics:**

- Evidence-led hierarchy: health, risk, blocked work, and required action appear before decorative metrics.
- Protocol-native specificity: coordinates, digests, Repository types, and lifecycle stages remain visible and copyable.
- Restrained signal color: cyan identifies primary action, current location, focus, and trusted links; semantic colors retain their status meanings.
- Layered but grounded surfaces: borders and tonal separation establish structure; shadows are reserved for real elevation.
- Crisp motion: short, interruptible feedback supports state change and never delays frequent navigation.

## Colors

The palette is neutral-first with a single cool signal family and explicit semantic status colors.

Runtime themes follow [ADR 0005](docs/adr/0005-console-semantic-theme-system.md): a constrained Theme Package is resolved once into typed surface, content, border, action, link, focus, selection, navigation, status, visualization, identity, and effect roles. Ant Design component tokens and custom CSS variables consume that same projection. Page CSS must name a semantic role rather than a palette color.

### Primary

- **Signal Cyan** (`primary-dark`, `primary-light`): primary actions, links, focus, selected navigation, trusted lifecycle affordances, and current-state emphasis.
- **Signal Wash** (`primary-soft-dark`, `primary-soft-light`): selected action or current-location backgrounds that must remain subordinate to content. Informational surfaces use the `info` status family instead.

### Neutral

- **Night Ledger** (`dark-bg`): the dark page canvas and strongest visual recess.
- **Verified Surface** (`dark-surface`): cards, tables, controls, and primary work areas.
- **Raised Instrument** (`dark-elevated`): modals, drawers, popovers, and other genuine overlays.
- **Operational Text** (`dark-text`, `light-text`): default readable content.
- **Decisive Text** (`dark-text-strong`, `light-text-strong`): page titles, values, and the strongest labels.
- **Supporting Text** (`dark-text-secondary`, `light-text-secondary`): explanations that still carry actionable meaning.
- **Muted Metadata** (`dark-text-muted`): timestamps, technical labels, and secondary metadata; never the only carrier of a state.
- **Daylight Canvas** (`light-bg`, `light-surface`): light-theme page and work surfaces with the same hierarchy as the dark theme.

### Named Rules

**The Signal Rarity Rule.** Cyan is reserved for action, location, focus, and trusted system links; it should not become a general decorative fill.

**The Neutral Selection Rule.** Native text selection is a content state. Its background is derived from foreground and container neutrals, never from the primary action color.

**The Accent Budget Rule.** Generic icons, neutral badges, empty states, disabled controls, ordinary hover, card borders, and large decorative washes do not spend the primary accent. Actual identity marks are the only decorative exception.

**The Status Has Words Rule.** Success, warning, and error colors must be paired with text, an icon, or both. Color alone never communicates operational state.

## Typography

**Display Font:** Inter with system UI fallbacks
**Body Font:** Inter with system UI fallbacks
**Label/Mono Font:** JetBrains Mono with platform monospace fallbacks

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

The system is layered and flat by default. Background, surface, border, and overlay tones carry most hierarchy. Cards use a low structural shadow; popovers and modals use the stronger raised shadow. Blur is limited to shell chrome and translucent surfaces where it clarifies layering, not as a universal visual effect.

### Shadow Vocabulary

- **Structural Card:** the low `--ag-shadow-card` shadow separates work surfaces without making every card float.
- **Raised Instrument:** the stronger `--ag-shadow-pop` shadow belongs to modals, drawers, dropdowns, and popovers.
- **Primary Action Signal:** the compact Ant Design primary-button shadow reinforces the principal action without spreading glow across the page.

**The Flat-Until-Raised Rule.** Hover may adjust border or tonal background. Large shadows appear only when the component has actually moved above its context.

## Shapes

Controls use gently curved 8px corners, standard work surfaces use 10px corners, and overlays may use 12px corners. Tooltips are tighter at 6px. Full pills and circles are reserved for compact status dots, avatars, and truly circular controls. Borders are quiet and semi-transparent in dark mode, becoming crisp neutral dividers in light mode.

Adjacent elements should share a compatible radius family. A nested control must not look softer and more decorative than the surface that contains it.

## Components

### Buttons

- **Shape:** compact and stable (`control` radius, 34px default height; 44px minimum on coarse pointers).
- **Primary:** one dominant action per decision surface, using Signal Cyan and strong readable text.
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
- The selected item combines a restrained cyan wash, readable cyan text, and a narrow location indicator.
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
