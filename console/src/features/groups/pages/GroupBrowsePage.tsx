import { Link, useParams } from "react-router-dom";
import { ArrowLeftOutlined } from "@ant-design/icons";
import { PageHeader } from "../../../components/ui/Layout";
import { Badge, FormatBadge } from "../../../components/ui/Badge";
import { ErrorBanner, Loading } from "../../../components/ui/Feedback";
import { usePreferences } from "../../../lib/preferences";
import { RepositoryBrowseTree } from "../../repository/RepositoryBrowseTree";
import { useGroupBrowseRoot, useSetGroupBrowseRoot } from "../groupQueries";

export function GroupBrowsePage() {
  const { groupId = "" } = useParams();
  const { text } = usePreferences();
  const rootQuery = useGroupBrowseRoot(groupId);
  const page = rootQuery.data ?? null;
  const error = rootQuery.error;
  const load = () => rootQuery.refetch();
  const setPage = useSetGroupBrowseRoot(groupId);
  return (
    <div className="ag-page-stack ag-group-browse-page">
      <Link to="/groups" className="ag-group-browse-back">
        <ArrowLeftOutlined aria-hidden /> {text("返回分组", "Back to groups")}
      </Link>
      <PageHeader
        title={page?.groupName ?? text("分组目录", "Group directory")}
        description={text(
          "浏览成员提供的制品，追溯每个本地来源。",
          "Explore member artifacts and trace each local source.",
        )}
      />
      {error !== null && (
        <ErrorBanner error={error} onRetry={() => void load()} />
      )}
      {!page && error === null && <Loading />}
      {page && (
        <>
          <div className="ag-group-browse-context">
            <div className="ag-group-browse-labels">
              <FormatBadge format={page.format} />
              <Badge>Group</Badge>
              <span>{text("只读视图", "Read-only view")}</span>
            </div>
            <p>
              {text(
                "目录展示已发布制品与已知缓存。前序 Proxy 暂无缓存，不代表上游不存在；实际下载仍按协议解析。",
                "This directory contains publications and known cache. An earlier Proxy without cached data may still serve the artifact; downloads follow protocol resolution.",
              )}
            </p>
          </div>
          <RepositoryBrowseTree
            key={page.groupId}
            repo={{
              id: page.groupId,
              name: page.groupName,
              format: page.format,
              type: "group",
            }}
            initialPage={page}
            onGroupRefresh={setPage}
            onOpenInList={() => undefined}
          />
          <section
            className="ag-group-browse-candidates"
            aria-label={text(
              "可见成员候选顺序",
              "Visible member candidate order",
            )}
          >
            <h3>
              {text("可见成员候选顺序", "Visible member candidate order")}
            </h3>
            <ol>
              {page.candidates.map((candidate) => (
                <li key={candidate.repositoryId}>
                  <span>{candidate.resolutionOrder}</span>
                  <Link to={`/repositories/${candidate.repositoryId}`}>
                    {candidate.repositoryName}
                  </Link>
                  <Badge>{candidate.type}</Badge>
                </li>
              ))}
            </ol>
          </section>
        </>
      )}
    </div>
  );
}
