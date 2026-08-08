import { expect, test, type Page } from "@playwright/test";
import { authenticateAsAdmin } from "./support/auth";

const ids = {
  maven: "10000000-0000-0000-0000-000000000001",
  oci: "10000000-0000-0000-0000-000000000002",
  conan: "10000000-0000-0000-0000-000000000003",
  managedMaven: "10000000-0000-0000-0000-000000000004",
};

async function mockCatalog(
  page: Page,
  repository: { id: string; name: string; format: "maven" | "oci" | "conan" },
) {
  await page.route("**/api/v2/public/repositories", (route) =>
    route.fulfill({
      json: { enabled: true, items: [{ ...repository, type: "hosted" }] },
    }),
  );
}

async function chooseVersion(page: Page, search: string) {
  // Ant Design hides the placeholder once a value is selected; the first
  // combobox in each expanded row is the primary version selector.
  const selector = page.getByRole("combobox").first();
  await selector.click();
  await selector.fill(search);
  await selector.press("ArrowDown");
  await selector.press("Enter");
}

async function selectedArtifact(page: Page) {
  return new URL(page.url()).searchParams.get("artifact");
}

test("Maven versions stay grouped and selecting one updates the version deep link", async ({
  page,
}) => {
  await mockCatalog(page, {
    id: ids.maven,
    name: "maven-public",
    format: "maven",
  });
  await page.route(
    `**/api/v2/repositories/${ids.maven}/artifact-search**`,
    (route) =>
      route.fulfill({
        json: {
          items: [
            {
              coordinate: "com.example:demo:1.0.0",
              digest: `sha256:${"a".repeat(64)}`,
              createdAt: "2026-01-01T00:00:00Z",
              publisher: "ci",
            },
            {
              coordinate: "com.example:demo:2.0.0",
              digest: `sha256:${"b".repeat(64)}`,
              createdAt: "2026-02-01T00:00:00Z",
              publisher: "ci",
            },
          ],
        },
      }),
  );

  await page.goto(
    `/browse?repository=${ids.maven}&artifact=${encodeURIComponent("com.example:demo:2.0.0")}`,
  );
  await expect(
    page.getByText("com.example:demo", { exact: true }),
  ).toBeVisible();
  await expect(page.getByText(/2 个版本/)).toBeVisible();
  await chooseVersion(page, "1.0.0");
  await expect
    .poll(() => selectedArtifact(page))
    .toBe("com.example:demo:1.0.0");
  await expect(
    page.getByText("com.example:demo:1.0.0", { exact: true }),
  ).toBeVisible();
});

test("managed Maven deep links scan later pages for the exact snapshot build", async ({
  page,
}) => {
  const coordinate = "com.example:deep-link:1.0-SNAPSHOT";
  let laterPageRequests = 0;
  await authenticateAsAdmin(page);
  await page.route("**/api/v2/repositories?**", (route) => {
    const url = new URL(route.request().url());
    if (url.pathname.endsWith("/repositories")) {
      return route.fulfill({ json: { items: [] } });
    }
    return route.continue();
  });
  await page.route(`**/api/v2/repositories/${ids.managedMaven}**`, (route) => {
    const url = new URL(route.request().url());
    const path = url.pathname;
    if (path.endsWith("/artifact-search")) {
      const pageToken = url.searchParams.get("pageToken");
      if (pageToken) laterPageRequests += 1;
      const builds = pageToken
        ? [51]
        : Array.from({ length: 50 }, (_, index) => index + 1);
      return route.fulfill({
        json: {
          items: builds.map((build) => ({
            coordinate,
            buildNumber: build,
            digest: `sha256:${(build === 51 ? "f" : "a").repeat(64)}`,
            createdAt: `2026-01-${String(Math.min(build, 28)).padStart(2, "0")}T00:00:00Z`,
            publisher: "ci",
          })),
          nextPageToken: pageToken ? undefined : "snapshot-page-2",
        },
      });
    }
    if (path.endsWith("/maven/coordinates")) {
      return route.fulfill({
        json: {
          items: [
            {
              coordinate,
              buildNumber: 51,
              digest: `sha256:${"f".repeat(64)}`,
              createdAt: "2026-02-01T00:00:00Z",
              publisher: "ci",
            },
          ],
        },
      });
    }
    if (path.endsWith("/capabilities")) {
      return route.fulfill({
        json: {
          format: "maven",
          type: "hosted",
          operations: ["read", "browse", "delete"],
        },
      });
    }
    if (path.endsWith("/effective-access")) {
      const allowed = {
        allowed: true,
        source: "administrator",
        reason: "administrator",
      };
      return route.fulfill({
        json: {
          actor: "admin",
          repository: {
            id: ids.managedMaven,
            name: "managed-maven",
            format: "maven",
            type: "hosted",
            state: "active",
          },
          anonymousRead: {
            allowed: false,
            source: "repository_policy",
            reason: "repository_anonymous_read_disabled",
          },
          permissions: { read: allowed, write: allowed, admin: allowed },
        },
      });
    }
    if (path.endsWith("/capacity")) {
      return route.fulfill({
        json: {
          repositoryId: ids.managedMaven,
          format: "maven",
          usedBytes: 0,
          objectCount: 51,
          quotaBytes: 0,
        },
      });
    }
    return route.fulfill({
      json: {
        id: ids.managedMaven,
        name: "managed-maven",
        format: "maven",
        type: "hosted",
        anonymousRead: false,
        state: "active",
        version: "1",
      },
    });
  });

  await page.goto(
    `/repositories/${ids.managedMaven}?artifact=${encodeURIComponent(coordinate)}&build=51`,
  );
  await expect(
    page.getByText("1.0-SNAPSHOT #51", { exact: true }),
  ).toBeVisible();
  expect(laterPageRequests).toBeGreaterThan(0);
});

