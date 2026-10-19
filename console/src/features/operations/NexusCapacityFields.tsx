import { Input } from "antd";
import type { NexusMigrationCapabilities } from "../../client";
import { Notice } from "../../components/ui/Feedback";
import { Field } from "../../components/ui/Layout";
import { usePreferences } from "../../lib/preferences";
import { errorText } from "../../messages/translate";
import {
  capacityEvidence,
  capacityFields,
  type CapacityInput,
} from "./nexusBatchModel";

export function NexusCapacityFields({
  value,
  onChange,
  binding,
  previous,
  disabled,
}: {
  value: CapacityInput;
  onChange: (value: CapacityInput) => void;
  binding: NexusMigrationCapabilities;
  previous?: string;
  disabled: boolean;
}) {
  const { t } = usePreferences();
  let validation = "";
  if (Object.values(value).every((item) => item.trim())) {
    try {
      capacityEvidence(value, binding, previous);
    } catch (error) {
      validation = errorText(error, t, "nexusBatch.required");
    }
  }
  return (
    <section
      className="ag-migration-form"
      aria-label={t("nexusBatch.capabilities")}
    >
      <h3>{t("nexusBatch.capabilities")}</h3>
      <p className="ag-migration-technical">
        {t("nexusBatch.binding", {
          target: binding.targetId,
          pool: binding.poolId,
        })}
      </p>
      <p className="text-sm text-fg-secondary">
        {t("nexusBatch.capacityHint")}
      </p>
      {previous && (
        <p className="text-xs text-fg-secondary">
          {t("nexusBatch.previousEvidence", { time: previous })}
        </p>
      )}
      <div className="ag-nexus-fields">
        {(["observedAt", "validUntil"] as const).map((key) => (
          <Field key={key} label={t(`nexusBatch.${key}`)}>
            <Input
              aria-label={t(`nexusBatch.${key}`)}
              value={value[key]}
              placeholder="2026-10-19T10:00:00+08:00"
              disabled={disabled}
              onChange={(event) =>
                onChange({ ...value, [key]: event.target.value })
              }
            />
          </Field>
        ))}
      </div>
      <p className="text-xs text-fg-secondary">{t("nexusBatch.timeHint")}</p>
      <div className="ag-nexus-fields">
        {capacityFields.map((key) => (
          <Field key={key} label={t(`nexusBatch.${key}`)}>
            <Input
              aria-label={t(`nexusBatch.${key}`)}
              inputMode="numeric"
              value={value[key]}
              disabled={disabled}
              autoComplete="off"
              onChange={(event) =>
                onChange({ ...value, [key]: event.target.value })
              }
            />
          </Field>
        ))}
      </div>
      {validation && (
        <Notice tone="warning" title={validation} closable={false} />
      )}
    </section>
  );
}
