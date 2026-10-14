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
  const { t } = usePreferences();
  return (
    <>
      <FilterBar embedded actions={actions}>
        <AuditSelect
          label={t("audit.repository")}
          value={filters.repository}
          placeholder={t("audit.allRepositories")}
          options={repositoryOptions.map((value) => ({ value, label: value }))}
          onChange={(repository) => onChange({ repository })}
        />
        <AuditSelect
          label={t("audit.outcome")}
          value={filters.outcome}
          placeholder={t("audit.allOutcomes")}
          options={outcomeOptions}
          searchByValue
          onChange={(outcome) => onChange({ outcome })}
        />
        <FilterField label={t("audit.loadWindow")}>
          <Select
            className="min-w-[150px]"
            value={String(filters.limit)}
            options={[50, 100, 200].map((value) => ({
              value: String(value),
              label: t("audit.latest", { count: value }),
            }))}
            onChange={(value) => onChange({ limit: Number(value) })}
          />
        </FilterField>
        <FilterField label={t("audit.dateRange")}>
          <DatePicker.RangePicker
            className="w-full min-w-0 sm:min-w-[310px]"
            allowEmpty={[true, true]}
            format="YYYY-MM-DD HH:mm"
            showTime={{ format: "HH:mm" }}
            value={filters.dateRange}
            placeholder={[t("audit.startTime"), t("audit.endTime")]}
            presets={[
              {
                label: t("audit.lastHour"),
                value: () => [dayjs().subtract(1, "hour"), dayjs()],
              },
              {
                label: t("audit.last24Hours"),
                value: () => [dayjs().subtract(24, "hour"), dayjs()],
              },
              {
                label: t("audit.last7Days"),
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
                {t("audit.advancedFilters")}{" "}
                <span className="ml-2 text-fg-disabled">
                  {t("audit.operationActorGroupAndFormat")}
                </span>
              </span>
            ),
            children: (
              <FilterBar embedded>
                <AuditSelect
                  label={t("audit.operation2")}
                  value={filters.operation}
                  placeholder={t("audit.allOperations")}
                  options={operationOptions}
                  searchByValue
                  onChange={(operation) => onChange({ operation })}
                />
                <AuditSelect
                  label={t("audit.actor")}
                  value={filters.actor}
                  placeholder={t("audit.allActors")}
                  options={actorOptions}
                  searchByValue
                  onChange={(actor) => onChange({ actor })}
                />
                <AuditSelect
                  label={t("audit.group")}
                  value={filters.group}
                  placeholder={t("audit.allGroups")}
                  options={groupOptions.map((value) => ({
                    value,
                    label: value,
                  }))}
                  onChange={(group) => onChange({ group })}
                />
                <AuditSelect
                  label={t("audit.artifactFormat")}
                  value={filters.format}
                  placeholder={t("audit.allFormats")}
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
