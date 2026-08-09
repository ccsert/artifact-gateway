import {
  createContext,
  useCallback,
  useContext,
  useEffect,
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

  useEffect(() => {
    applyToken(token);
  }, [token]);

  const setToken = useCallback((next: string, nextRole = "") => {
    const trimmed = next.trim();
    localStorage.setItem(TOKEN_KEY, trimmed);
    setTokenState(trimmed);
    setAuthenticated(Boolean(trimmed));
    if (nextRole) localStorage.setItem(ROLE_KEY, nextRole);
    else localStorage.removeItem(ROLE_KEY);
    setRole(nextRole);
    setIdentity(null);
  }, []);

  const clearToken = useCallback(() => {
    localStorage.removeItem(TOKEN_KEY);
    localStorage.removeItem(ROLE_KEY);
    setTokenState("");
    setRole("");
    setAuthenticated(false);
    setIdentity(null);
    setIdentityLoading(false);
    void fetch("/auth/logout", { method: "POST", credentials: "include" });
  }, []);

  useEffect(() => {
    let cancelled = false;
    setIdentityLoading(true);
    void fetch("/auth/session", {
      credentials: "include",
      headers: token ? { Authorization: `Bearer ${token}` } : undefined,
    })
      .then(async (response) => {
        if (cancelled) return;
        if (!response.ok) return;
        const session = (await response.json()) as {
          authenticated?: boolean;
          identity?: CurrentIdentity;
        };
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
        if (cancelled) return;
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
        if (!cancelled) setIdentityLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [token]);

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
