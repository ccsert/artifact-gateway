import type { PieConfig } from "@ant-design/plots";
import {
  lazy,
  Suspense,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { formatBytes } from "../../lib/format";
import { artifactFormatVisualizationSlot } from "../../lib/artifactFormatVisuals";
import { usePreferences } from "../../lib/preferences";

const DashboardPiePlot = lazy(
  () => import("./dashboard-charts/DashboardPiePlot"),
);

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
}

interface ThemedStorageChartDatum extends StorageChartDatum {
  color: string;
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
    }))
    .filter((datum) => datum.bytes > 0);
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
  const { colorMode, resolvedTheme, text } = usePreferences();
  const chartData = bytesByFormat ? buildStorageChartData(bytesByFormat) : [];
  const palette = resolvedTheme.roles.visualization.categorical;
  const data: ThemedStorageChartDatum[] = chartData.map((datum) => {
    const slot = artifactFormatVisualizationSlot(datum.format);
    return {
      ...datum,
      color:
        (slot === undefined ? undefined : palette[slot]) ??
        resolvedTheme.roles.visualization.fallback,
    };
  });

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

  const surfaceColor = resolvedTheme.roles.surface.container;
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
