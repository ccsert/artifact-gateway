import { useState } from "react";
import { Link } from "react-router-dom";
import { OrderedListOutlined, ReloadOutlined } from "@ant-design/icons";
import { Button, Space } from "antd";
import type { Group } from "../../../client";
import { Badge } from "../../../components/ui/Badge";
import {
  EmptyState,
  ErrorBanner,
  Loading,
} from "../../../components/ui/Feedback";
import { Modal } from "../../../components/ui/Modal";
import { usePreferences } from "../../../lib/preferences";
import { useGroupResolution } from "../groupQueries";
import { useQueryClient } from "@tanstack/react-query";
import { groupKeys } from "../../../lib/query/keys";
import "../group-pages.css";

export function GroupResolutionDialog({ group }: { group: Group }) {
  const { t } = usePreferences();
  const [open, setOpen] = useState(false);
  const planQuery = useGroupResolution(group.id, open);
  const plan = planQuery.data ?? null;
  const loading = planQuery.isFetching;
  const error = planQuery.error;
  const load = () => planQuery.refetch();
  const queryClient = useQueryClient();
  // Closing abandons a read in flight, so the next opening asks again
  // instead of showing an answer requested by an earlier opening.
  const close = () => {
    setOpen(false);
    void queryClient.cancelQueries({
      queryKey: groupKeys.resolution(group.id),
    });
  };

  return (
    <>
      <Button
        size="small"
        icon={<OrderedListOutlined aria-hidden />}
        onClick={() => setOpen(true)}
      >
        {t("groups.resolutionOrder")}
      </Button>
      <Modal
        open={open}
        onClose={() => close()}
        title={t("groups.resolutionOrder2", { name: group.name })}
        footer={
          <Space>
            <Button
              aria-label={t("groups.refresh")}
              icon={<ReloadOutlined aria-hidden />}
              loading={loading}
              onClick={() => void load()}
            >
              {t("groups.refresh")}
            </Button>
            <Button aria-label={t("groups.close")} onClick={() => close()}>
              {t("groups.close")}
            </Button>
          </Space>
        }
      >
        <div className="ag-page-stack ag-group-resolution">
          <p className="text-sm text-fg-secondary">
            {t("groups.hostedMembersComeFirstMembersOfTheSame2")}
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
                  <p className="text-sm text-fg-secondary">
                    {t(
                      "groups.configuredMemberSAreCurrentlyExcludedFromResolution",
                      { excludedMemberCount: plan.excludedMemberCount },
                    )}
                  </p>
                )}
                {plan.members.length === 0 ? (
                  <EmptyState compact title={t("groups.noEligibleMembers")} />
                ) : (
                  <ol
                    className="flex flex-col gap-3"
                    aria-label={t("groups.effectiveCandidateOrder")}
                  >
                    {plan.members.map((member) => (
                      <li
                        key={member.repositoryId}
                        className="flex min-w-0 items-start gap-3 border-b border-line pb-3"
                        data-testid="resolution-member"
                      >
                        <span className="shrink-0 font-mono text-sm text-fg-tertiary">
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
                            <span className="text-xs text-fg-tertiary">
                              {t("groups.configuredPosition", {
                                position: member.configuredPosition + 1,
                              })}
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
