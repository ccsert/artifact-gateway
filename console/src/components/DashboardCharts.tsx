import type { LineConfig, PieConfig } from "@ant-design/plots";
import { theme as antdTheme } from "antd";
import {
  lazy,
  Suspense,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { formatBytes } from "../lib/format";
import type { DashboardSample } from "../lib/history";
import { usePreferences } from "../lib/preferences";

const DashboardPiePlot = lazy(
  () => import("./dashboard-charts/DashboardPiePlot"),
);
const DashboardLinePlot = lazy(
  () => import("./dashboard-charts/DashboardLinePlot"),
);

const FORMAT_COLORS: Record<string, string> = {
  oci: "#22d3ee",
  maven: "#fbbf24",
  npm: "#f43f5e",
  pypi: "#34d399",
  go: "#60a5fa",
  conan: "#a78bfa",
  raw: "#38bdf8",
  apt: "#fb923c",
};

const FORMAT_ORDER = [
  "oci",
  "maven",
  "npm",
  "pypi",
  "go",
  "conan",
  "raw",
  "apt",
] as const;

interface StorageChartDatum {
  format: string;
  label: string;
  bytes: number;
  color: string;
}

interface TrendDatum {
  time: Date;
  timeLabel: string;
  value: number;
}

export function buildStorageChartData(
  bytesByFormat: Record<string, number>,
): StorageChartDatum[] {
  const knownFormats = new Set<string>(FORMAT_ORDER);
  const formats = [
    ...FORMAT_ORDER,
    ...Object.keys(bytesByFormat)
      .filter((format) => !knownFormats.has(format))
      .sort(),
  ];

  return formats
    .map((format) => ({
      format,
      label: format.toUpperCase(),
      bytes: Math.max(0, bytesByFormat[format] ?? 0),
      color: FORMAT_COLORS[format] ?? "#71717a",
    }))
    .filter((datum) => datum.bytes > 0);
}

function formatChartTime(value: Date | number | string, locale: string) {
  const date = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(date.getTime())) return String(value);
  return new Intl.DateTimeFormat(locale, {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  }).format(date);
}

function EmptyChart({ children }: { children: string }) {
  return (
    <div className="flex min-h-48 items-center justify-center px-4 text-center text-sm text-zinc-600">
      {children}
    </div>
  );
}

function ChartLoadingPlaceholder({
  height,
  label,
}: {
  height: number;
  label: string;
}) {
  return (
    <div
      className="flex items-center justify-center px-4 text-center text-xs text-zinc-600"
      style={{ height }}
      role="status"
    >
      {label}
    </div>
  );
}

function DeferredChart({
  children,
  height,
  label,
}: {
  children: ReactNode;
  height: number;
  label: string;
}) {
  const containerRef = useRef<HTMLDivElement>(null);
  const [shouldLoad, setShouldLoad] = useState(
    () => typeof IntersectionObserver === "undefined",
  );

  useEffect(() => {
    if (shouldLoad) return;
    const container = containerRef.current;
    if (!container || typeof IntersectionObserver === "undefined") {
      setShouldLoad(true);
      return;
    }

    const observer = new IntersectionObserver(
      (entries) => {
        if (entries.some((entry) => entry.isIntersecting)) {
          setShouldLoad(true);
          observer.disconnect();
        }
      },
      { rootMargin: "240px 0px" },
    );
    observer.observe(container);
    return () => observer.disconnect();
  }, [shouldLoad]);

  const fallback = <ChartLoadingPlaceholder height={height} label={label} />;

  return (
    <div ref={containerRef} className="min-w-0" style={{ minHeight: height }}>
      {shouldLoad ? (
        <Suspense fallback={fallback}>{children}</Suspense>
      ) : (
        fallback
      )}
    </div>
  );
}

export function StorageByFormatChart({
  bytesByFormat,
  totalBytes,
}: {
  bytesByFormat: Record<string, number> | null;
  totalBytes: number | null;
}) {
  const { colorMode, text } = usePreferences();
  const { token } = antdTheme.useToken();
  const data = bytesByFormat ? buildStorageChartData(bytesByFormat) : [];

  if (totalBytes === null || totalBytes <= 0 || data.length === 0) {
    return (
      <EmptyChart>
        {text(
          "容量统计未启用或暂无数据",
          "Capacity metrics are unavailable or empty",
        )}
      </EmptyChart>
    );
  }

  const surfaceColor = token.colorBgContainer;
  const config: PieConfig = {
    data,
    angleField: "bytes",
    colorField: "format",
    innerRadius: 0.68,
    radius: 0.9,
    height: 224,
    autoFit: true,
    theme: colorMode,
    animate: false,
    label: false,
    legend: false,
    scale: {
      color: {
        domain: data.map((datum) => datum.format),
        range: data.map((datum) => datum.color),
      },
    },
    style: {
      stroke: surfaceColor,
      lineWidth: 2,
    },
    state: {
      active: { lineWidth: 3, stroke: surfaceColor },
      inactive: { opacity: 0.45 },
    },
    interaction: { elementHighlight: true },
    tooltip: {
      title: { field: "label" },
      items: [
        {
          field: "bytes",
          name: text("存储占用", "Storage used"),
          valueFormatter: (value) => formatBytes(Number(value)),
        },
      ],
    },
  };

  return (
    <div className="grid min-w-0 items-center gap-4 sm:grid-cols-[minmax(0,1fr)_auto]">
      <div
        className="relative min-w-0"
        role="img"
        aria-label={text(
          `各制品格式的存储占比，合计 ${formatBytes(totalBytes)}`,
          `Storage share by artifact format, ${formatBytes(totalBytes)} total`,
        )}
        data-testid="storage-by-format-chart"
      >
        <DeferredChart
          height={224}
          label={text("正在加载图表…", "Loading chart…")}
        >
          <DashboardPiePlot key={colorMode} config={config} />
        </DeferredChart>
        <div className="pointer-events-none absolute inset-0 flex flex-col items-center justify-center">
          <span className="text-lg font-semibold tabular-nums text-zinc-100">
            {formatBytes(totalBytes)}
          </span>
          <span className="text-xs uppercase tracking-wider text-zinc-500">
            {text("合计", "Total")}
          </span>
        </div>
      </div>
      <ul
        className="grid min-w-36 grid-cols-2 gap-x-5 gap-y-2 sm:grid-cols-1"
        aria-label={text("格式图例", "Format legend")}
      >
        {data.map((datum) => (
          <li
            key={datum.format}
            className="grid grid-cols-[10px_minmax(0,1fr)_auto] items-center gap-2 text-xs"
          >
            <span
              className="h-2.5 w-2.5 rounded-sm"
              style={{ backgroundColor: datum.color }}
              aria-hidden="true"
            />
            <span className="text-zinc-300">{datum.label}</span>
            <span className="tabular-nums text-zinc-500">
              {Math.round((datum.bytes / totalBytes) * 100)}%
            </span>
          </li>
        ))}
      </ul>
    </div>
  );
}

