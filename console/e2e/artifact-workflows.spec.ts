import {
  expect,
  test,
  type APIRequestContext,
  type Page,
} from "@playwright/test";

const token = process.env.GATEWAY_ADMIN_TOKEN;

test.skip(
  !token,
  "GATEWAY_ADMIN_TOKEN is required for the artifact workflow browser gate",
);
test.describe.configure({ mode: "serial" });

const suffix = `${Date.now()}-${Math.random().toString(16).slice(2, 8)}`;
const repositoryName = `e2e-raw-${suffix}`;
const artifactPath = `packages/${suffix}/demo.txt`;
const apiKeyName = `e2e-expiring-${suffix}`;
let repositoryId = "";
let apiKeyId = "";
let anonymousPolicyWasEnabled = true;

const authorization = () => ({ Authorization: `Bearer ${token!}` });

async function expectOK(
  response: Awaited<ReturnType<APIRequestContext["get"]>>,
  action: string,
) {
  expect(
    response.ok(),
    `${action}: ${response.status()} ${await response.text()}`,
  ).toBeTruthy();
}

async function authenticate(page: Page) {
  await page.addInitScript(
    ([accessToken, role]) => {
      localStorage.setItem("ag.console.token", accessToken);
      localStorage.setItem("ag.console.role", role);
    },
    [token!, "admin"],
  );
}

test.beforeAll(async ({ request }) => {
  const create = await request.post("/api/v2/repositories", {
    headers: { ...authorization(), "Idempotency-Key": `e2e-create-${suffix}` },
    data: {
      name: repositoryName,
      format: "raw",
      type: "hosted",
      anonymousRead: true,
    },
  });
  await expectOK(create, "create Raw repository");
  repositoryId = (await create.json()).id as string;

  const currentPolicy = await request.get("/api/v2/anonymous-access-policy", {
    headers: authorization(),
  });
  await expectOK(currentPolicy, "read anonymous policy");
  const policy = (await currentPolicy.json()) as {
    version: string;
    enabled: boolean;
  };
  anonymousPolicyWasEnabled = policy.enabled;
  if (!policy.enabled) {
    const enable = await request.put("/api/v2/anonymous-access-policy", {
      headers: { ...authorization(), "If-Match": policy.version },
      data: { version: policy.version, enabled: true },
    });
    await expectOK(enable, "enable anonymous policy");
  }

  const publish = await request.put(`/raw/${repositoryName}/${artifactPath}`, {
    headers: { ...authorization(), "Content-Type": "text/plain" },
    data: "artifact gateway e2e payload",
  });
  await expectOK(publish, "publish Raw artifact");
});

test.afterAll(async ({ request }) => {
  if (apiKeyId) {
    await expectOK(
      await request.delete(`/api/v2/api-keys/${apiKeyId}`, {
        headers: authorization(),
      }),
      "revoke test API key",
    );
  }
  if (repositoryId) {
    const removeArtifact = await request.delete(
      `/raw/${repositoryName}/${artifactPath}`,
      { headers: authorization() },
    );
    if (removeArtifact.status() !== 404) {
      await expectOK(removeArtifact, "remove test Raw artifact");
    }
    await expectOK(
      await request.delete(`/api/v2/repositories/${repositoryId}`, {
        headers: authorization(),
      }),
      "delete test repository",
    );
  }
  if (!anonymousPolicyWasEnabled) {
    const current = await request.get("/api/v2/anonymous-access-policy", {
      headers: authorization(),
    });
    await expectOK(current, "read anonymous policy for cleanup");
    const policy = (await current.json()) as { version: string };
    await expectOK(
      await request.put("/api/v2/anonymous-access-policy", {
        headers: { ...authorization(), "If-Match": policy.version },
        data: { version: policy.version, enabled: false },
      }),
      "restore anonymous policy",
    );
  }
});

test("anonymous browsing and global-search deep links reach the exact artifact", async ({
  page,
}) => {
  await page.goto(
    `/browse?repository=${repositoryId}&artifact=${encodeURIComponent(artifactPath)}`,
  );
  await expect(page.getByRole("heading", { name: "公开制品" })).toBeVisible();
  await expect(
    page.getByRole("row").filter({ hasText: artifactPath }).first(),
  ).toBeVisible();

  await authenticate(page);
  await page.goto(`/search?q=${encodeURIComponent(`packages/${suffix}`)}`);
  await expect(page.getByText(artifactPath, { exact: true })).toBeVisible();
  await page.getByRole("link", { name: repositoryName, exact: true }).click();
  await expect(page).toHaveURL(
    new RegExp(`/repositories/${repositoryId}\\?artifact=`),
  );
  await expect(page.getByText(artifactPath, { exact: true })).toBeVisible();
});

