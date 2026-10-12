import type { CSSProperties } from "react";
import type { ConsoleTheme } from "../../client";
import { resolveConsoleTheme } from "../../lib/consoleTheme";

/** A miniature of a theme's canvas, surface, text and primary action. */
export function ThemePreview({ theme }: { theme: ConsoleTheme }) {
  const { roles } = resolveConsoleTheme(theme);
  const style = {
    "--preview-bg": roles.surface.canvas,
    "--preview-surface": roles.surface.container,
    "--preview-border": roles.border.subtle,
    "--preview-text": roles.content.primary,
    "--preview-muted": roles.content.secondary,
    "--preview-primary": roles.action.primary,
  } as CSSProperties;
  return (
    <div className="ag-theme-preview" style={style} aria-hidden="true">
      <span className="ag-theme-preview-rail" />
      <span className="ag-theme-preview-content">
        <span className="ag-theme-preview-line ag-theme-preview-line-strong" />
        <span className="ag-theme-preview-line" />
        <span className="ag-theme-preview-action" />
      </span>
    </div>
  );
}
