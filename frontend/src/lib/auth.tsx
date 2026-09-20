import {
  createContext,
  type ReactNode,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
} from "react";
import { Navigate, useLocation } from "react-router-dom";
import { api, setUnauthorizedHandler } from "./api";

export type Role = "viewer" | "operator" | "admin";

export interface User {
  id: number;
  username: string;
  role: Role;
  disabled: boolean;
  must_change_password: boolean;
}

const rank: Record<Role, number> = { viewer: 1, operator: 2, admin: 3 };

interface AuthContextValue {
  user: User | null;
  /** False until the first /auth/me answer arrives. */
  ready: boolean;
  login: (username: string, password: string) => Promise<User>;
  logout: () => Promise<void>;
  refresh: () => Promise<void>;
  /** Whether the signed-in user holds at least this role. */
  can: (min: Role) => boolean;
}

const AuthContext = createContext<AuthContextValue | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<User | null>(null);
  const [ready, setReady] = useState(false);

  const refresh = useCallback(async () => {
    try {
      const res = await api.get<{ user: User }>("/auth/me");
      setUser(res.data.user);
    } catch {
      setUser(null);
    } finally {
      setReady(true);
    }
  }, []);

  useEffect(() => {
    refresh();
  }, [refresh]);

  // Any 401 from the API (session expired or revoked) drops us to the login page.
  useEffect(() => {
    setUnauthorizedHandler(() => setUser(null));
    return () => setUnauthorizedHandler(null);
  }, []);

  const login = useCallback(async (username: string, password: string) => {
    const res = await api.post<{ user: User }>("/auth/login", { username, password });
    setUser(res.data.user);
    return res.data.user;
  }, []);

  const logout = useCallback(async () => {
    try {
      await api.post("/auth/logout");
    } finally {
      setUser(null);
    }
  }, []);

  const value = useMemo<AuthContextValue>(
    () => ({
      user,
      ready,
      login,
      logout,
      refresh,
      can: (min) => !!user && rank[user.role] >= rank[min],
    }),
    [user, ready, login, logout, refresh],
  );

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth() {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuth must be used within AuthProvider");
  return ctx;
}

/**
 * Guards a page: sends anonymous visitors to the login page, forces users
 * with a temporary password to change it first, and (optionally) requires a
 * minimum role. The server enforces all of this too — this only decides what
 * the UI shows.
 */
export function RequireAuth({ children, min }: { children: ReactNode; min?: Role }) {
  const { user, ready, can } = useAuth();
  const location = useLocation();

  if (!ready) return null;
  if (!user) return <Navigate to="/login" replace />;
  if (user.must_change_password && location.pathname !== "/change-password") {
    return <Navigate to="/change-password" replace />;
  }
  if (min && !can(min)) return <Navigate to="/" replace />;
  return <>{children}</>;
}
