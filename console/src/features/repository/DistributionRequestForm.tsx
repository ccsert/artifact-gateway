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
  const { text } = usePreferences();
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
            {text("发起晋升 / 复制", "Start promotion / replication")}
          </div>
          <p className="mt-1 max-w-3xl text-xs leading-5 text-fg-tertiary">
            {text(
              "选择不可变制品和同格式目标仓库。晋升经目标仓安全准入后发布副本；复制创建可续传计划，不代表通过安全准入。",
              "Select an immutable artifact and a target repository of the same format. Promotion publishes a copy after target admission; replication creates a resumable plan without security approval.",
            )}
          </p>
        </div>
        <div className="grid min-w-0 grid-cols-1 items-start gap-4 md:grid-cols-[minmax(0,1.6fr)_minmax(0,1fr)]">
          <Field
            group
            label={text("源制品", "Source artifact")}
            hint={
              aptPackages
                ? text(
                    "当前套件的可见软件包；输入包名筛选。",
                    "Visible packages in the current suite; type a package name to filter.",
                  )
                : text(
                    "最多显示 50 条；输入包名、路径或坐标前缀可缩小范围。",
                    "Up to 50 items are shown. Type a package, path, or coordinate prefix to narrow the results.",
                  )
            }
          >
            {aptPackages ? (
              <Select
                aria-label={text(
                  "搜索并选择源制品",
                  "Search and select a source artifact",
                )}
                className="w-full"
                showSearch={{ optionFilterProp: "label" }}
                placeholder={text(
                  "选择当前套件的软件包",
                  "Select a package in this suite",
                )}
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
                ariaLabel={text(
                  "搜索并选择源制品",
                  "Search and select a source artifact",
                )}
              />
            )}
          </Field>
          <Field
            group
            label={text("目标 Hosted 仓库", "Target Hosted repository")}
          >
            <Select
              aria-label={text("选择目标仓库", "Select target repository")}
              className="w-full"
              showSearch={{ optionFilterProp: "label" }}
              value={targetId || undefined}
              placeholder={text(
                "选择同格式仓库…",
                "Select a repository with the same format…",
              )}
              options={targets.map((r) => ({ value: r.id, label: r.name }))}
              notFoundContent={text(
                "没有可用的同格式 Hosted 仓库",
                "No compatible Hosted repository is available",
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
              label={text("目标发行套件", "Target suite")}
              hint={text(
                "目标仓库会使用自己的签名服务发布新快照，并保留已有成员。",
                "The target repository signs a new snapshot with its own signer and preserves existing members.",
              )}
            >
              <Input
                aria-label={text("目标发行套件", "Target suite")}
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
              ? text("收起高级手动输入", "Hide advanced manual input")
              : text("高级手动输入", "Advanced manual input")}
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
              label={text("制品坐标", "Artifact coordinate")}
              hint={text(
                `${repo.format} 生命周期使用的不可变版本标识`,
                `Immutable version identity used by ${repo.format} lifecycle operations`,
              )}
            >
              <Input
                aria-label={text("制品坐标", "Artifact coordinate")}
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
            <Field label={text("摘要 digest", "Digest")}>
              <Input
                aria-label={text("摘要 digest", "Digest")}
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
              {text("评估准入", "Evaluate admission")}
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
              {text("晋升", "Promote")}
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
              {text("复制", "Replicate")}
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
