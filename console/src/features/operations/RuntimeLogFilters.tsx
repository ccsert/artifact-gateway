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
  const { text } = usePreferences();
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
      aria-label={text("日志筛选", "Log filters")}
    >
      <div className="ag-runtime-log-filter-main">
        <FilterField
          label={text("关键词", "Keyword")}
          className="ag-runtime-log-keyword"
        >
          <Input
            prefix={<SearchOutlined />}
            value={keyword}
            allowClear
            placeholder={text("搜索日志内容", "Search log content")}
            onChange={(event) => setKeyword(event.target.value)}
            onPressEnter={search}
          />
        </FilterField>
        <FilterField label={text("时间范围", "Time range")}>
          <Select
            virtual={false}
            className="w-full"
            aria-label={text("时间范围", "Time range")}
            value={timeRange}
            onChange={(value) => {
              setTimeRange(value);
              setWindowError(null);
            }}
            options={[
              { value: 900, label: text("最近 15 分钟", "Last 15 minutes") },
              { value: 3600, label: text("最近 1 小时", "Last hour") },
              { value: 86400, label: text("最近 24 小时", "Last 24 hours") },
              { value: "custom", label: text("自定义", "Custom") },
            ]}
          />
        </FilterField>
        <FilterField label={text("级别", "Level")}>
          <Select
            virtual={false}
            className="w-full"
            aria-label={text("级别", "Level")}
            value={level}
            onChange={setLevel}
            options={[
              { value: "", label: text("全部级别", "All levels") },
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
          <FilterField
            label={text("起止时间（本地时区）", "Start and end (local time)")}
          >
            <DatePicker.RangePicker
              className="w-full"
              showTime
              format="YYYY-MM-DD HH:mm:ss"
              classNames={{ popup: { root: "ag-runtime-log-calendar" } }}
              value={range}
              status={windowError ? "error" : undefined}
              placeholder={[
                text("开始日期", "Start date"),
                text("结束日期", "End date"),
              ]}
              onChange={(value) => {
                setRange(value as [Dayjs, Dayjs] | null);
                setWindowError(null);
              }}
              allowClear
            />
          </FilterField>
          <span className="ag-runtime-log-hint">
            {text(
              "最多 24 小时，结束时间不能超过当前时间 5 分钟。",
              "At most 24 hours; end no later than 5 minutes from now.",
            )}
          </span>
        </div>
      ) : null}
      <div className="ag-runtime-log-filter-secondary">
        <FilterField label={text("组件", "Component")}>
          <AutoComplete
            value={component}
            onChange={setComponent}
            options={components.map((value) => ({ value }))}
            placeholder={text(
              "全部组件，可输入精确名称",
              "All components; enter exact name",
            )}
            className="w-full"
          />
        </FilterField>
        <FilterField label="Request ID">
          <Input
            value={requestId}
            allowClear
            placeholder={text("按请求定位", "Find a request")}
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
          {text("更多筛选", "More filters")}
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
          <FilterField label={text("节点", "Instance")}>
            <Input
              value={instanceId}
              allowClear
              placeholder={text("当前进程", "Current process")}
              onChange={(event) => setInstanceId(event.target.value)}
              onPressEnter={search}
            />
          </FilterField>
          <FilterField label="Trace ID">
            <Input
              value={traceId}
              allowClear
              placeholder={text("按链路关联", "Correlate a trace")}
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
          title={text("请调整时间范围", "Adjust the time range")}
          description={text(
            {
              required: "请选择完整的开始和结束时间。",
              order: "开始时间不能晚于结束时间。",
              duration: "时间范围不能超过 24 小时。",
              future: "结束时间不能超过当前时间 5 分钟。",
            }[windowError],
            {
              required: "Choose both start and end times.",
              order: "Start must not be after end.",
              duration: "Time range cannot exceed 24 hours.",
              future: "End cannot be more than 5 minutes from now.",
            }[windowError],
          )}
        />
      ) : null}
      <div className="ag-runtime-log-filter-actions">
        <span className="ag-runtime-log-hint">
          {timeRange === "custom"
            ? text(
                "固定时间快照；选择最近范围可跟随新日志。",
                "Fixed snapshot; choose a recent range to follow new logs.",
              )
            : text(
                "最近范围随每次查询和跟随自动更新。",
                "Recent ranges update on every query and follow read.",
              )}
        </span>
        <Space wrap>
          <Button onClick={clearFilters}>
            {text("清除筛选", "Clear filters")}
          </Button>
          <Button
            icon={<ReloadOutlined />}
            loading={loading}
            onClick={onRefresh}
          >
            {text("刷新快照", "Refresh snapshot")}
          </Button>
          <Button icon={<SearchOutlined />} type="primary" onClick={search}>
            {text("查询", "Search")}
          </Button>
        </Space>
      </div>
    </div>
  );
}