test("retention dry-run and lifecycle jobs are visible in the repository console", async ({
  request,
  page,
}) => {
  const current = await request.get(
    `/api/v2/repositories/${repositoryId}/retention-policy`,
    { headers: authorization() },
  );
  await expectOK(current, "read Raw retention policy");
  const policy = (await current.json()) as {
    version: string;
    keepDays: number;
    minimumVersions: number;
    snapshotKeepDays?: number;
    maximumVersions?: number;
  };
  const enabled = await request.put(
    `/api/v2/repositories/${repositoryId}/retention-policy`,
    {
      headers: { ...authorization(), "If-Match": policy.version },
      data: {
        ...policy,
        enabled: true,
        keepDays: 1,
      },
    },
  );
  await expectOK(enabled, "enable Raw retention policy");
  const enabledPolicy = (await enabled.json()) as { version: string };

  await authenticate(page);
  await page.goto(`/repositories/${repositoryId}`);
  await page.getByRole("tab", { name: "保留策略" }).click();
  await expect(page.getByRole("switch")).toBeChecked();
  await page.getByRole("button", { name: "试运行" }).click();
  await expect(page.getByText("没有需要清理的路径资产")).toBeVisible();

  const execute = await request.post(
    `/api/v2/repositories/${repositoryId}/retention:execute`,
    {
      headers: {
        ...authorization(),
        "Idempotency-Key": `e2e-retention-${suffix}`,
        "If-Match": enabledPolicy.version,
      },
    },
  );
  await expectOK(execute, "enqueue Raw retention job");

  await page.getByRole("tab", { name: "生命周期任务" }).click();
  await expect(
    page.getByRole("row").filter({ hasText: "retention" }).first(),
  ).toBeVisible();
});

test("a deleted artifact can be restored from the repository tombstone tab", async ({
  request,
  page,
}) => {
  const remove = await request.delete(
    `/raw/${repositoryName}/${artifactPath}`,
    { headers: authorization() },
  );
  await expectOK(remove, "delete Raw artifact");

  await authenticate(page);
  await page.goto(`/repositories/${repositoryId}`);
  const tombstoneTab = page.getByRole("tab", { name: "墓碑", exact: true });
  await tombstoneTab.click();
  await expect(tombstoneTab).toHaveAttribute("aria-selected", "true");
  const tombstoneTable = page
    .getByRole("table")
    .filter({ has: page.getByRole("columnheader", { name: "坐标" }) });
  const row = tombstoneTable
    .getByRole("row")
    .filter({ hasText: artifactPath });
  await expect(row).toBeVisible();
  await row.getByRole("button", { name: /恢\s*复/ }).click();
  await page.getByRole("button", { name: /恢\s*复/ }).last().click();
  await expect(
    page.getByText(`已恢复 ${artifactPath}`, { exact: true }),
  ).toBeVisible();
  await expect(row).toHaveCount(0);

  const restored = await request.get(`/raw/${repositoryName}/${artifactPath}`);
  await expectOK(restored, "read restored Raw artifact anonymously");
  expect(await restored.text()).toBe("artifact gateway e2e payload");
});

test("expired API keys are clearly distinguished from active and revoked keys", async ({
  request,
  page,
}) => {
  const create = await request.post("/api/v2/api-keys", {
    headers: authorization(),
    data: {
      name: apiKeyName,
      roles: ["reader"],
      expiresAt: new Date(Date.now() + 1_500).toISOString(),
    },
  });
  await expectOK(create, "create short-lived API key");
  apiKeyId = ((await create.json()) as { id: string }).id;
  await new Promise((resolve) => setTimeout(resolve, 1_800));

  await authenticate(page);
  await page.goto("/keys");
  const row = page.getByRole("row").filter({ hasText: apiKeyName });
  await expect(row).toBeVisible();
  await expect(row.getByText("expired", { exact: true })).toBeVisible();
});
