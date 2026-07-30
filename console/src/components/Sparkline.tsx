interface SparklineProps {
  data: number[];
  width?: number;
  height?: number;
  color?: string;
  format?: (value: number) => string;
  label?: string;
}

// A tiny inline-SVG sparkline with no chart dependency. Values are scaled to
// the observed min/max; a flat series still renders as a centered line.
export function Sparkline({ data, width = 120, height = 32, color = '#22d3ee', format, label }: SparklineProps) {
  if (data.length < 2) {
    return (
      <div className="flex h-8 items-center text-[10px] text-zinc-600" style={{ width }}>
        {label ?? '暂无历史数据'}
      </div>
    );
  }
  const min = Math.min(...data);
  const max = Math.max(...data);
  const span = max - min || 1;
  const stepX = width / (data.length - 1);
  const pad = 2;
  const usableH = height - pad * 2;
  const points = data
    .map((v, i) => {
      const x = i * stepX;
      const y = pad + usableH - ((v - min) / span) * usableH;
      return `${x.toFixed(1)},${y.toFixed(1)}`;
    })
    .join(' ');
  const latest = data[data.length - 1];
  return (
    <div className="flex items-center gap-2">
      <svg width={width} height={height} viewBox={`0 0 ${width} ${height}`} className="shrink-0">
        <polyline points={points} fill="none" stroke={color} strokeWidth={1.5} strokeLinejoin="round" strokeLinecap="round" />
      </svg>
      {format && <span className="text-[11px] text-zinc-500">{format(latest)}</span>}
    </div>
  );
}
