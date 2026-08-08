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
  const [identity, setIdentity] = useState<CurrentIdentity | null>(null);
  const [identityLoading, setIdentityLoading] = useState(Boolean(token));

  useEffect(() => {
    applyToken(token);
  }, [token]);

  const setToken = useCallback((next: string, nextRole = "") => {
    const trimmed = next.trim();
    localStorage.setItem(TOKEN_KEY, trimmed);
    setTokenState(trimmed);
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
    setIdentity(null);
    setIdentityLoading(false);
  }, []);

  useEffect(() => {
    if (!token) {
      setIdentity(null);
      setIdentityLoading(false);
      return;
    }
    let cancelled = false;
    setIdentityLoading(true);
    void fetch("/api/v2/identity", {
      headers: { Authorization: `Bearer ${token}` },
    })
      .then(async (response) => {
        if (cancelled) return;
        if (response.status === 401) {
          clearToken();
          return;
        }
        if (!response.ok) return;
        const current = (await response.json()) as CurrentIdentity;
        if (cancelled) return;
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
  }, [token, clearToken]);

  return (
    <AuthContext.Provider
      value={{
        token,
        role,
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
