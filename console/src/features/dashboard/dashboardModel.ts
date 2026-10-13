import type {
  OverviewRepositoryStatistics,
  OverviewWindowCounts,
} from "../../client";

export type StatisticsWindow = keyof OverviewWindowCounts;

export function sortRepositoryStatistics(
  repositories: OverviewRepositoryStatistics[],
  window: StatisticsWindow,
) {
  return [...repositories].sort(
    (left, right) =>
      right.requests[window] - left.requests[window] ||
      left.name.localeCompare(right.name),
  );
}
