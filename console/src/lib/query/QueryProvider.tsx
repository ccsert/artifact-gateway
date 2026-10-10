import { QueryClientProvider, useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { useAuth } from "../auth";
import { createQueryClient } from "./client";

/**
 * Cached reads belong to one signed-in principal. When the token or the
 * resolved actor changes — sign-out, a different user, a replaced token —
 * every cached answer is dropped so protected data never outlives its owner.
 */
function IdentityCacheScope() {
  const queryClient = useQueryClient();
  const { token, identity } = useAuth();
  const principal = `${token}\u0000${identity?.actor ?? ""}`;
  const previous = useRef(principal);

  useEffect(() => {
    if (previous.current === principal) return;
    previous.current = principal;
    queryClient.clear();
  }, [principal, queryClient]);

  return null;
}

/** Must sit inside AuthProvider. */
export function ConsoleQueryProvider({ children }: { children: ReactNode }) {
  const [queryClient] = useState(createQueryClient);
  return (
    <QueryClientProvider client={queryClient}>
      <IdentityCacheScope />
      {children}
    </QueryClientProvider>
  );
}
