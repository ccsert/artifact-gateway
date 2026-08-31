import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  ApiOutlined,
  KeyOutlined,
  PlusOutlined,
  ReloadOutlined,
  RobotOutlined,
  SafetyCertificateOutlined,
  StopOutlined,
} from "@ant-design/icons";
import { Alert, Button, Input, Select, Space, Table, Typography } from "antd";
import type { ColumnsType } from "antd/es/table";
import {
  createServiceAccount,
  createServiceAccountCredential,
  listServiceAccountCredentials,
  listServiceAccounts,
  revokeServiceAccountCredential,
  updateServiceAccount,
} from "../client";
import type {
  CreatedServiceAccountCredential,
  ServiceAccount,
  ServiceAccountCredential,
} from "../client";
import { Badge, StateBadge } from "../components/Badge";
import { CopyableValue, MetricStrip } from "../components/ConsolePrimitives";
import { EmptyState, ErrorBanner, Loading } from "../components/Feedback";
import { Card, CardHeader, Field, PageHeader } from "../components/Layout";
import { ConfirmDialog, Modal, useDisclosure } from "../components/Modal";
import { formatDate } from "../lib/format";
import { usePreferences } from "../lib/preferences";

const credentialState = (credential: ServiceAccountCredential) => {
  if (credential.revokedAt) return "revoked";
  if (
    credential.expiresAt &&
    new Date(credential.expiresAt).getTime() <= Date.now()
  )
    return "expired";
  return "active";
};

function TokenReveal({
  credential,
  onDone,
}: {
  credential: CreatedServiceAccountCredential;
  onDone: () => void;
}) {
  const { text } = usePreferences();
  return (
    <Modal
      open
      onClose={onDone}
      title={text(
        "凭据已签发：立即保存 Token",
        "Credential issued: save the token",
      )}
    >
      <div className="space-y-4">
        <Alert
          type="warning"
          showIcon
          title={text("明文只显示一次", "Plaintext is shown once")}
          description={text(
            "关闭后无法重新查看。请把 Token 写入 CI Secret 或外部密钥管理系统，不要提交到仓库。",
            "It cannot be viewed again. Put the token in a CI secret or external secret manager, never in source control.",
          )}
        />
        <div className="rounded-xl border border-zinc-700 bg-zinc-950 p-4">
          <Typography.Text
            className="block break-all font-mono text-xs"
            copyable={{
              text: credential.token,
              tooltips: [
                text("复制 Token", "Copy token"),
                text("已复制", "Copied"),
              ],
            }}
          >
            {credential.token}
          </Typography.Text>
        </div>
        <div className="rounded-lg border border-zinc-800 bg-zinc-900/50 px-3 py-2 text-xs text-zinc-500">
          {text("凭据名称", "Credential name")}: {credential.name} ·{" "}
          {text("到期", "Expires")}: {formatDate(credential.expiresAt)}
        </div>
        <div className="flex justify-end">
          <Button type="primary" onClick={onDone}>
            {text("我已安全保存", "I stored it securely")}
          </Button>
        </div>
      </div>
    </Modal>
  );
}

