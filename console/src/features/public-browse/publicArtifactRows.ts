import type { Translate } from "../../lib/preferences";
import type { ArtifactSummary } from "../../client";
import { shortDigest } from "../../lib/format";
import { usageFor, type UsageSnippet } from "../../lib/usage";
import type { PublicRepository } from "./types";
import type { OciManifestDetail, OciTagPage } from "./usePublicVersionPages";
import { formatDescriptor } from "../../formats";

export type ProtocolVersion = {
  value: string;
  label: string;
  searchText: string;
  createdAt?: string;
  digest?: string;
};

export interface PublicArtifactTableRow {
  key: string;
  item: ArtifactSummary;
  isOci: boolean;
  expanded: boolean;
  page?: OciTagPage;
  protocolVersions: ProtocolVersion[];
  protocolVersionsLoaded: boolean;
  protocolVersionsLoading: boolean;
  protocolVersionsError?: string;
  nextProtocolPage?: string;
  selectedProtocolVersionValue: string;
  selectedProtocolVersionItem?: ProtocolVersion;
  selectedTag?: string;
  ociDetail?: OciManifestDetail;
  selectedProtocolHref: string;
  snippets: UsageSnippet[];
}

type ArtifactHref = (
  coordinate: string,
  buildNumber?: number,
  tag?: string,
) => string;

/**
 * One table row per artifact of the generic (non-grouped) formats, with the
 * OCI version the row currently shows: the visitor's pick, else the
 * deep-linked tag, else the first loaded tag.
 */
export function buildPublicArtifactRows({
  items,
  selectedRepository,
  expandedCoordinate,
  artifactParam,
  tagParam,
  ociTagPages,
  ociManifestDetails,
  selectedProtocolVersions,
  artifactHref,
  t,
}: {
  items: ArtifactSummary[];
  selectedRepository: PublicRepository | undefined;
  expandedCoordinate: string | null;
  artifactParam: string;
  tagParam: string;
  ociTagPages: Record<string, OciTagPage>;
  ociManifestDetails: Record<string, OciManifestDetail>;
  selectedProtocolVersions: Record<string, string>;
  artifactHref: ArtifactHref;
  t: Translate;
}): PublicArtifactTableRow[] {
  return items.map((item, index) => {
    const isOci =
      formatDescriptor(selectedRepository?.format)?.publicVersionSource ===
      "ociTags";
    const expanded =
      expandedCoordinate === item.coordinate ||
      artifactParam === item.coordinate;
    const page = isOci ? ociTagPages[item.coordinate] : undefined;
    const tags = isOci ? (page?.items ?? []) : [];
    const protocolVersions: ProtocolVersion[] = isOci
      ? [
          ...new Set(
            tagParam &&
              artifactParam === item.coordinate &&
              !tags.includes(tagParam)
              ? [...tags, tagParam]
              : tags,
          ),
        ].map((tag) => ({
          value: tag,
          label: tag.startsWith("sha256:")
            ? t("publicBrowse.untaggedDigest", { digest: shortDigest(tag) })
            : tag,
          searchText: tag,
          digest: tag.startsWith("sha256:") ? tag : undefined,
        }))
      : [];
    const protocolVersionsLoaded = isOci ? page?.loaded === true : true;
    const protocolVersionsLoading = isOci ? page?.loading === true : false;
    const protocolVersionsError = isOci ? page?.error : undefined;
    const nextProtocolPage = isOci ? page?.nextCursor : undefined;
    const preferredProtocolVersion =
      (selectedProtocolVersions[item.coordinate] &&
        protocolVersions.some(
          (version) =>
            version.value === selectedProtocolVersions[item.coordinate],
        ) &&
        selectedProtocolVersions[item.coordinate]) ||
      (tagParam &&
        artifactParam === item.coordinate &&
        protocolVersions.some((version) => version.value === tagParam) &&
        tagParam) ||
      protocolVersions[0]?.value ||
      "";
    const selectedProtocolVersionValue = protocolVersions.some(
      (version) => version.value === preferredProtocolVersion,
    )
      ? preferredProtocolVersion
      : protocolVersions[0]?.value || preferredProtocolVersion;
    const selectedProtocolVersionItem = protocolVersions.find(
      (version) => version.value === selectedProtocolVersionValue,
    );
    const selectedTag = isOci ? selectedProtocolVersionItem?.value : undefined;
    const ociDetail =
      isOci && selectedTag
        ? ociManifestDetails[`${item.coordinate}\x00${selectedTag}`]
        : undefined;
    return {
      key: `${item.coordinate}-${index}`,
      item,
      isOci,
      expanded,
      page,
      protocolVersions,
      protocolVersionsLoaded,
      protocolVersionsLoading,
      protocolVersionsError,
      nextProtocolPage,
      selectedProtocolVersionValue,
      selectedProtocolVersionItem,
      selectedTag,
      ociDetail,
      selectedProtocolHref: artifactHref(
        item.coordinate,
        undefined,
        selectedTag,
      ),
      snippets: selectedRepository
        ? usageFor(
            t,
            selectedRepository.format,
            selectedRepository.name,
            item.coordinate,
            selectedTag,
          )
        : [],
    };
  });
}
