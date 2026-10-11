import { useState } from "react";
import { Button, Select, Space } from "antd";
import { applyAuthorizationTemplate, listGrants } from "../../client";
import type { AuthorizationTemplate, Repository } from "../../client";
import { ErrorBanner } from "../../components/ui/Feedback";
import { Modal } from "../../components/ui/Modal";
import { usePreferences } from "../../lib/preferences";
import { accessKeys } from "../../lib/query/keys";
import { useInvalidateAccess } from "./accessQueries";

/**
 * Applies a template to one repository, guarded by that repository's current
 * grant version. Mount it with a new `key` per opening.
 */
export function ApplyTemplateDialog({
  open,
  template: applyTarget,
  repositories,
  onClose,
}: {
  open: boolean;
  template: AuthorizationTemplate | null;
  repositories: Repository[];
  onClose: () => void;
}) {
  const { text } = usePreferences();
  const invalidate = useInvalidateAccess();
  const [applyRepository, setApplyRepository] = useState("");
  const [applying, setApplying] = useState(false);
  const [applyError, setApplyError] = useState<unknown>(null);

  const apply = async () => {
    if (!applyTarget || !applyRepository) return;
    setApplying(true);
    setApplyError(null);
    const current = await listGrants({
      path: { repositoryId: applyRepository },
    });
    if (current.error || !current.data) {
      setApplying(false);
      setApplyError(
        current.error ??
          new Error(
            text(
              "加载仓库授权版本失败",
              "Failed to load repository grant version",
            ),
          ),
      );
      return;
    }
    const version =
      current.response?.headers.get("ETag")?.replaceAll('"', "") ?? "1";
    const result = await applyAuthorizationTemplate({
      path: { templateId: applyTarget.id },
      headers: { "If-Match": version },
      body: { repositoryId: applyRepository },
    });
    setApplying(false);
    if (result.error) {
      setApplyError(result.error);
      return;
    }
    await invalidate(accessKeys.grants());
    onClose();
  };

  return (
    <Modal
      open={open}
      title={text("应用授权模板", "Apply authorization template")}
      onClose={() => onClose()}
      busy={applying}
      footer={
        <Space>
          <Button onClick={() => onClose()} disabled={applying}>
            {text("取消", "Cancel")}
          </Button>
          <Button
            type="primary"
            loading={applying}
            disabled={!applyRepository}
            onClick={() => void apply()}
          >
            {text("应用", "Apply")}
          </Button>
        </Space>
      }
    >
      {applyError !== null && (
        <div className="mb-3">
          <ErrorBanner error={applyError} />
        </div>
      )}
      <p className="mb-3 text-sm text-fg-tertiary">
        {text(
          "选择目标仓库。应用会替换该仓库现有授权规则，并保留并发版本保护。",
          "Choose a target repository. Applying replaces its current grants with optimistic concurrency protection.",
        )}
      </p>
      <Select
        className="w-full"
        showSearch={{ optionFilterProp: "label" }}
        placeholder={text("选择仓库", "Select repository")}
        value={applyRepository || undefined}
        onChange={setApplyRepository}
        options={repositories
          .filter((repository) => repository.state === "active")
          .map((repository) => ({
            value: repository.id,
            label: `${repository.name} · ${repository.format}`,
          }))}
      />
    </Modal>
  );
}
