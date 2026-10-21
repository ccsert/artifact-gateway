import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import type { FormatProfile } from "../../client";
import { PreferencesProvider } from "../../lib/preferences";
import { OCIDiscoveryNotice } from "./OCIDiscoveryNotice";

const profiles = vi.hoisted(() => vi.fn());
vi.mock("../../features/repository/repositoryQueries", () => ({
  useFormatProfiles: profiles,
}));

const profile: FormatProfile = {
  format: "oci",
  repositoryTypes: ["hosted", "proxy"],
  groupSupported: true,
  anonymousRead: true,
  hostedOperations: ["read", "publish"],
  proxyOperations: ["read"],
  ociDiscovery: {
    catalog: true,
    referrers: true,
    tagFallback: false,
    signatureVerification: false,
  },
};

afterEach(() => {
  cleanup();
  profiles.mockReset();
});

it("explains cache coverage, source authority, and signer trust from advertised API support", () => {
  profiles.mockReturnValue({ data: [profile] });
  render(
    <PreferencesProvider>
      <OCIDiscoveryNotice />
    </PreferencesProvider>,
  );
  const notice = screen.getByRole("region", { name: "OCI 目录与关联制品" });
  expect(notice).toHaveTextContent("缓存列表不代表完整库存");
  expect(notice).toHaveTextContent("Group 成员调整后仍保持原来源");
  expect(notice).toHaveTextContent("尚未验证签名者身份与信任策略");
  expect(notice).toHaveTextContent("不使用标签回退");
});

it.each([undefined, [], [{ ...profile, ociDiscovery: undefined }]])(
  "does not invent discovery capabilities while the server profile is absent: %j",
  (data) => {
    profiles.mockReturnValue({ data });
    const { container } = render(
      <PreferencesProvider>
        <OCIDiscoveryNotice />
      </PreferencesProvider>,
    );
    expect(container).toBeEmptyDOMElement();
  },
);