test("OCI tag selection remains stable after manifest metadata finishes loading", async ({
  page,
}) => {
  await mockCatalog(page, { id: ids.oci, name: "oci-public", format: "oci" });
  await page.route(
    `**/api/v2/repositories/${ids.oci}/artifact-search**`,
    (route) =>
      route.fulfill({ json: { items: [{ coordinate: "library/postgres" }] } }),
  );
  await page.route(
    `**/api/v2/repositories/${ids.oci}/oci/manifests**`,
    (route) =>
      route.fulfill({
        json: {
          items: [
            {
              digest: `sha256:${"a".repeat(64)}`,
              mediaType: "application/vnd.oci.image.manifest.v1+json",
              size: 114,
              tags: ["14"],
            },
            {
              digest: `sha256:${"b".repeat(64)}`,
              mediaType: "application/vnd.oci.image.manifest.v1+json",
              size: 115,
              tags: ["15"],
            },
            {
              digest: `sha256:${"c".repeat(64)}`,
              mediaType: "application/vnd.oci.image.manifest.v1+json",
              size: 116,
              tags: ["16"],
            },
          ],
        },
      }),
  );
  await page.route(/\/v2\/.+\/tags\/list(?:\?|$)/, (route) =>
    route.fulfill({
      json: { name: "library/postgres", tags: ["14", "15", "16"] },
    }),
  );
  await page.route(/\/v2\/.+\/manifests\//, async (route) => {
    const tag = decodeURIComponent(
      new URL(route.request().url()).pathname.split("/").at(-1) ?? "14",
    );
    const digestCharacter = tag === "16" ? "c" : tag === "15" ? "b" : "a";
    await route.fulfill({
      headers: {
        "Content-Type": "application/vnd.oci.image.manifest.v1+json",
        "Docker-Content-Digest": `sha256:${digestCharacter.repeat(64)}`,
      },
      json: {
        schemaVersion: 2,
        config: { digest: `sha256:${"d".repeat(64)}`, size: 100 },
        layers: [{ size: Number(tag) }],
      },
    });
  });
  await page.route(/\/v2\/.+\/blobs\//, (route) =>
    route.fulfill({
      json: {
        created: "2026-03-01T00:00:00Z",
        config: {
          Labels: { "org.opencontainers.image.authors": "database-team" },
        },
      },
    }),
  );

  await page.goto(
    `/browse?repository=${ids.oci}&artifact=library%2Fpostgres&tag=15`,
  );
  await expect(
    page.getByRole("cell", { name: "library/postgres", exact: true }),
  ).toBeVisible();
  await chooseVersion(page, "16");
  await expect
    .poll(() => new URL(page.url()).searchParams.get("tag"))
    .toBe("16");
  await expect(
    page.getByText(`sha256:${"c".repeat(64)}`, { exact: true }),
  ).toBeVisible();
  await page.waitForTimeout(200);
  await expect
    .poll(() => new URL(page.url()).searchParams.get("tag"))
    .toBe("16");
});

test("Conan package versions are grouped, searchable, and collapsible", async ({
  page,
}) => {
  await mockCatalog(page, {
    id: ids.conan,
    name: "conan-public",
    format: "conan",
  });
  await page.route(
    `**/api/v2/repositories/${ids.conan}/artifact-search**`,
    (route) =>
      route.fulfill({
        json: {
          items: [
            { coordinate: "demo/1.0/user/stable", publisher: "builder" },
            { coordinate: "demo/2.0/user/stable", publisher: "builder" },
          ],
        },
      }),
  );
  await page.route(
    `**/api/v2/repositories/${ids.conan}/conan/recipe-revisions**`,
    (route) => {
      const reference =
        new URL(route.request().url()).searchParams.get("reference") ?? "";
      const revision = reference.includes("/1.0/")
        ? "revision-one"
        : "revision-two";
      return route.fulfill({
        json: {
          items: [
            {
              reference,
              revision,
              digest: `sha256:${(reference.includes("/1.0/") ? "a" : "b").repeat(64)}`,
              state: "visible",
              createdAt: "2026-04-01T00:00:00Z",
            },
          ],
        },
      });
    },
  );

  await page.goto(
    `/browse?repository=${ids.conan}&artifact=${encodeURIComponent("demo/2.0/user/stable")}&revision=revision-two`,
  );
  const packageRow = page
    .getByRole("row")
    .filter({ hasText: "demo/user/stable" })
    .first();
  await expect(packageRow).toBeVisible();
  await expect(packageRow.getByRole("cell").nth(2)).toHaveText("2");
  await chooseVersion(page, "1.0");
  await expect.poll(() => selectedArtifact(page)).toBe("demo/1.0/user/stable");
  await expect(
    page.getByText("revision-one", { exact: true }).first(),
  ).toBeVisible();
  await page.getByRole("button", { name: "收起" }).click();
  await expect(page.getByRole("combobox")).toHaveCount(0);
});
