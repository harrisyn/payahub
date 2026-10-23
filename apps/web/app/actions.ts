"use server";

import { revalidatePath } from "next/cache";
import { cookies } from "next/headers";
import { redirect } from "next/navigation";

import { api, SESSION_COOKIE } from "@/lib/api";
import type { APIKey, Method, Profile, Suggestion } from "@/lib/types";

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

// savePayerUse turns a catalogue payer on or off for one environment and
// records whether the organisation has an account on its platform.
export async function savePayerUse(_: FormState, form: FormData): Promise<FormState> {
  const payer = text(form, "payer");
  const env = text(form, "environment");
  const account = text(form, "account");
  const res = await api(`/v1/payer-settings/${encodeURIComponent(payer)}/${env}`, {
    method: "PUT",
    body: { enabled: form.get("enabled") === "on", ...(account ? { account } : {}) },
  });
  if (!res.ok) return { error: res.error.message };
  revalidatePath("/console", "layout");
  return { ok: "Saved." };
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

// saveAccount seals the organisation's credentials on a platform (or on a
// standalone payer's own interface). Every field is sent together.
export async function saveAccount(_: FormState, form: FormData): Promise<FormState> {
  const connector = text(form, "connector");
  const env = text(form, "environment");
  const credentials: Record<string, string> = {};
  for (const [k, v] of form.entries()) {
    if (k.startsWith("cred_")) credentials[k.slice(5)] = String(v);
  }
  const res = await api(`/v1/connector-accounts/${encodeURIComponent(connector)}/${env}`, {
    method: "PUT",
    body: { credentials },
  });
  if (!res.ok) return { error: res.error.message };
  revalidatePath("/console", "layout");
  return { ok: "Account saved and sealed. Check the connection next." };
}

export async function checkAccount(_: FormState, form: FormData): Promise<FormState> {
  const res = await api<{ ok: boolean; message: string }>(
    `/v1/connector-accounts/${encodeURIComponent(text(form, "connector"))}/${text(form, "environment")}/check`,
    { method: "POST" },
  );
  revalidatePath("/console", "layout");
  // A saved account shows its result from the stored check; a connector with
  // no account to store (the sandbox payer) shows it here.
  if (!res.ok) return { error: res.error.message };
  return form.get("show") === "1" ? (res.data.ok ? { ok: res.data.message } : { error: res.data.message }) : {};
}

export type SuggestState = FormState & {
  rows?: { code: string; name: string; suggestions: Suggestion[] }[];
};

// suggestMappings reads a pasted list ("code, name" per line) and asks for
// standard items that may match each.
export async function suggestMappings(_: SuggestState, form: FormData): Promise<SuggestState> {
  const items = String(form.get("list") ?? "")
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean)
    .map((line) => {
      const m = line.match(/^([^,\t;]+)[,\t;]\s*(.+)$/);
      return m ? { code: m[1].trim(), name: m[2].trim() } : { code: line, name: line };
    });
  if (!items.length) return { error: "Paste at least one line: your code, then the item's name." };
  if (items.length > 200) return { error: "Up to 200 lines at a time." };
  const res = await api<{ data: { code: string; name: string; suggestions: Suggestion[] }[] }>("/v1/items/suggest", {
    method: "POST",
    body: { items },
  });
  if (!res.ok) return { error: res.error.message };
  return { rows: res.data.data };
}

export async function saveMappings(_: FormState, form: FormData): Promise<FormState> {
  const mappings = form
    .getAll("row")
    .map(String)
    .map((i) => ({ local_code: text(form, `code_${i}`), local_name: text(form, `name_${i}`), item_id: text(form, `item_${i}`) }))
    .filter((m) => m.item_id);
  if (!mappings.length) return { error: "Choose a standard item for at least one line." };
  const res = await api("/v1/item-mappings", { method: "PUT", body: { mappings } });
  if (!res.ok) {
    const details = res.error.details as Record<string, string> | undefined;
    return { error: details ? Object.entries(details).map(([k, v]) => `${k}: ${v}`).join(" ") : res.error.message };
  }
  revalidatePath("/console", "layout");
  return { ok: `${mappings.length} mapping${mappings.length === 1 ? "" : "s"} saved.` };
}

