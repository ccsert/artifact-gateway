import { useState } from "react";
import { PlusOutlined } from "@ant-design/icons";
import { Button } from "antd";
import {
  revokeServiceAccountCredential,
  updateServiceAccount,
} from "../../client";
import type {
  CreatedServiceAccountCredential,
  ServiceAccount,
  ServiceAccountCredential,
} from "../../client";
import { Badge } from "../../components/ui/Badge";
import { MetricStrip } from "../../components/ui/ConsolePrimitives";
import {
  EmptyState,
  ErrorBanner,
  Loading,
  Notice,
} from "../../components/ui/Feedback";
import { Card, PageHeader, Pagination } from "../../components/ui/Layout";
import { ConfirmDialog, useDisclosure } from "../../components/ui/Modal";
import { OneTimeTokenDialog } from "../../components/ui/OneTimeTokenDialog";
import { SectionBoundary } from "../../components/ui/SectionBoundary";
import { formatDate } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";
import {
  CreateServiceAccountDialog,
  IssueCredentialDialog,
} from "./ServiceAccountDialogs";
import { ServiceAccountDetail, credentialState } from "./ServiceAccountDetail";
import {
  flattenPages,
  useCredentialPages,
  useInvalidateCredentials,
  useInvalidateServiceAccounts,
  useServiceAccountPages,
} from "./serviceAccountQueries";

