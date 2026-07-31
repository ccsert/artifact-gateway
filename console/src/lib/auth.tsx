import { createContext, useCallback, useContext, useEffect, useState } from 'react';
import type { ReactNode } from 'react';
import { client } from '../client/client.gen';

const TOKEN_KEY = 'ag.console.token';

interface AuthContextValue {
  token: string;
  setToken: (token: string) => void;
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

  useEffect(() => {
    applyToken(token);
  }, [token]);

  const setToken = useCallback((next: string) => {
    const trimmed = next.trim();
    localStorage.setItem(TOKEN_KEY, trimmed);
    setTokenState(trimmed);
  }, []);

  const clearToken = useCallback(() => {
    localStorage.removeItem(TOKEN_KEY);
    setTokenState('');
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
    <AuthContext.Provider value={{ token, setToken, clearToken }}>
      {children}
    </AuthContext.Provider>
  );
}

export function useAuth(): AuthContextValue {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error('useAuth must be used within AuthProvider');
  return ctx;
}
