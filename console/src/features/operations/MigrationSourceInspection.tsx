import { Button } from "antd";
import {
  preflightMigrationSource,
  type MigrationSource,
  type MigrationSourcePreflight,
} from "../../client";
import { EmptyState, ErrorBanner, Loading } from "../../components/ui/Feedback";
import { Card } from "../../components/ui/Layout";
import { usePreferences } from "../../lib/preferences";
import { assertSourceSnapshot, migrationRequest } from "./migrationModel";
import { useMigrationRead } from "./migrationQueries";
import { MigrationRepositoryPlanning } from "./MigrationRepositoryPlanning";

export function MigrationSourceInspection({
  source,
}: {
  source: MigrationSource;
}) {
  const { t } = usePreferences();
  const preflight = useMigrationRead<MigrationSourcePreflight>();
  const run = () =>
    preflight.run(async (signal) => {
      const result = await migrationRequest(
        preflightMigrationSource({ path: { sourceId: source.id }, signal }),
      );
      assertSourceSnapshot(result, source);
      return result;
    });
  return (
    <div className="ag-page-stack ag-migration-inspection">
      {preflight.error !== null && (
        <ErrorBanner
          error={preflight.error}
          tone={preflight.data ? "warning" : "error"}
          onRetry={() => {
            void run();
          }}
        />
      )}
      {preflight.error !== null && preflight.data && (
        <p className="text-sm text-fg-secondary">
          {t("migration.previousResult")}
        </p>
      )}
      <Card>
        <div className="ag-migration-header">
          <h2>{t("migration.preflightTitle")}</h2>
          <Button
            type="primary"
            loading={preflight.busy}
            onClick={() => {
              void run();
            }}
          >
            {t("migration.preflight")}
          </Button>
        </div>
        {!preflight.data ? (
          preflight.busy ? (
            <Loading />
          ) : (
            preflight.error === null && (
              <EmptyState
                title={t("migration.preflightEmpty")}
                hint={t("migration.preflightEmptyHint")}
              />
            )
          )
        ) : (
          <MigrationRepositoryPlanning
            key={preflight.revision}
            source={source}
            preflight={preflight.data}
          />
        )}
      </Card>
    </div>
  );
}
