import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { PreferencesProvider } from "../../lib/preferences";
import { SectionBoundary } from "./SectionBoundary";

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
});
