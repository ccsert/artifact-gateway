import { SearchOutlined } from "@ant-design/icons";
import { Tooltip } from "antd";
import { Link } from "react-router-dom";
import { usePreferences } from "../lib/preferences";
import { SiteBrandMark, SiteName } from "../components/ui/SiteBrand";
import { useConnectedBuild } from "../features/operations/systemQueries";

export function BrandLockup({ collapsed = false }: { collapsed?: boolean }) {
  return (
    <div
      className="ag-brand-lockup flex items-center"
      data-collapsed={collapsed ? "true" : "false"}
    >
      <SiteBrandMark className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg text-sm font-bold text-on-identity" />
      <div className="ag-brand-copy min-w-0">
        <div className="truncate text-sm font-semibold text-fg-strong">
          <SiteName />
        </div>
        <div className="text-xs uppercase tracking-widest text-fg-disabled">
          Console
        </div>
      </div>
    </div>
  );
}

export function CommandTrigger({
  collapsed,
  onOpen,
}: {
  collapsed: boolean;
  onOpen: () => void;
}) {
  const { t } = usePreferences();
  const shortcut =
    typeof navigator !== "undefined" &&
    /Mac|iP(hone|ad)/.test(navigator.platform)
      ? "⌘K"
      : "Ctrl K";
  return (
    <Tooltip
      title={collapsed ? t("palette.trigger") : undefined}
      placement="right"
    >
      <button
        type="button"
        className="ag-command-trigger"
        data-collapsed={collapsed ? "true" : "false"}
        aria-label={t("palette.trigger")}
        aria-keyshortcuts="Meta+K Control+K"
        onClick={onOpen}
      >
        <SearchOutlined aria-hidden="true" />
        <span className="ag-command-trigger-label">{t("palette.trigger")}</span>
        <kbd className="ag-command-trigger-kbd">{shortcut}</kbd>
      </button>
    </Tooltip>
  );
}

export function ConnectedVersion() {
  const { t } = usePreferences();
  const build = useConnectedBuild();

  const version =
    build?.version && build.version !== "unknown"
      ? build.version === "dev"
        ? t("shell.developmentBuild")
        : build.version
      : t("shell.versionUnknown");
  const revision =
    build?.revision && build.revision !== "unknown"
      ? ` · ${build.revision.slice(0, 12)}`
      : "";

  return (
    <Link
      to="/system?tab=diagnostics"
      className="ag-sider-meta text-xs leading-4 text-fg-tertiary hover:text-fg"
      title={t("shell.viewDiagnosticsForTheConnectedNode")}
    >
      {t("shell.connectedNode")} · {version}
      {revision}
    </Link>
  );
}
