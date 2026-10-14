import { useEffect, useRef, useState } from "react";
import type { Dispatch, SetStateAction } from "react";
import {
  DownOutlined,
  FilterOutlined,
  ReloadOutlined,
  SearchOutlined,
  UpOutlined,
} from "@ant-design/icons";
import {
  Alert,
  AutoComplete,
  Button,
  DatePicker,
  Input,
  Select,
  Space,
} from "antd";
import type { Dayjs } from "dayjs";
import { FilterField } from "../../components/ui/ConsolePrimitives";
import { usePreferences } from "../../lib/preferences";
import type { LogQuery } from "./useRuntimeLogStream";
import {
  customRuntimeLogWindow,
  type RuntimeLogRangeError,
  type RuntimeLogTimeRange,
} from "./runtimeLogWindow";

/**
 * The log filter drafts. Search turns them into the stream's query; an audit
 * deep link (`requestId`/`traceId`) replaces those two fields in both the
 * drafts and the running query.
 */
export function RuntimeLogFilters({
  auditRequestId,
  auditTraceId,
  components,
  loading,
  onRefresh,
  setQuery,
}: {
  auditRequestId: string;
  auditTraceId: string;
  /** Component names the current log source reports. */
  components: string[];
  loading: boolean;
  onRefresh: () => void;
  setQuery: Dispatch<SetStateAction<LogQuery>>;
}) {
  const { t } = usePreferences();
  const lastAuditLink = useRef(`${auditRequestId}\n${auditTraceId}`);
  const [range, setRange] = useState<[Dayjs, Dayjs] | null>(null);
  const [timeRange, setTimeRange] = useState<RuntimeLogTimeRange>(3600);
  const [windowError, setWindowError] = useState<RuntimeLogRangeError | null>(
    null,
  );
  const [moreFilters, setMoreFilters] = useState(Boolean(auditTraceId));
  const [instanceId, setInstanceId] = useState("");
  const [level, setLevel] = useState("");
  const [component, setComponent] = useState("");
  const [requestId, setRequestId] = useState(auditRequestId);
  const [traceId, setTraceId] = useState(auditTraceId);
  const [keyword, setKeyword] = useState("");
  useEffect(() => {
    const link = `${auditRequestId}\n${auditTraceId}`;
    if (lastAuditLink.current === link) return;
    lastAuditLink.current = link;
    setRequestId(auditRequestId);
    setTraceId(auditTraceId);
    setQuery((current) => ({
      ...current,
      requestId: auditRequestId || undefined,
      traceId: auditTraceId || undefined,
    }));
  }, [auditRequestId, auditTraceId, setQuery]);

  const clearFilters = () => {
    setRange(null);
    setTimeRange(3600);
    setWindowError(null);
    setInstanceId("");
    setLevel("");
    setComponent("");
    setRequestId("");
    setTraceId("");
    setKeyword("");
    setQuery({ limit: 50 });
  };

  const search = () => {
    const window = timeRange === "custom" ? customRuntimeLogWindow(range) : {};
    if (window.error) {
      setWindowError(window.error);
      return;
    }
    setWindowError(null);
    setQuery({
      ...window.bounds,
      windowSeconds:
        timeRange !== "custom" && timeRange !== 3600 ? timeRange : undefined,
      instanceId: instanceId.trim() || undefined,
      level: level || undefined,
      component: component.trim() || undefined,
      requestId: requestId.trim() || undefined,
      traceId: traceId.trim() || undefined,
      keyword: keyword.trim() || undefined,
      limit: 50,
    });
  };

  return (
    <div
      className="ag-runtime-log-filters"
      role="search"
      aria-label={t("operations.logFilters")}
    >
      <div className="ag-runtime-log-filter-main">
        <FilterField
          label={t("operations.keyword")}
          className="ag-runtime-log-keyword"
        >
          <Input
            prefix={<SearchOutlined />}
            value={keyword}
            allowClear
            placeholder={t("operations.searchLogContent")}
            onChange={(event) => setKeyword(event.target.value)}
            onPressEnter={search}
          />
        </FilterField>
        <FilterField label={t("operations.timeRange")}>
          <Select
            virtual={false}
            className="w-full"
            aria-label={t("operations.timeRange")}
            value={timeRange}
            onChange={(value) => {
              setTimeRange(value);
              setWindowError(null);
            }}
            options={[
              { value: 900, label: t("operations.last15Minutes") },
              { value: 3600, label: t("operations.lastHour") },
              { value: 86400, label: t("operations.last24Hours") },
              { value: "custom", label: t("operations.custom") },
            ]}
          />
        </FilterField>
        <FilterField label={t("operations.level")}>
          <Select
            virtual={false}
            className="w-full"
            aria-label={t("operations.level")}
            value={level}
            onChange={setLevel}
            options={[
              { value: "", label: t("operations.allLevels") },
              ...["ERROR", "WARN", "INFO", "DEBUG"].map((value) => ({
                value,
                label: value,
              })),
            ]}
          />
        </FilterField>
      </div>
      {timeRange === "custom" ? (
        <div className="ag-runtime-log-custom-range">
          <FilterField label={t("operations.startAndEndLocalTime")}>
            <DatePicker.RangePicker
              className="w-full"
              showTime
              format="YYYY-MM-DD HH:mm:ss"
              classNames={{ popup: { root: "ag-runtime-log-calendar" } }}
              value={range}
              status={windowError ? "error" : undefined}
              placeholder={[t("operations.startDate"), t("operations.endDate")]}
              onChange={(value) => {
                setRange(value as [Dayjs, Dayjs] | null);
                setWindowError(null);
              }}
              allowClear
            />
          </FilterField>
          <span className="ag-runtime-log-hint">
            {t("operations.atMost24HoursEndNoLaterThan")}
          </span>
        </div>
      ) : null}
      <div className="ag-runtime-log-filter-secondary">
        <FilterField label={t("operations.component")}>
          <AutoComplete
            value={component}
            onChange={setComponent}
            options={components.map((value) => ({ value }))}
            placeholder={t("operations.allComponentsEnterExactName")}
            className="w-full"
          />
        </FilterField>
        <FilterField label="Request ID">
          <Input
            value={requestId}
            allowClear
            placeholder={t("operations.findARequest")}
            onChange={(event) => setRequestId(event.target.value)}
            onPressEnter={search}
          />
        </FilterField>
        <Button
          className="ag-runtime-log-more"
          icon={<FilterOutlined />}
          aria-expanded={moreFilters}
          aria-controls="runtime-log-more-filters"
          onClick={() => setMoreFilters(!moreFilters)}
        >
          {t("operations.moreFilters")}
          {instanceId || traceId
            ? " · " + (Number(Boolean(instanceId)) + Number(Boolean(traceId)))
            : ""}
          {moreFilters ? <UpOutlined /> : <DownOutlined />}
        </Button>
      </div>
      {moreFilters ? (
        <div
          id="runtime-log-more-filters"
          className="ag-runtime-log-filter-more"
        >
          <FilterField label={t("operations.instance")}>
            <Input
              value={instanceId}
              allowClear
              placeholder={t("operations.currentProcess")}
              onChange={(event) => setInstanceId(event.target.value)}
              onPressEnter={search}
            />
          </FilterField>
          <FilterField label="Trace ID">
            <Input
              value={traceId}
              allowClear
              placeholder={t("operations.correlateATrace")}
              onChange={(event) => setTraceId(event.target.value)}
              onPressEnter={search}
            />
          </FilterField>
        </div>
      ) : null}
      {windowError ? (
        <Alert
          type="warning"
          showIcon
          title={t("operations.adjustTheTimeRange")}
          description={t(
            (
              {
                required: "operations.chooseBothStartAndEndTimes",
                order: "operations.startMustNotBeAfterEnd",
                duration: "operations.timeRangeCannotExceed24Hours",
                future: "operations.endCannotBeMoreThan5MinutesFromNow",
              } as const
            )[windowError],
          )}
        />
      ) : null}
      <div className="ag-runtime-log-filter-actions">
        <span className="ag-runtime-log-hint">
          {timeRange === "custom"
            ? t("operations.fixedSnapshotChooseARecentRangeToFollow")
            : t("operations.recentRangesUpdateOnEveryQueryAndFollow")}
        </span>
        <Space wrap>
          <Button onClick={clearFilters}>{t("operations.clearFilters")}</Button>
          <Button
            icon={<ReloadOutlined />}
            loading={loading}
            onClick={onRefresh}
          >
            {t("operations.refreshSnapshot")}
          </Button>
          <Button icon={<SearchOutlined />} type="primary" onClick={search}>
            {t("operations.search")}
          </Button>
        </Space>
      </div>
    </div>
  );
}
