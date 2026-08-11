import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { getArtifactQuarantine, replaceArtifactQuarantine } from "../client";
import type { ArtifactQuarantine } from "../client";
import { PreferencesProvider } from "../lib/preferences";
import { ArtifactQuarantinePanel } from "./ArtifactQuarantinePanel";

vi.mock("../client", () => ({
  getArtifactQuarantine: vi.fn(),
  replaceArtifactQuarantine: vi.fn(),
}));

const mockGetQuarantine = vi.mocked(getArtifactQuarantine);
const mockReplaceQuarantine = vi.mocked(replaceArtifactQuarantine);
const digest = `sha256:${"a".repeat(64)}`;

function quarantine(
  overrides: Partial<ArtifactQuarantine> = {},
): ArtifactQuarantine {
  return {
    repositoryId: "repo-1",
    format: "raw",
    coordinate: "releases/widget.bin",
    digest,
    state: "quarantined",
    reason: "critical vulnerability under investigation",
    version: "1",
    updatedBy: "alice",
    updatedAt: "2026-08-11T08:00:00Z",
    quarantinedAt: "2026-08-11T08:00:00Z",
    ...overrides,
  };
}

function renderPanel(canManage = true) {
  return render(
    <PreferencesProvider>
      <ArtifactQuarantinePanel
        repositoryId="repo-1"
        coordinate="releases/widget.bin"
        digest={digest}
        canManage={canManage}
      />
    </PreferencesProvider>,
  );
}

afterEach(() => {
  cleanup();
  localStorage.clear();
  vi.clearAllMocks();
});

describe("ArtifactQuarantinePanel", () => {
  it("shows active state read-only without management permission", async () => {
    mockGetQuarantine.mockResolvedValue({ data: quarantine() } as never);
    renderPanel(false);

    expect(await screen.findByText("制品已隔离")).toBeInTheDocument();
    expect(mockGetQuarantine).toHaveBeenCalledOnce();
    expect(
      screen.queryByRole("button", { name: /解\s*除\s*隔\s*离/ }),
    ).not.toBeInTheDocument();
  });

  it("treats 404 as unquarantined and creates the first record at version zero", async () => {
    const user = userEvent.setup();
    const created = quarantine();
    mockGetQuarantine
      .mockResolvedValueOnce({
        error: { status: 404, code: "not_found" },
      } as never)
      .mockResolvedValueOnce({ data: created } as never);
    mockReplaceQuarantine.mockResolvedValue({ data: created } as never);

    renderPanel();

    await user.click(
      await screen.findByRole("button", { name: /隔\s*离\s*制\s*品/ }),
    );
    const confirm = screen.getByRole("button", { name: /确\s*认\s*隔\s*离/ });
    expect(confirm).toBeDisabled();

    await user.type(screen.getByLabelText("隔离原因"), "  critical CVE  ");
    await user.click(confirm);

    await waitFor(() =>
      expect(mockReplaceQuarantine).toHaveBeenCalledWith({
        path: { repositoryId: "repo-1" },
        query: { coordinate: "releases/widget.bin", digest },
        headers: { "If-Match": "0" },
        body: { state: "quarantined", reason: "critical CVE" },
      }),
    );
    await waitFor(() => expect(mockGetQuarantine).toHaveBeenCalledTimes(2));
    expect(await screen.findByText("制品已隔离")).toBeInTheDocument();
  });

  it("uses the current version and keeps transition errors inside the modal", async () => {
    const user = userEvent.setup();
    mockGetQuarantine.mockResolvedValue({
      data: quarantine({ version: "7" }),
    } as never);
    mockReplaceQuarantine.mockResolvedValue({
      error: {
        status: 412,
        code: "version_conflict",
        message: "quarantine version changed",
      },
    } as never);

    renderPanel();

    expect(await screen.findByText("制品已隔离")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /解\s*除\s*隔\s*离/ }));
    await user.type(screen.getByLabelText("解除原因"), "review completed");
    await user.click(screen.getByRole("button", { name: /确\s*认\s*解\s*除/ }));

    await waitFor(() =>
      expect(mockReplaceQuarantine).toHaveBeenCalledWith(
        expect.objectContaining({
          headers: { "If-Match": "7" },
          body: { state: "released", reason: "review completed" },
        }),
      ),
    );
    expect(await screen.findByText("请求出错")).toBeInTheDocument();
    expect(screen.getByText("quarantine version changed")).toBeInTheDocument();
    expect(screen.getByRole("dialog")).toBeInTheDocument();
  });
});
