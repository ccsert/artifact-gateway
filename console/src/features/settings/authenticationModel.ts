import type { OidcSettings, OidcSettingsUpdateWritable } from "../../client";

export interface AuthenticationFormValues {
  enabled: boolean;
  issuer: string;
  audience: string;
  jwksUrl: string;
  clientId: string;
  clientSecret: string;
  redirectUrl: string;
  scopes: string[];
  adminSubjects: string[];
  memberRoles: string[];
  adminRoles: string[];
  provisioningMode: "disabled" | "jit";
  emailLinkingEnabled: boolean;
  jitDefaultRole: "none" | "member" | "admin";
}

type Text = (zh: string, en: string) => string;

/** The form's starting values; the client secret is never read back. */
export function toFormValues(settings: OidcSettings): AuthenticationFormValues {
  return {
    enabled: settings.enabled,
    issuer: settings.issuer,
    audience: settings.audience,
    jwksUrl: settings.jwksUrl ?? "",
    clientId: settings.clientId,
    clientSecret: "",
    redirectUrl: settings.redirectUrl,
    scopes: settings.scopes,
    adminSubjects: settings.adminSubjects,
    memberRoles: settings.memberRoles,
    adminRoles: settings.adminRoles,
    provisioningMode: settings.provisioningMode,
    emailLinkingEnabled: settings.emailLinkingEnabled,
    jitDefaultRole: settings.jitDefaultRole,
  };
}

/** Field errors the server would otherwise reject; empty when valid. */
export function authenticationFieldErrors(
  values: AuthenticationFormValues,
  text: Text,
) {
  if (!values.enabled) return [];
  const required: (keyof AuthenticationFormValues)[] = ["issuer", "audience"];
  const missing = required.filter((name) => !String(values[name] ?? "").trim());
  const fields = missing.map((name) => ({
    name,
    errors: [text("启用 OIDC 时必填", "Required when OIDC is enabled")],
  }));
  const hasClientId = Boolean(values.clientId.trim());
  const hasRedirectUrl = Boolean(values.redirectUrl.trim());
  if (hasClientId !== hasRedirectUrl) {
    fields.push({
      name: hasClientId ? "redirectUrl" : "clientId",
      errors: [
        text(
          "Client ID 与回调地址必须同时配置",
          "Client ID and Redirect URL must be configured together",
        ),
      ],
    });
  }
  if (values.clientSecret.trim() && !hasClientId) {
    fields.push({
      name: "clientId",
      errors: [
        text(
          "配置 Client Secret 时必须填写 Client ID",
          "Client ID is required when a client secret is configured",
        ),
      ],
    });
  }
  return fields;
}

export function toUpdateBody(
  values: AuthenticationFormValues,
  clearSecret: boolean,
): OidcSettingsUpdateWritable {
  const body: OidcSettingsUpdateWritable = {
    enabled: values.enabled,
    issuer: values.issuer.trim(),
    audience: values.audience.trim(),
    clientId: values.clientId.trim(),
    redirectUrl: values.redirectUrl.trim(),
    scopes: values.scopes ?? [],
    adminSubjects: values.adminSubjects ?? [],
    memberRoles: values.memberRoles ?? [],
    adminRoles: values.adminRoles ?? [],
    provisioningMode: values.provisioningMode,
    emailLinkingEnabled: values.emailLinkingEnabled,
    jitDefaultRole: values.jitDefaultRole,
  };
  if (values.jwksUrl.trim()) body.jwksUrl = values.jwksUrl.trim();
  if (values.clientSecret.trim())
    body.clientSecret = values.clientSecret.trim();
  if (clearSecret) body.clearClientSecret = true;
  return body;
}
