import { useEffect, useState, type Dispatch, type SetStateAction } from "react";
import { SearchOutlined } from "@ant-design/icons";
import type { ArtifactSummary } from "../../client";
import { EmptyState, ErrorBanner, Loading } from "../../components/ui/Feedback";
import { Card } from "../../components/ui/Layout";
import {
  conanArtifactGroups,
  mavenArtifactGroups,
  mavenVersionKey,
} from "../../lib/publicBrowseModel";
import { usePreferences } from "../../lib/preferences";
import type { UsageSnippet } from "../../lib/usage";
import { ConanGroupTable, type ConanRevisionPage } from "./ConanGroupTable";
import { MavenGroupTable } from "./MavenGroupTable";
import { PublicArtifactTable } from "./PublicArtifactTable";
import { PublicBrowseStateSurface } from "./PublicBrowseChrome";
import {
  buildPublicArtifactRows,
  type PublicArtifactTableRow,
} from "./publicArtifactRows";
import type { PublicRepository } from "./types";

export interface PublicBrowseActions {
  artifactHref: (
    coordinate: string,
    buildNumber?: number,
    tag?: string,
    revision?: string,
    version?: string,
  ) => string;
  openArtifact: (
    coordinate: string,
    buildNumber?: number,
    tag?: string,
    revision?: string,
    version?: string,
  ) => void;
  clearArtifactParams: () => void;
  copyCoordinate: (coordinate: string) => unknown;
  copyPageLink: (href: string) => unknown;
  copyUsage: (snippet: UsageSnippet) => unknown;
  toggleArtifactRow: (row: PublicArtifactTableRow) => void;
  selectOciVersion: (coordinate: string, value: string) => void;
  loadOciTags: (coordinate: string, after?: string) => Promise<void>;
  loadOciManifest: (coordinate: string, tag: string) => Promise<void>;
  loadConanRevisions: (
    coordinate: string,
    query?: string,
    pageToken?: string,
  ) => Promise<void>;
  setSelectedProtocolVersions: Dispatch<SetStateAction<Record<string, string>>>;
  setProtocolVersionFilter: (value: string) => void;
}

/**
 * The selected repository's artifacts: Maven and Conan grouped by component,
 * every other format as one row per artifact.
 */
