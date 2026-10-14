import { ApiOutlined, SafetyCertificateOutlined } from "@ant-design/icons";
import { Form, Input, Select, Switch } from "antd";
import { usePreferences } from "../../lib/preferences";

/** Where the provider is and which scopes the Console asks for. */
export function OidcProviderFields() {
  const { t } = usePreferences();
  return (
    <section className="ag-settings-section">
      <div className="ag-settings-section-title">
        <ApiOutlined />
        <span>{t("settings.providerConnection")}</span>
      </div>
      <div className="grid grid-cols-2 gap-x-5">
        <Form.Item
          className="col-span-2"
          name="issuer"
          label="Issuer URL"
          extra={t("settings.forExampleAKeycloakRealmOrGitLabOIDC")}
        >
          <Input placeholder="https://id.example.com/realms/artifacts" />
        </Form.Item>
        <Form.Item name="audience" label={t("settings.aPIAudience")}>
          <Input placeholder="artifact-gateway-api" />
        </Form.Item>
        <Form.Item name="clientId" label="Client ID">
          <Input placeholder="artifact-gateway-console" />
        </Form.Item>
        <Form.Item name="redirectUrl" label={t("settings.redirectURL")}>
          <Input placeholder="https://gateway.example.com/auth/oidc/callback" />
        </Form.Item>
        <Form.Item
          name="jwksUrl"
          label="JWKS URL"
          extra={t("settings.leaveEmptyToUseProviderDiscovery")}
        >
          <Input placeholder={t("settings.discoveredAutomatically")} />
        </Form.Item>
        <Form.Item className="col-span-2" name="scopes" label="Scopes">
          <Select
            mode="tags"
            tokenSeparators={[",", " "]}
            placeholder="openid profile email"
          />
        </Form.Item>
      </div>
    </section>
  );
}

/** How provider roles and unlinked identities map to local accounts. */
export function OidcRoleMappingFields() {
  const { t } = usePreferences();
  return (
    <section className="ag-settings-section ag-settings-section-wide">
      <div className="ag-settings-section-title">
        <SafetyCertificateOutlined />
        <span>{t("settings.roleMapping")}</span>
      </div>
      <p className="mb-4 text-xs text-fg-tertiary">
        {t("settings.supportsCommonKeycloakRealmClientRolesTopLevel")}
      </p>
      <div className="grid grid-cols-2 gap-x-5">
        <Form.Item name="memberRoles" label="Member">
          <Select mode="tags" tokenSeparators={[",", " "]} />
        </Form.Item>
        <Form.Item name="adminRoles" label="Admin">
          <Select mode="tags" tokenSeparators={[",", " "]} />
        </Form.Item>
        <Form.Item
          className="col-span-3"
          name="adminSubjects"
          label={t("settings.administratorSubjectAllowlist")}
          extra={t("settings.useOnlyForAccountsThatRequireAdministratorAccess")}
        >
          <Select mode="tags" tokenSeparators={[",", " "]} />
        </Form.Item>
        <Form.Item
          name="provisioningMode"
          label={t("settings.unlinkedIdentities")}
          extra={t(
            "settings.whenDisabledOnlyAdministratorLinkedIdentitiesCanEnter",
          )}
        >
          <Select
            options={[
              {
                value: "disabled",
                label: t("settings.keepExternalPrincipal"),
              },
              {
                value: "jit",
                label: t("settings.provisionOnFirstSignIn"),
              },
            ]}
          />
        </Form.Item>
        <Form.Item
          name="jitDefaultRole"
          label={t("settings.jITDefaultRole")}
          extra={t("settings.usedOnlyWhenNoExternalRoleMappingMatches")}
        >
          <Select
            options={[
              {
                value: "none",
                label: t("settings.noneAwaitingApproval"),
              },
              {
                value: "member",
                label: t("settings.memberNoRepositoryAccess"),
              },
              { value: "admin", label: "admin" },
            ]}
          />
        </Form.Item>
        <Form.Item
          name="emailLinkingEnabled"
          label={t("settings.verifiedEmailLinking")}
          valuePropName="checked"
          extra={t("settings.onlyEmailVerifiedTrueClaimsAreUsedMultiple")}
        >
          <Switch
            checkedChildren={t("settings.on")}
            unCheckedChildren={t("settings.off")}
          />
        </Form.Item>
      </div>
    </section>
  );
}
