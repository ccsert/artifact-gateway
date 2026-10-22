import { useState } from "react";
import { Input } from "antd";
import type { Repository } from "../../client";
import { EmptyState, Notice } from "../../components/ui/Feedback";
import { Field } from "../../components/ui/Layout";
import { usePreferences } from "../../lib/preferences";

import { APTSuiteOperations } from "./APTSuiteOperations";
import "./apt.css";

export function APTOperationsTab({
  repo,
  canAdmin,
}: {
  repo: Repository;
  canAdmin: boolean;
}) {
  const { t } = usePreferences();
  const [suite, setSuite] = useState("stable");
  const [query, setQuery] = useState("stable");
  if (!canAdmin)
    return (
      <EmptyState
        title={t("repository.repositoryAdministratorAccessRequired")}
      />
    );
  if (repo.format !== "apt" || repo.type !== "hosted")
    return <EmptyState title={t("repository.aptFirstHostedOnly")} />;
  return (
    <div className="ag-page-stack ag-apt-operations">
      <Notice
        tone="info"
        closable={false}
        title={t("repository.aPTSignedSnapshotsOperatorPreview")}
        description={t(
          "repository.membershipChangesRebuildAndSignACompleteSnapshot",
        )}
      />
      <div className="ag-apt-toolbar">
        <Field
          label={t("repository.suite")}
          hint={t("repository.enterTheExactSuiteForExampleStableOr")}
        >
          <Input.Search
            aria-label={t("repository.suite")}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onSearch={() => {
              if (query.trim()) setSuite(query.trim());
            }}
            enterButton={t("repository.view")}
          />
        </Field>
      </div>
      <APTSuiteOperations
        key={`${repo.id}:${suite}`}
        repo={repo}
        suite={suite}
      />
    </div>
  );
}
