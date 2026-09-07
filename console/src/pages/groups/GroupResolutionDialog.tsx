import { useCallback, useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { OrderedListOutlined, ReloadOutlined } from "@ant-design/icons";
import { Button, Space } from "antd";
import { getGroupResolution } from "../../client";
import type { Group, GroupResolution } from "../../client";
import { Badge } from "../../components/Badge";
import { EmptyState, ErrorBanner, Loading } from "../../components/Feedback";
import { Modal } from "../../components/Modal";
import { usePreferences } from "../../lib/preferences";

export function GroupResolutionDialog({ group }: { group: Group }) {
  const { text } = usePreferences();
  const [open, setOpen] = useState(false);
  const [plan, setPlan] = useState<GroupResolution | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const requestVersion = useRef(0);

  const load = useCallback(async () => {
    const version = ++requestVersion.current;
    setLoading(true);
    setError(null);
    try {
      const response = await getGroupResolution({
        path: { groupId: group.id },
      });
      if (version !== requestVersion.current) return;
      if (response.error || !response.data)
        throw response.error ?? new Error("Empty resolution response");
      setPlan(response.data);
    } catch (requestError) {
      if (version === requestVersion.current) setError(requestError);
    } finally {
      if (version === requestVersion.current) setLoading(false);
    }
  }, [group.id]);

  useEffect(() => {
    if (open) void load();
    return () => {
      requestVersion.current += 1;
    };
  }, [load, open]);

  return (
    <>
      <Button
        size="small"
        icon={<OrderedListOutlined aria-hidden />}
        onClick={() => setOpen(true)}
      >
        {text("解析顺序", "Resolution order")}
      </Button>
      <Modal
        open={open}
        onClose={() => setOpen(false)}
        title={text(
          `解析顺序：${group.name}`,
          `Resolution order: ${group.name}`,
        )}
        footer={
          <Space>
            <Button
              aria-label={text("刷新", "Refresh")}
              icon={<ReloadOutlined aria-hidden />}
              loading={loading}
              onClick={() => void load()}
            >
              {text("刷新", "Refresh")}
            </Button>
            <Button
              aria-label={text("关闭", "Close")}
              onClick={() => setOpen(false)}
            >
              {text("关闭", "Close")}
            </Button>
          </Space>
        }
      >
        <div className="ag-page-stack ag-group-resolution">
          <p className="text-sm text-zinc-400">
            {text(
              "Hosted 优先，同类型成员按配置位置排序。这里展示候选顺序，具体命中还取决于制品、读取权限和协议规则。",
              "Hosted members come first; members of the same type follow their configured positions. These are candidates, while actual hits depend on the artifact, read permissions and protocol rules.",
            )}
          </p>
          {error !== null && (
            <ErrorBanner error={error} onRetry={() => void load()} />
          )}
          {!plan && loading && error === null ? (
            <Loading />
          ) : (
            plan && (
              <>
                {plan.excludedMemberCount > 0 && (
                  <p className="text-sm text-zinc-400">
                    {text(
                      `${plan.excludedMemberCount} 个配置成员当前未参与解析。`,
                      `${plan.excludedMemberCount} configured member(s) are currently excluded from resolution.`,
                    )}
                  </p>
                )}
                {plan.members.length === 0 ? (
                  <EmptyState
                    compact
                    title={text("暂无可参与解析的成员", "No eligible members")}
                  />
                ) : (
                  <ol
                    className="flex flex-col gap-3"
                    aria-label={text(
                      "实际候选顺序",
                      "Effective candidate order",
                    )}
                  >
                    {plan.members.map((member) => (
                      <li
                        key={member.repositoryId}
                        className="flex min-w-0 items-start gap-3 border-b border-zinc-800 pb-3"
                        data-testid="resolution-member"
                      >
                        <span className="shrink-0 font-mono text-sm text-zinc-500">
                          {member.resolutionOrder}.
                        </span>
                        <div className="flex min-w-0 flex-1 flex-col gap-2">
                          <Link
                            className="break-all text-sm font-medium"
                            to={`/repositories/${member.repositoryId}`}
                          >
                            {member.repositoryName}
                          </Link>
                          <div className="flex flex-wrap items-center gap-2">
                            <Badge>{member.type}</Badge>
                            <span className="text-xs text-zinc-500">
                              {text(
                                `配置位置 ${member.configuredPosition + 1}`,
                                `Configured position ${member.configuredPosition + 1}`,
                              )}
                            </span>
                          </div>
                        </div>
                      </li>
                    ))}
                  </ol>
                )}
              </>
            )
          )}
        </div>
      </Modal>
    </>
  );
}