function TrendLineChart({
  data,
  color,
  label,
  valueFormatter,
  emptyLabel,
}: {
  data: TrendDatum[];
  color: string;
  label: string;
  valueFormatter: (value: number) => string;
  emptyLabel: string;
}) {
  const { colorMode, locale, text } = usePreferences();
  const { token } = antdTheme.useToken();

  if (data.length === 0) return <EmptyChart>{emptyLabel}</EmptyChart>;

  const surfaceColor = token.colorBgContainer;
  const config: LineConfig = {
    data,
    xField: "time",
    yField: "value",
    height: 184,
    autoFit: true,
    theme: colorMode,
    animate: false,
    scale: {
      x: { type: "time" },
      y: { nice: true },
    },
    axis: {
      x: {
        title: false,
        grid: false,
        line: true,
        tick: false,
        tickCount: 3,
        labelAutoHide: true,
        labelAutoRotate: false,
        labelFormatter: (value: Date | number | string) =>
          formatChartTime(value, locale),
      },
      y: {
        title: false,
        line: false,
        tick: false,
        tickCount: 3,
        labelFormatter: (value: number | string) =>
          valueFormatter(Number(value)),
      },
    },
    style: { stroke: color, lineWidth: 2 },
    point: {
      sizeField: 3,
      style: { fill: color, stroke: surfaceColor, lineWidth: 1.5 },
    },
    tooltip: {
      title: { field: "timeLabel" },
      items: [
        {
          field: "value",
          name: label,
          valueFormatter: (value) => valueFormatter(Number(value)),
        },
      ],
    },
    interaction: {
      tooltip: { shared: true, crosshairs: true },
    },
  };

  return (
    <div
      className="min-w-0"
      role="img"
      aria-label={text(
        `${label}近期趋势`,
        `Recent ${label.toLowerCase()} trend`,
      )}
      data-testid="dashboard-trend-chart"
    >
      <DeferredChart
        height={184}
        label={text("正在加载图表…", "Loading chart…")}
      >
        <DashboardLinePlot key={`${colorMode}-${locale}`} config={config} />
      </DeferredChart>
    </div>
  );
}

export function DashboardTrendCharts({
  history,
}: {
  history: DashboardSample[];
}) {
  const { locale, text } = usePreferences();
  const { token } = antdTheme.useToken();
  const orderedHistory = [...history].sort((a, b) => a.t - b.t);
  const repositoryData = orderedHistory.map((sample) => ({
    time: new Date(sample.t),
    timeLabel: formatChartTime(sample.t, locale),
    value: sample.repos,
  }));
  const storageData = orderedHistory
    .filter(
      (sample): sample is DashboardSample & { bytes: number } =>
        sample.bytes !== null,
    )
    .map((sample) => ({
      time: new Date(sample.t),
      timeLabel: formatChartTime(sample.t, locale),
      value: sample.bytes,
    }));

  return (
    <div className="grid min-w-0 grid-cols-1 gap-6 px-5 py-6 sm:grid-cols-2">
      <section className="min-w-0" aria-labelledby="repository-trend-title">
        <h3
          id="repository-trend-title"
          className="mb-2 text-xs font-medium uppercase tracking-wider text-zinc-500"
        >
          {text("仓库数", "Repositories")}
        </h3>
        <TrendLineChart
          data={repositoryData}
          color={token.colorPrimary}
          label={text("仓库数", "Repositories")}
          valueFormatter={(value) => String(Math.round(value))}
          emptyLabel={text("暂无历史数据", "No history yet")}
        />
      </section>
      <section className="min-w-0" aria-labelledby="storage-trend-title">
        <h3
          id="storage-trend-title"
          className="mb-2 text-xs font-medium uppercase tracking-wider text-zinc-500"
        >
          {text("存储占用", "Storage used")}
        </h3>
        <TrendLineChart
          data={storageData}
          color={token.colorWarning}
          label={text("存储占用", "Storage used")}
          valueFormatter={formatBytes}
          emptyLabel={text("容量未启用", "Capacity unavailable")}
        />
      </section>
    </div>
  );
}
