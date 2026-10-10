import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { PreferencesProvider } from "../../lib/preferences";
import { RecentAuditList } from "./RecentAuditList";

describe("RecentAuditList", () => {
  it("states audit events in words instead of operation and outcome codes", () => {
    render(
      <PreferencesProvider>
        <RecentAuditList
          records={[
            {
              occurredAt: "2026-10-01T09:12:44Z",
              actor: "user:alice",
              operation: "repository.grants.upsert",
              outcome: "resolved",
              repository: "maven-releases",
              requestId: "req-1",
            },
            {
              occurredAt: "2026-10-01T08:03:59Z",
              actor: "user:bob",
              operation: "get",
              outcome: "access_denied",
              repository: "containers",
              resource: "platform/api",
              requestId: "req-2",
            },
            {
              occurredAt: "2026-10-01T07:00:00Z",
              operation: "future.operation",
              outcome: "future_outcome",
              requestId: "req-3",
            },
          ]}
        />
      </PreferencesProvider>,
    );

    const [granted, denied, unknown] = screen.getAllByRole("listitem");
    expect(granted).not.toHaveTextContent("repository.grants.upsert");
    expect(granted).not.toHaveTextContent("resolved");
    expect(within(granted).getByText("maven-releases")).toBeInTheDocument();
    expect(granted).toHaveTextContent("user:alice");

    expect(denied).toHaveTextContent("读取（GET）");
    expect(denied).toHaveTextContent("containers/platform/api");
    expect(denied).toHaveTextContent("访问被拒");
    expect(denied).toHaveAttribute("data-denied", "true");

    // Codes a newer backend writes degrade to the raw code, never a blank.
    expect(unknown).toHaveTextContent("future.operation");
    expect(unknown).toHaveTextContent("future_outcome");
    expect(unknown).toHaveTextContent("匿名");
  });
});