export function ServiceAccountsPage() {
  const { text } = usePreferences();
  const createDialog = useDisclosure();
  const credentialDialog = useDisclosure();
  const accountsQuery = useServiceAccountPages();
  const invalidateAccounts = useInvalidateServiceAccounts();
  const invalidateCredentials = useInvalidateCredentials();
  const accounts: ServiceAccount[] | null = flattenPages(accountsQuery.data);
  const nextAccountsPageToken = accountsQuery.hasNextPage ? "more" : "";
  const loadingMoreAccounts = accountsQuery.isFetchingNextPage;
  const [selectedAccountId, setSelectedAccountId] = useState("");
  // The chosen account while it exists, otherwise the first one.
  const effectiveAccountId = accounts?.some(
    (account) => account.id === selectedAccountId,
  )
    ? selectedAccountId
    : (accounts?.[0]?.id ?? "");
  const selectedAccount =
    accounts?.find((account) => account.id === effectiveAccountId) ?? null;
  const credentialsQuery = useCredentialPages(effectiveAccountId);
  const credentials: ServiceAccountCredential[] | null = effectiveAccountId
    ? flattenPages(credentialsQuery.data)
    : [];
  const nextCredentialsPageToken = credentialsQuery.hasNextPage ? "more" : "";
  // Write failures on the page itself; dialogs keep their own.
  const [accountError, setAccountError] = useState<unknown>(null);
  const [revokeError, setRevokeError] = useState<unknown>(null);
  const error = accountError ?? accountsQuery.error ?? null;
  const credentialsError = revokeError ?? credentialsQuery.error ?? null;
  const [busy, setBusy] = useState(false);
  const [reveal, setReveal] = useState<CreatedServiceAccountCredential | null>(
    null,
  );
  const [credentialToRevoke, setCredentialToRevoke] =
    useState<ServiceAccountCredential | null>(null);
  const [accountStateChange, setAccountStateChange] =
    useState<ServiceAccount | null>(null);

  const loadAccounts = () => {
    setAccountError(null);
    void invalidateAccounts();
  };
  const loadCredentials = () => {
    setRevokeError(null);
    if (effectiveAccountId) void invalidateCredentials(effectiveAccountId);
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
        setAccountError(updateError);
        return;
      }
      setAccountStateChange(null);
      await invalidateAccounts();
    } catch (nextError) {
      setAccountError(nextError);
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
        setRevokeError(revokeError);
        return;
      }
      setCredentialToRevoke(null);
      await invalidateCredentials(selectedAccount.id);
    } catch (nextError) {
      setRevokeError(nextError);
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

      <Notice
        tone="info"
        closable={false}
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
          />
        </Card>
      ) : (
        <div className="ag-page-primary ag-service-account-workspace grid items-start gap-4 xl:grid-cols-[300px_minmax(0,1fr)]">
          <Card bodyClassName="p-2">
            <div className="px-2 pb-2 pt-1 text-xs font-medium uppercase tracking-wider text-fg-disabled">
              {text("机器主体", "Machine principals")}
            </div>
            <div className="space-y-1">
              {accounts.map((account) => (
                <button
                  key={account.id}
                  type="button"
                  className={`w-full rounded-lg border px-3 py-3 text-left transition-colors ${
                    account.id === effectiveAccountId
                      ? "border-[var(--ag-border-default)] bg-[var(--ag-navigation-selected-start)]"
                      : "border-transparent hover:border-[var(--ag-border-subtle)] hover:bg-[var(--ag-surface-hover)]"
                  }`}
                  onClick={() => setSelectedAccountId(account.id)}
                >
                  <div className="flex items-center justify-between gap-2">
                    <span className="truncate text-sm font-medium text-fg">
                      {account.name}
                    </span>
                    <Badge
                      tone={account.state === "active" ? "success" : "neutral"}
                    >
                      {account.state}
                    </Badge>
                  </div>
                  <p className="mt-1 line-clamp-2 text-xs leading-5 text-fg-disabled">
                    {account.description ||
                      text("未填写用途", "No purpose documented")}
                  </p>
                </button>
              ))}
            </div>
            <Pagination
              hasMore={!!nextAccountsPageToken}
              loading={loadingMoreAccounts}
              onMore={() => void accountsQuery.fetchNextPage()}
              label={text("加载更多账号", "Load more accounts")}
            />
          </Card>

          {selectedAccount && (
            <SectionBoundary resetKeys={[selectedAccount.id]}>
              <ServiceAccountDetail
                account={selectedAccount}
                credentials={credentials}
                credentialsError={credentialsError}
                nextCredentialsPageToken={nextCredentialsPageToken}
                loadingMoreCredentials={credentialsQuery.isFetchingNextPage}
                onRefresh={loadCredentials}
                onMoreCredentials={() => void credentialsQuery.fetchNextPage()}
                onIssue={credentialDialog.show}
                onRevoke={setCredentialToRevoke}
                onToggleState={() => setAccountStateChange(selectedAccount)}
              />
            </SectionBoundary>
          )}
        </div>
      )}

      <CreateServiceAccountDialog
        open={createDialog.open}
        onClose={createDialog.hide}
        onCreated={async (account) => {
          createDialog.hide();
          await invalidateAccounts();
          setSelectedAccountId(account.id);
        }}
      />
      {selectedAccount && (
        <IssueCredentialDialog
          open={credentialDialog.open}
          account={selectedAccount}
          onClose={credentialDialog.hide}
          onIssued={async (credential) => {
            credentialDialog.hide();
            setReveal(credential);
            await invalidateCredentials(selectedAccount.id);
          }}
        />
      )}

      {reveal && (
        <OneTimeTokenDialog
          title={text(
            "凭据已签发：立即保存 Token",
            "Credential issued: save the token",
          )}
          warningTitle={text("明文只显示一次", "Plaintext is shown once")}
          warningDescription={text(
            "关闭后无法重新查看。请把 Token 写入 CI Secret 或外部密钥管理系统，不要提交到仓库。",
            "It cannot be viewed again. Put the token in a CI secret or external secret manager, never in source control.",
          )}
          token={reveal.token}
          details={
            <>
              {text("凭据名称", "Credential name")}: {reveal.name} ·{" "}
              {text("到期", "Expires")}: {formatDate(reveal.expiresAt)}
            </>
          }
          doneLabel={text("我已安全保存", "I stored it securely")}
          onDone={() => setReveal(null)}
        />
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
