import { CopyableValue } from "../../components/ui/ConsolePrimitives";
import { usePreferences } from "../../lib/preferences";

/**
 * A grant principal as people read it: its kind on the first line, the exact
 * actor below. Identifiers are shown whole and only cut by the column edge,
 * with a copy action, so two similar ids can still be told apart.
 */
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
  const kind = value.startsWith("service-account:")
    ? t("access.serviceAccount")
    : value.startsWith("api-key:")
      ? "API Key"
      : null;
  if (!kind) {
    return (
      <div>
        <div className="text-xs font-medium text-fg">
          {t("access.oidcCustom")}
        </div>
        <div className="mt-0.5 font-mono text-xs text-fg-tertiary">{value}</div>
      </div>
    );
  }
  return (
    <div className="min-w-0">
      <div className="text-xs font-medium text-fg">{kind}</div>
      <CopyableValue
        value={value}
        label={value.slice(value.indexOf(":") + 1)}
        className="max-w-full text-xs text-fg-disabled"
      />
    </div>
  );
}
