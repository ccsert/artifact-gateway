import { Button, Input, Select } from "antd";
import type { AptLifecyclePackage, Repository } from "../../client";
import { Field } from "../../components/ui/Layout";
import { RepositoryArtifactSelect } from "./RepositoryArtifactSelect";
import { usePreferences } from "../../lib/preferences";

import { PromotionEvaluationAlert } from "./PromotionEvaluationAlert";
import { useDistributionRequest } from "./useDistributionRequest";

/**
 * Promote or replicate one artifact to another hosted repository of the same
 * format, after checking the target's security policy.
 */
export function DistributionRequestForm({
  repo,
  repos,
  aptPackages,
  disabled,
  setActionError,
  setNotice,
  onSubmitted,
}: {
  repo: Repository;
  repos: Repository[];
  aptPackages?: AptLifecyclePackage[];
  disabled: boolean;
  setActionError: (error: unknown) => void;
  setNotice: (notice: string) => void;
  /** Re-read the plan list after a request is accepted. */
  onSubmitted: () => Promise<unknown>;
}) {
  const { t } = usePreferences();
  const {
    suiteTarget,
    targetId,
    setTargetId,
    aptTargetSuite,
    setAptTargetSuite,
    coordinate,
    setCoordinate,
    digest,
    setDigest,
    selectedArtifact,
    setSelectedArtifact,
    manualIdentity,
    busy,
    evaluation,
    evaluating,
    targets,
    coordinateExample,
    invalidateEvaluation,
    submit,
    evaluate,
    promotionBlocked,
    artifactQuarantined,
    chooseArtifact,
    toggleManualIdentity,
  } = useDistributionRequest({
    repo,
    repos,
    disabled,
    setActionError,
    setNotice,
    onSubmitted,
  });

  return (
    <>
      {/* 发起表单 */}
      <div className="rounded-lg border border-line p-4">
        <div className="mb-4">
          <div className="text-sm font-medium text-fg">
            {t("repository.startPromotionReplication")}
          </div>
          <p className="mt-1 max-w-3xl text-xs leading-5 text-fg-tertiary">
            {t("repository.selectAnImmutableArtifactAndATargetRepository")}
          </p>
        </div>
        <div className="grid min-w-0 grid-cols-1 items-start gap-4 md:grid-cols-[minmax(0,1.6fr)_minmax(0,1fr)]">
          <Field
            group
            label={t("repository.sourceArtifact")}
            hint={
              aptPackages
                ? t("repository.visiblePackagesInTheCurrentSuiteTypeA")
                : t("repository.upTo50ItemsAreShownTypeA")
            }
          >
            {aptPackages ? (
              <Select
                aria-label={t("repository.searchAndSelectASourceArtifact")}
                className="w-full"
                showSearch={{ optionFilterProp: "label" }}
                placeholder={t("repository.selectAPackageInThisSuite")}
                value={selectedArtifact?.key}
                disabled={disabled || busy !== null}
                options={aptPackages.map((item) => ({
                  value: item.publicationSessionId,
                  disabled: !item.poolPath,
                  label: `${item.revision.package} ${item.revision.version} · ${item.revision.architecture}`,
                }))}
                onChange={(id) => {
                  const item = aptPackages.find(
                    (p) => p.publicationSessionId === id,
                  );
                  if (item)
                    chooseArtifact({
                      key: id,
                      coordinate: item.poolPath,
                      digest: item.revision.digest,
                    });
                }}
              />
            ) : (
              <RepositoryArtifactSelect
                repo={repo}
                purpose="distribution"
                value={selectedArtifact}
                onChange={chooseArtifact}
                disabled={disabled || busy !== null}
                ariaLabel={t("repository.searchAndSelectASourceArtifact")}
              />
            )}
          </Field>
          <Field group label={t("repository.targetHostedRepository")}>
            <Select
              aria-label={t("repository.selectTargetRepository")}
              className="w-full"
              showSearch={{ optionFilterProp: "label" }}
              value={targetId || undefined}
              placeholder={t("repository.selectARepositoryWithTheSameFormat")}
              options={targets.map((r) => ({ value: r.id, label: r.name }))}
              notFoundContent={t(
                "repository.noCompatibleHostedRepositoryIsAvailable",
              )}
              disabled={disabled || busy !== null}
              onChange={(value) => {
                setTargetId(value);
                invalidateEvaluation();
              }}
            />
          </Field>
        </div>

        {suiteTarget && (
          <div className="ag-apt-distribution-scope">
            <Field
              label={t("repository.targetSuite")}
              hint={t("repository.theTargetRepositorySignsANewSnapshotWith")}
            >
              <Input
                aria-label={t("repository.targetSuite")}
                placeholder="stable"
                value={aptTargetSuite}
                disabled={disabled || busy !== null}
                onChange={(e) => setAptTargetSuite(e.target.value)}
              />
            </Field>
          </div>
        )}
        <div className="mt-2 flex justify-end">
          <Button
            type="link"
            size="small"
            className="h-auto px-0 py-0 text-xs"
            disabled={disabled || busy !== null}
            onClick={toggleManualIdentity}
          >
            {manualIdentity
              ? t("repository.hideAdvancedManualInput")
              : t("repository.advancedManualInput")}
          </Button>
        </div>

        {selectedArtifact && !manualIdentity && (
          <div className="mt-3 rounded-md border border-[var(--ag-status-info-border)] bg-[var(--ag-status-info-soft)] px-3 py-2.5">
            <p className="break-all font-mono text-xs text-fg">
              {selectedArtifact.coordinate}
            </p>
            <p className="mt-0.5 break-all font-mono text-xs text-fg-tertiary">
              {selectedArtifact.digest}
            </p>
          </div>
        )}

        {manualIdentity && (
          <div className="mt-3 grid grid-cols-1 gap-4 md:grid-cols-2">
            <Field
              label={t("repository.artifactCoordinate")}
              hint={t(
                "repository.immutableVersionIdentityUsedByLifecycleOperations",
                { format: repo.format },
              )}
            >
              <Input
                aria-label={t("repository.artifactCoordinate")}
                disabled={disabled || busy !== null}
                className="font-mono"
                placeholder={coordinateExample ?? "coordinate"}
                value={coordinate}
                onChange={(event) => {
                  setSelectedArtifact(null);
                  setCoordinate(event.target.value);
                  invalidateEvaluation();
                }}
              />
            </Field>
            <Field label={t("repository.digest2")}>
              <Input
                aria-label={t("repository.digest2")}
                disabled={disabled || busy !== null}
                className="font-mono"
                placeholder="sha256:…"
                value={digest}
                onChange={(event) => {
                  setSelectedArtifact(null);
                  setDigest(event.target.value);
                  invalidateEvaluation();
                }}
              />
            </Field>
          </div>
        )}

        <div className="mt-4 flex flex-wrap justify-end gap-2 border-t border-line pt-3">
          <div className="flex flex-wrap justify-end gap-2">
            <Button
              loading={evaluating}
              onClick={() => void evaluate()}
              disabled={
                disabled ||
                busy !== null ||
                evaluating ||
                !targetId ||
                !coordinate.trim() ||
                !digest.trim()
              }
            >
              {t("repository.evaluateAdmission")}
            </Button>
            <Button
              type="primary"
              loading={busy === "promote"}
              onClick={() => submit("promote")}
              disabled={
                disabled ||
                busy !== null ||
                promotionBlocked ||
                evaluating ||
                (suiteTarget && !aptTargetSuite.trim()) ||
                !targetId ||
                !coordinate.trim() ||
                !digest.trim()
              }
            >
              {t("repository.promote")}
            </Button>
            <Button
              loading={busy === "replicate"}
              onClick={() => submit("replicate")}
              disabled={
                disabled ||
                busy !== null ||
                artifactQuarantined ||
                evaluating ||
                (suiteTarget && !aptTargetSuite.trim()) ||
                !targetId ||
                !coordinate.trim() ||
                !digest.trim()
              }
            >
              {t("repository.replicate")}
            </Button>
          </div>
        </div>
        {evaluation && (
          <PromotionEvaluationAlert
            evaluation={evaluation}
            artifactQuarantined={artifactQuarantined}
          />
        )}
      </div>
    </>
  );
}