export async function deleteMapping(form: FormData) {
  await api(`/v1/item-mappings/${encodeURIComponent(text(form, "code"))}`, { method: "DELETE" });
  revalidatePath("/console", "layout");
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
      // Standard item codes: the API turns each into the insurer's own code.
      lines: [
        { description: "Consultation, general practitioner", unit_price: 15000, code: { system: "payahub", value: "SRV-0001" } },
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

// Operator actions edit the shared catalogue. The API checks the operator
// role on every call; these only shape the forms.

export async function savePayer(_: FormState, form: FormData): Promise<FormState> {
  const id = text(form, "id").toLowerCase();
  const fields: Record<string, string> = {};
  for (const [k, v] of form.entries()) {
    if (k.startsWith("field_")) fields[k.slice(6)] = String(v).trim();
  }
  const res = await api(`/v1/operator/payers/${encodeURIComponent(id)}`, {
    method: "PUT",
    body: {
      name: text(form, "name"),
      country: text(form, "country"),
      connector: text(form, "connector"),
      fields,
      notes: text(form, "notes"),
      inactive: form.get("inactive") === "on",
    },
  });
  if (!res.ok) {
    const details = res.error.details as Record<string, string> | undefined;
    return { error: res.error.message, fields: details && typeof details === "object" ? details : undefined };
  }
  revalidatePath("/console", "layout");
  if (form.get("new") === "1") redirect(`/console/operator/${id}`);
  return { ok: "Saved. Every organisation sees the change within a few seconds." };
}

export type CodesState = FormState & { warnings?: string[] };

// saveCodes sends only the rows whose code the operator changed; an emptied
// code removes the payer's code for that item.
export async function saveCodes(_: CodesState, form: FormData): Promise<CodesState> {
  const payer = text(form, "payer");
  const changes: { item_id: string; code: string }[] = [];
  for (const [k, v] of form.entries()) {
    if (!k.startsWith("code:")) continue;
    const item = k.slice(5);
    const code = String(v).trim();
    if (code !== text(form, `was:${item}`)) changes.push({ item_id: item, code });
  }
  if (!changes.length) return { error: "Nothing changed." };
  const res = await api<{ applied: number; warnings: string[] }>(`/v1/operator/payers/${encodeURIComponent(payer)}/codes`, {
    method: "PUT",
    body: { changes },
  });
  if (!res.ok) {
    const details = res.error.details as Record<string, string> | undefined;
    return { error: res.error.message, fields: details && typeof details === "object" ? details : undefined };
  }
  revalidatePath("/console/operator", "layout");
  return { ok: `${res.data.applied} ${res.data.applied === 1 ? "code" : "codes"} saved.`, warnings: res.data.warnings };
}

export async function acceptSuggestions(_: FormState, form: FormData): Promise<FormState> {
  const payer = text(form, "payer");
  const res = await api<{ applied: number }>(`/v1/operator/payers/${encodeURIComponent(payer)}/codes/accept`, {
    method: "POST",
    body: { min_confidence: Number(text(form, "min_confidence")) || 0.9 },
  });
  if (!res.ok) return { error: res.error.message };
  revalidatePath("/console/operator", "layout");
  return res.data.applied
    ? { ok: `${res.data.applied} ${res.data.applied === 1 ? "match" : "matches"} accepted.` }
    : { error: "No match was sure enough. Map the rest by hand." };
}

export type ListState = FormState & { skipped?: { line: number; reason: string }[] };

export async function loadPayerList(_: ListState, form: FormData): Promise<ListState> {
  const payer = text(form, "payer");
  const file = form.get("file");
  let csv = text(form, "csv");
  if (file instanceof File && file.size > 0) csv = await file.text();
  if (!csv) return { error: "Choose a CSV file or paste the list." };
  const res = await api<{ entries: number; skipped: { line: number; reason: string }[] }>(
    `/v1/operator/payers/${encodeURIComponent(payer)}/list`,
    { method: "PUT", body: { csv } },
  );
  if (!res.ok) return { error: res.error.message };
  revalidatePath("/console/operator", "layout");
  return { ok: `${res.data.entries.toLocaleString("en-GB")} codes loaded. This replaces the earlier list.`, skipped: res.data.skipped };
}

// Price list. The file goes to the API as base64; prices are typed in major
// units here and sent in minor units.

export type ImportState = FormState & {
  skipped?: { line: number; reason: string }[];
  changes?: { added: number; changed: number; unchanged: number; removed: number };
};

export async function importPrices(_: ImportState, form: FormData): Promise<ImportState> {
  const file = form.get("file");
  if (!(file instanceof File) || file.size === 0) return { error: "Choose a CSV or Excel file." };
  if (file.size > 16 << 20) return { error: "The file is over 16 MB. Split it and import each part." };
  const res = await api<{ rows: number; skipped: { line: number; reason: string }[]; changes: ImportState["changes"] }>(
    "/v1/price-list/import",
    {
      method: "POST",
      body: {
        file: Buffer.from(await file.arrayBuffer()).toString("base64"),
        mode: text(form, "mode") === "replace" ? "replace" : "merge",
        currency: text(form, "currency") || "GHS",
      },
    },
  );
  if (!res.ok) return { error: res.error.message };
  revalidatePath("/console", "layout");
  return { ok: `${res.data.rows.toLocaleString("en-GB")} prices read.`, skipped: res.data.skipped, changes: res.data.changes };
}

export async function addPrice(_: FormState, form: FormData): Promise<FormState> {
  const price = Number(text(form, "price").replace(/,/g, ""));
  if (!Number.isFinite(price) || price < 0 || text(form, "price") === "") return { error: "Enter the price, like 45.00." };
  const res = await api("/v1/price-list", {
    method: "PUT",
    body: {
      prices: [
        {
          local_code: text(form, "code"),
          name: text(form, "name"),
          kind: text(form, "kind"),
          unit_price: Math.round(price * 100),
          currency: text(form, "currency") || "GHS",
        },
      ],
    },
  });
  if (!res.ok) {
    const details = res.error.details as Record<string, string> | undefined;
    return { error: details ? `This line has ${Object.values(details).join(", ")}.` : res.error.message };
  }
  revalidatePath("/console", "layout");
  return { ok: "Saved." };
}

export async function deletePrice(form: FormData) {
  await api(`/v1/price-list/${encodeURIComponent(text(form, "code"))}`, { method: "DELETE" });
  revalidatePath("/console", "layout");
}

// confirmItems saves the standard item for one or more of the price list's
// codes, as a person's confirmation.
export async function confirmItems(form: FormData) {
  const mappings = form.getAll("pick").map((v) => {
    const [local_code, item_id, ...name] = String(v).split("\u001f");
    return { local_code, item_id, local_name: name.join("") };
  });
  for (let i = 0; i < mappings.length; i += 500) {
    const res = await api("/v1/item-mappings", { method: "PUT", body: { mappings: mappings.slice(i, i + 500) } });
    if (!res.ok) break;
  }
  revalidatePath("/console", "layout");
}

export type PayerImportRow = {
  line: number;
  id: string;
  name: string;
  connector: string;
  action: "add" | "fill" | "same" | "conflict" | "skip";
  fields?: Record<string, string>;
  reason?: string;
};
export type PayerImportState = FormState & { rows?: PayerImportRow[]; file?: string; saved?: number };

// importPayers checks a file first (nothing saved) and keeps it, so the
// operator can import exactly what they saw.
export async function importPayers(_: PayerImportState, form: FormData): Promise<PayerImportState> {
  const upload = form.get("upload");
  let file = text(form, "file");
  if (upload instanceof File && upload.size > 0) {
    if (upload.size > 6 << 20) return { error: "The file is over 6 MB." };
    file = Buffer.from(await upload.arrayBuffer()).toString("base64");
  }
  if (!file) return { error: "Choose the CSV or Excel export of Serenity's payers." };
  const dryRun = text(form, "intent") !== "import";
  const res = await api<{ rows: PayerImportRow[]; saved: number }>("/v1/operator/payers/import", {
    method: "POST",
    body: { file, dry_run: dryRun },
  });
  if (!res.ok) return { error: res.error.message };
  if (dryRun) return { rows: res.data.rows, file };
  revalidatePath("/console", "layout");
  return {
    rows: res.data.rows,
    saved: res.data.saved,
    ok: `${res.data.saved} ${res.data.saved === 1 ? "payer" : "payers"} saved.`,
  };
}

export type ImportedClaim = {
  reference: string;
  rows: number[];
  payer_id?: string;
  member_number?: string;
  lines: number;
  total: number;
  currency?: string;
  errors: { field: string; message: string }[];
  issues: { code: string; severity: "error" | "warning" | "review"; field: string; message: string }[];
  state?: "draft" | "needs_review" | "ready";
  claim_id?: string;
  replayed?: boolean;
  queued?: boolean;
};
export type ClaimUploadState = FormState & { claims?: ImportedClaim[]; file?: string; saved?: boolean };

// uploadClaims checks a file of claim lines first (nothing saved) and keeps
// it, so what is saved is exactly what the person saw.
export async function uploadClaims(_: ClaimUploadState, form: FormData): Promise<ClaimUploadState> {
  const upload = form.get("upload");
  let file = text(form, "file");
  if (upload instanceof File && upload.size > 0) {
    if (upload.size > 16 << 20) return { error: "The file is over 16 MB. Split it and upload each part." };
    file = Buffer.from(await upload.arrayBuffer()).toString("base64");
  }
  if (!file) return { error: "Choose a CSV or Excel file of claim lines." };
  const intent = text(form, "intent");
  const res = await api<{ claims: ImportedClaim[]; ready: number }>("/v1/claims/import", {
    method: "POST",
    body: { file, dry_run: intent === "check", submit: intent === "send" },
  });
  if (!res.ok) return { error: res.error.message };
  if (intent === "check") return { claims: res.data.claims, file };
  revalidatePath("/console", "layout");
  const saved = res.data.claims.filter((c) => c.claim_id).length;
  const queued = res.data.claims.filter((c) => c.queued).length;
  return {
    claims: res.data.claims,
    saved: true,
    ok: `${saved} ${saved === 1 ? "claim" : "claims"} saved${queued ? `, ${queued} sent to the insurer` : ""}.`,
  };
}