export function ServiceAccountsPage() {
  const { locale, text } = usePreferences();
  const createDialog = useDisclosure();
  const credentialDialog = useDisclosure();
  const [accounts, setAccounts] = useState<ServiceAccount[] | null>(null);
  const [nextAccountsPageToken, setNextAccountsPageToken] = useState("");
  const [loadingMoreAccounts, setLoadingMoreAccounts] = useState(false);
  const [selectedAccountId, setSelectedAccountId] = useState("");
  const [credentials, setCredentials] = useState<
    ServiceAccountCredential[] | null
  >(null);
  const [nextCredentialsPageToken, setNextCredentialsPageToken] = useState("");
  const [loadingMoreCredentials, setLoadingMoreCredentials] = useState(false);
  const credentialRequestId = useRef(0);
  const [error, setError] = useState<unknown>(null);
  const [credentialsError, setCredentialsError] = useState<unknown>(null);
  const [busy, setBusy] = useState(false);
  const [accountName, setAccountName] = useState("");
  const [accountDescription, setAccountDescription] = useState("");
  const [credentialName, setCredentialName] = useState("");
  const [validDays, setValidDays] = useState<30 | 90 | 180 | 365>(90);
  const [reveal, setReveal] = useState<CreatedServiceAccountCredential | null>(
    null,
  );
  const [credentialToRevoke, setCredentialToRevoke] =
    useState<ServiceAccountCredential | null>(null);
  const [accountStateChange, setAccountStateChange] =
    useState<ServiceAccount | null>(null);

  const selectedAccount = useMemo(
    () => accounts?.find((account) => account.id === selectedAccountId) ?? null,
    [accounts, selectedAccountId],
  );

  const loadAccounts = useCallback(
    async (pageToken?: string) => {
      setError(null);
      if (pageToken) setLoadingMoreAccounts(true);
      try {
        const { data, error: loadError } = await listServiceAccounts({
          query: { pageSize: 200, pageToken },
        });
        if (loadError || !data) {
          setError(
            loadError ??
              new Error(
                text("加载服务账号失败", "Failed to load service accounts"),
              ),
          );
          return;
        }
        setAccounts((current) =>
          pageToken && current ? [...current, ...data.items] : data.items,
        );
        setNextAccountsPageToken(data.nextPageToken ?? "");
        setSelectedAccountId((current) =>
          pageToken || data.items.some((account) => account.id === current)
            ? current
            : (data.items[0]?.id ?? ""),
        );
      } catch (nextError) {
        setError(nextError);
      } finally {
        if (pageToken) setLoadingMoreAccounts(false);
      }
    },
    [text],
  );

  const loadCredentials = useCallback(
    async (pageToken?: string) => {
      const requestId = ++credentialRequestId.current;
      if (!selectedAccountId) {
        setCredentials([]);
        setNextCredentialsPageToken("");
        setLoadingMoreCredentials(false);
        return;
      }
      setCredentialsError(null);
      if (pageToken) setLoadingMoreCredentials(true);
      else setLoadingMoreCredentials(false);
      try {
        const { data, error: loadError } = await listServiceAccountCredentials({
          path: { serviceAccountId: selectedAccountId },
          query: { pageSize: 200, pageToken },
        });
        if (requestId !== credentialRequestId.current) return;
        if (loadError || !data) {
          setCredentialsError(
            loadError ??
              new Error(text("加载凭据失败", "Failed to load credentials")),
          );
          return;
        }
        setCredentials((current) =>
          pageToken && current ? [...current, ...data.items] : data.items,
        );
        setNextCredentialsPageToken(data.nextPageToken ?? "");
      } catch (nextError) {
        if (requestId === credentialRequestId.current) {
          setCredentialsError(nextError);
        }
      } finally {
        if (requestId === credentialRequestId.current) {
          setLoadingMoreCredentials(false);
        }
      }
    },
    [selectedAccountId, text],
  );

  useEffect(() => {
    void loadAccounts();
  }, [loadAccounts]);

  useEffect(() => {
    setCredentials(null);
    setNextCredentialsPageToken("");
    void loadCredentials();
  }, [loadCredentials]);

  const submitAccount = async () => {
    setBusy(true);
    setError(null);
    try {
      const { data, error: createError } = await createServiceAccount({
        body: {
          name: accountName.trim(),
          description: accountDescription.trim() || undefined,
        },
      });
      if (createError || !data) {
        setError(createError ?? new Error(text("创建失败", "Creation failed")));
        return;
      }
      createDialog.hide();
      setAccountName("");
      setAccountDescription("");
      await loadAccounts();
      setSelectedAccountId(data.id);
    } catch (nextError) {
      setError(nextError);
    } finally {
      setBusy(false);
    }
  };

  const submitCredential = async () => {
    if (!selectedAccount) return;
    setBusy(true);
    setCredentialsError(null);
    try {
      const { data, error: createError } = await createServiceAccountCredential(
        {
          path: { serviceAccountId: selectedAccount.id },
          body: {
            name: credentialName.trim(),
            expiresAt: new Date(
              Date.now() + validDays * 86_400_000,
            ).toISOString(),
          },
        },
      );
      if (createError || !data) {
        setCredentialsError(
          createError ?? new Error(text("签发失败", "Issuance failed")),
        );
        return;
      }
      credentialDialog.hide();
      setCredentialName("");
      setValidDays(90);
      setReveal(data);
      await loadCredentials();
    } catch (nextError) {
      setCredentialsError(nextError);
    } finally {
      setBusy(false);
    }
  };

  const confirmAccountState = async () => {
    if (!accountStateChange) return;
    setBusy(true);
    const nextState =
      accountStateChange.state === "active" ? "disabled" : "active";
    try {
      const { error: updateError } = await updateServiceAccount({
        path: { serviceAccountId: accountStateChange.id },
        headers: { "If-Match": accountStateChange.version },
        body: { state: nextState },
      });
      if (updateError) {
        setError(updateError);
        return;
      }
      setAccountStateChange(null);
      await loadAccounts();
    } catch (nextError) {
      setError(nextError);
    } finally {
      setBusy(false);
    }
  };

  const confirmRevoke = async () => {
    if (!selectedAccount || !credentialToRevoke) return;
    setBusy(true);
    try {
      const { error: revokeError } = await revokeServiceAccountCredential({
        path: {
          serviceAccountId: selectedAccount.id,
          credentialId: credentialToRevoke.id,
        },
      });
      if (revokeError) {
        setCredentialsError(revokeError);
        return;
      }
      setCredentialToRevoke(null);
      await loadCredentials();
    } catch (nextError) {
      setCredentialsError(nextError);
    } finally {
      setBusy(false);
    }
  };

  const activeAccounts =
    accounts?.filter((account) => account.state === "active") ?? [];
  const activeCredentials =
    credentials?.filter(
      (credential) => credentialState(credential) === "active",
    ) ?? [];
  const columns: ColumnsType<ServiceAccountCredential> = [
    {
      title: text("凭据", "Credential"),
      dataIndex: "name",
      key: "name",
      render: (name: string) => (
        <div className="flex items-center gap-2">
          <KeyOutlined className="text-zinc-600" />
          <span className="font-medium text-zinc-200">{name}</span>
        </div>
      ),
    },
    {
      title: text("状态", "State"),
      key: "state",
      width: 120,
      render: (_value, credential) => (
        <StateBadge state={credentialState(credential)} />
      ),
    },
    {
      title: text("到期时间", "Expires"),
      dataIndex: "expiresAt",
      key: "expiresAt",
      width: 180,
      render: (value?: string) => (
        <span className="text-xs text-zinc-500">
          {formatDate(value, locale)}
        </span>
      ),
    },
    {
      title: text("最后使用", "Last used"),
      dataIndex: "lastUsedAt",
      key: "lastUsedAt",
      width: 180,
      render: (value?: string) => (
        <span className="text-xs text-zinc-500">
          {formatDate(value, locale)}
        </span>
      ),
    },
    {
      title: text("操作", "Actions"),
      key: "actions",
      align: "right",
      width: 92,
      render: (_value, credential) =>
        credentialState(credential) === "active" ? (
          <Button
            danger
            type="text"
            size="small"
            aria-label={text(
              `吊销 ${credential.name}`,
              `Revoke ${credential.name}`,
            )}
            onClick={() => setCredentialToRevoke(credential)}
          >
            {text("吊销", "Revoke")}
          </Button>
        ) : null,
    },
  ];

  return (
    <div className="ag-page-stack">
      <PageHeader
        title={text("服务账号", "Service accounts")}
        description={text(
          "为 Jenkins、CI 机器人和第三方应用提供稳定身份；凭据可以独立签发、轮换和吊销。",
          "Give Jenkins, CI robots, and third-party applications a stable identity while credentials rotate independently.",
        )}
        actions={
          <Button
            type="primary"
            icon={<PlusOutlined />}
            onClick={createDialog.show}
          >
            {text("新建服务账号", "New service account")}
          </Button>
        }
      />

      <Alert
        type="info"
        showIcon
        icon={<SafetyCertificateOutlined />}
        title={text(
          "机器身份与凭据分离",
          "Machine identity is separate from credentials",
        )}
        description={text(
          "Repository Grant 绑定稳定的 service-account:<id> 主体。轮换 Token 时无需修改仓库授权；禁用账号会一次性拒绝它的全部凭据。",
          "Repository grants bind to the stable service-account:<id> principal. Token rotation does not change grants, and disabling the account rejects every credential at once.",
        )}
      />

      <MetricStrip
        items={[
          {
            label: text("服务账号", "Service accounts"),
            value:
              accounts === null
                ? "—"
                : `${accounts.length}${nextAccountsPageToken ? "+" : ""}`,
            hint: text("稳定机器主体", "Stable machine principals"),
          },
          {
            label: text("启用", "Active"),
            value: `${activeAccounts.length}${nextAccountsPageToken ? "+" : ""}`,
            hint: text("可接受凭据认证", "May accept credential auth"),
            tone: "success",
          },
          {
            label: text("当前账号有效凭据", "Active credentials"),
            value: `${activeCredentials.length}${nextCredentialsPageToken ? "+" : ""}`,
            hint: text("支持零停机轮换", "Supports zero-downtime rotation"),
          },
        ]}
      />

      {error !== null && accounts !== null && (
        <ErrorBanner error={error} onRetry={loadAccounts} />
      )}
      {accounts === null ? (
        error !== null ? (
          <ErrorBanner error={error} onRetry={loadAccounts} />
        ) : (
          <Card bodyClassName="p-8">
            <Loading
              label={text("加载服务账号…", "Loading service accounts…")}
            />
          </Card>
        )
      ) : accounts.length === 0 ? (
        <Card bodyClassName="p-8">
          <EmptyState
            title={text("暂无服务账号", "No service accounts")}
            hint={text(
              "为第一个 CI 机器人创建稳定身份。",
              "Create a stable identity for your first CI robot.",
            )}
            action={
              <Button type="primary" onClick={createDialog.show}>
                {text("新建服务账号", "New service account")}
              </Button>
            }
          />
        </Card>
      ) : (
        <div className="ag-page-primary ag-service-account-workspace grid items-start gap-4 xl:grid-cols-[300px_minmax(0,1fr)]">
          <Card bodyClassName="p-2">
            <div className="px-2 pb-2 pt-1 text-xs font-medium uppercase tracking-wider text-zinc-600">
              {text("机器主体", "Machine principals")}
            </div>
            <div className="space-y-1">
              {accounts.map((account) => (
                <button
                  key={account.id}
                  type="button"
                  className={`w-full rounded-lg border px-3 py-3 text-left transition-colors ${
                    account.id === selectedAccountId
                      ? "border-[var(--ag-border-default)] bg-[var(--ag-navigation-selected-start)]"
                      : "border-transparent hover:border-[var(--ag-border-subtle)] hover:bg-[var(--ag-surface-hover)]"
                  }`}
                  onClick={() => setSelectedAccountId(account.id)}
                >
                  <div className="flex items-center justify-between gap-2">
                    <span className="truncate text-sm font-medium text-zinc-200">
                      {account.name}
                    </span>
                    <Badge
                      tone={account.state === "active" ? "success" : "neutral"}
                    >
                      {account.state}
                    </Badge>
                  </div>
                  <p className="mt-1 line-clamp-2 text-xs leading-5 text-zinc-600">
                    {account.description ||
                      text("未填写用途", "No purpose documented")}
                  </p>
                </button>
              ))}
            </div>
            {nextAccountsPageToken && (
              <div className="border-t border-zinc-800/70 px-2 pt-2">
                <Button
                  block
                  type="text"
                  loading={loadingMoreAccounts}
                  onClick={() => void loadAccounts(nextAccountsPageToken)}
                >
                  {text("加载更多账号", "Load more accounts")}
                </Button>
              </div>
            )}
          </Card>

          {selectedAccount && (
            <Card>
              <div className="border-b border-zinc-800/70 px-5 py-4">
                <div className="flex flex-wrap items-start justify-between gap-4">
                  <div className="flex min-w-0 items-start gap-3">
                    <div className="flex size-10 shrink-0 items-center justify-center rounded-xl border border-[var(--ag-border-default)] bg-[var(--ag-surface-hover)] text-[var(--ag-content-secondary)]">
                      <RobotOutlined />
                    </div>
                    <div className="min-w-0">
                      <div className="flex flex-wrap items-center gap-2">
                        <h2 className="text-base font-semibold text-zinc-100">
                          {selectedAccount.name}
                        </h2>
                        <Badge
                          tone={
                            selectedAccount.state === "active"
                              ? "success"
                              : "neutral"
                          }
                        >
                          {selectedAccount.state}
                        </Badge>
                      </div>
                      <p className="mt-1 text-xs text-zinc-500">
                        {selectedAccount.description ||
                          text("未填写用途", "No purpose documented")}
                      </p>
                    </div>
                  </div>
                  <Space wrap>
                    <Button
                      icon={<ReloadOutlined />}
                      onClick={() => void loadCredentials()}
                    >
                      {text("刷新", "Refresh")}
                    </Button>
                    <Button
                      danger={selectedAccount.state === "active"}
                      icon={
                        selectedAccount.state === "active" ? (
                          <StopOutlined />
                        ) : (
                          <SafetyCertificateOutlined />
                        )
                      }
                      onClick={() => setAccountStateChange(selectedAccount)}
                    >
                      {selectedAccount.state === "active"
                        ? text("禁用账号", "Disable account")
                        : text("重新启用", "Enable account")}
                    </Button>
                  </Space>
                </div>
                <div className="mt-4 rounded-lg border border-zinc-800 bg-zinc-950/50 px-3 py-2.5">
                  <div className="text-xs font-medium uppercase tracking-wider text-zinc-600">
                    {text("稳定授权主体", "Stable grant principal")}
                  </div>
                  <CopyableValue
                    value={`service-account:${selectedAccount.id}`}
                    className="mt-1 max-w-full text-xs text-zinc-300"
                  />
                </div>
              </div>
              <CardHeader
                title={text("可轮换凭据", "Rotating credentials")}
                extra={
                  <Button
                    type="primary"
                    icon={<ApiOutlined />}
                    disabled={selectedAccount.state !== "active"}
                    onClick={credentialDialog.show}
                  >
                    {text("签发新凭据", "Issue credential")}
                  </Button>
                }
              />
              {credentialsError !== null && (
                <div className="px-5 pt-4">
                  <ErrorBanner
                    error={credentialsError}
                    onRetry={loadCredentials}
                  />
                </div>
              )}
              {credentials === null ? (
                <div className="p-8">
                  <Loading label={text("加载凭据…", "Loading credentials…")} />
                </div>
              ) : credentials.length === 0 ? (
                <div className="p-8">
                  <EmptyState
                    title={text("暂无凭据", "No credentials")}
                    hint={text(
                      "签发第一枚短期凭据，并保存到 CI Secret。",
                      "Issue the first short-lived credential and store it in a CI secret.",
                    )}
                  />
                </div>
              ) : (
                <>
                  <Table<ServiceAccountCredential>
                    rowKey="id"
                    columns={columns}
                    dataSource={credentials}
                    pagination={false}
                    size="small"
                    scroll={{ x: 760 }}
                  />
                  {nextCredentialsPageToken && (
                    <div className="border-t border-zinc-800/70 p-3 text-center">
                      <Button
                        type="text"
                        loading={loadingMoreCredentials}
                        onClick={() =>
                          void loadCredentials(nextCredentialsPageToken)
                        }
                      >
                        {text("加载更多凭据", "Load more credentials")}
                      </Button>
                    </div>
                  )}
                </>
              )}
            </Card>
          )}
        </div>
      )}

      <Modal
        open={createDialog.open}
        title={text("新建服务账号", "New service account")}
        onClose={createDialog.hide}
        footer={
          <Space>
            <Button onClick={createDialog.hide} disabled={busy}>
              {text("取消", "Cancel")}
            </Button>
            <Button
              type="primary"
              loading={busy}
              disabled={!accountName.trim()}
              onClick={() => void submitAccount()}
            >
              {text("创建", "Create")}
            </Button>
          </Space>
        }
      >
        <div className="space-y-4">
          <Field
            label={text("账号名称", "Account name")}
            hint={text(
              "例如 pipeone-ci 或 release-bot",
              "For example pipeone-ci or release-bot",
            )}
          >
            <Input
              value={accountName}
              maxLength={128}
              onChange={(event) => setAccountName(event.target.value)}
            />
          </Field>
          <Field label={text("用途说明", "Purpose")}>
            <Input.TextArea
              value={accountDescription}
              maxLength={1024}
              rows={3}
              onChange={(event) => setAccountDescription(event.target.value)}
            />
          </Field>
        </div>
      </Modal>

      <Modal
        open={credentialDialog.open}
        title={text("签发新凭据", "Issue credential")}
        onClose={credentialDialog.hide}
        footer={
          <Space>
            <Button onClick={credentialDialog.hide} disabled={busy}>
              {text("取消", "Cancel")}
            </Button>
            <Button
              type="primary"
              loading={busy}
              disabled={!credentialName.trim()}
              onClick={() => void submitCredential()}
            >
              {text("签发", "Issue")}
            </Button>
          </Space>
        }
      >
        <div className="space-y-4">
          <Field
            label={text("凭据名称", "Credential name")}
            hint={text(
              "建议包含使用方和轮换批次",
              "Include the consumer and rotation batch",
            )}
          >
            <Input
              value={credentialName}
              maxLength={128}
              placeholder="jenkins-blue"
              onChange={(event) => setCredentialName(event.target.value)}
            />
          </Field>
          <Field label={text("有效期", "Validity")}>
            <Select<typeof validDays>
              className="w-full"
              value={validDays}
              onChange={setValidDays}
              options={[
                { value: 30, label: text("30 天", "30 days") },
                {
                  value: 90,
                  label: text("90 天（推荐）", "90 days (recommended)"),
                },
                { value: 180, label: text("180 天", "180 days") },
                { value: 365, label: text("365 天", "365 days") },
              ]}
            />
          </Field>
          <p className="text-xs leading-5 text-zinc-500">
            {text(
              "新旧凭据可以短暂并存：先部署新 Token、验证成功，再吊销旧 Token，实现零停机轮换。",
              "Old and new credentials may overlap briefly: deploy the new token, verify it, then revoke the old token for zero-downtime rotation.",
            )}
          </p>
        </div>
      </Modal>

      {reveal && (
        <TokenReveal credential={reveal} onDone={() => setReveal(null)} />
      )}
      <ConfirmDialog
        open={credentialToRevoke !== null}
        title={text("吊销凭据", "Revoke credential")}
        message={text(
          `吊销 ${credentialToRevoke?.name ?? ""} 后，使用该 Token 的任务会立即认证失败；同一服务账号的其他凭据不受影响。`,
          `After revoking ${credentialToRevoke?.name ?? ""}, jobs using that token fail authentication immediately. Other credentials remain valid.`,
        )}
        confirmLabel={text("确认吊销", "Revoke")}
        danger
        busy={busy}
        onClose={() => setCredentialToRevoke(null)}
        onConfirm={() => void confirmRevoke()}
      />
      <ConfirmDialog
        open={accountStateChange !== null}
        title={
          accountStateChange?.state === "active"
            ? text("禁用服务账号", "Disable service account")
            : text("重新启用服务账号", "Enable service account")
        }
        message={
          accountStateChange?.state === "active"
            ? text(
                "该账号的全部现有凭据会立即停止认证。仓库授权与审计主体会保留，重新启用后未过期且未吊销的凭据可再次使用。",
                "Every existing credential stops authenticating immediately. Grants and audit identity remain, and non-expired credentials work again after re-enabling.",
              )
            : text(
                "未过期且未吊销的凭据将重新具备认证能力。",
                "Non-expired, non-revoked credentials will authenticate again.",
              )
        }
        confirmLabel={
          accountStateChange?.state === "active"
            ? text("确认禁用", "Disable")
            : text("确认启用", "Enable")
        }
        danger={accountStateChange?.state === "active"}
        busy={busy}
        onClose={() => setAccountStateChange(null)}
        onConfirm={() => void confirmAccountState()}
      />
    </div>
  );
}
