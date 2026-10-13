import { ApiOutlined, SafetyCertificateOutlined } from "@ant-design/icons";
import { Form, Input, Select, Switch } from "antd";
import { usePreferences } from "../../lib/preferences";

/** Where the provider is and which scopes the Console asks for. */
export function OidcProviderFields() {
  const { text } = usePreferences();
  return (
    <section className="ag-settings-section">
      <div className="ag-settings-section-title">
        <ApiOutlined />
        <span>{text("提供方连接", "Provider connection")}</span>
      </div>
      <div className="grid grid-cols-2 gap-x-5">
        <Form.Item
          className="col-span-2"
          name="issuer"
          label="Issuer URL"
          extra={text(
            "例如 Keycloak Realm 或 GitLab 的 OIDC Issuer。",
            "For example, a Keycloak realm or GitLab OIDC issuer.",
          )}
        >
          <Input placeholder="https://id.example.com/realms/artifacts" />
        </Form.Item>
        <Form.Item name="audience" label={text("API Audience", "API audience")}>
          <Input placeholder="artifact-gateway-api" />
        </Form.Item>
        <Form.Item name="clientId" label="Client ID">
          <Input placeholder="artifact-gateway-console" />
        </Form.Item>
        <Form.Item name="redirectUrl" label={text("回调地址", "Redirect URL")}>
          <Input placeholder="https://gateway.example.com/auth/oidc/callback" />
        </Form.Item>
        <Form.Item
          name="jwksUrl"
          label="JWKS URL"
          extra={text(
            "留空时使用提供方 discovery 文档。",
            "Leave empty to use provider discovery.",
          )}
        >
          <Input placeholder={text("自动发现", "Discovered automatically")} />
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
  const { text } = usePreferences();
  return (
    <section className="ag-settings-section ag-settings-section-wide">
      <div className="ag-settings-section-title">
        <SafetyCertificateOutlined />
        <span>{text("角色映射", "Role mapping")}</span>
      </div>
      <p className="mb-4 text-xs text-fg-tertiary">
        {text(
          "支持 Keycloak Realm/Client Roles、顶层 roles、groups 等常见声明；匹配 admin 映射即成为管理员，匹配 member 映射则通过审批但不附带仓库权限（仓库访问始终来自按仓库授权），两者同时匹配时 admin 优先。",
          "Supports common Keycloak realm/client roles, top-level roles, and groups claims. A matched admin mapping makes the account an administrator; a matched member mapping approves it without repository access, which always comes from per-repository grants. Admin wins when both match.",
        )}
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
          label={text(
            "管理员 Subject 白名单",
            "Administrator subject allowlist",
          )}
          extra={text(
            "仅用于必须按稳定 sub 直接授予管理员权限的账号。",
            "Use only for accounts that require administrator access by stable sub claim.",
          )}
        >
          <Select mode="tags" tokenSeparators={[",", " "]} />
        </Form.Item>
        <Form.Item
          name="provisioningMode"
          label={text("未绑定身份", "Unlinked identities")}
          extra={text(
            "关闭时只有管理员手动绑定的身份可以进入本地账户；JIT 会在首次登录时创建账户。",
            "When disabled, only administrator-linked identities can enter local accounts. JIT creates an account on first sign-in.",
          )}
        >
          <Select
            options={[
              {
                value: "disabled",
                label: text("拒绝并保持外部身份", "Keep external principal"),
              },
              {
                value: "jit",
                label: text("首次登录自动创建", "Provision on first sign-in"),
              },
            ]}
          />
        </Form.Item>
        <Form.Item
          name="jitDefaultRole"
          label={text("JIT 默认角色", "JIT default role")}
          extra={text(
            "仅当声明没有匹配角色映射时使用；选择 none 后新用户会进入待授权状态，选择 member 则不附带任何仓库权限、需按仓库授权。",
            "Used only when no external role mapping matches; none registers new users without access, member registers them with no repository access until granted.",
          )}
        >
          <Select
            options={[
              {
                value: "none",
                label: text("none · 待授权", "none · awaiting approval"),
              },
              {
                value: "member",
                label: text(
                  "member · 无仓库权限",
                  "member · no repository access",
                ),
              },
              { value: "admin", label: "admin" },
            ]}
          />
        </Form.Item>
        <Form.Item
          name="emailLinkingEnabled"
          label={text("已验证邮箱自动关联", "Verified email linking")}
          valuePropName="checked"
          extra={text(
            "仅使用 email_verified=true 的声明；多个匹配账户会拒绝登录。",
            "Only email_verified=true claims are used; multiple matching accounts reject sign-in.",
          )}
        >
          <Switch
            checkedChildren={text("允许", "On")}
            unCheckedChildren={text("关闭", "Off")}
          />
        </Form.Item>
      </div>
    </section>
  );
}
