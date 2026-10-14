import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Link, MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it } from "vitest";
import { AntdProvider } from "../../app/AntdProvider";
import { PreferencesProvider } from "../../lib/preferences";
import type { GrantRow } from "./accessModel";
import { GrantsPanel } from "./GrantsPanel";

const rows: GrantRow[] = [
  {
    repositoryId: "repo-custom",
    repositoryName: "custom-actor-releases",
    format: "raw",
    principal: "oidc:service-account:sa-1",
    scopes: ["repositories:read"],
    resourcePrefix: "",
  },
  {
    repositoryId: "repo-1",
    repositoryName: "maven-releases",
    format: "maven",
    principal: "service-account:sa-1",
    scopes: ["repositories:write"],
    resourcePrefix: "",
  },
  {
    repositoryId: "repo-2",
    repositoryName: "npm-hosted",
    format: "npm",
    principal: "user:alice",
    scopes: ["repositories:read"],
    resourcePrefix: "",
  },
];

afterEach(() => cleanup());

describe("GrantsPanel", () => {
  it("opens filtered to exactly the principal a link names and keeps manual search partial", async () => {
    const user = userEvent.setup();
    render(
      <MemoryRouter
        initialEntries={["/access?principal=service-account%3Asa-1"]}
      >
        <PreferencesProvider>
          <AntdProvider>
            <GrantsPanel rows={rows} error={null} onRetry={() => {}} />
            <Link to="/access?principal=user%3Aalice">
              Open another principal
            </Link>
          </AntdProvider>
        </PreferencesProvider>
      </MemoryRouter>,
    );
    expect(screen.getByDisplayValue("service-account:sa-1")).toBeVisible();
    expect(screen.getByText("maven-releases")).toBeInTheDocument();
    expect(screen.queryByText("npm-hosted")).not.toBeInTheDocument();
    expect(screen.queryByText("custom-actor-releases")).not.toBeInTheDocument();

    const principal = screen.getByDisplayValue("service-account:sa-1");
    await user.clear(principal);
    await user.type(principal, "sa-1");
    expect(screen.getByText("maven-releases")).toBeInTheDocument();
    expect(screen.getByText("custom-actor-releases")).toBeInTheDocument();
    await user.click(
      screen.getByRole("link", { name: "Open another principal" }),
    );
    expect(screen.getByDisplayValue("user:alice")).toBeVisible();
    expect(screen.getByText("npm-hosted")).toBeInTheDocument();
    expect(screen.queryByText("maven-releases")).not.toBeInTheDocument();
    expect(screen.queryByText("custom-actor-releases")).not.toBeInTheDocument();
  });
});
