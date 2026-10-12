import type { Key } from "react";
import type { AptLifecyclePackage } from "../../client";
import { Button, InputNumber } from "antd";
import { Field } from "../../components/ui/Layout";
import { shortDigest } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";
import { ConsoleTable } from "../../components/ui/ConsolePrimitives";

import { packageName } from "./aptModel";

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
  const { text } = usePreferences();
  const packageColumns = [
    {
      title: text("软件包", "Package"),
      key: "package",
      render: (_: unknown, item: AptLifecyclePackage) => (
        <div>
          <strong>{packageName(item)}</strong>
          <div className="ag-apt-secondary">{item.component}</div>
        </div>
      ),
    },
    {
      title: text("规范路径 / SHA-256", "Canonical path / SHA-256"),
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
        <p>
          {text(
            "选择软件包，预览从当前套件移除的影响。",
            "Select packages to preview removal from the current suite.",
          )}
        </p>
        <Button
          danger
          disabled={disabled || !hasCurrent || selected.length === 0}
          onClick={() => onPreviewDelete(selected.map(String))}
        >
          {text("预览删除", "Preview deletion")}
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
        aria-label={text("保留预览", "Retention preview")}
      >
        <h4>{text("按上传时间保留", "Retain by upload time")}</h4>
        <p>
          {text(
            "按组件、包名和架构分组；同时超过保留数量和天数才会入选，不按 Debian 版本排序。",
            "Grouped by component, package and architecture. Only uploads beyond both limits qualify; this is not Debian version ordering.",
          )}
        </p>
        <div className="ag-apt-toolbar">
          <Field label={text("保留最新数量", "Keep latest uploads")}>
            <InputNumber
              aria-label={text("保留最新数量", "Keep latest uploads")}
              min={1}
              precision={0}
              value={keepLatest}
              disabled={disabled}
              onChange={(v) => setKeepLatest(v ?? 1)}
            />
          </Field>
          <Field label={text("早于天数", "Older than days")}>
            <InputNumber
              aria-label={text("早于天数", "Older than days")}
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
            {text("预览保留清理", "Preview retention")}
          </Button>
        </div>
      </section>
    </div>
  );
}
