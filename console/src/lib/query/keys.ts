/**
 * Query keys, one factory per resource. Keys nest from broad to narrow so a
 * mutation invalidates exactly the reads it can have changed:
 *
 *   invalidateQueries({ queryKey: repositoryKeys.all })        every repository read
 *   invalidateQueries({ queryKey: repositoryKeys.detail(id) }) one repository
 */
export const repositoryKeys = {
  all: ["repositories"] as const,
  list: () => [...repositoryKeys.all, "list"] as const,
  capacities: () => [...repositoryKeys.all, "capacities"] as const,
  detail: (id: string) => [...repositoryKeys.all, "detail", id] as const,
  capabilities: (id: string) =>
    [...repositoryKeys.detail(id), "capabilities"] as const,
  effectiveAccess: (id: string) =>
    [...repositoryKeys.detail(id), "effective-access"] as const,
  capacity: (id: string) => [...repositoryKeys.detail(id), "capacity"] as const,
};

export const formatKeys = {
  profiles: ["formats", "profiles"] as const,
};
