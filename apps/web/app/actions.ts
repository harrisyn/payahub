"use server";

import { revalidatePath } from "next/cache";
import { cookies } from "next/headers";
import { redirect } from "next/navigation";

import { api, SESSION_COOKIE } from "@/lib/api";
import type { APIKey, Method, Profile } from "@/lib/types";

export type FormState = { error?: string; fields?: Record<string, string>; ok?: string };

// A key is shown once, right after it is made: it rides in a short-lived
// httpOnly cookie to the next page, which shows it until it is dismissed.
import { NEW_KEY_COOKIE } from "@/lib/flash";

const secure = process.env.NODE_ENV === "production";

async function setSession(token: string) {
  (await cookies()).set(SESSION_COOKIE, token, {
    httpOnly: true,
    secure,
    sameSite: "lax",
    path: "/",
    maxAge: 30 * 24 * 3600,
  });
}

async function flashKey(key: string, env: string) {
  (await cookies()).set(NEW_KEY_COOKIE, JSON.stringify({ key, env }), {
    httpOnly: true,
    secure,
    sameSite: "strict",
    path: "/console",
    maxAge: 3600,
  });
}

const text = (f: FormData, k: string) => String(f.get(k) ?? "").trim();

export async function signUp(_: FormState, form: FormData): Promise<FormState> {
  const res = await api<{ session_token: string; sandbox_key: string }>("/v1/signup", {
    method: "POST",
    token: null,
    body: {
      name: text(form, "name"),
      email: text(form, "email"),
      organization: text(form, "organization"),
      country: text(form, "country") || "GH",
      password: String(form.get("password") ?? ""),
    },
  });
  if (!res.ok) {
    const details = res.error.details as Record<string, string> | undefined;
    return { error: res.error.message, fields: details && typeof details === "object" ? details : undefined };
  }
  await setSession(res.data.session_token);
  await flashKey(res.data.sandbox_key, "sandbox");
  redirect("/console/setup");
}

export async function signIn(_: FormState, form: FormData): Promise<FormState> {
  const res = await api<{ session_token: string }>("/v1/sessions", {
    method: "POST",
    token: null,
    body: { email: text(form, "email"), password: String(form.get("password") ?? "") },
  });
  if (!res.ok) return { error: res.error.message };
  await setSession(res.data.session_token);
  redirect("/console");
}

export async function signOut() {
  await api("/v1/sessions/current", { method: "DELETE" });
  const jar = await cookies();
  jar.delete(SESSION_COOKIE);
  jar.delete(NEW_KEY_COOKIE);
  redirect("/signin");
}

export async function dismissKey() {
  (await cookies()).delete({ name: NEW_KEY_COOKIE, path: "/console" });
  revalidatePath("/console", "layout");
}

export async function saveProfile(_: FormState, form: FormData): Promise<FormState> {
  const profile: Profile = {
    billing_system: text(form, "billing_system") as Profile["billing_system"],
    claims_leave_by: text(form, "claims_leave_by") as Profile["claims_leave_by"],
    monthly_claims: Number(text(form, "monthly_claims") || 0),
    has_developers: form.get("has_developers") === "yes",
    payers: form.getAll("payers").map(String),
  };
  const method = text(form, "method");
  if (method) profile.method = method as Method;
  const res = await api("/v1/organization/profile", { method: "PUT", body: profile });
  if (!res.ok) {
    const details = res.error.details as Record<string, string> | undefined;
    return { error: res.error.message, fields: details };
  }
  revalidatePath("/console", "layout");
  return { ok: "Saved" };
}

export async function chooseMethod(form: FormData) {
  const org = await api<{ organization: { profile: Profile | null } }>("/v1/organization");
  if (!org.ok || !org.data.organization.profile) return;
  await api("/v1/organization/profile", {
    method: "PUT",
    body: { ...org.data.organization.profile, method: text(form, "method") },
  });
  revalidatePath("/console", "layout");
}

