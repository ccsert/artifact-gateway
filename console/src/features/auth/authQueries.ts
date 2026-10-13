import { useQuery } from "@tanstack/react-query";

export interface OidcLoginConfig {
  enabled: boolean;
  issuer?: string;
}

/**
 * Whether single sign-on is configured. Discovery is optional: when it is
 * offline or refused, local and token sign-in remain available.
 */
export function useOidcLoginConfig() {
  return useQuery({
    queryKey: ["auth", "oidc-login-config"],
    queryFn: async ({ signal }): Promise<OidcLoginConfig> => {
      try {
        const response = await fetch("/auth/oidc/config", {
          credentials: "include",
          signal,
        });
        if (!response.ok) return { enabled: false };
        return (await response.json()) as OidcLoginConfig;
      } catch (error) {
        if (signal.aborted) throw error;
        return { enabled: false };
      }
    },
    meta: { authFailure: "ignore" },
    staleTime: 0,
  });
}
