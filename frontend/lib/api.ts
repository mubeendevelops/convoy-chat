import type { ApiErrorBody, ApiErrorCode } from "./types";

const API_URL = process.env.NEXT_PUBLIC_API_URL ?? "http://localhost:8080";

// Locked decision (plan.md): JWT lives in localStorage, not a cookie — the
// accepted v1 XSS-readability tradeoff. Centralized here so Phase 10's auth
// hook/store reads and writes the same key rather than each picking its own.
const TOKEN_STORAGE_KEY = "convoychat_token";

export function getToken(): string | null {
  if (typeof window === "undefined") return null;
  return window.localStorage.getItem(TOKEN_STORAGE_KEY);
}

export function setToken(token: string): void {
  if (typeof window === "undefined") return;
  window.localStorage.setItem(TOKEN_STORAGE_KEY, token);
}

export function clearToken(): void {
  if (typeof window === "undefined") return;
  window.localStorage.removeItem(TOKEN_STORAGE_KEY);
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
  method?: "GET" | "POST" | "DELETE";
  body?: unknown;
  headers?: Record<string, string>;
  /** Attach the Bearer token from localStorage. Default true. */
  auth?: boolean;
}

function safeJsonParse(text: string): unknown {
  try {
    return JSON.parse(text);
  } catch {
    return undefined;
  }
}

async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
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
  delete: <T>(path: string, options?: BodylessOptions) =>
    request<T>(path, { ...options, method: "DELETE" }),
};
