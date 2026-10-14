import {
  ArrowDownOutlined,
  ArrowUpOutlined,
  CloseOutlined,
  PlusOutlined,
} from "@ant-design/icons";
import { Button, Tooltip } from "antd";
import type { Repository } from "../../client";
import { usePreferences } from "../../lib/preferences";

// 成员排序选择器：左侧候选仓库（点击添加），右侧已选成员（上下移动调序、移除）。
// 比"按点击顺序编号"更直观——顺序就是列表顺序，可直接调整。

function TypeDot({ type }: { type?: "hosted" | "proxy" }) {
  const proxy = type === "proxy";
  return (
    <span
      title={proxy ? "proxy" : "hosted"}
      className={`inline-block h-1.5 w-1.5 shrink-0 rounded-full ${proxy ? "bg-[var(--ag-content-disabled)]" : "bg-[var(--ag-content-secondary)]"}`}
    />
  );
}

export function MemberOrderPicker({
  candidates,
  memberIds,
  onChange,
}: {
  candidates: Repository[];
  memberIds: string[];
  onChange: (ids: string[]) => void;
}) {
  const { t } = usePreferences();
  const repoOf = (id: string) => candidates.find((r) => r.id === id);
  const repoName = (id: string) => repoOf(id)?.name ?? id.slice(0, 8) + "…";
  const available = candidates.filter((r) => !memberIds.includes(r.id));

  const move = (index: number, dir: -1 | 1) => {
    const next = [...memberIds];
    const target = index + dir;
    if (target < 0 || target >= next.length) return;
    [next[index], next[target]] = [next[target], next[index]];
    onChange(next);
  };

  const remove = (id: string) => onChange(memberIds.filter((x) => x !== id));
  const add = (id: string) => onChange([...memberIds, id]);

  return (
    <div className="grid grid-cols-2 gap-3">
      {/* 候选 */}
      <div>
        <div className="mb-1.5 text-xs text-fg-tertiary">
          {t("groups.available", { length: available.length })}
        </div>
        <div className="max-h-56 space-y-1 overflow-y-auto rounded-lg border border-line p-1.5">
          {available.length === 0 && (
            <div className="px-2 py-4 text-center text-xs text-fg-disabled">
              {t("groups.allAdded")}
            </div>
          )}
          {available.map((r) => (
            <Button
              key={r.id}
              type="text"
              onClick={() => add(r.id)}
              block
              icon={<PlusOutlined />}
              iconPlacement="end"
              className="!flex !h-auto !justify-between !px-2.5 !py-1.5 !text-left !text-xs"
            >
              <span className="flex items-center gap-1.5 font-mono">
                <TypeDot type={r.type} />
                {r.name}
              </span>
            </Button>
          ))}
        </div>
      </div>
      {/* 已选（有序） */}
      <div>
        <div className="mb-1.5 text-xs text-fg-tertiary">
          {t("groups.configuredOrderWithinEachType", {
            length: memberIds.length,
          })}
        </div>
        <div className="max-h-56 space-y-1 overflow-y-auto rounded-lg border border-line p-1.5">
          {memberIds.length === 0 && (
            <div className="px-2 py-4 text-center text-xs text-fg-disabled">
              {t("groups.addMembersFromTheLeft")}
            </div>
          )}
          {memberIds.map((id, i) => (
            <div
              key={id}
              className="flex items-center gap-1 rounded-md bg-[var(--ag-surface-hover)] px-2 py-1.5"
            >
              <span className="w-5 shrink-0 text-center font-mono text-xs text-[var(--ag-content-secondary)]">
                {i + 1}
              </span>
              <TypeDot type={repoOf(id)?.type} />
              <span
                className="min-w-0 flex-1 truncate font-mono text-xs text-fg"
                title={repoName(id)}
              >
                {repoName(id)}
              </span>
              <Tooltip title={t("groups.moveUp")}>
                <Button
                  type="text"
                  size="small"
                  aria-label={t("groups.moveUp")}
                  icon={<ArrowUpOutlined />}
                  onClick={() => move(i, -1)}
                  disabled={i === 0}
                />
              </Tooltip>
              <Tooltip title={t("groups.moveDown")}>
                <Button
                  type="text"
                  size="small"
                  aria-label={t("groups.moveDown")}
                  icon={<ArrowDownOutlined />}
                  onClick={() => move(i, 1)}
                  disabled={i === memberIds.length - 1}
                />
              </Tooltip>
              <Tooltip title={t("groups.remove")}>
                <Button
                  type="text"
                  size="small"
                  danger
                  aria-label={t("groups.remove")}
                  icon={<CloseOutlined />}
                  onClick={() => remove(id)}
                />
              </Tooltip>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}