export function PublicArtifactResults({
  selectedRepository,
  items,
  error,
  artifactRowOptions,
  onRetry,
  artifactParam,
  buildParam,
  revisionParam,
  versionParam,
  copiedCoordinate,
  conanRevisionPages,
  selectedProtocolVersions,
  protocolVersionFilter,
  actions,
}: {
  selectedRepository: PublicRepository | undefined;
  items: ArtifactSummary[] | null;
  error: unknown;
  artifactRowOptions: Omit<
    Parameters<typeof buildPublicArtifactRows>[0],
    "items" | "selectedRepository" | "artifactHref"
  >;
  onRetry: () => void;
  artifactParam: string;
  buildParam: string;
  revisionParam: string;
  versionParam: string;
  copiedCoordinate: string | null;
  conanRevisionPages: Record<string, ConanRevisionPage>;
  selectedProtocolVersions: Record<string, string>;
  protocolVersionFilter: string;
  actions: PublicBrowseActions;
}) {
  const { t } = usePreferences();
  const {
    artifactHref,
    openArtifact,
    clearArtifactParams,
    copyCoordinate,
    copyPageLink,
    copyUsage,
    toggleArtifactRow,
    selectOciVersion,
    loadOciTags,
    loadOciManifest,
    loadConanRevisions,
    setSelectedProtocolVersions,
    setProtocolVersionFilter,
  } = actions;
  const [expandedMavenGroup, setExpandedMavenGroup] = useState<string | null>(
    null,
  );
  const [selectedMavenVersion, setSelectedMavenVersion] = useState<
    string | null
  >(null);
  const [expandedConanGroup, setExpandedConanGroup] = useState<string | null>(
    null,
  );
  const [selectedConanReferences, setSelectedConanReferences] = useState<
    Record<string, string>
  >({});

  useEffect(() => {
    setExpandedConanGroup(null);
    setSelectedConanReferences({});
  }, [selectedRepository?.id]);

  const groupedItems =
    selectedRepository?.format === "maven"
      ? mavenArtifactGroups(items ?? [])
      : null;
  const groupedConanItems =
    selectedRepository?.format === "conan"
      ? conanArtifactGroups(items ?? [])
      : null;
  const artifactTableRows = buildPublicArtifactRows({
    ...artifactRowOptions,
    items: items ?? [],
    selectedRepository,
    artifactHref,
  });

  return (
    <div className="grid gap-4">
      {Boolean(error) && items && (
        <PublicBrowseStateSurface>
          <ErrorBanner error={error} tone="warning" onRetry={onRetry} />
        </PublicBrowseStateSurface>
      )}
      {error && !items ? (
        <PublicBrowseStateSurface>
          <ErrorBanner error={error} onRetry={onRetry} />
        </PublicBrowseStateSurface>
      ) : !items ? (
        <PublicBrowseStateSurface>
          <Loading label={t("publicBrowse.loadingPublicArtifacts")} />
        </PublicBrowseStateSurface>
      ) : items.length === 0 ? (
        <PublicBrowseStateSurface>
          <EmptyState
            icon={<SearchOutlined />}
            title={t("publicBrowse.noMatchingPublicArtifacts")}
            hint={t("publicBrowse.emptyArtifactsHint")}
          />
        </PublicBrowseStateSurface>
      ) : (
        <Card>
          <div className="flex items-center justify-between border-b border-line px-4 py-3">
            <div className="text-xs text-fg-tertiary">
              {t("publicBrowse.found")}{" "}
              <span className="font-medium text-fg-secondary">
                {groupedItems?.length ??
                  groupedConanItems?.length ??
                  items.length}
              </span>{" "}
              {t("publicBrowse.artifacts")}
              {(groupedItems || groupedConanItems) && (
                <span className="ml-1 text-fg-disabled">
                  {t("publicBrowse.versions", { versionCount: items.length })}
                </span>
              )}
            </div>
            <div className="text-xs text-fg-disabled">
              {t("publicBrowse.anonymousReadOnly")}
            </div>
          </div>
          {groupedItems && selectedRepository ? (
            <MavenGroupTable
              groups={groupedItems}
              repository={selectedRepository}
              artifactParam={artifactParam}
              buildParam={buildParam}
              expandedGroup={expandedMavenGroup}
              selectedVersionKey={selectedMavenVersion}
              copiedCoordinate={copiedCoordinate}
              onExpand={(groupKey, versionKey) => {
                setExpandedMavenGroup(groupKey);
                setSelectedMavenVersion(versionKey);
              }}
              onCollapse={() => {
                setExpandedMavenGroup(null);
                clearArtifactParams();
              }}
              onSelectVersion={(group, versionKey) => {
                setSelectedMavenVersion(versionKey);
                const version = group.versions.find(
                  (item, index) => mavenVersionKey(item, index) === versionKey,
                );
                if (version)
                  openArtifact(version.coordinate, version.buildNumber);
              }}
              artifactHref={artifactHref}
              onCopyPageLink={(href) => void copyPageLink(href)}
              onCopyUsage={(snippet) => void copyUsage(snippet)}
            />
          ) : groupedConanItems && selectedRepository ? (
            <ConanGroupTable
              groups={groupedConanItems}
              repository={selectedRepository}
              artifactParam={artifactParam}
              revisionParam={revisionParam}
              expandedGroup={expandedConanGroup}
              selectedReferences={selectedConanReferences}
              selectedRevisions={selectedProtocolVersions}
              revisionPages={conanRevisionPages}
              versionFilter={protocolVersionFilter}
              copiedCoordinate={copiedCoordinate}
              onExpand={(key, reference) => {
                setExpandedConanGroup(key);
                setSelectedConanReferences((current) => ({
                  ...current,
                  [key]: reference,
                }));
              }}
              onCollapse={() => setExpandedConanGroup(null)}
              onSelectReference={(key, reference) =>
                setSelectedConanReferences((current) => ({
                  ...current,
                  [key]: reference,
                }))
              }
              onSelectRevision={(reference, revision) =>
                setSelectedProtocolVersions((current) => ({
                  ...current,
                  [reference]: revision,
                }))
              }
              onFilterChange={setProtocolVersionFilter}
              onLoadRevisions={(reference, filter, pageToken) =>
                void loadConanRevisions(reference, filter, pageToken)
              }
              onOpenArtifact={(reference, revision) =>
                openArtifact(reference, undefined, undefined, revision)
              }
              onClearArtifactParams={clearArtifactParams}
              artifactHref={(reference, revision) =>
                artifactHref(reference, undefined, undefined, revision)
              }
              onCopyCoordinate={(value) => void copyCoordinate(value)}
              onCopyPageLink={(value) => void copyPageLink(value)}
              onCopyUsage={(snippet) => void copyUsage(snippet)}
            />
          ) : (
            <PublicArtifactTable
              rows={artifactTableRows}
              repository={selectedRepository!}
              artifactParam={artifactParam}
              versionParam={versionParam}
              copiedCoordinate={copiedCoordinate}
              artifactHref={artifactHref}
              onToggleRow={toggleArtifactRow}
              onOpenArtifact={openArtifact}
              onSelectOciVersion={selectOciVersion}
              onLoadOciTags={loadOciTags}
              onLoadOciManifest={loadOciManifest}
              onCopyCoordinate={copyCoordinate}
              onCopyPageLink={copyPageLink}
              onCopyUsage={copyUsage}
            />
          )}
        </Card>
      )}
    </div>
  );
}
