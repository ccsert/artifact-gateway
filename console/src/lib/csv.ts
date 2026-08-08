function csvField(value: unknown): string {
  const text = value === null || value === undefined ? "" : String(value);
  if (/[",\n\r]/.test(text)) {
    return `"${text.replace(/"/g, '""')}"`;
  }
  return text;
}

export function toCsv(
  columns: string[],
  rows: (string | number | null | undefined)[][],
): string {
  const head = columns.map(csvField).join(",");
  const body = rows.map((row) => row.map(csvField).join(",")).join("\r\n");
  return `${head}\r\n${body}`;
}

// A UTF-8 BOM is prepended so spreadsheet tools open Chinese columns correctly.
export function downloadCsv(filename: string, csv: string): void {
  const blob = new Blob([`\uFEFF${csv}`], { type: "text/csv;charset=utf-8;" });
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = filename;
  document.body.appendChild(anchor);
  anchor.click();
  document.body.removeChild(anchor);
  URL.revokeObjectURL(url);
}
