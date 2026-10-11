import {
  MetadataItem,
  UsageSnippetBlock,
} from "../../components/ui/PublicBrowsePrimitives";
import { formatDate } from "../../lib/format";
import { artifactCoordinateForDisplay, formatDescriptor } from "../../formats";
import { formatViews } from "../../formats/views";
import { usePreferences } from "../../lib/preferences";
import type { UsageSnippet } from "../../lib/usage";
import { OciVersionPanel } from "./OciVersionPanel";
import type { PublicArtifactTableRow } from "./publicArtifactRows";
import type { PublicRepository } from "./types";

/** What an expanded artifact row shows, per format. */
export function PublicArtifactDetail({
  row,
  repository,
  artifactParam,
  versionParam,
  copiedCoordinate,
  onOpenArtifact,
  onSelectOciVersion,
  onLoadOciTags,
  onLoadOciManifest,
  onCopyPageLink,
  onCopyUsage,
}: {
  row: PublicArtifactTableRow;
  repository: PublicRepository;
  artifactParam: string;
  versionParam: string;
  copiedCoordinate: string | null;
  onOpenArtifact: (
    coordinate: string,
    buildNumber?: number,
    tag?: string,
    revision?: string,
    version?: string,
  ) => void;
  onSelectOciVersion: (coordinate: string, value: string) => void;
  onLoadOciTags: (coordinate: string, after?: string) => Promise<void>;
  onLoadOciManifest: (coordinate: string, tag: string) => Promise<void>;
  onCopyPageLink: (href: string) => unknown;
  onCopyUsage: (snippet: UsageSnippet) => unknown;
}) {
  const { locale, t } = usePreferences();
  const selectedRepository: PublicRepository | undefined = repository;
  const openArtifact = onOpenArtifact;
  const copyUsage = onCopyUsage;
  const PublicDetail =
    selectedRepository &&
    formatDescriptor(selectedRepository.format)?.publicDetail
      ? formatViews(selectedRepository.format).ArtifactDetail
      : undefined;
  if (selectedRepository && PublicDetail) {
    return (
      <PublicDetail
        repositoryName={selectedRepository.name}
        artifact={row.item}
        initialVersion={
          artifactParam === row.item.coordinate
            ? versionParam || row.item.version
            : row.item.version
        }
        onVersionChange={(version) =>
          openArtifact(
            row.item.coordinate,
            undefined,
            undefined,
            undefined,
            version,
          )
        }
      />
    );
  }
  if (!row.isOci) {
    return (
      <div className="px-2 py-1">
        <div className="mb-4 grid grid-cols-2 gap-x-4 gap-y-3 border-b border-line pb-4 text-xs sm:grid-cols-4">
          <MetadataItem
            label={t("publicBrowse.repository")}
            value={selectedRepository?.name ?? "—"}
            mono
          />
          <MetadataItem
            label={t("publicBrowse.artifactCoordinate")}
            value={artifactCoordinateForDisplay(
              selectedRepository?.format ?? "",
              row.item.coordinate,
            )}
            mono
          />
          <MetadataItem
            label={t("publicBrowse.published")}
            value={formatDate(row.item.createdAt, locale)}
          />
          <MetadataItem
            label={t("publicBrowse.publisher")}
            value={row.item.publisher ?? t("publicBrowse.notRecorded")}
            mono
          />
        </div>
        <div className="grid gap-2 sm:grid-cols-2">
          {row.snippets.map((snippet) => (
            <UsageSnippetBlock
              key={snippet.label}
              snippet={snippet}
              copied={copiedCoordinate === snippet.code}
              onCopy={() => void copyUsage(snippet)}
            />
          ))}
        </div>
      </div>
    );
  }
  return (
    <OciVersionPanel
      row={row}
      copiedCoordinate={copiedCoordinate}
      onSelectVersion={(value) =>
        onSelectOciVersion(row.item.coordinate, value)
      }
      onLoadOciTags={onLoadOciTags}
      onLoadOciManifest={onLoadOciManifest}
      onCopyPageLink={onCopyPageLink}
      onCopyUsage={onCopyUsage}
    />
  );
}
