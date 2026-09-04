# Console Themes Use One Resolved Semantic Projection

[简体中文](0005-console-semantic-theme-system.zh-CN.md) · [Documentation index](../README.md)

Status: accepted

## Context

Artifact Gateway Console has four built-in Theme Packages and can load more
packages from the server. A package contains a constrained Ant Design seed and
alias-token subset. The runtime previously projected that package through
several unrelated paths:

- Ant Design received one `ThemeConfig`;
- global CSS received a flat set of `--ag-*` variables;
- Gateway Dark and Gateway Light had a second compatibility variable table;
- `styles.css` contained another dark/light bootstrap palette and route-level
  color literals.

The result looked themed but did not behave as a system. The primary accent was
also reused for text selection, ordinary section icons, neutral empty states,
decorative glows, and generic lifecycle metadata. Changing a theme therefore
changed too many unrelated visual signals, while a CSS-only fix could easily
drift from Ant Design components.

The design system already says the signal color is rare. The implementation
needs an enforceable contract that makes that rule local, testable, and
extensible without exposing arbitrary CSS through the server API.

## Considered designs

### 1. Consume Ant Design CSS variables directly

Custom Console CSS could read generated `--ag-ant-*` variables. This minimizes
one projection layer, but it couples product-specific UI to Ant Design's token
names and generation behavior. It also does not define product roles such as
text selection, shell chrome, navigation location, or identity artwork, and it
cannot provide a complete pre-React bootstrap palette.

### 2. Put a complete CSS-variable map in every Theme Package

The server could accept all Ant Design tokens and every Console CSS variable.
This gives package authors maximum control, but it creates two color systems,
expands the untrusted configuration surface, makes schema evolution expensive,
and lets a theme accidentally change geometry or operational meaning.

### 3. Resolve one semantic projection in the Console

The server package remains a constrained color input. The Console resolves it
through the selected Ant Design algorithm, then projects the resolved tokens
into a typed set of product roles. Ant Design component tokens and custom CSS
variables are both built from those roles. A single runtime applies the whole
projection in one render transaction.

This design adds one internal layer, but that layer is the product vocabulary
the other approaches lack. It is the selected design.

## Decision

Theme Package schema version 1 remains the external contract. It continues to
accept stable color seed/alias tokens and cannot change typography, density,
spacing, component geometry, or arbitrary CSS. The package schema does not
change. Administrator-managed packages add management resources and persistent
storage without widening the package's executable surface.

The Console owns a `ResolvedConsoleTheme` deep module with this public shape:

```text
ConsoleTheme package
        │
        ▼
Ant Design mode algorithm + exact package aliases
        │
        ▼
ResolvedConsoleTheme
  ├─ antDesign: ThemeConfig
  ├─ token: resolved Ant Design global tokens
  ├─ roles: typed Console semantic roles
  └─ cssVariables: complete semantic CSS projection
        │
        ├─ ConfigProvider
        └─ document root in the same theme commit
```

The resolver is pure. The runtime application function is the only code that
writes theme variables to the document. Theme previews consume resolved roles
instead of recreating a palette. Built-in visual compatibility is expressed as
typed role overrides, not an unstructured CSS-variable map.

The CSS bootstrap palette exists only to prevent an unthemed first paint while
site settings load. It uses the same semantic variable names, supports both
color modes, and is completely overwritten by the runtime projection. It is
not a fifth source of theme behavior.

## Package ownership and lifecycle

The runtime catalog has three explicit sources with fixed ownership:

1. `builtin` packages ship with the Gateway binary;
2. `directory` packages are owned by operators through
   `GATEWAY_CONSOLE_THEME_DIR` and the `gateway theme` CLI;
3. `managed` packages are uploaded by administrators and stored in PostgreSQL.

Built-in and directory IDs are reserved and cannot be replaced or deleted by
the management API. Managed packages use an integer version exposed as an
opaque string and require `If-Match` for replacement and deletion. A managed
package must be disabled in persisted site settings before it can be deleted.
Install, replace, and delete operations are audited.

The Console sends an uploaded JSON object to the server for authoritative
strict validation before showing a preview. Installing a new package stages it
in the enabled-theme draft when capacity allows; it does not become available
to users until an administrator saves site settings. This separates package
storage from deployment-wide activation and keeps concurrent site-setting
changes protected by their own version.

## Semantic role contract

