import { expect, test } from "@playwright/test";
import { authenticateAsAdmin } from "./support/auth";

test("OCI manifest without image descriptors is not shown as 0 B", async ({
  page,
}) => {
  await authenticateAsAdmin(page);
  await page.route("**/api/v2/repositories?pageSize=1", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ items: [] }),
    }),
  );

  await page.route("**/api/v2/artifact-search**", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        items: [
          {
            repositoryId: "repo-oci",
            repositoryName: "docker-images",
            format: "oci",
            coordinate: "demo/api",
            digest: "sha256:" + "a".repeat(64),
            size: 179,
            createdAt: "2026-08-05T00:00:00Z",
          },
        ],
        searchedRepositories: 1,
      }),
    }),
  );
  await page.route("**/api/v2/repositories/repo-oci/oci/manifests**", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        items: [
          {
            digest: "sha256:" + "a".repeat(64),
            mediaType: "application/vnd.oci.image.manifest.v1+json",
            size: 179,
            tags: ["1.0.0"],
          },
        ],
      }),
    }),
  );
  await page.route("**/auth/token", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ token: "mock-registry-token" }),
    }),
  );
  await page.route("**/v2/docker-images/demo/api/manifests/1.0.0", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/vnd.oci.image.manifest.v1+json",
      body: JSON.stringify({
        schemaVersion: 2,
        mediaType: "application/vnd.oci.image.manifest.v1+json",
        annotations: { "org.opencontainers.image.version": "1.0.0" },
      }),
    }),
  );

  await page.goto("/search?q=demo%2Fapi");
  await expect(
    page.getByText("demo/api", { exact: true }).first(),
  ).toBeVisible();
  await page.getByRole("button", { name: "展开 demo/api" }).click();

  await expect(page.getByText("镜像大小", { exact: true })).toBeVisible();
  await expect(page.getByText("无层数据", { exact: true })).toBeVisible();
  await expect(page.getByText("0 B", { exact: true })).not.toBeVisible();
});
