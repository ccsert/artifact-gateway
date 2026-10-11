import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback } from "react";
import { listApiKeys } from "../../client";
import { unwrap } from "../../lib/query/client";
import { identityKeys } from "../../lib/query/keys";

export function useApiKeys({ enabled = true } = {}) {
  return useQuery({
    queryKey: identityKeys.apiKeys(),
    queryFn: ({ signal }) => unwrap(listApiKeys({ signal })),
    enabled,
  });
}

export function useInvalidateApiKeys() {
  const queryClient = useQueryClient();
  return useCallback(
    () => queryClient.invalidateQueries({ queryKey: identityKeys.apiKeys() }),
    [queryClient],
  );
}
