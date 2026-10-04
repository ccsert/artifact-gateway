import { cleanup, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, expect, it, vi } from "vitest";
import { PreferencesProvider } from "../../lib/preferences";
import { SystemPage } from "./System";

vi.mock("../operations/SystemDiagnosticsPanel", () => ({
  SystemDiagnosticsPanel: () => <p>diagnostics fixture</p>,
}));
vi.mock("../operations/RuntimeNodesPanel", () => ({
  RuntimeNodesPanel: () => null,
}));
vi.mock("../operations/RuntimeLogsPanel", () => ({
  RuntimeLogsPanel: () => <p>logs fixture</p>,
}));
vi.mock("../../lib/auth", () => ({ useAuth: () => ({ identity: null }) }));
afterEach(cleanup);

it("opens quota alerts through the System route tab", () => {
  render(
    <MemoryRouter initialEntries={["/system?tab=alerts"]}>
      <PreferencesProvider>
        <SystemPage />
      </PreferencesProvider>
    </MemoryRouter>,
  );
  expect(
    screen.getByRole("tab", { name: "配额告警", selected: true }),
  ).toBeInTheDocument();
  expect(screen.queryByText("diagnostics fixture")).not.toBeInTheDocument();
});
