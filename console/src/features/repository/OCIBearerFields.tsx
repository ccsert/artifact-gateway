import { useId } from "react";
import { Input, Switch } from "antd";
import { Field } from "../../components/ui/Layout";
import { usePreferences } from "../../lib/preferences";
import type { ProxySettingsDraft } from "./useProxySettingsDraft";

export function OCIBearerFields({ draft }: { draft: ProxySettingsDraft }) {
  const { t } = usePreferences();
  const realmId = useId();
  const serviceId = useId();
  return (
    <div
      role="group"
      aria-label={t("repository.ociTokenExchange")}
      className="rounded-lg border border-line bg-surface-translucent px-4 py-3"
    >
      <div className="flex items-start justify-between gap-4">
        <div className="min-w-0">
          <div className="text-sm font-medium text-fg">
            {t("repository.ociTokenExchange")}
          </div>
          <p className="mt-1 text-xs leading-5 text-fg-tertiary">
            {t("repository.ociTokenBindingHint")}
          </p>
        </div>
        <Switch
          checked={draft.ociBearerEnabled}
          onChange={draft.setOciBearerEnabled}
          aria-label={t("repository.ociTokenExchange")}
        />
      </div>
      {draft.ociBearerEnabled && (
        <div className="mt-3 flex flex-col gap-3 border-t border-line pt-3">
          <Field
            id={realmId}
            label={t("repository.ociTokenServiceURL")}
            hint={t("repository.ociTokenServiceURLHint")}
          >
            <Input
              id={realmId}
              aria-describedby={`${realmId}-hint`}
              type="url"
              autoComplete="off"
              spellCheck={false}
              placeholder="https://auth.example/token"
              value={draft.ociBearerRealm}
              onChange={(event) => draft.setOciBearerRealm(event.target.value)}
            />
          </Field>
          <Field
            id={serviceId}
            label={t("repository.ociTokenAudience")}
            hint={t("repository.ociTokenAudienceHint")}
          >
            <Input
              id={serviceId}
              aria-describedby={`${serviceId}-hint`}
              autoComplete="off"
              spellCheck={false}
              placeholder="registry.example"
              value={draft.ociBearerService}
              onChange={(event) =>
                draft.setOciBearerService(event.target.value)
              }
            />
          </Field>
        </div>
      )}
    </div>
  );
}
