import { useAuthStore } from "./auth-store";
import type { ApiErrorBody, ApiErrorCode, AuthResponse } from "./types";

const API_URL = process.env.NEXT_PUBLIC_API_URL ?? "http://localhost:8080";

// Locked decision (plan.md): JWT lives in localStorage, not a cookie — the
// accepted v1 XSS-readability tradeoff. The Zustand auth store (lib/auth-
// store.ts) is the single source of truth for it; getState() works outside
// React components, so this file just reads from it rather than keeping a
// second copy of the token in its own localStorage key.
function getToken(): string | null {
  return useAuthStore.getState().token;
}

// Thrown for every non-2xx response. `code` matches the backend's
// {"error":{"code","message"}} envelope (internal/httpx) whenever the server
// actually returned that shape; falls back to "internal_error" for a
// malformed/non-JSON body or a network-level failure (server unreachable).
export class ApiError extends Error {
  readonly status: number;
  readonly code: ApiErrorCode;

  constructor(status: number, code: ApiErrorCode, message: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
  }
}

interface RequestOptions {
  method?: "GET" | "POST" | "PATCH" | "DELETE";
  body?: unknown;
  headers?: Record<string, string>;
  /** Attach the Bearer token from the auth store. Default true. */
  auth?: boolean;
}

function safeJsonParse(text: string): unknown {
  try {
    return JSON.parse(text);
  } catch {
    return undefined;
  }
}

// ---- Access-token expiry + transparent refresh (Phase 3) ----

// Decodes a JWT's `exp` claim (seconds since epoch) with nothing but the
// standard base64url payload split — no new dependency for what's a
// one-line decode. Returns null for a missing/malformed token or one with
// no exp claim; callers treat that as "can't tell", not "expired".
function decodeJwtExpiryMs(token: string): number | null {
  try {
    const payload = token.split(".")[1];
    if (!payload) return null;
    const json = atob(payload.replace(/-/g, "+").replace(/_/g, "/"));
    const claims = JSON.parse(json) as { exp?: number };
    return typeof claims.exp === "number" ? claims.exp * 1000 : null;
  } catch {
    return null;
  }
}

// True when token is missing, malformed-in-a-way-we-can't-decode-but-that's-
// fine-defer-to-the-server, already expired, or expiring within thresholdMs.
// Used both by request()'s reactive 401 handling below and by
// useWebSocket.tsx, which needs to check *before* attempting a handshake
// (the WS upgrade never goes through request(), so it can't rely on a 401
// to trigger a refresh the way REST calls do).
export function isAccessTokenExpiringSoon(token: string | null, thresholdMs = 10_000): boolean {
  if (!token) return true;
  const expiryMs = decodeJwtExpiryMs(token);
  if (expiryMs === null) return false;
  return expiryMs - Date.now() <= thresholdMs;
}

let refreshPromise: Promise<string> | null = null;

async function doRefresh(): Promise<string> {
  const { refreshToken, clearAuth, setAuth } = useAuthStore.getState();
  if (!refreshToken) {
    clearAuth();
    throw new ApiError(401, "unauthorized", "no refresh token available");
  }

  let res: Response;
  try {
    res = await fetch(`${API_URL}/api/v1/auth/refresh`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ refresh_token: refreshToken }),
    });
  } catch {
    throw new ApiError(0, "internal_error", "could not reach the server");
  }

  const text = await res.text();
  const data = text ? safeJsonParse(text) : undefined;

  if (!res.ok) {
    clearAuth();
    const errorBody = data as Partial<ApiErrorBody> | undefined;
    const code = errorBody?.error?.code ?? "internal_error";
    const message = errorBody?.error?.message || res.statusText || "session refresh failed";
    throw new ApiError(res.status, code, message);
  }

  const auth = data as AuthResponse;
  setAuth(auth.user, auth.token, auth.refresh_token);
  return auth.token;
}

// Rotates the stored refresh token for a fresh access+refresh pair,
// updating the auth store on success (or clearing it on failure — an
// invalid/expired/reused refresh token means the session is over, full
// stop). Concurrent callers share one in-flight request rather than each
// rotating separately: two parallel rotations would each look valid to the
// server in isolation, but the second to land would find the first's
// rotation had already consumed the token it started with, tripping the
// backend's reuse detection (see internal/store's RotateRefreshToken) and
// revoking the whole session — exactly the failure mode this dedup exists to
// avoid. Deliberately bypasses request() below (a raw fetch) so it can never
// recursively trigger request()'s own 401-retry path.
export function refreshAccessToken(): Promise<string> {
  if (!refreshPromise) {
    refreshPromise = doRefresh().finally(() => {
      refreshPromise = null;
    });
  }
  return refreshPromise;
}

async function request<T>(path: string, options: RequestOptions = {}, allowRefreshRetry = true): Promise<T> {
  const { method = "GET", body, headers = {}, auth = true } = options;

  const requestHeaders: Record<string, string> = { ...headers };
  if (body !== undefined) {
    requestHeaders["Content-Type"] = "application/json";
  }
  if (auth) {
    const token = getToken();
    if (token) {
      requestHeaders["Authorization"] = `Bearer ${token}`;
    }
  }

  let res: Response;
  try {
    res = await fetch(`${API_URL}${path}`, {
      method,
      headers: requestHeaders,
      body: body !== undefined ? JSON.stringify(body) : undefined,
    });
  } catch {
    throw new ApiError(0, "internal_error", "could not reach the server");
  }

  const text = await res.text();
  const data = text ? safeJsonParse(text) : undefined;

  if (!res.ok) {
    // A 401 on an authed call most likely just means the access token
    // expired mid-session — refresh once and retry, transparently to the
    // caller. Never fires for auth:false calls (login/signup, which 401 for
    // real credential reasons) or for the retry itself (allowRefreshRetry
    // guards against looping on a refresh that didn't actually fix anything).
    if (res.status === 401 && auth && allowRefreshRetry) {
      try {
        await refreshAccessToken();
        return request<T>(path, options, false);
      } catch {
        // Refresh itself failed — refreshAccessToken() already cleared the
        // session; fall through and surface the original 401 below so the
        // caller's error handling (and the route guards reacting to the
        // now-null token) still run.
      }
    }
    const errorBody = data as Partial<ApiErrorBody> | undefined;
    const code = errorBody?.error?.code ?? "internal_error";
    const message = errorBody?.error?.message || res.statusText || "request failed";
    throw new ApiError(res.status, code, message);
  }

  return data as T;
}

type BodylessOptions = Omit<RequestOptions, "method" | "body">;

export const api = {
  get: <T>(path: string, options?: BodylessOptions) =>
    request<T>(path, { ...options, method: "GET" }),
  post: <T>(path: string, body?: unknown, options?: BodylessOptions) =>
    request<T>(path, { ...options, method: "POST", body }),
  patch: <T>(path: string, body?: unknown, options?: BodylessOptions) =>
    request<T>(path, { ...options, method: "PATCH", body }),
  delete: <T>(path: string, options?: BodylessOptions) =>
    request<T>(path, { ...options, method: "DELETE" }),
};
