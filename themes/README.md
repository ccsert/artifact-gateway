# Console theme packages

Artifact Gateway loads Console themes from three sources:

1. four bundled packages: the original `Gateway Dark` and `Gateway Light`, plus
   the extension packages `Aerok Dark` and `Aerok Light`;
2. optional operator-owned `*.theme.json` files in
   `GATEWAY_CONSOLE_THEME_DIR`;
3. administrator-managed packages uploaded in Console and persisted in
   PostgreSQL.

Theme Package v1 intentionally accepts a bounded Ant Design Seed/Alias token
subset. Component dimensions and page geometry remain owned by the Console, so
an AI-generated theme can change the visual palette without silently changing
typography, control density, or responsive layout. Theme Package v1 therefore
rejects component overrides, font tokens, and geometry tokens.

Generate a package against [`console-theme.schema.json`](console-theme.schema.json).
An administrator can upload it from **Site settings → Console themes**. The
server strictly validates the package, shows a resolved preview, and then lets
the administrator install it or replace an existing managed package. A newly
installed package is only staged for activation; use **Save and apply** to make
it selectable by users.

Operators can instead validate and install a directory-owned package with the
Gateway CLI:

```bash
go run ./cmd/gateway theme validate --file /path/to/acme.theme.json --format json
go run ./cmd/gateway theme install --file /path/to/acme.theme.json --dir ./themes --format json
go run ./cmd/gateway theme list --dir ./themes --format json
```

The running Gateway rereads the external directory and PostgreSQL catalog when
site settings are requested. New packages therefore appear without rebuilding
the Console or Gateway. Managed package replacement and deletion require the
current `If-Match` version; deletion is rejected while the theme remains
enabled in saved site settings. Each mutation is audited.

Managed packages cannot replace built-in or directory-owned IDs. Directory
packages cannot replace a bundled ID. Invalid or duplicate packages fail closed
and are not returned to the Console. Theme Package v1 is JSON-only: it accepts
bounded color tokens and never CSS, JavaScript, fonts, or external assets.
