import "server-only";

import { cookies } from "next/headers";

export const NEW_KEY_COOKIE = "payahub_new_key";

// readFlashKey returns a key made moments ago, which the console shows once.
export async function readFlashKey(): Promise<{ key: string; env: string } | null> {
  const raw = (await cookies()).get(NEW_KEY_COOKIE)?.value;
  if (!raw) return null;
  try {
    return JSON.parse(raw);
  } catch {
    return null;
  }
}
