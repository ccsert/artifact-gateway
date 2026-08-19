# Console theme packages

Artifact Gateway loads Console themes from two sources:

1. four bundled packages: the original `Gateway Dark` and `Gateway Light`, plus
   the extension packages `Aerok Dark` and `Aerok Light`;
2. optional `*.theme.json` files in `GATEWAY_CONSOLE_THEME_DIR`.

Theme Package v1 intentionally accepts a bounded Ant Design Seed/Alias token
subset. Component dimensions and page geometry remain owned by the Console, so
an AI-generated theme can change the visual palette without silently changing
typography, control density, or responsive layout. Theme Package v1 therefore
rejects component overrides, font tokens, and geometry tokens.

Generate a package against [`console-theme.schema.json`](console-theme.schema.json),
then validate and install it with the Gateway CLI:

```bash
go run ./cmd/gateway theme validate --file /path/to/acme.theme.json --format json
go run ./cmd/gateway theme install --file /path/to/acme.theme.json --dir ./themes --format json
go run ./cmd/gateway theme list --dir ./themes --format json
```

The running Gateway rereads the external directory when site settings are
requested. A newly installed package therefore appears in System Settings
without rebuilding the Console or Gateway. An administrator must still enable
it and may choose it as the deployment default.

External packages cannot replace any bundled theme ID. Invalid or
duplicate packages fail closed and are not returned to the Console.
