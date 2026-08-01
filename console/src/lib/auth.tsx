import { createContext, useCallback, useContext, useEffect, useState } from 'react';
import type { ReactNode } from 'react';
import { client } from '../client/client.gen';

const TOKEN_KEY = 'ag.console.token';
const ROLE_KEY = 'ag.console.role';

interface AuthContextValue {
  token: string;
  role: string;
  setToken: (token: string, role?: string) => void;
  clearToken: () => void;
}

const AuthContext = createContext<AuthContextValue | null>(null);

function applyToken(token: string) {
  client.setConfig({
    baseUrl: '/api/v2',
    auth: () => (token ? token : undefined),
  });
}

export function AuthProvider({ children }: { children: ReactNode }) {
  const [token, setTokenState] = useState<string>(() => {
    const stored = localStorage.getItem(TOKEN_KEY) ?? '';
    applyToken(stored);
    return stored;
  });
  const [role, setRole] = useState<string>(() => localStorage.getItem(ROLE_KEY) ?? '');

  useEffect(() => {
    applyToken(token);
  }, [token]);

  const setToken = useCallback((next: string, nextRole = '') => {
    const trimmed = next.trim();
    localStorage.setItem(TOKEN_KEY, trimmed);
    setTokenState(trimmed);
    if (nextRole) localStorage.setItem(ROLE_KEY, nextRole);
    else localStorage.removeItem(ROLE_KEY);
    setRole(nextRole);
  }, []);

  const clearToken = useCallback(() => {
    localStorage.removeItem(TOKEN_KEY);
    localStorage.removeItem(ROLE_KEY);
    setTokenState('');
    setRole('');
  }, []);

  useEffect(() => {
    if (!token) return;
    let cancelled = false;
    void fetch('/api/v2/repositories?pageSize=1', {
      headers: { Authorization: `Bearer ${token}` },
    }).then((response) => {
      if (!cancelled && response.status === 401) {
        clearToken();
      }
    }).catch(() => {
      // A temporary network failure should not log the operator out.
    });
    return () => {
      cancelled = true;
    };
  }, [token, clearToken]);

  return (
    <AuthContext.Provider value={{ token, role, setToken, clearToken }}>
      {children}
    </AuthContext.Provider>
  );
}

export function useAuth(): AuthContextValue {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error('useAuth must be used within AuthProvider');
  return ctx;
}
