import { useRef, useState } from "react";
import { Button, Tabs } from "antd";
import { ReloadOutlined } from "@ant-design/icons";
import {
  applyAptLifecycle,
  previewAptLifecycle,
  pruneAptSnapshots,
} from "../../client";
import type { AptLifecycleRequest, Repository } from "../../client";
import { ErrorBanner, Loading, Notice } from "../../components/ui/Feedback";
import { usePreferences } from "../../lib/preferences";
import { RepositoryDistributionTab } from "./RepositoryDistributionTab";

import { APTArchiveOperations } from "./APTArchiveOperations";
import { APTCurrentSnapshot } from "./APTCurrentSnapshot";
import { APTRecoveryList, APTSnapshotHistory } from "./APTLifecyclePanels";
import { APTPackagesPanel } from "./APTPackagesPanel";
import { APTReviewDialog } from "./APTReviewDialog";
import { type Review } from "./aptModel";
import { useAptLifecycleState } from "./repositoryQueries";

/** One suite's packages, snapshots, retention and archive operations. */
export function APTSuiteOperations({
  repo,
  suite,
}: {
  repo: Repository;
  suite: string;
}) {
  const { t } = usePreferences();
  const stateQuery = useAptLifecycleState(
    repo.id,
    suite,
    t("repository.snapshotResponseIsEmpty"),
  );
  const state = stateQuery.data ?? null;
  const error = stateQuery.error;
  const [actionError, setActionError] = useState<unknown>(null);
  const loading = stateQuery.isFetching;
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState("");
  const [selected, setSelected] = useState<React.Key[]>([]);
  const [keepLatest, setKeepLatest] = useState(1);
  const [olderThanDays, setOlderThanDays] = useState(30);
  const [review, setReview] = useState<Review | null>(null);
  const generation = useRef(0);
  const inFlight = useRef(false);
  const current = state?.snapshots.find(
    (item) => item.snapshot.state === "visible",
  )?.snapshot;

  // A reload discards the open review and selection; a preview that started
  // before it must not land on the reloaded state.
  const load = async () => {
    generation.current++;
    setReview(null);
    setSelected([]);
    await stateQuery.refetch();
  };

  const run = async (action: () => Promise<void>) => {
    if (inFlight.current) return;
    inFlight.current = true;
    setBusy(true);
    setActionError(null);
    setNotice("");
    try {
      await action();
    } catch (err) {
      setActionError(err);
    } finally {
      inFlight.current = false;
      setBusy(false);
    }
  };

  const preview = (request: AptLifecycleRequest) =>
    run(async () => {
      const version = generation.current;
      setReview(null);
      const response = await previewAptLifecycle({
        path: { repositoryId: repo.id },
        body: request,
      });
      if (version !== generation.current) return;
      if (response.error || !response.data)
        throw (
          response.error ?? new Error(t("repository.previewResponseIsEmpty"))
        );
      if (response.data.expectedSnapshotId !== request.expectedSnapshotId)
        throw new Error(
          t("repository.theSnapshotChangedRefreshAndPreviewAgain"),
        );
      setReview({
        request: {
          ...request,
          ...(request.action === "retention"
            ? { publicationSessionIds: response.data.removeSessionIds }
            : {}),
        },
        plan: response.data,
        key: crypto.randomUUID(),
      });
    });

  const apply = () =>
    run(async () => {
      if (!review) return;
      const response = await applyAptLifecycle({
        path: { repositoryId: repo.id },
        body: review.request,
        headers: { "Idempotency-Key": review.key },
      });
      if (response.error) {
        if (
          response.response?.status === 409 ||
          response.error.status === 409
        ) {
          setReview(null);
          throw new Error(
            t("repository.theSnapshotOrCandidatesChangedOrGovernanceBlocked"),
          );
        }
        throw response.error;
      }
      if (!response.data)
        throw new Error(t("repository.noApplyResultWasReceivedRetryTheSame"));
      setReview(null);
      setNotice(
        t("repository.publishedSnapshot", {
          suite,
          sequence: response.data.sequence,
        }),
      );
      await load();
    });

  const prune = (id: string) =>
    run(async () => {
      const response = await pruneAptSnapshots({
        path: { repositoryId: repo.id },
        body: { snapshotIds: [id] },
      });
      if (response.error) throw response.error;
      setNotice(
        t(
          "repository.snapshotReferencesPrunedUnreferencedObjectsAreReclaimedAsynchronously",
        ),
      );
      await load();
    });

  const request = (
    action: AptLifecycleRequest["action"],
  ): AptLifecycleRequest => ({
    suite,
    expectedSnapshotId: current?.id ?? "",
    action,
  });
  const disabled = busy || loading || error !== null;
  const removed =
    state?.packages.filter((item) =>
      review?.plan.removeSessionIds.includes(item.publicationSessionId),
    ) ?? [];
  const restored =
    state?.deletions.filter((item) =>
      review?.plan.restoreIds.includes(item.id),
    ) ?? [];
  const changedCount =
    (review?.plan.removeSessionIds.length ?? 0) +
    (review?.plan.restoreIds.length ?? 0);

  return (
    <div className="ag-page-stack">
      <div className="ag-apt-heading">
        <h3>{suite}</h3>
        <Button
          icon={<ReloadOutlined aria-hidden />}
          loading={loading}
          disabled={busy}
          onClick={() => void load()}
        >
          {t("repository.refreshSnapshots")}
        </Button>
      </div>
      {error !== null && <ErrorBanner error={error} onRetry={load} />}
      {actionError !== null && !review && <ErrorBanner error={actionError} />}
      {notice && (
        <Notice tone="success" title={notice} onClose={() => setNotice("")} />
      )}
      {!state ? (
        loading ? (
          <Loading />
        ) : null
      ) : (
        <>
          <APTCurrentSnapshot current={current} />
          <Tabs
            className="ag-compact-tabs"
            tabBarGutter={24}
            destroyOnHidden
            items={[
              {
                key: "packages",
                label: t("repository.packages", {
                  length: state.packages.length,
                }),
                children: (
                  <APTPackagesPanel
                    packages={state.packages}
                    // Previews run against the visible snapshot.
                    disabled={disabled}
                    hasCurrent={Boolean(current)}
                    selected={selected}
                    onSelect={setSelected}
                    keepLatest={keepLatest}
                    setKeepLatest={setKeepLatest}
                    olderThanDays={olderThanDays}
                    setOlderThanDays={setOlderThanDays}
                    onPreviewDelete={(ids) =>
                      void preview({
                        ...request("delete"),
                        publicationSessionIds: ids,
                      })
                    }
                    onPreviewRetention={(keepLatest, olderThanDays) =>
                      void preview({
                        ...request("retention"),
                        keepLatest,
                        olderThanDays,
                      })
                    }
                  />
                ),
              },
              {
                key: "recovery",
                label: t("repository.recovery", {
                  length: state.deletions.length,
                }),
                children: (
                  <APTRecoveryList
                    deletions={state.deletions}
                    // Recovery previews against the visible snapshot.
                    disabled={disabled || !current}
                    onRestore={(id) =>
                      void preview({ ...request("restore"), deletionIds: [id] })
                    }
                  />
                ),
              },
              {
                key: "history",
                label: t("repository.snapshots", {
                  length: state.snapshots.length,
                }),
                children: (
                  <APTSnapshotHistory
                    snapshots={state.snapshots}
                    disabled={disabled}
                    onPrune={(id) => void prune(id)}
                  />
                ),
              },
              {
                key: "archive",
                label: t("repository.disasterRecovery"),
                children: (
                  <APTArchiveOperations
                    repo={repo}
                    snapshots={state.snapshots}
                    disabled={disabled}
                    onRestored={load}
                  />
                ),
              },
              {
                key: "distribution",
                label: t("repository.promoteReplicate"),
                children: (
                  <RepositoryDistributionTab
                    key={current?.id ?? "empty"}
                    repo={repo}
                    aptPackages={state.packages}
                    disabled={disabled}
                  />
                ),
              },
            ]}
          />
        </>
      )}
      <APTReviewDialog
        review={review}
        busy={busy}
        actionError={actionError}
        removed={removed}
        restored={restored}
        changedCount={changedCount}
        onCancel={() => {
          setReview(null);
          setActionError(null);
        }}
        onApply={() => void apply()}
      />
    </div>
  );
}
