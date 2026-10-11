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
  const { t } = usePreferences();
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
        title={t("identity.serviceAccounts")}
        description={t(
          "identity.giveJenkinsCiRobotsAndThirdPartyApplicationsA",
        )}
        actions={
          <Button
            type="primary"
            icon={<PlusOutlined />}
            onClick={createDialog.show}
          >
            {t("identity.newServiceAccount")}
          </Button>
        }
      />

      <Notice
        tone="info"
        closable={false}
        title={t("identity.machineIdentityIsSeparateFromCredentials")}
        description={t(
          "identity.repositoryGrantsBindToTheStableServiceAccountId",
        )}
      />

      <MetricStrip
        items={[
          {
            label: t("identity.serviceAccounts"),
            value:
              accounts === null
                ? "—"
                : `${accounts.length}${nextAccountsPageToken ? "+" : ""}`,
            hint: t("identity.stableMachinePrincipals"),
          },
          {
            label: t("identity.active2"),
            value: `${activeAccounts.length}${nextAccountsPageToken ? "+" : ""}`,
            hint: t("identity.mayAcceptCredentialAuth"),
            tone: "success",
          },
          {
            label: t("identity.activeCredentials"),
            value: `${activeCredentials.length}${nextCredentialsPageToken ? "+" : ""}`,
            hint: t("identity.supportsZeroDowntimeRotation"),
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
            <Loading label={t("identity.loadingServiceAccounts")} />
          </Card>
        )
      ) : accounts.length === 0 ? (
        <Card bodyClassName="p-8">
          <EmptyState
            title={t("identity.noServiceAccounts")}
            hint={t("identity.createAStableIdentityForYourFirstCiRobot")}
          />
        </Card>
      ) : (
        <div className="ag-page-primary ag-service-account-workspace grid items-start gap-4 xl:grid-cols-[300px_minmax(0,1fr)]">
          <Card bodyClassName="p-2">
            <div className="px-2 pb-2 pt-1 text-xs font-medium uppercase tracking-wider text-fg-disabled">
              {t("identity.machinePrincipals")}
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
                    {account.description || t("identity.noPurposeDocumented")}
                  </p>
                </button>
              ))}
            </div>
            <Pagination
              hasMore={!!nextAccountsPageToken}
              loading={loadingMoreAccounts}
              onMore={() => void accountsQuery.fetchNextPage()}
              label={t("identity.loadMoreAccounts")}
            />
          </Card>

          {selectedAccount && (
            <SectionBoundary
              resetKeys={[selectedAccount.id]}
              recoveryKeys={[selectedAccount, credentialsQuery.data]}
              onReset={() => credentialsQuery.refetch({ throwOnError: true })}
            >
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
          title={t("identity.credentialIssuedSaveTheToken")}
          warningTitle={t("identity.plaintextIsShownOnce")}
          warningDescription={t("identity.itCannotBeViewedAgainPutTheTokenIn")}
          token={reveal.token}
          details={
            <>
              {t("identity.credentialName")}: {reveal.name} ·{" "}
              {t("identity.expires2")}: {formatDate(reveal.expiresAt)}
            </>
          }
          doneLabel={t("identity.iStoredItSecurely")}
          onDone={() => setReveal(null)}
        />
      )}
      <ConfirmDialog
        open={credentialToRevoke !== null}
        title={t("identity.revokeCredential")}
        message={t(
          "identity.afterRevokingJobsUsingThatTokenFailAuthenticationImmediately",
          { credentialToRevoke: String(credentialToRevoke?.name ?? "") },
        )}
        confirmLabel={t("identity.revoke3")}
        danger
        busy={busy}
        onClose={() => setCredentialToRevoke(null)}
        onConfirm={() => void confirmRevoke()}
      />
      <ConfirmDialog
        open={accountStateChange !== null}
        title={
          accountStateChange?.state === "active"
            ? t("identity.disableServiceAccount")
            : t("identity.enableServiceAccount")
        }
        message={
          accountStateChange?.state === "active"
            ? t(
                "identity.everyExistingCredentialStopsAuthenticatingImmediatelyGrantsAndAudit",
              )
            : t("identity.nonExpiredNonRevokedCredentialsWillAuthenticateAgain")
        }
        confirmLabel={
          accountStateChange?.state === "active"
            ? t("identity.disable")
            : t("identity.enable")
        }
        danger={accountStateChange?.state === "active"}
        busy={busy}
        onClose={() => setAccountStateChange(null)}
        onConfirm={() => void confirmAccountState()}
      />
    </div>
  );
}
