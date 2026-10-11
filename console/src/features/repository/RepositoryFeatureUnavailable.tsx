import { EmptyState } from "../../components/ui/Feedback";
import { usePreferences } from "../../lib/preferences";

export function RepositoryFeatureUnavailable({
  feature,
  unsupportedFormat,
}: {
  feature: string;
  unsupportedFormat?: string;
}) {
  const { text } = usePreferences();
  return (
    <EmptyState
      title={text(`${feature}功能未启用`, `${feature} is unavailable`)}
      hint={
        unsupportedFormat
          ? text(
              `当前 Console 尚未支持 ${unsupportedFormat} 格式`,
              `This Console does not support the ${unsupportedFormat} format yet`,
            )
          : text(
              "当前后端构建尚未挂载此管理端点（返回 404）",
              "The current backend does not expose this endpoint (404)",
            )
      }
    />
  );
}
