import {
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Repository } from "../client";
import { PreferencesProvider } from "../lib/preferences";
import { RawUploadDialog } from "./RawUploadDialog";

vi.mock("../lib/auth", () => ({
  useAuth: () => ({ token: "test-token" }),
}));

const repository: Repository = {
  id: "11111111-1111-4111-8111-111111111111",
  name: "raw-releases",
  format: "raw",
  type: "hosted",
  allowedHosts: [],
  anonymousRead: false,
  mavenStrictPublication: false,
  state: "active",
  version: "1",
};

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("RawUploadDialog", () => {
  it("uploads Unicode file names using the server's canonical Raw path encoding", async () => {
    const user = userEvent.setup();
    const fetchMock = vi
      .fn()
      .mockResolvedValue(new Response(null, { status: 201 }));
    vi.stubGlobal("fetch", fetchMock);
    render(
      <PreferencesProvider>
        <RawUploadDialog repo={repository} onUploaded={vi.fn()} />
      </PreferencesProvider>,
    );

    await user.click(screen.getByRole("button", { name: /上传$/ }));
    const dialog = screen.getByRole("dialog");
    const fileInput =
      document.querySelector<HTMLInputElement>('input[type="file"]');
    expect(fileInput).not.toBeNull();
    const fileName = "ChatGPT Image 2026年8月19日 13_56_07 (1).png";
    const file = new File(["image"], fileName, { type: "image/png" });
    await user.upload(fileInput!, file);
    await user.click(within(dialog).getByRole("button", { name: /上\s*传/ }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledOnce());
    expect(fetchMock).toHaveBeenCalledWith(
      "/raw/raw-releases/ChatGPT%20Image%202026%E5%B9%B48%E6%9C%8819%E6%97%A5%2013_56_07%20%281%29.png",
      expect.objectContaining({ method: "PUT", body: file }),
    );
  });

  it("explains invalid target paths before sending the upload", async () => {
    const user = userEvent.setup();
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    render(
      <PreferencesProvider>
        <RawUploadDialog repo={repository} onUploaded={vi.fn()} />
      </PreferencesProvider>,
    );

    await user.click(screen.getByRole("button", { name: /上传$/ }));
    const dialog = screen.getByRole("dialog");
    const fileInput =
      document.querySelector<HTMLInputElement>('input[type="file"]');
    expect(fileInput).not.toBeNull();
    await user.upload(fileInput!, new File(["image"], "image.png"));
    await user.clear(within(dialog).getByRole("textbox", { name: "目标路径" }));
    await user.type(
      within(dialog).getByRole("textbox", { name: "目标路径" }),
      "releases/../image.png",
    );
    await user.click(within(dialog).getByRole("button", { name: /上\s*传/ }));

    expect(fetchMock).not.toHaveBeenCalled();
    expect(
      await within(dialog).findByText(/目标路径无效.*不能使用.*\.\..*路径段/),
    ).toBeInTheDocument();
  });

  it("rejects an absolute-looking path instead of silently removing its leading slash", async () => {
    const user = userEvent.setup();
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    render(
      <PreferencesProvider>
        <RawUploadDialog repo={repository} onUploaded={vi.fn()} />
      </PreferencesProvider>,
    );

    await user.click(screen.getByRole("button", { name: /上传$/ }));
    const dialog = screen.getByRole("dialog");
    const fileInput =
      document.querySelector<HTMLInputElement>('input[type="file"]');
    await user.upload(fileInput!, new File(["image"], "image.png"));
    const pathInput = within(dialog).getByRole("textbox", { name: "目标路径" });
    await user.clear(pathInput);
    await user.type(pathInput, "/releases/image.png");
    await user.click(within(dialog).getByRole("button", { name: /上\s*传/ }));

    expect(fetchMock).not.toHaveBeenCalled();
    expect(
      await within(dialog).findByText(/目标路径必须相对于仓库根/),
    ).toBeInTheDocument();
  });

  it("preserves intentional leading and trailing spaces in a relative path", async () => {
    const user = userEvent.setup();
    const fetchMock = vi
      .fn()
      .mockResolvedValue(new Response(null, { status: 201 }));
    vi.stubGlobal("fetch", fetchMock);
    render(
      <PreferencesProvider>
        <RawUploadDialog repo={repository} onUploaded={vi.fn()} />
      </PreferencesProvider>,
    );

    await user.click(screen.getByRole("button", { name: /上传$/ }));
    const dialog = screen.getByRole("dialog");
    const fileInput =
      document.querySelector<HTMLInputElement>('input[type="file"]');
    const file = new File(["image"], "image.png");
    await user.upload(fileInput!, file);
    const pathInput = within(dialog).getByRole("textbox", { name: "目标路径" });
    await user.clear(pathInput);
    await user.type(pathInput, " release/image.png ");
    await user.click(within(dialog).getByRole("button", { name: /上\s*传/ }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledOnce());
    expect(fetchMock).toHaveBeenCalledWith(
      "/raw/raw-releases/%20release/image.png%20",
      expect.objectContaining({ method: "PUT", body: file }),
    );
  });

  it("translates a server Raw path rejection into an actionable message", async () => {
    const user = userEvent.setup();
    const fetchMock = vi
      .fn()
      .mockResolvedValue(new Response("invalid raw path\n", { status: 400 }));
    vi.stubGlobal("fetch", fetchMock);
    render(
      <PreferencesProvider>
        <RawUploadDialog repo={repository} onUploaded={vi.fn()} />
      </PreferencesProvider>,
    );

    await user.click(screen.getByRole("button", { name: /上传$/ }));
    const dialog = screen.getByRole("dialog");
    const fileInput =
      document.querySelector<HTMLInputElement>('input[type="file"]');
    expect(fileInput).not.toBeNull();
    await user.upload(fileInput!, new File(["image"], "image.png"));
    await user.click(within(dialog).getByRole("button", { name: /上\s*传/ }));

    expect(
      await within(dialog).findByText(
        /上传失败.*目标路径不符合 Raw 路径规则.*支持中文、空格和括号/,
      ),
    ).toBeInTheDocument();
    expect(within(dialog).getByText("Raw 文件上传失败")).toBeInTheDocument();
    expect(
      within(dialog).queryByText(/invalid raw path/),
    ).not.toBeInTheDocument();
  });
});
