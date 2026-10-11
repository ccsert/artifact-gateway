import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
} from "react";
import type { ReactNode } from "react";
import { client } from "../client/client.gen";
import type { CurrentIdentity } from "../client/types.gen";

const TOKEN_KEY = "ag.console.token";
const ROLE_KEY = "ag.console.role";

interface AuthContextValue {
  token: string;
  role: string;
  authenticated: boolean;
  identity: CurrentIdentity | null;
  identityLoading: boolean;
  setToken: (token: string, role?: string) => void;
  clearToken: () => void;
  handleAuthFailure: (status: 401 | 403) => void;
}

const AuthContext = createContext<AuthContextValue | null>(null);

function applyToken(token: string) {
  client.setConfig({
    baseUrl: "/api/v2",
    auth: () => (token ? token : undefined),
    credentials: "include",
  });
}

export function AuthProvider({ children }: { children: ReactNode }) {
  const [token, setTokenState] = useState<string>(() => {
    const stored = localStorage.getItem(TOKEN_KEY) ?? "";
    applyToken(stored);
    return stored;
  });
  const [role, setRole] = useState<string>(
    () => localStorage.getItem(ROLE_KEY) ?? "",
  );
  const [authenticated, setAuthenticated] = useState(Boolean(token));
  const [identity, setIdentity] = useState<CurrentIdentity | null>(null);
  const [identityLoading, setIdentityLoading] = useState(true);
  const [sessionInvalidated, setSessionInvalidated] = useState(false);
  const sessionGeneration = useRef(0);

  useEffect(() => {
    applyToken(token);
  }, [token]);

  const setToken = useCallback((next: string, nextRole = "") => {
    const trimmed = next.trim();
    ++sessionGeneration.current;
    setSessionInvalidated(false);
    localStorage.setItem(TOKEN_KEY, trimmed);
    setTokenState(trimmed);
    setAuthenticated(Boolean(trimmed));
    if (nextRole) localStorage.setItem(ROLE_KEY, nextRole);
    else localStorage.removeItem(ROLE_KEY);
    setRole(nextRole);
    setIdentity(null);
  }, []);

  const clearToken = useCallback(() => {
    // Invalidate current probes synchronously and suppress cookie discovery
    // until an explicit new login. Logout may still be pending on the server.
    ++sessionGeneration.current;
    setSessionInvalidated(true);
    localStorage.removeItem(TOKEN_KEY);
    localStorage.removeItem(ROLE_KEY);
    setTokenState("");
    setRole("");
    setAuthenticated(false);
    setIdentity(null);
    setIdentityLoading(false);
    void fetch("/auth/logout", { method: "POST", credentials: "include" });
  }, []);

  const handleAuthFailure = useCallback(
    (status: 401 | 403) => {
      // Forbidden is a resource decision, not an expired session. Its caller
      // keeps the identity and shows the permission error instead of logging out.
      if (status === 401) clearToken();
    },
    [clearToken],
  );

  useEffect(() => {
    if (sessionInvalidated) return;
    let cancelled = false;
    const generation = sessionGeneration.current;
    const isCurrent = () =>
      !cancelled && generation === sessionGeneration.current;
    setIdentityLoading(true);
    void fetch("/auth/session", {
      credentials: "include",
      headers: token ? { Authorization: `Bearer ${token}` } : undefined,
    })
      .then(async (response) => {
        if (!isCurrent()) return;
        if (!response.ok) return;
        const session = (await response.json()) as {
          authenticated?: boolean;
          identity?: CurrentIdentity;
        };
        if (!isCurrent()) return;
        if (!session.authenticated || !session.identity) {
          localStorage.removeItem(TOKEN_KEY);
          localStorage.removeItem(ROLE_KEY);
          setTokenState("");
          setRole("");
          setAuthenticated(false);
          setIdentity(null);
          return;
        }
        const current = session.identity;
        if (!isCurrent()) return;
        setAuthenticated(true);
        setIdentity(current);
        const resolvedRole = current.role ?? "";
        setRole(resolvedRole);
        if (resolvedRole) localStorage.setItem(ROLE_KEY, resolvedRole);
        else localStorage.removeItem(ROLE_KEY);
      })
      .catch(() => {
        // A temporary network failure should not log the operator out.
      })
      .finally(() => {
        if (isCurrent()) setIdentityLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [token, sessionInvalidated]);

  return (
    <AuthContext.Provider
      value={{
        token,
        role,
        authenticated,
        identity,
        identityLoading,
        setToken,
        clearToken,
        handleAuthFailure,
      }}
    >
      {children}
    </AuthContext.Provider>
  );
}

export function useAuth(): AuthContextValue {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuth must be used within AuthProvider");
  return ctx;
}
