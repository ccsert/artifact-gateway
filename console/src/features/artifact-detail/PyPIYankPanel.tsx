import { useId, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Button, Input, Select, Space } from "antd";
import { getPyPiYankState, updatePyPiYankState } from "../../client";
import type { PyPiYankState } from "../../client";
import { usePreferences } from "../../lib/preferences";
import { protocolKeys, repositoryKeys } from "../../lib/query/keys";
import { unwrap } from "../../lib/query/client";
import { ErrorBanner, Loading } from "../../components/ui/Feedback";
import { Modal } from "../../components/ui/Modal";
import { errorText } from "../../messages/translate";

type PyPIYankPanelProps = {
  repositoryId: string;
  repoName?: string;
  project: string;
  version: string;
  canManage: boolean;
};

export function PyPIYankPanel(props: PyPIYankPanelProps) {
  // A draft and its captured revision belong to exactly one selected version.
  return (
    <YankPanel
      key={`${props.repositoryId}:${props.project}:${props.version}`}
      {...props}
    />
  );
}

function YankPanel({
  repositoryId,
  repoName,
  project,
  version,
  canManage,
}: PyPIYankPanelProps) {
  const { t } = usePreferences();
  const queryClient = useQueryClient();
  const fieldId = useId();
  const queryKey = [
    ...repositoryKeys.detail(repositoryId),
    "pypi-yank",
    project,
    version,
  ];
  const query = useQuery({
    queryKey,
    enabled: canManage,
    staleTime: 0,
    meta: { authFailure: "ignore" },
    queryFn: async ({ signal }) => {
      const result = await unwrap(
        getPyPiYankState({ path: { repositoryId, project, version }, signal }),
      );
      if (!validYankState(result, repositoryId, project, version))
        throw new Error(t("artifactDetail.yankMissingResponse"));
      return result;
    },
  });
  const [draft, setDraft] = useState<PyPiYankState | null>(null);
  const [filename, setFilename] = useState("");
  const [yanked, setYanked] = useState(true);
  const [reason, setReason] = useState("");
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<unknown>(null);
  const state = query.data;
  if (!canManage) return null;
  if (query.isPending)
    return (
      <div>
        <Loading />
      </div>
    );
  if (!state)
    return (
      <div>
        <ErrorBanner
          error={errorText(query.error, t, "artifactDetail.yankFailed")}
          onRetry={() => void query.refetch()}
        />
      </div>
    );

  const validReason =
    new TextEncoder().encode(reason).length <= 1024 &&
    !Array.from(reason).some((character) => {
      const point = character.codePointAt(0) ?? 0;
      return point < 32 || (point >= 127 && point <= 159);
    });
  const open = () => {
    setDraft(state);
    setFilename("");
    setYanked(!state.files.every((file) => file.yanked));
    setReason("");
    setSaveError(null);
  };
  const refresh = async () => {
    setDraft(null);
    setSaveError(null);
    await query.refetch();
  };
  const save = async () => {
    if (!draft || saving || (yanked && !validReason)) return;
    setSaving(true);
    setSaveError(null);
    try {
      const result = await unwrap(
        updatePyPiYankState({
          path: { repositoryId, project, version },
          headers: { "If-Match": `"${draft.revision}"` },
          body: {
            yanked,
            ...(yanked ? { reason } : {}),
            ...(filename ? { filename } : {}),
          },
        }),
      );
      if (!validYankState(result, repositoryId, project, version))
        throw new Error(t("artifactDetail.yankMissingResponse"));
      const previous = new Map(
        draft.files.map((file) => [file.filename, file]),
      );
      if (
        result.files.length !== draft.files.length ||
        result.files.some((file) => {
          const old = previous.get(file.filename);
          if (!old || old.digest !== file.digest || old.size !== file.size)
            return true;
          const selected = filename === "" || filename === file.filename;
          return (
            file.yanked !== (selected ? yanked : old.yanked) ||
            file.reason !== (selected ? (yanked ? reason : "") : old.reason)
          );
        })
      )
        throw new Error(t("artifactDetail.yankMissingResponse"));
      queryClient.setQueryData(queryKey, result);
      if (repoName)
        await queryClient.invalidateQueries({
          queryKey: protocolKeys.pypiProject(repoName, project),
        });
      setDraft(null);
    } catch (error) {
      setSaveError(error);
    } finally {
      setSaving(false);
    }
  };
  return (
    <section
      className="grid min-w-0 gap-3 border-b border-line pb-3"
      aria-label={t("artifactDetail.yankState")}
    >
      {query.isError && (
        <ErrorBanner error={query.error} onRetry={() => void query.refetch()} />
      )}
      <div className="flex flex-wrap items-center justify-between gap-3">
        <span className="text-sm text-fg-secondary">
          {t("artifactDetail.yankSummary", {
            yanked: state.files.filter((file) => file.yanked).length,
            total: state.files.length,
          })}
        </span>
        <Button onClick={open}>{t("artifactDetail.yankManage")}</Button>
      </div>
      <Modal
        open={draft !== null}
        title={`${t("artifactDetail.yankManage")} · ${project}==${version}`}
        onClose={() => setDraft(null)}
        busy={saving}
        footer={
          <Space>
            <Button disabled={saving} onClick={() => setDraft(null)}>
              {t("artifactDetail.cancel")}
            </Button>
            <Button
              type="primary"
              loading={saving}
              disabled={(yanked && !validReason) || saveError !== null}
              onClick={() => void save()}
            >
              {t(
                yanked
                  ? "artifactDetail.yankConfirm"
                  : "artifactDetail.unyankConfirm",
              )}
            </Button>
          </Space>
        }
      >
        <div className="ag-page-stack">
          <p className="text-sm text-fg-secondary">
            {t("artifactDetail.yankExplanation")}
          </p>
          {saveError !== null && (
            <>
              <ErrorBanner error={saveError} />
              <Button onClick={() => void refresh()}>
                {t("artifactDetail.yankRefresh")}
              </Button>
            </>
          )}
          <div className="grid gap-2">
            <label htmlFor={`${fieldId}-target`}>
              {t("artifactDetail.yankTarget")}
            </label>
            <Select
              id={`${fieldId}-target`}
              showSearch
              value={filename}
              onChange={setFilename}
              options={[
                {
                  value: "",
                  label: t("artifactDetail.yankAllFiles", {
                    total: draft?.files.length ?? 0,
                  }),
                },
                ...(draft?.files ?? []).map((file) => ({
                  value: file.filename,
                  label: file.filename,
                })),
              ]}
            />
          </div>
          <div className="grid gap-2">
            <label htmlFor={`${fieldId}-action`}>
              {t("artifactDetail.yankAction")}
            </label>
            <Select
              id={`${fieldId}-action`}
              value={yanked ? "yank" : "restore"}
              onChange={(value) => setYanked(value === "yank")}
              options={[
                { value: "yank", label: t("artifactDetail.yankFiles") },
                { value: "restore", label: t("artifactDetail.unyankFiles") },
              ]}
            />
          </div>
          {yanked && (
            <div className="grid gap-2">
              <label htmlFor={`${fieldId}-reason`}>
                {t("artifactDetail.yankReason")}
              </label>
              <Input.TextArea
                id={`${fieldId}-reason`}
                value={reason}
                onChange={(event) => setReason(event.target.value)}
                autoSize={{ minRows: 2, maxRows: 5 }}
                status={validReason ? undefined : "error"}
              />
              <span className="text-xs text-fg-tertiary">
                {t("artifactDetail.yankReasonLimit")}
              </span>
            </div>
          )}
        </div>
      </Modal>
    </section>
  );
}

function validYankState(
  state: PyPiYankState | undefined,
  repositoryId: string,
  project: string,
  version: string,
): state is PyPiYankState {
  return Boolean(
    state &&
    state.repositoryId === repositoryId &&
    state.project === project &&
    state.version === version &&
    /^[a-f0-9]{64}$/.test(state.revision) &&
    Array.isArray(state.files) &&
    state.files.length > 0 &&
    new Set(state.files.map((file) => file.filename)).size ===
      state.files.length &&
    state.files.every(
      (file) =>
        typeof file.filename === "string" &&
        file.filename !== "" &&
        typeof file.yanked === "boolean" &&
        typeof file.reason === "string" &&
        /^sha256:[a-f0-9]{64}$/.test(file.digest) &&
        Number.isSafeInteger(file.size) &&
        file.size >= 0,
    ),
  );
}
