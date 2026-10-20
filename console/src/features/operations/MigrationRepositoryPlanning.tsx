import { useState } from "react";
import { Button, Checkbox, Input, Tag } from "antd";
import {
  discoverMigrationSource,
  previewMigrationSourceMapping,
  type MigrationSource,
  type MigrationSourceDiscovery,
  type MigrationSourceMapping,
  type MigrationSourceMappingPreview,
  type MigrationSourcePreflight,
} from "../../client";
import { ConsoleTable } from "../../components/ui/ConsolePrimitives";
import {
  EmptyState,
  ErrorBanner,
  Loading,
  Notice,
} from "../../components/ui/Feedback";
import { Field } from "../../components/ui/Layout";
import { formatBytes } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";
import {
  assertSourceSnapshot,
  migrationReason,
  migrationRequest,
  observation,
} from "./migrationModel";
import { useMigrationRead } from "./migrationQueries";
import type { BatchDraft } from "./nexusBatchModel";
import { migrationTargetFormat } from "./migrationFormats";

export function MigrationRepositoryPlanning({
  source,
  preflight,
  onPlanBatch,
}: {
  source: MigrationSource;
  preflight: MigrationSourcePreflight;
  onPlanBatch?: (draft: BatchDraft) => void;
}) {
  const { t } = usePreferences();
  const [selected, setSelected] = useState<string[]>([]);
  const [targets, setTargets] = useState<Record<string, string>>({});
  const discovery = useMigrationRead<MigrationSourceDiscovery>();
  const preview = useMigrationRead<MigrationSourceMappingPreview>();
  const repositories = preflight.repositories.filter((repository) =>
    selected.includes(repository.name),
  );
  const hosted = repositories.filter(
    (repository) =>
      repository.status === "supported" && repository.type === "hosted",
  );
  const busy = discovery.busy || preview.busy;
  const mappings: MigrationSourceMapping[] = repositories.flatMap(
    (repository) => {
      const format = migrationTargetFormat(repository.format);
      const type = repository.type;
      if (
        !format ||
        (type !== "hosted" && type !== "proxy" && type !== "group")
      ) {
        return [];
      }
      return [
        {
          sourceRepository: repository.name,
          targetName: targets[repository.name]?.trim() ?? "",
          format,
          type,
          ...(repository.type === "group"
            ? {
                targetMembers: repository.members
                  .map((member) => targets[member]?.trim() ?? "")
                  .filter(Boolean),
              }
            : {}),
        },
      ];
    },
  );
  const discover = () =>
    discovery.run(async (signal) => {
      const result = await migrationRequest(
        discoverMigrationSource({
          path: { sourceId: source.id },
          body: { repositories: hosted.map((repository) => repository.name) },
          signal,
        }),
      );
      assertSourceSnapshot(result, source);
      return result;
    });
  const checkMapping = () =>
    preview.run(async (signal) => {
      const result = await migrationRequest(
        previewMigrationSourceMapping({
          path: { sourceId: source.id },
          body: { mappings },
          signal,
        }),
      );
      assertSourceSnapshot(result, source);
      return result;
    });
  return (
    <div className="ag-migration-content">
      <p className="text-sm text-fg-secondary">
        {t("migration.visibleScope", { version: preflight.sourceVersion })}
      </p>
      <p className="text-xs text-fg-tertiary">
        {observation(preflight.stats, t)}
      </p>
      {preflight.repositories.length === 0 ? (
        <EmptyState compact title={t("migration.noRepositories")} />
      ) : (
        <>
          <p className="text-xs text-fg-secondary">
            {t("migration.targetNameHint")}
          </p>
          <ul className="ag-migration-repositories">
            {preflight.repositories.map((repository) => {
              const chosen = selected.includes(repository.name);
              const selectable =
                repository.status !== "unsupported" &&
                migrationTargetFormat(repository.format) !== undefined;
              return (
                <li className="ag-migration-repository" key={repository.name}>
                  <div>
                    <Checkbox
                      aria-label={t("migration.chooseRepository", {
                        name: repository.name,
                      })}
                      checked={chosen}
                      disabled={
                        busy ||
                        !selectable ||
                        (!chosen && selected.length >= 10)
                      }
                      onChange={(event) => {
                        discovery.reset();
                        preview.reset();
                        setSelected((current) =>
                          event.target.checked
                            ? [...current, repository.name]
                            : current.filter(
                                (name) => name !== repository.name,
                              ),
                        );
                      }}
                    >
                      <span className="ag-migration-technical">
                        {repository.name}
                      </span>
                    </Checkbox>
                    <div className="ag-migration-notes">
                      <span>
                        {repository.format} · {repository.type}
                        {repository.versionPolicy
                          ? ` · ${repository.versionPolicy}`
                          : ""}
                      </span>
                      <span>
                        <Tag
                          color={
                            repository.status === "supported"
                              ? "success"
                              : "default"
                          }
                        >
                          {t(
                            repository.status === "supported"
                              ? "migration.supported"
                              : repository.status === "preview_only"
                                ? "migration.previewOnly"
                                : "migration.unsupported",
                          )}
                        </Tag>
                      </span>
                      {repository.reason && (
                        <span>{migrationReason(repository.reason, t)}</span>
                      )}
                      {repository.type === "group" && (
                        <>
                          <span>
                            {t("migration.members")}:{" "}
                            {repository.members.join(" → ")}
                          </span>
                          <span>{t("migration.groupHint")}</span>
                        </>
                      )}
                      {repository.fields
                        .filter((field) => field.disposition !== "expressible")
                        .map((field) => (
                          <span key={field.field}>
                            {field.field}:{" "}
                            {t(
                              field.disposition === "manual"
                                ? "migration.reasonFieldManual"
                                : "migration.reasonFieldUnsupported",
                            )}
                          </span>
                        ))}
                    </div>
                  </div>
                  {selectable && (
                    <Field label={t("migration.targetName")}>
                      <Input
                        aria-label={t("migration.targetFor", {
                          name: repository.name,
                        })}
                        value={targets[repository.name] ?? ""}
                        maxLength={63}
                        disabled={!chosen || busy}
                        onChange={(event) => {
                          preview.reset();
                          setTargets((current) => ({
                            ...current,
                            [repository.name]: event.target.value,
                          }));
                        }}
                      />
                    </Field>
                  )}
                </li>
              );
            })}
          </ul>
          <div className="ag-migration-actions">
            <Button
              loading={discovery.busy}
              disabled={busy || hosted.length === 0}
              onClick={() => {
                void discover();
              }}
            >
              {t("migration.discover", { count: hosted.length })}
            </Button>
            <Button
              type="primary"
              loading={preview.busy}
              disabled={
                busy ||
                mappings.length === 0 ||
                mappings.some((mapping) => !mapping.targetName)
              }
              onClick={() => {
                void checkMapping();
              }}
            >
              {t("migration.preview")}
            </Button>
          </div>
        </>
      )}
      {(discovery.busy || discovery.error !== null || discovery.data) && (
        <section
          className="ag-migration-result"
          aria-label={t("migration.discoveryTitle")}
        >
          <h3>{t("migration.discoveryTitle")}</h3>
          {discovery.error !== null && (
            <ErrorBanner
              error={discovery.error}
              tone={discovery.data ? "warning" : "error"}
              onRetry={() => {
                void discover();
              }}
            />
          )}
          {discovery.data ? (
            <>
              {discovery.error !== null && (
                <p className="text-sm text-fg-secondary">
                  {t("migration.previousResult")}
                </p>
              )}
              <p className="text-sm text-fg-secondary">
                {t("migration.discoverySummary", {
                  assets: discovery.data.assets.length,
                  bytes: formatBytes(
                    discovery.data.assets.reduce(
                      (sum, asset) => sum + asset.size,
                      0,
                    ),
                  ),
                  unsupported: discovery.data.unsupportedAssets.length,
                })}
              </p>
              <p className="text-xs text-fg-tertiary">
                {observation(discovery.data.stats, t)}
              </p>
              <ConsoleTable
                dataSource={discovery.data.assets}
                rowKey={(asset) => `${asset.repository}:${asset.path}`}
                tableLayout="fixed"
                size="small"
                locale={{ emptyText: t("migration.noAssets") }}
                columns={[
                  {
                    title: t("migration.sourceRepository"),
                    dataIndex: "repository",
                    width: 96,
                    render: (value: string) => (
                      <span className="ag-migration-wrap">{value}</span>
                    ),
                  },
                  {
                    title: t("migration.path"),
                    dataIndex: "path",
                    render: (value: string, asset) => (
                      <div className="ag-migration-wrap">
                        <span className="font-mono text-xs">{value}</span>
                        {asset.checksum.sha256 && (
                          <p className="text-xs text-fg-tertiary">
                            SHA-256: {asset.checksum.sha256}
                          </p>
                        )}
                      </div>
                    ),
                  },
                  {
                    title: t("migration.size"),
                    dataIndex: "size",
                    width: 96,
                    render: (value: number) => (
                      <span className="ag-migration-wrap">
                        {formatBytes(value)}
                      </span>
                    ),
                  },
                ]}
              />
              {discovery.data.unsupportedAssets.length > 0 && (
                <>
                  <h3>{t("migration.unsupportedAssets")}</h3>
                  <ConsoleTable
                    dataSource={discovery.data.unsupportedAssets}
                    rowKey={(asset) => `${asset.repository}:${asset.path}`}
                    tableLayout="fixed"
                    size="small"
                    columns={[
                      {
                        title: t("migration.path"),
                        dataIndex: "path",
                        render: (value: string, asset) => (
                          <div className="ag-migration-wrap">
                            {asset.repository}:{" "}
                            <span className="font-mono text-xs">{value}</span>
                          </div>
                        ),
                      },
                      {
                        title: t("migration.reason"),
                        dataIndex: "reason",
                        render: (value: string) => (
                          <span className="ag-migration-wrap">
                            {migrationReason(value, t)}
                          </span>
                        ),
                      },
                    ]}
                  />
                </>
              )}
            </>
          ) : (
            discovery.busy && <Loading />
          )}
        </section>
      )}
      {(preview.busy || preview.error !== null || preview.data) && (
        <section
          className="ag-migration-result"
          aria-label={t("migration.mappingTitle")}
        >
          <h3>{t("migration.mappingTitle")}</h3>
          {preview.error !== null && (
            <ErrorBanner
              error={preview.error}
              tone={preview.data ? "warning" : "error"}
              onRetry={() => {
                void checkMapping();
              }}
            />
          )}
          {preview.data ? (
            <>
              {preview.error !== null && (
                <p className="text-sm text-fg-secondary">
                  {t("migration.previousResult")}
                </p>
              )}
              <Notice
                tone={preview.data.valid ? "info" : "warning"}
                title={t(
                  preview.data.valid
                    ? "migration.mappingValid"
                    : "migration.mappingInvalid",
                )}
                closable={false}
              />
              <p className="text-sm text-fg-secondary">
                {t("migration.mappingHint")}
              </p>
              {onPlanBatch &&
                preview.data.valid &&
                !preview.error &&
                hosted.length > 0 && (
                  <div className="ag-migration-actions">
                    <Button
                      type="primary"
                      disabled={busy}
                      onClick={() =>
                        onPlanBatch({
                          sourceId: source.id,
                          mappings: mappings
                            .filter(
                              (mapping) =>
                                mapping.type === "hosted" &&
                                hosted.some(
                                  (item) =>
                                    item.name === mapping.sourceRepository,
                                ),
                            )
                            .map(({ sourceRepository, targetName }) => ({
                              sourceRepository,
                              targetName,
                            })),
                        })
                      }
                    >
                      {t("nexusBatch.useMappings", { count: hosted.length })}
                    </Button>
                  </div>
                )}
              <p className="text-xs text-fg-tertiary">
                {observation(preview.data.stats, t)}
              </p>
              <ul className="ag-migration-repositories">
                {preview.data.mappings.map((mapping) => (
                  <li
                    className="ag-migration-repository"
                    key={mapping.sourceRepository}
                  >
                    <div className="ag-migration-wrap">
                      <span className="font-mono text-xs">
                        {mapping.sourceRepository} → {mapping.targetName}
                      </span>
                      <div className="ag-migration-notes">
                        <span>
                          {t(
                            mapping.status === "ready"
                              ? "migration.ready"
                              : mapping.status === "manual"
                                ? "migration.manual"
                                : mapping.status === "invalid"
                                  ? "migration.invalid"
                                  : "migration.unsupported",
                          )}
                        </span>
                        {mapping.targetMembers.length > 0 && (
                          <span>
                            {t("migration.targetMembers", {
                              members: mapping.targetMembers.join(" → "),
                            })}
                          </span>
                        )}
                      </div>
                    </div>
                    <div className="ag-migration-notes">
                      {mapping.reasons.map((reason, index) => (
                        <span key={`${reason}:${index}`}>
                          {migrationReason(reason, t)}
                        </span>
                      ))}
                      {mapping.fields.map((field) => (
                        <span key={field.field}>
                          {field.field}:{" "}
                          {t(
                            field.disposition === "manual"
                              ? "migration.reasonFieldManual"
                              : field.disposition === "unsupported"
                                ? "migration.reasonFieldUnsupported"
                                : "migration.reasonFieldExpressible",
                          )}
                        </span>
                      ))}
                    </div>
                  </li>
                ))}
              </ul>
            </>
          ) : (
            preview.busy && <Loading />
          )}
        </section>
      )}
    </div>
  );
}
