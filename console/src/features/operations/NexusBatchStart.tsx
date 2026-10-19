import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Button, Checkbox, Select } from "antd";
import { Link } from "react-router-dom";
import {
  preflightMigrationSource,
  startNexusMigrationBatch,
  type MigrationSourcePreflight,
  type NexusMigrationBatch,
  type NexusMigrationCapabilities,
} from "../../client";
import {
  EmptyState,
  ErrorBanner,
  Loading,
  Notice,
} from "../../components/ui/Feedback";
import { Card, Field, Pagination } from "../../components/ui/Layout";
import { usePreferences } from "../../lib/preferences";
import { useRepositoryPages } from "../repository/repositoryQueries";
import { assertSourceSnapshot, migrationRequest } from "./migrationModel";
import {
  useMigrationRead,
  useMigrationSource,
  useMigrationSources,
} from "./migrationQueries";
import {
  batchRequest,
  capacityEvidence,
  emptyCapacity,
  type BatchDraft,
} from "./nexusBatchModel";
import { batchOptions, useBatchCommand } from "./nexusBatchQueries";
import { NexusCapacityFields } from "./NexusCapacityFields";

export function NexusBatchStart({
  draft,
  binding,
  onClose,
  onCreated,
}: {
  draft: BatchDraft | null;
  binding: NexusMigrationCapabilities;
  onClose: () => void;
  onCreated: (batch: NexusMigrationBatch) => void;
}) {
  const { t } = usePreferences();
  const client = useQueryClient();
  const [sourceId, setSourceId] = useState(draft?.sourceId ?? "");
  const sources = useMigrationSources();
  const source = useMigrationSource(sourceId);
  const repositories = useRepositoryPages();
  const targets = repositories.data?.pages.flatMap((page) => page.items) ?? [];
  const preflight = useMigrationRead<MigrationSourcePreflight>();
  const [selected, setSelected] = useState(
    draft?.mappings.map((mapping) => mapping.sourceRepository) ?? [],
  );
  const [chosen, setChosen] = useState<Record<string, string>>({});
  const [capacity, setCapacity] = useState(emptyCapacity);
  const command = useBatchCommand();
  const sourceOptions = sources.data?.pages.flatMap((page) => page.items) ?? [];
  const supported =
    preflight.data?.repositories.filter(
      (item) =>
        item.type === "hosted" &&
        item.status === "supported" &&
        ["raw", "maven2"].includes(item.format),
    ) ?? [];
  const selectedRepositories = supported.filter((item) =>
    selected.includes(item.name),
  );
  const targetFor = (name: string) => {
    if (Object.hasOwn(chosen, name)) return chosen[name];
    const seed =
      draft?.sourceId === sourceId
        ? draft.mappings.find((item) => item.sourceRepository === name)
        : undefined;
    return (
      seed?.targetRepositoryId ??
      targets.find((target) => target.name === seed?.targetName)?.id ??
      ""
    );
  };
  const mappings = selectedRepositories.map((item) => ({
    sourceRepository: item.name,
    targetRepositoryId: targetFor(item.name),
  }));
  let capacityValid = false;
  try {
    capacityEvidence(capacity, binding);
    capacityValid = true;
  } catch {
    /* Unknown is deliberately not executable. */
  }
  const mappingsValid =
    mappings.length > 0 &&
    mappings.length <= 10 &&
    new Set(mappings.map((item) => item.targetRepositoryId)).size ===
      mappings.length &&
    selectedRepositories.every((item) =>
      targets.some(
        (target) =>
          target.id === targetFor(item.name) &&
          target.state === "active" &&
          target.type === "hosted" &&
          target.format === (item.format === "maven2" ? "maven" : "raw"),
      ),
    );
  const sourceValid =
    source.data &&
    preflight.data?.sourceVersion === source.data.version &&
    preflight.data.sourceId === sourceId &&
    !preflight.error &&
    !source.error;
  const runPreflight = () =>
    preflight.run(async (signal) => {
      if (!source.data) throw new Error("missing_source");
      const result = await migrationRequest(
        preflightMigrationSource({ path: { sourceId }, signal }),
      );
      assertSourceSnapshot(result, source.data);
      return result;
    });
  return (
    <Card className="ag-nexus-start">
      <div className="ag-migration-header">
        <h2>{t("nexusBatch.new")}</h2>
        <Button disabled={command.busy} onClick={onClose}>
          {t("nexusBatch.close")}
        </Button>
      </div>
      <form
        className="ag-migration-content"
        onSubmit={(event) => {
          event.preventDefault();
          if (
            !sourceValid ||
            !mappingsValid ||
            command.busy ||
            preflight.busy ||
            repositories.error
          )
            return;
          void command.run(
            {
              operation: "start",
              sourceId,
              sourceVersion: source.data!.version,
              mappings,
              capacity,
            },
            async (key) => {
              const evidence = capacityEvidence(capacity, binding);
              return batchRequest(
                startNexusMigrationBatch({
                  headers: { "Idempotency-Key": key },
                  body: {
                    sourceId,
                    sourceVersion: source.data!.version,
                    mappings,
                    policyVersion: "release-raw-v1",
                    capacityEvidence: evidence,
                  },
                }),
                true,
              );
            },
            async (created) => {
              onCreated(await client.fetchQuery(batchOptions(created.id)));
            },
          );
        }}
      >
        {command.error !== null && <ErrorBanner error={command.error} />}
        {sources.error !== null && (
          <ErrorBanner
            error={sources.error}
            onRetry={() => {
              void sources.refetch();
            }}
          />
        )}
        <Field label={t("nexusBatch.source")}>
          <Select
            aria-label={t("nexusBatch.source")}
            className="w-full"
            value={sourceId || undefined}
            loading={sources.isFetching}
            disabled={command.busy}
            options={sourceOptions.map((item) => ({
              label: item.name,
              value: item.id,
            }))}
            onChange={(id: string) => {
              setSourceId(id);
              setSelected([]);
              setChosen({});
              preflight.reset();
            }}
          />
        </Field>
        <Pagination
          hasMore={sources.hasNextPage}
          loading={sources.isFetchingNextPage}
          onMore={() => {
            void sources.fetchNextPage();
          }}
          label={t("nexusBatch.moreSources")}
        />
        {source.error !== null && (
          <ErrorBanner
            error={source.error}
            onRetry={() => {
              void source.refetch();
            }}
          />
        )}
        {sourceId && !source.data && !source.error && <Loading />}
        {source.data && (
          <div className="ag-migration-actions">
            <span className="ag-migration-technical">
              {source.data.name} ·{" "}
              {t("migration.version", { version: source.data.version })}
            </span>
            <Button
              disabled={
                command.busy || source.isFetching || Boolean(source.error)
              }
              loading={preflight.busy}
              onClick={() => {
                void runPreflight();
              }}
            >
              {t("migration.preflight")}
            </Button>
          </div>
        )}
        {preflight.error !== null && <ErrorBanner error={preflight.error} />}
        {preflight.data && (
          <>
            <p className="text-sm text-fg-secondary">
              {t("nexusBatch.explicitMapping")}
            </p>
            <p className="text-xs text-fg-secondary">
              {t("nexusBatch.targetHint")}{" "}
              <Link to="/repositories">
                {t("nexusBatch.manageRepositories")}
              </Link>
            </p>
            {repositories.error !== null && (
              <ErrorBanner
                error={repositories.error}
                onRetry={() => {
                  void repositories.refetch();
                }}
              />
            )}
            {repositories.isPending && <Loading />}
            {supported.length === 0 ? (
              <EmptyState compact title={t("migration.noRepositories")} />
            ) : (
              <ul className="ag-migration-repositories">
                {supported.map((item) => (
                  <li className="ag-migration-repository" key={item.name}>
                    <Checkbox
                      checked={selected.includes(item.name)}
                      disabled={
                        command.busy ||
                        (!selected.includes(item.name) && selected.length >= 10)
                      }
                      onChange={(event) =>
                        setSelected((previous) =>
                          event.target.checked
                            ? [...previous, item.name]
                            : previous.filter((name) => name !== item.name),
                        )
                      }
                    >
                      <span className="ag-migration-wrap">
                        {item.name} · {item.format}
                      </span>
                    </Checkbox>
                    <Field
                      label={t("nexusBatch.targetFor", { name: item.name })}
                    >
                      <Select
                        aria-label={t("nexusBatch.targetFor", {
                          name: item.name,
                        })}
                        className="w-full"
                        value={targetFor(item.name) || undefined}
                        disabled={command.busy || !selected.includes(item.name)}
                        options={targets
                          .filter(
                            (target) =>
                              target.type === "hosted" &&
                              target.state === "active" &&
                              target.format ===
                                (item.format === "maven2" ? "maven" : "raw"),
                          )
                          .map((target) => ({
                            label: target.name,
                            value: target.id,
                          }))}
                        onChange={(id: string) =>
                          setChosen((previous) => ({
                            ...previous,
                            [item.name]: id,
                          }))
                        }
                      />
                    </Field>
                  </li>
                ))}
              </ul>
            )}
            <Pagination
              hasMore={repositories.hasNextPage}
              loading={repositories.isFetchingNextPage}
              onMore={() => {
                void repositories.fetchNextPage();
              }}
              label={t("nexusBatch.moreRepositories")}
            />
          </>
        )}
        <NexusCapacityFields
          value={capacity}
          onChange={setCapacity}
          binding={binding}
          disabled={command.busy}
        />
        <Notice
          tone="info"
          title={t("nexusBatch.rescanHint")}
          closable={false}
        />
        <div className="ag-migration-actions">
          <Button
            type="primary"
            htmlType="submit"
            loading={command.busy}
            disabled={
              !sourceValid ||
              !mappingsValid ||
              !capacityValid ||
              preflight.busy ||
              Boolean(repositories.error)
            }
          >
            {t("nexusBatch.start")}
          </Button>
        </div>
      </form>
    </Card>
  );
}
