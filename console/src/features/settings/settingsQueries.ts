import { useQuery } from "@tanstack/react-query";
import { getOidcSettings } from "../../client";
import { unwrap } from "../../lib/query/client";
import { settingsKeys } from "../../lib/query/keys";

/** The OIDC provider settings; the Authentication page writes the answer back. */
export function useOidcSettings(emptyMessage: string) {
  return useQuery({
    queryKey: settingsKeys.oidc(),
    queryFn: async ({ signal }) => {
      const settings = await unwrap(getOidcSettings({ signal }));
      if (!settings) throw new Error(emptyMessage);
      return settings;
    },
    staleTime: 0,
  });
}
