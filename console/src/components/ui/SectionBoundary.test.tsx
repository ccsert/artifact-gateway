import { act, cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { PreferencesProvider } from "../../lib/preferences";
import { SectionBoundary } from "./SectionBoundary";
import { useDashboardResource } from "../../features/dashboard/useDashboardResource";

let broken = true;

function Flaky() {
  if (broken) throw new Error("panel exploded");
  return <p>panel content</p>;
}

function Page({ resetKey = "a", onReset = () => {} }) {
  return (
    <PreferencesProvider>
      <h1>page title</h1>
      <SectionBoundary
        title="面板不可用"
        resetKeys={[resetKey]}
        onReset={onReset}
      >
        <Flaky />
      </SectionBoundary>
      <p>sibling section</p>
    </PreferencesProvider>
  );
}

afterEach(() => {
  cleanup();
  broken = true;
  vi.restoreAllMocks();
});

describe("SectionBoundary", () => {
  it("recovers from refreshed data after one Retry while its sibling remains usable", async () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    let completeRefresh!: (value: string) => void;
    const read = vi
      .fn<() => Promise<string>>()
      .mockResolvedValueOnce("broken")
      .mockImplementation(
        () =>
          new Promise((resolve) => {
            completeRefresh = resolve;
          }),
      );
    function Content({ value }: { value: string }) {
      if (value === "broken") throw new Error("malformed answer");
      return <p>Recovered panel</p>;
    }
    function ResourcePage() {
      const resource = useDashboardResource(read);
      return (
        <PreferencesProvider>
          <SectionBoundary onReset={resource.reload}>
            {resource.data ? (
              <Content value={resource.data} />
            ) : (
              <p>Loading panel</p>
            )}
          </SectionBoundary>
          <button>Sibling action</button>
        </PreferencesProvider>
      );
    }
    const user = userEvent.setup();
    render(<ResourcePage />);
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "malformed answer",
    );
    await user.click(screen.getByRole("button", { name: /重试/ }));
    expect(
      screen.getByRole("button", { name: "Sibling action" }),
    ).toBeEnabled();
    await act(async () => {
      completeRefresh("valid");
    });
    expect(await screen.findByText("Recovered panel")).toBeVisible();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(read).toHaveBeenCalledTimes(2);
  });
  it("keeps the rest of the page when one section fails, and retries it", async () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    const onReset = vi.fn();
    render(<Page onReset={onReset} />);

    expect(screen.getByText("page title")).toBeInTheDocument();
    expect(screen.getByText("sibling section")).toBeInTheDocument();
    expect(screen.getByRole("alert")).toHaveTextContent("面板不可用");
    expect(screen.getByRole("alert")).toHaveTextContent("panel exploded");

    broken = false;
    await userEvent
      .setup()
      .click(screen.getByRole("button", { name: /重\s*试/ }));
    expect(onReset).toHaveBeenCalledOnce();
    expect(screen.getByText("panel content")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("clears a failure when its reset keys change", () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    const { rerender } = render(<Page resetKey="a" />);
    expect(screen.getByRole("alert")).toBeInTheDocument();

    broken = false;
    rerender(<Page resetKey="b" />);
    expect(screen.getByText("panel content")).toBeInTheDocument();
  });

  it("ignores a previous section's late Retry failure after navigation", async () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    let reject!: (error: Error) => void;
    const pending = new Promise<void>((_, fail) => {
      reject = fail;
    });
    const { rerender } = render(<Page resetKey="a" onReset={() => pending} />);
    await userEvent.setup().click(screen.getByRole("button", { name: /重试/ }));
    broken = false;
    rerender(<Page resetKey="b" />);
    expect(screen.getByText("panel content")).toBeVisible();
    await act(async () => {
      reject(new Error("previous section failed"));
    });
    expect(screen.getByText("panel content")).toBeVisible();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });
});
