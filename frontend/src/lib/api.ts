import axios from "axios";

export const api = axios.create({
  baseURL: "/api",
  // The session lives in an HttpOnly cookie the browser attaches by itself;
  // no token is ever stored where page scripts (or an XSS payload) can read it.
  withCredentials: true,
  // State-changing requests must carry this header. A cross-site page cannot
  // add it without a CORS preflight the server refuses (CSRF protection).
  headers: { "X-Requested-With": "squidadmin" },
});

/** Called when the server says the session is gone (expired, revoked, disabled). */
let onUnauthorized: (() => void) | null = null;
export function setUnauthorizedHandler(fn: (() => void) | null) {
  onUnauthorized = fn;
}

api.interceptors.response.use(
  (res) => res,
  (err) => {
    // A failed *login* is also a 401, but it isn't a lost session.
    const isLogin = err.config?.url?.includes("/auth/login");
    if (err.response?.status === 401 && !isLogin) onUnauthorized?.();
    return Promise.reject(err);
  },
);

/** WebSocket URL for an API path. The session cookie authenticates it. */
export function wsURL(path: string): string {
  const proto = window.location.protocol === "https:" ? "wss" : "ws";
  return `${proto}://${window.location.host}/api${path}`;
}

export function errorMessage(err: unknown, fallback: string): string {
  const e = err as { response?: { data?: { error?: string } } };
  return e.response?.data?.error ?? fallback;
}
