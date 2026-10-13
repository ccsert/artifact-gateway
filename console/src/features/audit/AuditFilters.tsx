import type { ReactNode } from "react";
import { Collapse, DatePicker, Select } from "antd";
import dayjs from "dayjs";
import { FilterBar, FilterField } from "../../components/ui/ConsolePrimitives";
import { usePreferences } from "../../lib/preferences";
import type { AuditFilters as Filters } from "./auditQueries";

type Option = { value: string; label: string };

function AuditSelect({
  label,
  value,
  placeholder,
  options,
  searchByValue,
  onChange,
}: {
  label: string;
  value: string;
  placeholder: string;
  options: { value: string; label: string }[];
  searchByValue?: boolean;
  onChange: (value: string) => void;
}) {
  return (
    <FilterField label={label}>
      <Select
        className="w-full min-w-[170px]"
        allowClear
        showSearch={{
          // Options carry a readable label and the stored machine code, and a
          // reader may search by either — for operations, pasting the code
          // from a CSV export has to find the row it names.
          optionFilterProp: searchByValue ? ["label", "value"] : "label",
        }}
        value={value || undefined}
        placeholder={placeholder}
        options={options}
        onChange={(next) => onChange(next ?? "")}
      />
    </FilterField>
  );
}

/** The audit filters: common ones up front, the rest under "Advanced". */
export function AuditFilters({
  filters,
  onChange,
  repositoryOptions,
  groupOptions,
  outcomeOptions,
  operationOptions,
  actorOptions,
  formatOptions,
  actions,
}: {
  filters: Filters;
  onChange: (change: Partial<Filters>) => void;
  repositoryOptions: string[];
  groupOptions: string[];
  outcomeOptions: Option[];
  operationOptions: Option[];
  actorOptions: Option[];
  formatOptions: Option[];
  actions: ReactNode;
}) {
  const { text } = usePreferences();
  return (
    <>
      <FilterBar embedded actions={actions}>
        <AuditSelect
          label={text("仓库", "Repository")}
          value={filters.repository}
          placeholder={text("全部仓库", "All repositories")}
          options={repositoryOptions.map((value) => ({ value, label: value }))}
          onChange={(repository) => onChange({ repository })}
        />
        <AuditSelect
          label={text("结果", "Outcome")}
          value={filters.outcome}
          placeholder={text("全部结果", "All outcomes")}
          options={outcomeOptions}
          searchByValue
          onChange={(outcome) => onChange({ outcome })}
        />
        <FilterField label={text("加载窗口", "Load window")}>
          <Select
            className="min-w-[150px]"
            value={String(filters.limit)}
            options={[50, 100, 200].map((value) => ({
              value: String(value),
              label: text(`最近 ${value} 条`, `Latest ${value}`),
            }))}
            onChange={(value) => onChange({ limit: Number(value) })}
          />
        </FilterField>
        <FilterField label={text("时间范围", "Date range")}>
          <DatePicker.RangePicker
            className="w-full min-w-0 sm:min-w-[310px]"
            allowEmpty={[true, true]}
            format="YYYY-MM-DD HH:mm"
            showTime={{ format: "HH:mm" }}
            value={filters.dateRange}
            placeholder={[
              text("起始时间", "Start time"),
              text("结束时间", "End time"),
            ]}
            presets={[
              {
                label: text("最近 1 小时", "Last hour"),
                value: () => [dayjs().subtract(1, "hour"), dayjs()],
              },
              {
                label: text("最近 24 小时", "Last 24 hours"),
                value: () => [dayjs().subtract(24, "hour"), dayjs()],
              },
              {
                label: text("最近 7 天", "Last 7 days"),
                value: () => [dayjs().subtract(7, "day"), dayjs()],
              },
            ]}
            onChange={(dates) =>
              onChange({
                dateRange: dates ? [dates[0], dates[1]] : [null, null],
              })
            }
          />
        </FilterField>
      </FilterBar>
      <Collapse
        ghost
        className="border-t border-line"
        items={[
          {
            key: "advanced",
            label: (
              <span className="text-xs text-fg-secondary">
                {text("高级筛选", "Advanced filters")}{" "}
                <span className="ml-2 text-fg-disabled">
                  {text(
                    "操作、访问主体、分组、格式",
                    "Operation, actor, group, and format",
                  )}
                </span>
              </span>
            ),
            children: (
              <FilterBar embedded>
                <AuditSelect
                  label={text("操作类型", "Operation")}
                  value={filters.operation}
                  placeholder={text("全部操作类型", "All operations")}
                  options={operationOptions}
                  searchByValue
                  onChange={(operation) => onChange({ operation })}
                />
                <AuditSelect
                  label={text("访问主体", "Actor")}
                  value={filters.actor}
                  placeholder={text("全部访问主体", "All actors")}
                  options={actorOptions}
                  searchByValue
                  onChange={(actor) => onChange({ actor })}
                />
                <AuditSelect
                  label={text("所属分组", "Group")}
                  value={filters.group}
                  placeholder={text("全部分组", "All groups")}
                  options={groupOptions.map((value) => ({
                    value,
                    label: value,
                  }))}
                  onChange={(group) => onChange({ group })}
                />
                <AuditSelect
                  label={text("制品格式", "Artifact format")}
                  value={filters.format}
                  placeholder={text("全部格式", "All formats")}
                  options={formatOptions}
                  onChange={(format) => onChange({ format })}
                />
              </FilterBar>
            ),
          },
        ]}
      />
    </>
  );
}
