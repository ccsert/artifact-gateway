import { CopyableValue } from "../../components/ui/ConsolePrimitives";
import { usePreferences } from "../../lib/preferences";

/** A grant principal as people read it: kind, name, and the exact actor. */
export function Principal({ value }: { value: string }) {
  const { t } = usePreferences();
  if (value.startsWith("user:")) {
    return (
      <div>
        <div className="text-xs font-medium text-fg">
          {t("access.user")} · {value.slice("user:".length)}
        </div>
        <div className="mt-0.5 font-mono text-xs text-fg-disabled">{value}</div>
      </div>
    );
  }
  if (value.startsWith("service-account:")) {
    const id = value.slice("service-account:".length);
    return (
      <CopyableValue
        value={value}
        label={`${t("access.serviceAccount")} · ${id.slice(0, 8)}${id.length > 8 ? "…" : ""}`}
        className="text-xs text-fg"
      />
    );
  }
  if (!value.startsWith("api-key:")) {
    return (
      <div>
        <div className="text-xs font-medium text-fg">
          {t("access.oidcCustom")}
        </div>
        <div className="mt-0.5 font-mono text-xs text-fg-tertiary">{value}</div>
      </div>
    );
  }
  const id = value.slice("api-key:".length);
  return (
    <CopyableValue
      value={value}
      label={`API Key · ${id.slice(0, 8)}${id.length > 8 ? "…" : ""}`}
      className="text-xs text-fg"
    />
  );
}
