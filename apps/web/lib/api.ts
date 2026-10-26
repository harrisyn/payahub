import "server-only";

import { cookies } from "next/headers";

// The console talks to the Go API from the server only; the browser never
// sees the session token, which lives in an httpOnly cookie.
export const API_URL = process.env.PAYAHUB_API_URL ?? "http://localhost:8080";
export const SESSION_COOKIE = "payahub_session";

export type ApiError = { code: string; message: string; details?: Record<string, string> | unknown };
export type ApiResult<T> = { ok: true; status: number; data: T } | { ok: false; status: number; error: ApiError };

export async function api<T>(
  path: string,
  init: {
    method?: string;
    body?: unknown;
    token?: string | null;
    file?: { data: ArrayBuffer; type: string };
    headers?: Record<string, string>;
  } = {},
): Promise<ApiResult<T>> {
  const token = init.token === undefined ? (await cookies()).get(SESSION_COOKIE)?.value : init.token;
  let res: Response;
  try {
    res = await fetch(API_URL + path, {
      method: init.method ?? "GET",
      headers: {
        "Content-Type": init.file ? init.file.type : "application/json",
        ...(token ? { Authorization: `Bearer ${token}` } : {}),
        ...init.headers,
      },
      // A file goes as itself, with its own type; anything else as JSON.
      body: init.file ? init.file.data : init.body === undefined ? undefined : JSON.stringify(init.body),
      cache: "no-store",
    });
  } catch {
    return { ok: false, status: 503, error: { code: "api_unreachable", message: "We could not reach the Payahub API. Try again in a moment." } };
  }
  if (res.status === 204) return { ok: true, status: 204, data: undefined as T };
  const body = await res.json().catch(() => ({}));
  if (!res.ok) return { ok: false, status: res.status, error: body.error ?? { code: "error", message: res.statusText } };
  return { ok: true, status: res.status, data: body as T };
}
