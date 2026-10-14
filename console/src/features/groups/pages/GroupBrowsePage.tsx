import { Link, useParams } from "react-router-dom";
import { ArrowLeftOutlined } from "@ant-design/icons";
import { PageHeader } from "../../../components/ui/Layout";
import { Badge, FormatBadge } from "../../../components/ui/Badge";
import { ErrorBanner, Loading } from "../../../components/ui/Feedback";
import { usePreferences } from "../../../lib/preferences";
import { RepositoryBrowseTree } from "../../repository/RepositoryBrowseTree";
import { useGroupBrowseRoot, useSetGroupBrowseRoot } from "../groupQueries";
import "../group-pages.css";

export function GroupBrowsePage() {
  const { groupId = "" } = useParams();
  const { t } = usePreferences();
  const rootQuery = useGroupBrowseRoot(groupId);
  const page = rootQuery.data ?? null;
  const error = rootQuery.error;
  const load = () => rootQuery.refetch();
  const setPage = useSetGroupBrowseRoot(groupId);
  return (
    <div className="ag-page-stack ag-group-browse-page">
      <Link to="/groups" className="ag-group-browse-back">
        <ArrowLeftOutlined aria-hidden /> {t("groups.backToGroups")}
      </Link>
      <PageHeader
        title={page?.groupName ?? t("groups.groupDirectory")}
        description={t("groups.exploreMemberArtifactsAndTraceEachLocalSource")}
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
              <span>{t("groups.readOnlyView")}</span>
            </div>
            <p>
              {t("groups.thisDirectoryContainsPublicationsAndKnownCacheAn")}
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
            aria-label={t("groups.visibleMemberCandidateOrder")}
          >
            <h3>{t("groups.visibleMemberCandidateOrder")}</h3>
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
