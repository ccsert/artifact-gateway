// Client-side dashboard history, persisted in localStorage. This is a
// stopgap for "no storage-growth history": until a metrics time-series
// endpoint exists, the console samples totals on each dashboard visit so an
// operator can see recent movement. Samples are throttled to one per
// throttle window so repeated visits do not flood the series.

export interface DashboardSample {
  t: number; // epoch ms
  repos: number;
  bytes: number | null;
  objects: number | null;
}

const KEY = 'ag.console.dashboardHistory';
const MAX_POINTS = 240;
const THROTTLE_MS = 5 * 60 * 1000;

export function loadDashboardHistory(): DashboardSample[] {
  try {
    const raw = localStorage.getItem(KEY);
    if (!raw) return [];
    const parsed = JSON.parse(raw) as DashboardSample[];
    return Array.isArray(parsed) ? parsed.filter((s) => s && typeof s.t === 'number') : [];
  } catch {
    return [];
  }
}

// Appends a sample unless the last one is within the throttle window, in
// which case it is replaced. Returns the resulting series.
export function recordDashboardSample(sample: DashboardSample): DashboardSample[] {
  const history = loadDashboardHistory();
  const last = history[history.length - 1];
  let next: DashboardSample[];
  if (last && sample.t - last.t < THROTTLE_MS) {
    next = [...history.slice(0, -1), sample];
  } else {
    next = [...history, sample];
  }
  if (next.length > MAX_POINTS) {
    next = next.slice(next.length - MAX_POINTS);
  }
  try {
    localStorage.setItem(KEY, JSON.stringify(next));
  } catch {
    /* quota or disabled storage: ignore */
  }
  return next;
}
