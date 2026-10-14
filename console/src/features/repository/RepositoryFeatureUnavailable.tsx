import { EmptyState } from "../../components/ui/Feedback";
import { usePreferences } from "../../lib/preferences";

export function RepositoryFeatureUnavailable({
  feature,
  unsupportedFormat,
}: {
  feature: string;
  unsupportedFormat?: string;
}) {
  const { t } = usePreferences();
  return (
    <EmptyState
      title={t("repository.isUnavailable", { feature })}
      hint={
        unsupportedFormat
          ? t("repository.thisConsoleDoesNotSupportTheFormatYet", {
              unsupportedFormat,
            })
          : t("repository.theCurrentBackendDoesNotExposeThisEndpoint")
      }
    />
  );
}
