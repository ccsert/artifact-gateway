import { cleanup, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it } from "vitest";
import { AntdProvider } from "../../app/AntdProvider";
import { PreferencesProvider } from "../../lib/preferences";
import type { GrantRow } from "./accessModel";
import { GrantsPanel } from "./GrantsPanel";

const rows: GrantRow[] = [
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
  it("opens filtered to the principal a link names", () => {
    render(
      <MemoryRouter
        initialEntries={["/access?principal=service-account%3Asa-1"]}
      >
        <PreferencesProvider>
          <AntdProvider>
            <GrantsPanel rows={rows} error={null} onRetry={() => {}} />
          </AntdProvider>
        </PreferencesProvider>
      </MemoryRouter>,
    );
    expect(screen.getByDisplayValue("service-account:sa-1")).toBeVisible();
    expect(screen.getByText("maven-releases")).toBeInTheDocument();
    expect(screen.queryByText("npm-hosted")).not.toBeInTheDocument();
  });
});
