import { EmptyState } from "../../components/Feedback";
import { usePreferences } from "../../lib/preferences";

export function RepositoryFeatureUnavailable({ feature }: { feature: string }) {
  const { text } = usePreferences();
  return (
    <EmptyState
      title={text(`${feature}功能未启用`, `${feature} is unavailable`)}
      hint={text(
        "当前后端构建尚未挂载此管理端点（返回 404）",
        "The current backend does not expose this endpoint (404)",
      )}
    />
  );
}
