import { LocalizedError } from "../../messages/translate";
import { useState } from "react";
import { DeleteOutlined } from "@ant-design/icons";
import { Button, Popconfirm, Select } from "antd";
import { useAuth } from "../../lib/auth";
import { Loading, ErrorBanner } from "../../components/ui/Feedback";
import { Badge } from "../../components/ui/Badge";
import { UsageSnippetBlock } from "../../components/ui/PublicBrowsePrimitives";
import { ociUsage, type UsageSnippet } from "../../lib/usage";
import { usePreferences } from "../../lib/preferences";
import { ArtifactIntelligencePanel } from "./ArtifactIntelligencePanel";
import { ArtifactScanStatus } from "./ArtifactScanStatus";
import { ArtifactQuarantinePanel } from "./ArtifactQuarantinePanel";
import { useQueryClient } from "@tanstack/react-query";
import {
  ociManifestKey,
  useOciManifest,
  useOciManifests,
} from "./artifactDetailQueries";
import { ociRegistryToken, ociVersionOptions } from "./ociImageModel";
import { OciManifestView } from "./OciManifestView";
import { useClipboardAction } from "../../components/ui/ConsolePrimitives";

export function OciImageDetail({
  repositoryId,
  repository,
  image,
  initialReference,
  onDeleted,
  canQuarantine = false,
}: {
  repositoryId: string;
  repository: string;
  image: string;
  initialReference?: string;
  onDeleted?: () => void;
  canQuarantine?: boolean;
}) {
  const { token } = useAuth();
  const { t } = usePreferences();
  const queryClient = useQueryClient();
  const manifestsQuery = useOciManifests(repositoryId, image);
  const manifests = manifestsQuery.data ?? null;
  const name = `${repository}/${image}`;
  const versions = ociVersionOptions(manifests ?? [], t);
  // A deep link wins over an earlier manual choice; otherwise the choice
  // stands while that version still exists.
  const [choice, setChoice] = useState<{
    value: string;
    link: string | undefined;
  } | null>(null);
  const linked = versions.find(
    (version) =>
      version.value === initialReference || version.digest === initialReference,
  );
  const chosen =
    choice && choice.link === initialReference
      ? versions.find((version) => version.value === choice.value)
      : undefined;
  const selectedReference = (chosen ?? linked ?? versions[0])?.value ?? null;
  const setSelectedReference = (value: string) =>
    setChoice({ value, link: initialReference });
  const manifestQuery = useOciManifest(
    repositoryId,
    token,
    name,
    selectedReference,
  );
  const manifest = manifestQuery.data?.manifest ?? null;
  const config = manifestQuery.data?.config ?? null;
  const manifestLoading = manifestQuery.isFetching && !manifestQuery.data;
  const [actionError, setError] = useState<unknown>(null);
  const error = actionError ?? manifestsQuery.error ?? manifestQuery.error;
  const [deleting, setDeleting] = useState(false);
  const { copiedValue, copy } = useClipboardAction(1400);

  const selectedVersion = versions.find(
    (version) => version.value === selectedReference,
  );

  if (manifestsQuery.isPending)
    return <Loading label={t("artifactDetail.loadingImageDetails")} />;
  if (!manifests)
    return (
      <ErrorBanner
        error={
          error ??
          new LocalizedError("artifactDetail.failedToReadOciManifestList")
        }
      />
    );
  if (manifests.length === 0)
    return (
      <p className="py-6 text-center text-sm text-fg-tertiary">
        {t("artifactDetail.thisImageHasNoVisibleManifests")}
      </p>
    );

  const deleteReference = async () => {
    if (!selectedVersion) return;
    setDeleting(true);
    setError(null);
    try {
      const registryToken = await ociRegistryToken(token);
      const res = await fetch(
        `/v2/${name}/manifests/${encodeURIComponent(selectedVersion.value)}`,
        {
          method: "DELETE",
          headers: { Authorization: `Bearer ${registryToken}` },
        },
      );
      if (!res.ok)
        throw new Error(`${res.status}: ${(await res.text()).slice(0, 120)}`);
      queryClient.removeQueries({
        queryKey: ociManifestKey(repositoryId, name, selectedVersion.value),
      });
      const remaining = await manifestsQuery.refetch();
      if (remaining.data?.length === 0) onDeleted?.();
    } catch (e) {
      setError(e);
    } finally {
      setDeleting(false);
    }
  };

  return (
    <div className="space-y-4">
      {error !== null && (
        <ErrorBanner
          error={error}
          onRetry={() => {
            setError(null);
            if (manifestsQuery.error || !selectedReference)
              void manifestsQuery.refetch();
            else void manifestQuery.refetch();
          }}
        />
      )}
      {/* 版本选择 */}
      <div className="flex items-center gap-3">
        <label
          className="shrink-0 text-xs text-fg-tertiary"
          htmlFor="oci-version-select"
        >
          {t("artifactDetail.version")}
        </label>
        <Select
          id="oci-version-select"
          className="min-w-0 flex-1 font-mono text-xs"
          showSearch={{
            optionFilterProp: "label",
            filterOption: (input, option) =>
              String(option?.searchText ?? option?.label ?? "")
                .toLowerCase()
                .includes(input.toLowerCase()),
          }}
          value={selectedReference ?? undefined}
          options={versions}
          onChange={setSelectedReference}
          placeholder={t("artifactDetail.searchTagsOrDigests")}
          listHeight={280}
        />
        {selectedVersion && (
          <Popconfirm
            title={
              selectedVersion.kind === "tag"
                ? t("artifactDetail.unlinkThisImageTag")
                : t("artifactDetail.deleteThisUntaggedManifest")
            }
            description={
              selectedVersion.kind === "tag"
                ? t("artifactDetail.onlyTheTagIsRemovedTheManifestRemains")
                : t("artifactDetail.theManifestMovesToTombstonesAndCanBe")
            }
            okText={t("artifactDetail.delete")}
            cancelText={t("artifactDetail.cancel")}
            okButtonProps={{ danger: true }}
            onConfirm={() => void deleteReference()}
          >
            <Button
              danger
              size="small"
              icon={<DeleteOutlined />}
              loading={deleting}
            >
              {selectedVersion.kind === "tag"
                ? t("artifactDetail.unlinkTag")
                : t("artifactDetail.deleteManifest")}
            </Button>
          </Popconfirm>
        )}
      </div>

      {selectedVersion && (
        <div className="flex min-w-0 items-center gap-2 rounded border border-line px-3 py-2 text-xs">
          <span className="shrink-0 text-fg-tertiary">Digest</span>
          <code
            className="min-w-0 flex-1 overflow-x-auto whitespace-nowrap text-fg-secondary"
            title={selectedVersion.digest}
          >
            {selectedVersion.digest}
          </code>
          {selectedVersion.kind === "digest" && (
            <Badge tone="warning">{t("artifactDetail.untagged")}</Badge>
          )}
        </div>
      )}
      {selectedVersion && (
        <ArtifactQuarantinePanel
          repositoryId={repositoryId}
          coordinate={image}
          digest={selectedVersion.digest}
          canManage={canQuarantine}
        />
      )}
      {selectedVersion && (
        <ArtifactScanStatus
          repositoryId={repositoryId}
          format="oci"
          coordinate={image}
          digest={selectedVersion.digest}
        />
      )}
      {selectedVersion && (
        <ArtifactIntelligencePanel
          repositoryId={repositoryId}
          format="oci"
          coordinate={image}
          digest={selectedVersion.digest}
        />
      )}

      {selectedVersion && (
        <div className="rounded-lg border border-line bg-surface-translucent p-3">
          <div className="mb-2">
            <div className="text-xs font-medium text-fg-secondary">
              {t("artifactDetail.usage")}
            </div>
            <div className="mt-1 text-xs text-fg-tertiary">
              {t("artifactDetail.useTheSelectedTagOrDigestToPull")}
            </div>
          </div>
          <div className="grid gap-3 lg:grid-cols-3">
            {ociUsage(t, repository, image, selectedReference ?? undefined).map(
              (snippet: UsageSnippet) => (
                <UsageSnippetBlock
                  key={snippet.label}
                  snippet={snippet}
                  copied={copiedValue === snippet.code}
                  onCopy={() => void copy(snippet.code)}
                />
              ),
            )}
          </div>
        </div>
      )}

      {manifestLoading ? (
        <Loading label={t("artifactDetail.loadingManifest")} />
      ) : (
        manifest && (
          <OciManifestView
            manifest={manifest}
            config={config}
            manifestSize={selectedVersion?.size}
          />
        )
      )}
    </div>
  );
}
