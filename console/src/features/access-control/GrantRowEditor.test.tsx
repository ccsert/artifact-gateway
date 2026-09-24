import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { AuthorizationRole } from "../../client";
import { AntdProvider } from "../../app/AntdProvider";
import { PreferencesProvider } from "../../lib/preferences";
import { GrantRowEditor } from "./GrantRowEditor";
import type { DraftGrant, PrincipalOption } from "./grantDraft";

vi.mock("../../client", () => ({
  listApiKeys: vi.fn(),
  listAuthorizationRoles: vi.fn(),
  listGrants: vi.fn(),
  listServiceAccounts: vi.fn(),
  listUsers: vi.fn(),
  replaceGrants: vi.fn(),
}));

const principalChoices: PrincipalOption[] = [
  { value: "user:alice", label: "用户 · alice", detail: "全局角色 reader" },
  {
    value: "api-key:key-1",
    label: "API Key · deploy",
    detail: "全局角色 writer",
  },
  {
    value: "service-account:sa-1",
    label: "服务账号 · release-bot",
    detail: "无全局角色，由仓库授权决定",
  },
];

const releaseRole: AuthorizationRole = {
  id: "role-release",
  name: "发布机器人",
  scopes: ["repositories:read", "repositories:write"],
  version: "1",
  createdAt: "2026-08-18T00:00:00Z",
  updatedAt: "2026-08-18T00:00:00Z",
};

function draft(overrides: Partial<DraftGrant> = {}): DraftGrant {
  return {
    principal: "user:alice",
    scopes: ["repositories:read"],
    ...overrides,
  };
}

function renderRow(grant: DraftGrant, roles: AuthorizationRole[] = []) {
  const onChange = vi.fn<(next: DraftGrant) => void>();
  const onRemove = vi.fn<() => void>();
  render(
    <PreferencesProvider>
      <AntdProvider>
        <GrantRowEditor
          grant={grant}
          principalOptions={principalChoices}
          authorizationRoles={roles}
          format="raw"
          onChange={onChange}
          onRemove={onRemove}
        />
      </AntdProvider>
    </PreferencesProvider>,
  );
  return { onChange, onRemove };
}

function lastChange(onChange: ReturnType<typeof renderRow>["onChange"]) {
  return onChange.mock.calls.at(-1)?.[0];
}

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("GrantRowEditor", () => {
  it("offers the principal options it is given", async () => {
    const user = userEvent.setup();
    renderRow(draft({ principal: "" }));

    await user.click(screen.getByRole("combobox", { name: "授权主体" }));

    expect(screen.getByText(/用户 · alice/)).toBeInTheDocument();
    expect(screen.getByText(/API Key · deploy/)).toBeInTheDocument();
    expect(screen.getByText(/服务账号 · release-bot/)).toBeInTheDocument();
    expect(screen.getByText(/OIDC \/ 自定义 actor/)).toBeInTheDocument();
  });

  it("copies the scopes of a chosen custom authorization role", async () => {
    const user = userEvent.setup();
    const { onChange } = renderRow(draft(), [releaseRole]);

    await user.click(screen.getAllByRole("combobox")[1]);
    await user.click(screen.getByText("发布机器人"));

    const next = lastChange(onChange);
    expect(next?.scopes).toEqual(["repositories:read", "repositories:write"]);
    expect(next?.scopes).not.toBe(releaseRole.scopes);
    expect(next?.roleId).toBe("role-release");
    expect(next?.principal).toBe("user:alice");
  });

  it("maps a chosen built-in level to that level's scopes", async () => {
    const user = userEvent.setup();
    const { onChange } = renderRow(draft({ scopes: ["repositories:read"] }), [
      releaseRole,
    ]);

    await user.click(screen.getAllByRole("combobox")[1]);
    await user.click(screen.getByText("写入 · 发布 / 编辑"));

    const next = lastChange(onChange);
    expect(next?.scopes).toEqual(["repositories:write"]);
    expect(next?.roleId).toBeUndefined();
    expect(next?.principal).toBe("user:alice");
  });

  it("reports a resource prefix change picked from the scope segmented control", async () => {
    const user = userEvent.setup();
    const { onChange } = renderRow(draft({ resourcePrefix: "" }));

    await user.click(screen.getByText("限定范围"));

    expect(lastChange(onChange)?.resourcePrefix).toBe("releases/");
  });

  it("reports a typed resource prefix", async () => {
    const user = userEvent.setup();
    const { onChange } = renderRow(draft({ resourcePrefix: "releases/" }));

    // Principal select, permission select, then the resource prefix input.
    const prefixInput = screen.getAllByRole("combobox")[2];
    await user.type(prefixInput, "{End}x");

    expect(lastChange(onChange)?.resourcePrefix).toBe("releases/x");
  });
});
