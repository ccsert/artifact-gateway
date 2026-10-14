import { type ReactNode } from "react";
import { Button, Collapse, Popover, Tooltip } from "antd";
import { InfoCircleOutlined } from "@ant-design/icons";
import { Link } from "react-router-dom";
import type {
  Repository,
  RepositoryCapacity,
  RepositoryEffectiveAccess,
} from "../../client";
import { AccessDecisionSummary } from "../access-control/AccessDecisionSummary";
import { FormatBadge, StateBadge } from "../../components/ui/Badge";
import { IdentitySummary } from "../identity/IdentitySummary";
import { Card } from "../../components/ui/Layout";
import { formatBytes, formatNumber } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";
import { CopyButton } from "./RepositoryUsageGuides";

export function RepositoryTabSurface({
  standalone,
  children,
}: {
  standalone: boolean;
  children: ReactNode;
}) {
  return standalone ? children : <Card bodyClassName="p-4">{children}</Card>;
}

export function RepositoryConceptHelp({ repo }: { repo: Repository }) {
  const { t } = usePreferences();
  const typeLabel =
    repo.type === "proxy" ? "Proxy Repository" : "Hosted Repository";
  const concepts = [
    [
      "Repository",
      t("repository.aFormatNamespaceContainingAccessPolicyArtifactsAnd"),
    ],
    [
      typeLabel,
      repo.type === "proxy"
        ? t("repository.fetchesAndCachesUpstreamResponsesOnDemandPublishing")
        : t(
            "repository.storesVerifiedPublishedArtifactsAndSupportsDeletionRestore",
          ),
    ],
    ["Artifact", t("repository.aUserVisibleLogicalIdentitySuchAsA")],
    ["Asset", t("repository.anImmutableFileOrBlobUnderAnArtifact")],
    ...(repo.type === "proxy"
      ? [["Cache Entry", t("repository.anIndexAndBytesForAnUpstreamResponse")]]
      : []),
    ...(repo.type === "hosted"
      ? [
          [
            "Publication",
            t("repository.turnsCompleteValidatedStagedContentIntoAVisible"),
          ],
          [
            "Tombstone",
            t("repository.aRestorableDeletionRecordBytesAreReclaimedAfter"),
          ],
          [
            "Retention Policy",
            t("repository.selectsExpiredVersionsOrPathsByFormatRules"),
          ],
        ]
      : []),
  ];

  return (
    <Popover
      placement="bottomRight"
      title={t("repository.concepts")}
      content={
        <div className="grid max-w-[34rem] grid-cols-2 gap-x-5 gap-y-3 text-xs">
          {concepts.map(([term, description]) => (
            <div key={term}>
              <div className="font-medium text-fg">{term}</div>
              <div className="mt-0.5 leading-5 text-fg-tertiary">
                {description}
              </div>
            </div>
          ))}
        </div>
      }
    >
      <Tooltip title={t("repository.viewConcepts")}>
        <Button
          type="text"
          size="small"
          icon={<InfoCircleOutlined />}
          aria-label={t("repository.viewConcepts")}
        />
      </Tooltip>
    </Popover>
  );
}

export function RepositorySummary({
  repo,
  capacity,
  onOpenCapacity,
}: {
  repo: Repository;
  capacity: RepositoryCapacity | null;
  onOpenCapacity?: () => void;
}) {
  const { t } = usePreferences();
  const protocolPath = `${window.location.origin}/${repo.format}/${repo.name}`;

  return (
    <div
      className="border-b border-line pb-3"
      role="group"
      aria-label={t("repository.repositorySummary")}
    >
      <div className="ag-repository-summary-heading flex min-w-0 items-start justify-between gap-6">
        <div className="min-w-0">
          <div className="flex min-w-0 items-center gap-2">
            <h1 className="truncate text-xl font-semibold text-fg-strong">
              {repo.name}
            </h1>
            <FormatBadge format={repo.format} />
            <StateBadge state={repo.state} />
          </div>
          <div className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-fg-tertiary">
            <span>{repo.type ?? "hosted"}</span>
            <span aria-hidden="true">·</span>
            <span>
              {repo.anonymousRead
                ? t("repository.anonymousReads")
                : t("repository.privateReads")}
            </span>
            <span aria-hidden="true">·</span>
            <span className="font-mono">ID {repo.id}</span>
            <span aria-hidden="true">·</span>
            <span>v{repo.version}</span>
          </div>
        </div>
        <div className="ag-repository-summary-endpoint flex min-w-0 shrink-0 items-center gap-2 pt-0.5">
          <RepositoryConceptHelp repo={repo} />
          <span className="text-xs text-fg-tertiary">
            {t("repository.protocolEndpoint")}
          </span>
          <code
            className="min-w-0 max-w-[32rem] flex-1 truncate font-mono text-xs text-fg-secondary"
            title={protocolPath}
          >
            {protocolPath}
          </code>
          <CopyButton text={protocolPath} />
        </div>
      </div>
      <div className="mt-2 flex items-center gap-4 text-xs">
        {repo.anonymousRead && (
          <Link
            to={`/browse?repository=${encodeURIComponent(repo.id)}`}
            className="font-medium text-[var(--ag-link)] hover:text-[var(--ag-link-hover)]"
          >
            {t("repository.openPublicBrowser")}
          </Link>
        )}
        {onOpenCapacity && (
          <Button
            type="link"
            size="small"
            className="h-auto p-0 text-xs text-fg-secondary!"
            onClick={onOpenCapacity}
          >
            {capacity
              ? t("repository.objects2", {
                  bytes: formatBytes(capacity.usedBytes),
                  number: formatNumber(capacity.objectCount),
                })
              : t("repository.viewCapacity")}
          </Button>
        )}
      </div>
    </div>
  );
}

export function EffectiveAccessPanel({
  effectiveAccess,
}: {
  effectiveAccess: RepositoryEffectiveAccess;
}) {
  const { t } = usePreferences();
  return (
    <Collapse
      ghost
      className="mb-4 border-b border-line"
      items={[
        {
          key: "effective-access",
          label: (
            <span className="text-xs text-fg-secondary">
              {t("repository.effectiveAccess")}
              <span className="ml-2 font-mono text-fg-disabled">
                {effectiveAccess.actor}
              </span>
            </span>
          ),
          children: (
            <div className="border-t border-line pt-3 text-xs">
              <IdentitySummary identity={effectiveAccess.identity} />
              <div className="mt-4">
                <AccessDecisionSummary access={effectiveAccess} />
              </div>
            </div>
          ),
        },
      ]}
    />
  );
}
