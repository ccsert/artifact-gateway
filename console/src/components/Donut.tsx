import type { ReactNode } from "react";

export interface DonutSegment {
  label: string;
  value: number;
  color: string;
}

export function Donut({
  segments,
  size = 150,
  thickness = 20,
  format = (n) => String(n),
  centerLabel,
  centerSub,
}: {
  segments: DonutSegment[];
  size?: number;
  thickness?: number;
  format?: (value: number) => string;
  centerLabel?: ReactNode;
  centerSub?: ReactNode;
}) {
  const total = segments.reduce((sum, s) => sum + Math.max(0, s.value), 0);
  const radius = (size - thickness) / 2;
  const circumference = 2 * Math.PI * radius;
  const center = size / 2;
  let offset = 0;

  return (
    <div className="flex flex-wrap items-center gap-6">
      <div className="relative shrink-0" style={{ width: size, height: size }}>
        <svg width={size} height={size} viewBox={`0 0 ${size} ${size}`}>
          <g transform={`rotate(-90 ${center} ${center})`}>
            <circle
              cx={center}
              cy={center}
              r={radius}
              fill="none"
              stroke="rgb(39 39 42)"
              strokeWidth={thickness}
            />
            {total > 0 &&
              segments
                .filter((s) => s.value > 0)
                .map((s) => {
                  const len = (s.value / total) * circumference;
                  const node = (
                    <circle
                      key={s.label}
                      cx={center}
                      cy={center}
                      r={radius}
                      fill="none"
                      stroke={s.color}
                      strokeWidth={thickness}
                      strokeDasharray={`${len} ${circumference - len}`}
                      strokeDashoffset={-offset}
                    />
                  );
                  offset += len;
                  return node;
                })}
          </g>
        </svg>
        {(centerLabel || centerSub) && (
          <div className="absolute inset-0 flex flex-col items-center justify-center">
            {centerLabel && (
              <span className="text-lg font-semibold text-zinc-100">
                {centerLabel}
              </span>
            )}
            {centerSub && (
              <span className="text-xs uppercase tracking-wider text-zinc-500">
                {centerSub}
              </span>
            )}
          </div>
        )}
      </div>
      <ul className="space-y-1.5">
        {segments.map((s) => (
          <li key={s.label} className="flex items-center gap-2 text-xs">
            <span
              className="h-2.5 w-2.5 rounded-sm"
              style={{ backgroundColor: s.color }}
            />
            <span className="text-zinc-300">{s.label}</span>
            <span className="text-zinc-500">{format(s.value)}</span>
            <span className="ml-3 text-zinc-600">
              {total > 0 ? `${Math.round((s.value / total) * 100)}%` : "—"}
            </span>
          </li>
        ))}
      </ul>
    </div>
  );
}
