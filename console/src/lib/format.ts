export function formatBytes(bytes: number | undefined): string {
  if (bytes === undefined || bytes === null || Number.isNaN(bytes)) return "—";
  if (bytes === 0) return "0 B";
  const units = ["B", "KiB", "MiB", "GiB", "TiB", "PiB"];
  const exp = Math.min(Math.floor(Math.log2(bytes) / 10), units.length - 1);
  const value = bytes / 2 ** (exp * 10);
  return `${value >= 100 ? value.toFixed(0) : value.toFixed(1)} ${units[exp]}`;
}

export function formatDate(iso: string | undefined, locale?: string): string {
  if (!iso) return "—";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleString(locale, {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hour12: false,
  });
}

export function shortDigest(digest: string | undefined): string {
  if (!digest) return "—";
  if (digest.length <= 19) return digest;
  return `${digest.slice(0, 19)}…`;
}

export function formatNumber(n: number | undefined, locale?: string): string {
  if (n === undefined || n === null) return "—";
  return n.toLocaleString(locale);
}
