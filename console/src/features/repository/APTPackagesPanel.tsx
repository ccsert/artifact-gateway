import type { Key } from "react";
import type { AptLifecyclePackage } from "../../client";
import { Button, InputNumber } from "antd";
import { Field } from "../../components/ui/Layout";
import { shortDigest } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";
import { ConsoleTable } from "../../components/ui/ConsolePrimitives";

import { packageName } from "./aptModel";
import "./apt.css";

/**
 * The suite's packages: select some to preview their removal, or preview a
 * retention cleanup by upload count and age.
 */
export function APTPackagesPanel({
  packages,
  disabled,
  hasCurrent,
  selected,
  onSelect,
  keepLatest,
  setKeepLatest,
  olderThanDays,
  setOlderThanDays,
  onPreviewDelete,
  onPreviewRetention,
}: {
  packages: AptLifecyclePackage[];
  disabled: boolean;
  /** Changes preview against the suite's visible snapshot. */
  hasCurrent: boolean;
  selected: Key[];
  onSelect: (keys: Key[]) => void;
  /** Kept by the parent, so switching tabs does not reset them. */
  keepLatest: number;
  setKeepLatest: (value: number) => void;
  olderThanDays: number;
  setOlderThanDays: (value: number) => void;
  onPreviewDelete: (publicationSessionIds: string[]) => void;
  onPreviewRetention: (keepLatest: number, olderThanDays: number) => void;
}) {
  const { t } = usePreferences();
  const packageColumns = [
    {
      title: t("repository.package"),
      key: "package",
      render: (_: unknown, item: AptLifecyclePackage) => (
        <div>
          <strong>{packageName(item)}</strong>
          <div className="ag-apt-secondary">{item.component}</div>
        </div>
      ),
    },
    {
      title: t("repository.canonicalPathSHA256"),
      key: "identity",
      render: (_: unknown, item: AptLifecyclePackage) => (
        <div className="ag-apt-identity">
          <code>{item.poolPath}</code>
          <code title={item.revision.digest}>
            {shortDigest(item.revision.digest)}
          </code>
        </div>
      ),
    },
  ];

  return (
    <div className="ag-page-stack">
      <div className="ag-apt-heading">
        <p>{t("repository.selectPackagesToPreviewRemovalFromTheCurrent")}</p>
        <Button
          danger
          disabled={disabled || !hasCurrent || selected.length === 0}
          onClick={() => onPreviewDelete(selected.map(String))}
        >
          {t("repository.previewDeletion")}
        </Button>
      </div>
      <ConsoleTable
        rowKey="publicationSessionId"
        dataSource={packages}
        columns={packageColumns}
        scroll={{ x: 640 }}
        pagination={{}}
        rowSelection={{
          selectedRowKeys: selected,
          onChange: onSelect,
          getCheckboxProps: () => ({
            disabled: disabled || !hasCurrent,
          }),
        }}
      />
      <section
        className="ag-apt-retention"
        aria-label={t("repository.retentionPreview")}
      >
        <h4>{t("repository.retainByUploadTime")}</h4>
        <p>
          {t("repository.groupedByComponentPackageAndArchitectureOnlyUploads")}
        </p>
        <div className="ag-apt-toolbar">
          <Field label={t("repository.keepLatestUploads")}>
            <InputNumber
              aria-label={t("repository.keepLatestUploads")}
              min={1}
              precision={0}
              value={keepLatest}
              disabled={disabled}
              onChange={(v) => setKeepLatest(v ?? 1)}
            />
          </Field>
          <Field label={t("repository.olderThanDays")}>
            <InputNumber
              aria-label={t("repository.olderThanDays")}
              min={1}
              precision={0}
              value={olderThanDays}
              disabled={disabled}
              onChange={(v) => setOlderThanDays(v ?? 30)}
            />
          </Field>
          <Button
            disabled={disabled || !hasCurrent}
            onClick={() => onPreviewRetention(keepLatest, olderThanDays)}
          >
            {t("repository.previewRetention")}
          </Button>
        </div>
      </section>
    </div>
  );
}