export async function savePayer(_: FormState, form: FormData): Promise<FormState> {
  const payer = text(form, "payer");
  const env = text(form, "environment");
  const credentials: Record<string, string> = {};
  let any = false;
  for (const [k, v] of form.entries()) {
    if (k.startsWith("cred_")) {
      credentials[k.slice(5)] = String(v);
      if (String(v) !== "") any = true;
    }
  }
  const res = await api(`/v1/payer-settings/${encodeURIComponent(payer)}/${env}`, {
    method: "PUT",
    body: { enabled: form.get("enabled") === "on", ...(any ? { credentials } : {}) },
  });
  if (!res.ok) return { error: res.error.message };
  revalidatePath("/console", "layout");
  return { ok: any ? "Credentials saved and sealed. Check the connection next." : "Saved." };
}

export async function togglePayer(form: FormData) {
  const payer = text(form, "payer");
  const env = text(form, "environment");
  await api(`/v1/payer-settings/${encodeURIComponent(payer)}/${env}`, {
    method: "PUT",
    body: { enabled: form.get("enabled") === "true" },
  });
  revalidatePath("/console", "layout");
}

export async function checkPayer(_: FormState, form: FormData): Promise<FormState> {
  const res = await api<{ ok: boolean; message: string }>(
    `/v1/payer-settings/${encodeURIComponent(text(form, "payer"))}/${text(form, "environment")}/check`,
    { method: "POST" },
  );
  revalidatePath("/console", "layout");
  // The result is saved on the insurer setting and shown from there.
  return res.ok ? {} : { error: res.error.message };
}

export async function createKey(_: FormState, form: FormData): Promise<FormState> {
  const res = await api<APIKey>("/v1/api-keys", {
    method: "POST",
    body: { environment: text(form, "environment"), name: text(form, "name") },
  });
  if (!res.ok) return { error: res.error.message };
  await flashKey(res.data.key!, res.data.environment);
  revalidatePath("/console", "layout");
  return { ok: "Key created" };
}

export async function revokeKey(form: FormData) {
  await api(`/v1/api-keys/${encodeURIComponent(text(form, "id"))}`, { method: "DELETE" });
  revalidatePath("/console", "layout");
}

const testMembers = ["M-1001", "M-2044", "FLAKY-07", "LOSTREPLY-3", "REJECT-22"];

// sendTestClaim makes and submits one sandbox claim as the signed-in user,
// cycling through the sandbox's scripted paths so each try shows another.
export async function sendTestClaim(form: FormData) {
  const n = Number(text(form, "n") || 0);
  const member = testMembers[n % testMembers.length];
  const created = await api<{ id: string }>("/v1/claims", {
    method: "POST",
    body: {
      payer_id: "sandbox",
      member: { member_number: member },
      provider: { name: "Sandbox clinic" },
      encounter: { start_date: new Date().toISOString().slice(0, 10) },
      lines: [
        {
          description: "Consultation, general practitioner",
          unit_price: 15000,
          mapping: { source: { system: "local", value: "CONS" }, target: { system: "payer:sandbox", value: "C01" } },
        },
      ],
      diagnoses: [{ code: { system: "icd-10", value: "B54" }, role: "primary" }],
    },
  });
  if (created.ok) {
    await api(`/v1/claims/${created.data.id}/submit`, { method: "POST" });
    redirect(`/console/claims/${created.data.id}`);
  }
  revalidatePath("/console", "layout");
}

export async function choosePlan(_: FormState, form: FormData): Promise<FormState> {
  const res = await api<{ plan: string }>("/v1/organization/plan", { method: "PUT", body: { plan: text(form, "plan") } });
  if (!res.ok) return { error: res.error.message };
  revalidatePath("/console", "layout");
  return {
    ok:
      res.data.plan === "enterprise_requested"
        ? "Request sent. We will be in touch to agree terms; your trial carries on until then."
        : "Plan chosen. Live claims are no longer capped by the trial.",
  };
}
