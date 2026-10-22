import "server-only";

import { redirect } from "next/navigation";

import { api } from "./api";
import type { OrgView } from "./types";

// requireOrg loads the signed-in organisation or sends the visitor to sign in.
export async function requireOrg(): Promise<OrgView> {
  const res = await api<OrgView>("/v1/organization");
  if (!res.ok) {
    if (res.status === 401 || res.status === 403) redirect("/signin");
    throw new Error(res.error.message);
  }
  return res.data;
}
