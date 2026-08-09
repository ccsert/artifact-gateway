import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { IdentitySummary } from "./IdentitySummary";
import { PreferencesProvider } from "../lib/preferences";

const renderWithPreferences = (ui: React.ReactNode) =>
  render(<PreferencesProvider>{ui}</PreferencesProvider>);

afterEach(cleanup);

describe("IdentitySummary", () => {
  it("explains an OIDC role mapping without exposing unrelated claims", () => {
    renderWithPreferences(
      <IdentitySummary
        identity={{
          actor: "ci-user",
          kind: "oidc",
          role: "writer",
          administrator: false,
          oidc: {
            adminSubject: false,
            roleMappings: [
              { externalRole: "gateway-reader", gatewayRole: "reader" },
              { externalRole: "gateway-writer", gatewayRole: "writer" },
            ],
          },
        }}
      />,
    );

    expect(screen.getByText("ci-user")).toBeInTheDocument();
    expect(screen.getByText("OIDC")).toBeInTheDocument();
    expect(screen.getByText("写入者")).toBeInTheDocument();
    expect(screen.getByText("gateway-reader → reader")).toBeInTheDocument();
    expect(screen.getByText("gateway-writer → writer")).toBeInTheDocument();
  });

  it("makes the absence of a global role explicit", () => {
    renderWithPreferences(
      <IdentitySummary
        identity={{
          actor: "resolver",
          kind: "static_resolver",
          administrator: false,
        }}
      />,
    );

    expect(screen.getByText("静态解析凭据")).toBeInTheDocument();
    expect(screen.getByText("无，由仓库规则判定")).toBeInTheDocument();
  });
});