| Family | Purpose | Examples |
| --- | --- | --- |
| Surface | Depth and interaction without status meaning | canvas, container, translucent container, elevated, hover, disabled, shell chrome |
| Content | Readable hierarchy | strong, primary, secondary, tertiary, disabled, on-action, on-identity |
| Border | Structure and focus | default, subtle, row, strong, focus |
| Action | The principal operation | primary, hover, active, soft, shadow |
| Link | Navigable text and trusted deep links | default, hover, active |
| Focus | Keyboard location independent of component implementation | ring |
| Selection | Native text selection | neutral background and normal readable text |
| Navigation | Current application location | indicator, selected background, selected text |
| Status | Functional operational state | success, warning, danger, info with foreground, soft background, border |
| Visualization | Data encoding without operational-state meaning | stable categorical palette, primary/secondary trends, unknown-category fallback |
| Identity | The product/site mark only | gradient stops, foreground, outline, glow |
| Effect | Non-color structure | structural/elevated shadow, scrollbar, radii |

Component geometry remains a Console invariant. Component colors are derived
from roles: ordinary hover is neutral, selected navigation uses the navigation
role, input focus uses the focus role, and status components use only their
matching status family.

## Accent budget

The primary accent is allowed for:

- the single primary action in a decision surface;
- current navigation or tab location;
- keyboard focus;
- trusted text links and actionable lifecycle affordances;
- the actual product/site identity mark.

It is not allowed for:

- native text selection;
- ordinary hover on cards, rows, or secondary controls;
- generic section icons, neutral badges, empty states, or card borders;
- disabled controls;
- large decorative page washes or backgrounds;
- success, warning, danger, or informational state.

Text selection is derived from foreground and container neutrals, not from
`colorPrimary`. Its foreground stays the normal high-contrast content color.
Status colors always retain their functional meaning and are accompanied by
text, an icon, or both.

Data visualization uses a separate stable categorical palette and does not
borrow Action or Status roles. Chart axes, labels, and surfaces follow the
selected mode while category identity stays recognizable across themes. This
prevents one color from meaning both an operational state and an artifact
format on the same page.
The eight artifact formats share one format-to-Visualization-slot mapping
across charts, badges, and public-catalog marks. Unknown formats use the
neutral fallback instead of borrowing an operational status color.

## Precedence and compatibility

Resolution order is fixed:

1. the light or dark Ant Design algorithm derives a complete token map;
2. explicit package aliases overwrite derived aliases exactly;
3. Console semantic roles are derived from the complete token map;
4. typed built-in compatibility overrides preserve intentional Gateway shell
   characteristics;
5. Console component geometry invariants and semantic component colors produce
   the final `ThemeConfig`;
6. the complete CSS-variable projection is applied atomically.

Every projection is complete, so switching themes cannot retain a stale value
from the previous theme. Reduced-motion continues to skip spatial theme
transitions while applying the same final colors.

Theme motion uses a trigger-anchored 240 ms radial reveal: the old document
snapshot stays opaque while a circular clip expands the new snapshot from the
theme selector. Browsers without View Transitions, and users requesting reduced
motion, receive the same atomic commit without descendant-by-descendant color
interpolation. Neutral empty states use small semantic icons and useful copy
rather than theme-specific decorative illustrations.

## Migration rules

- Custom CSS must consume semantic `--ag-*` roles; it must not read generated
  Ant Design variables or introduce a package-specific selector.
- A new literal color in component CSS requires either an identity asset reason
  or a new reviewed semantic role.
- Application components must not use hue utilities such as `text-cyan-*` or
  `bg-rose-*`, or pass preset hue names such as `blue` and `green` to Ant
  Design components. Status uses semantic status names; categories use
  Visualization.
- Primary color usage must name one of the allowed accent-budget purposes.
- Existing generic `brand`, `text`, `surface`, and status aliases are migrated
  to their explicit role names.
- Theme Package schema version 2 is considered only when a real package cannot
  be represented by v1 inputs plus deterministic semantic projection. It must
  remain a small reviewed role whitelist, never an arbitrary variable map.

## Acceptance gates

A theme-system change is complete only when:

- all four built-in themes resolve the complete role contract;
- text selection is neutral and distinct from the primary action color;
- Ant Design and custom CSS change in the same theme commit;
- no stale variables remain after repeated theme switching;
- lint, type checking, unit tests, Ant Design checks, formatting, and build pass;
- real-browser checks cover dark/light, desktop/mobile, keyboard focus, text
  selection, rapid switching, and `prefers-reduced-motion`;
- custom extension themes continue to work without a server schema change;
- package upload covers strict validation, preview, install/replace, optimistic
  concurrency, audit, activation, and delete protection;
- a second Gateway instance reads the same managed catalog from PostgreSQL.

## Consequences

Theme behavior becomes a deep, testable module instead of a collection of
selectors. Adding a component now requires choosing a semantic role, which is
intentional friction against accent sprawl. The bootstrap palette still
duplicates two fallback values for first paint, but it no longer contains
independent behavior and is covered by contract tests. Theme authors retain
wide color freedom while the Console keeps ownership of hierarchy, semantics,
accessibility, and geometry.
