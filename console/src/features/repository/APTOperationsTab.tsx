import { useState } from "react";
import { Input } from "antd";
import type { Repository } from "../../client";
import { EmptyState, Notice } from "../../components/ui/Feedback";
import { Field } from "../../components/ui/Layout";
import { usePreferences } from "../../lib/preferences";

import { APTSuiteOperations } from "./APTSuiteOperations";

export function APTOperationsTab({
  repo,
  canAdmin,
}: {
  repo: Repository;
  canAdmin: boolean;
}) {
  const { text } = usePreferences();
  const [suite, setSuite] = useState("stable");
  const [query, setQuery] = useState("stable");
  if (!canAdmin)
    return (
      <EmptyState
        title={text(
          "需要仓库管理权限",
          "Repository administrator access required",
        )}
      />
    );
  return (
    <div className="ag-page-stack ag-apt-operations">
      <Notice
        tone="info"
        closable={false}
        title={text(
          "APT 签名快照 · 操作预览",
          "APT signed snapshots · Operator preview",
        )}
        description={text(
          "成员变更会生成完整索引并签署新快照。应用需要已配置的签名服务；生产密钥托管尚未完成验收。",
          "Membership changes rebuild and sign a complete snapshot. Applying requires a configured signer; production key custody has not been accepted.",
        )}
      />
      <div className="ag-apt-toolbar">
        <Field
          label={text("发行套件", "Suite")}
          hint={text(
            "输入完整套件名，例如 stable 或 bookworm。",
            "Enter the exact suite, for example stable or bookworm.",
          )}
        >
          <Input.Search
            aria-label={text("发行套件", "Suite")}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onSearch={() => {
              if (query.trim()) setSuite(query.trim());
            }}
            enterButton={text("查看", "View")}
          />
        </Field>
      </div>
      <APTSuiteOperations
        key={`${repo.id}:${suite}`}
        repo={repo}
        suite={suite}
      />
    </div>
  );
}
