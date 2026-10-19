import { useState } from "react";
import { useSearchParams } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import {
  DeleteOutlined,
  EditOutlined,
  PlusOutlined,
  ReloadOutlined,
} from "@ant-design/icons";
import { Button } from "antd";
import { deleteMigrationSource, type MigrationSource } from "../../client";
import {
  EmptyState,
  ErrorBanner,
  Loading,
  Notice,
} from "../../components/ui/Feedback";
import { Card, Pagination } from "../../components/ui/Layout";
import { ConfirmDanger } from "../../components/ui/Modal";
import { usePreferences } from "../../lib/preferences";
import { MigrationSourceEditor } from "./MigrationSourceEditor";
import { MigrationSourceInspection } from "./MigrationSourceInspection";
import { migrationRequest } from "./migrationModel";
import {
  migrationKeys,
  migrationSourceOptions,
  useMigrationSource,
  useMigrationSources,
} from "./migrationQueries";
import "./migration.css";

/** Administrator route owns access. URL state contains only the source ID. */
export function MigrationSourcesPanel() {
  const { t } = usePreferences();
  const queryClient = useQueryClient();
  const [params, setParams] = useSearchParams();
  const sourceId = params.get("source") ?? "";
  const list = useMigrationSources();
  const detail = useMigrationSource(sourceId);
  const sources = list.data?.pages.flatMap((page) => page.items) ?? null;
  const [editor, setEditor] = useState<{
    session: number;
    source: MigrationSource | null;
  } | null>(null);
  const [remove, setRemove] = useState<MigrationSource | null>(null);
  const select = (id: string) => {
    const next = new URLSearchParams(params);
    if (id) next.set("source", id);
    else next.delete("source");
    setParams(next);
  };
  const openEditor = (source: MigrationSource | null) => {
    setEditor((current) => ({ session: (current?.session ?? 0) + 1, source }));
  };
  const invalidate = () => {
    void queryClient.invalidateQueries({ queryKey: migrationKeys.all });
  };
  return (
    <div
      className="ag-page-stack ag-migration-panel"
      data-testid="migration-panel"
    >
      <Notice
        tone="info"
        title={t("migration.scope")}
        description={t("migration.scopeHint")}
      />
      {list.error !== null && (
        <ErrorBanner
          error={list.error}
          tone={sources ? "warning" : "error"}
          onRetry={() => {
            void list.refetch();
          }}
        />
      )}
      <Card>
        <div className="ag-migration-header">
          <h2>{t("migration.sources")}</h2>
          <div className="ag-migration-actions">
            <Button
              icon={<ReloadOutlined aria-hidden />}
              loading={list.isFetching}
              onClick={() => {
                void list.refetch();
              }}
            >
              {t("migration.refresh")}
            </Button>
            <Button
              type="primary"
              icon={<PlusOutlined aria-hidden />}
              onClick={() => openEditor(null)}
            >
              {t("migration.newSource")}
            </Button>
          </div>
        </div>
        {sources === null ? (
          list.error === null && <Loading />
        ) : sources.length === 0 ? (
          <EmptyState
            title={t("migration.noSources")}
            hint={t("migration.noSourcesHint")}
          />
        ) : (
          <ul className="ag-migration-source-list">
            {sources.map((source) => (
              <li key={source.id}>
                <button
                  type="button"
                  className="ag-migration-source-choice"
                  aria-pressed={source.id === sourceId}
                  onClick={() => select(source.id)}
                >
                  <span className="font-medium">{source.name}</span>
                  <span className="ag-migration-technical">
                    {source.baseUrl}
                  </span>
                  <span className="text-xs text-fg-secondary">
                    {t(
                      source.credentialsConfigured
                        ? "migration.credentialsConfigured"
                        : "migration.credentialsAbsent",
                    )}{" "}
                    · {t("migration.version", { version: source.version })}
                  </span>
                </button>
              </li>
            ))}
          </ul>
        )}
        <Pagination
          hasMore={list.hasNextPage}
          loading={list.isFetchingNextPage}
          onMore={() => {
            void list.fetchNextPage();
          }}
          label={t("migration.moreSources")}
        />
      </Card>
      {!sourceId ? (
        <EmptyState
          compact
          title={t("migration.selectSource")}
          hint={t("migration.selectSourceHint")}
        />
      ) : (
        <>
          {detail.error !== null && (
            <ErrorBanner
              error={detail.error}
              tone={detail.data ? "warning" : "error"}
              onRetry={() => {
                void detail.refetch();
              }}
            />
          )}
          {!detail.data ? (
            detail.error === null && <Loading />
          ) : (
            <>
              <Card>
                <div className="ag-migration-header">
                  <div className="min-w-0">
                    <h2>{detail.data.name}</h2>
                    <p className="ag-migration-technical">
                      {detail.data.baseUrl}
                    </p>
                    <p className="text-xs text-fg-secondary">
                      {t("migration.version", { version: detail.data.version })}
                    </p>
                  </div>
                  <div className="ag-migration-actions">
                    <Button
                      icon={<ReloadOutlined aria-hidden />}
                      loading={detail.isFetching}
                      onClick={() => {
                        void detail.refetch();
                      }}
                    >
                      {t("migration.refresh")}
                    </Button>
                    <Button
                      icon={<EditOutlined aria-hidden />}
                      onClick={() => openEditor(detail.data)}
                    >
                      {t("migration.edit")}
                    </Button>
                    <Button
                      danger
                      icon={<DeleteOutlined aria-hidden />}
                      onClick={() => setRemove(detail.data)}
                    >
                      {t("migration.delete")}
                    </Button>
                  </div>
                </div>
              </Card>
              <MigrationSourceInspection
                key={`${detail.data.id}:${detail.data.version}`}
                source={detail.data}
              />
            </>
          )}
        </>
      )}
      {editor && (
        <MigrationSourceEditor
          key={editor.session}
          source={editor.source}
          onClose={() => setEditor(null)}
          onSaved={(saved) => {
            queryClient.setQueryData(migrationKeys.detail(saved.id), saved);
            setEditor(null);
            select(saved.id);
            invalidate();
          }}
          onReload={async () => {
            if (!editor.source) return;
            const source = await queryClient.fetchQuery(
              migrationSourceOptions(editor.source.id),
            );
            queryClient.setQueryData(migrationKeys.detail(source.id), source);
            setEditor({ session: editor.session + 1, source });
            invalidate();
          }}
        />
      )}
      <ConfirmDanger
        target={remove}
        title={t("migration.deleteTitle")}
        message={(source) => t("migration.deleteHint", { name: source.name })}
        confirmLabel={t("migration.delete")}
        onClose={() => setRemove(null)}
        onConfirm={async (source) => {
          await migrationRequest(
            deleteMigrationSource({
              path: { sourceId: source.id },
              headers: { "If-Match": source.version },
            }),
          );
          queryClient.removeQueries({
            queryKey: migrationKeys.detail(source.id),
          });
          if (source.id === sourceId) select("");
          invalidate();
        }}
      />
    </div>
  );
}
