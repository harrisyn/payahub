"use server";

import { revalidatePath } from "next/cache";
import { cookies, headers } from "next/headers";
import { redirect } from "next/navigation";

import { api, SESSION_COOKIE } from "@/lib/api";
import type {
  AIProvider,
  APIKey,
  ClaimInput,
  MemberCheck,
  Extraction,
  Funding,
  Method,
  PayoutAccount,
  Profile,
  Suggestion,
  Withdrawal,
} from "@/lib/types";

export type FormState = {
  error?: string;
  fields?: Record<string, string>;
  ok?: string;
  // What was submitted, for a form that puts it back after a failed save.
  values?: Record<string, string | string[] | number | boolean>;
};

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
  const res = await api<{ session_token: string; sandbox_key: string }>(
    "/v1/signup",
    {
      method: "POST",
      token: null,
      body: {
        name: text(form, "name"),
        email: text(form, "email"),
        organization: text(form, "organization"),
        country: text(form, "country") || "GH",
        password: String(form.get("password") ?? ""),
      },
    },
  );
  if (!res.ok) {
    const details = res.error.details as Record<string, string> | undefined;
    return {
      error: res.error.message,
      fields: details && typeof details === "object" ? details : undefined,
    };
  }
  await setSession(res.data.session_token);
  await flashKey(res.data.sandbox_key, "sandbox");
  redirect("/console/setup");
}

export async function signIn(_: FormState, form: FormData): Promise<FormState> {
  const res = await api<{ session_token: string }>("/v1/sessions", {
    method: "POST",
    token: null,
    body: {
      email: text(form, "email"),
      password: String(form.get("password") ?? ""),
    },
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

export async function saveProfile(
  _: FormState,
  form: FormData,
): Promise<FormState> {
  const profile: Profile = {
    billing_system: text(form, "billing_system") as Profile["billing_system"],
    claims_leave_by: text(
      form,
      "claims_leave_by",
    ) as Profile["claims_leave_by"],
    monthly_claims: Number(text(form, "monthly_claims") || 0),
    has_developers: form.get("has_developers") === "yes",
    payers: form.getAll("payers").map(String),
  };
  const method = text(form, "method");
  if (method) profile.method = method as Method;
  const res = await api("/v1/organization/profile", {
    method: "PUT",
    body: profile,
  });
  if (!res.ok) {
    const details = res.error.details as Record<string, string> | undefined;
    return { error: res.error.message, fields: details };
  }
  revalidatePath("/console", "layout");
  return { ok: "Saved" };
}

export async function chooseMethod(form: FormData) {
  const org = await api<{ organization: { profile: Profile | null } }>(
    "/v1/organization",
  );
  if (!org.ok || !org.data.organization.profile) return;
  await api("/v1/organization/profile", {
    method: "PUT",
    body: { ...org.data.organization.profile, method: text(form, "method") },
  });
  revalidatePath("/console", "layout");
}

// savePayerUse turns a catalogue payer on or off for one environment and
// records whether the organisation has an account on its platform.
export async function savePayerUse(
  _: FormState,
  form: FormData,
): Promise<FormState> {
  const payer = text(form, "payer");
  const env = text(form, "environment");
  const account = text(form, "account");
  const res = await api(
    `/v1/payer-settings/${encodeURIComponent(payer)}/${env}`,
    {
      method: "PUT",
      body: {
        enabled: form.get("enabled") === "on",
        ...(account ? { account } : {}),
      },
    },
  );
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
export async function saveAccount(
  _: FormState,
  form: FormData,
): Promise<FormState> {
  const connector = text(form, "connector");
  const env = text(form, "environment");
  const credentials: Record<string, string> = {};
  for (const [k, v] of form.entries()) {
    if (k.startsWith("cred_")) credentials[k.slice(5)] = String(v);
  }
  const res = await api(
    `/v1/connector-accounts/${encodeURIComponent(connector)}/${env}`,
    {
      method: "PUT",
      body: { credentials },
    },
  );
  if (!res.ok) return { error: res.error.message };
  revalidatePath("/console", "layout");
  return { ok: "Account saved and sealed. Check the connection next." };
}

export async function checkAccount(
  _: FormState,
  form: FormData,
): Promise<FormState> {
  const res = await api<{ ok: boolean; message: string }>(
    `/v1/connector-accounts/${encodeURIComponent(text(form, "connector"))}/${text(form, "environment")}/check`,
    { method: "POST" },
  );
  revalidatePath("/console", "layout");
  // A saved account shows its result from the stored check; a connector with
  // no account to store (the sandbox payer) shows it here.
  if (!res.ok) return { error: res.error.message };
  return form.get("show") === "1"
    ? res.data.ok
      ? { ok: res.data.message }
      : { error: res.data.message }
    : {};
}

export type SuggestState = FormState & {
  rows?: { code: string; name: string; suggestions: Suggestion[] }[];
  method?: "words" | "model";
  note?: string;
};

// suggestMappings reads a pasted list ("code, name" per line) and asks for
// standard items that may match each.
export async function suggestMappings(
  _: SuggestState,
  form: FormData,
): Promise<SuggestState> {
  const items = String(form.get("list") ?? "")
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean)
    .map((line) => {
      const m = line.match(/^([^,\t;]+)[,\t;]\s*(.+)$/);
      return m
        ? { code: m[1].trim(), name: m[2].trim() }
        : { code: line, name: line };
    });
  if (!items.length)
    return {
      error: "Paste at least one line: your code, then the item's name.",
    };
  if (items.length > 200) return { error: "Up to 200 lines at a time." };
  const res = await api<{
    data: { code: string; name: string; suggestions: Suggestion[] }[];
    method: "words" | "model";
    note?: string;
  }>("/v1/items/suggest", {
    method: "POST",
    body: { items },
  });
  if (!res.ok) return { error: res.error.message };
  return { rows: res.data.data, method: res.data.method, note: res.data.note };
}

export async function saveMappings(
  _: FormState,
  form: FormData,
): Promise<FormState> {
  const mappings = form
    .getAll("row")
    .map(String)
    .map((i) => ({
      local_code: text(form, `code_${i}`),
      local_name: text(form, `name_${i}`),
      item_id: text(form, `item_${i}`),
    }))
    .filter((m) => m.item_id);
  if (!mappings.length)
    return { error: "Choose a standard item for at least one line." };
  const res = await api("/v1/item-mappings", {
    method: "PUT",
    body: { mappings },
  });
  if (!res.ok) {
    const details = res.error.details as Record<string, string> | undefined;
    return {
      error: details
        ? Object.entries(details)
            .map(([k, v]) => `${k}: ${v}`)
            .join(" ")
        : res.error.message,
    };
  }
  revalidatePath("/console", "layout");
  return {
    ok: `${mappings.length} mapping${mappings.length === 1 ? "" : "s"} saved.`,
  };
}

export async function deleteMapping(form: FormData) {
  await api(`/v1/item-mappings/${encodeURIComponent(text(form, "code"))}`, {
    method: "DELETE",
  });
  revalidatePath("/console", "layout");
}

export async function createKey(
  _: FormState,
  form: FormData,
): Promise<FormState> {
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
  await api(`/v1/api-keys/${encodeURIComponent(text(form, "id"))}`, {
    method: "DELETE",
  });
  revalidatePath("/console", "layout");
}

const testMembers = [
  "M-1001",
  "M-2044",
  "FLAKY-07",
  "LOSTREPLY-3",
  "REJECT-22",
];

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
        {
          description: "Consultation, general practitioner",
          unit_price: 15000,
          code: { system: "payahub", value: "SRV-0001" },
        },
      ],
      diagnoses: [
        { code: { system: "icd-10", value: "B54" }, role: "primary" },
      ],
    },
  });
  if (created.ok) {
    await api(`/v1/claims/${created.data.id}/submit`, { method: "POST" });
    redirect(`/console/claims/${created.data.id}`);
  }
  revalidatePath("/console", "layout");
}

export async function choosePlan(
  _: FormState,
  form: FormData,
): Promise<FormState> {
  const res = await api<{ plan: string }>("/v1/organization/plan", {
    method: "PUT",
    body: { plan: text(form, "plan") },
  });
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

export async function savePayer(
  _: FormState,
  form: FormData,
): Promise<FormState> {
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
    return {
      error: res.error.message,
      fields: details && typeof details === "object" ? details : undefined,
    };
  }
  revalidatePath("/console", "layout");
  if (form.get("new") === "1") redirect(`/console/operator/${id}`);
  return {
    ok: "Saved. Every organisation sees the change within a few seconds.",
  };
}

export type CodesState = FormState & { warnings?: string[] };

// saveCodes sends only the rows whose code the operator changed; an emptied
// code removes the payer's code for that item.
export async function saveCodes(
  _: CodesState,
  form: FormData,
): Promise<CodesState> {
  const payer = text(form, "payer");
  const changes: { item_id: string; code: string }[] = [];
  for (const [k, v] of form.entries()) {
    if (!k.startsWith("code:")) continue;
    const item = k.slice(5);
    const code = String(v).trim();
    if (code !== text(form, `was:${item}`))
      changes.push({ item_id: item, code });
  }
  if (!changes.length) return { error: "Nothing changed." };
  const res = await api<{ applied: number; warnings: string[] }>(
    `/v1/operator/payers/${encodeURIComponent(payer)}/codes`,
    {
      method: "PUT",
      body: { changes },
    },
  );
  if (!res.ok) {
    const details = res.error.details as Record<string, string> | undefined;
    return {
      error: res.error.message,
      fields: details && typeof details === "object" ? details : undefined,
    };
  }
  revalidatePath("/console/operator", "layout");
  return {
    ok: `${res.data.applied} ${res.data.applied === 1 ? "code" : "codes"} saved.`,
    warnings: res.data.warnings,
  };
}

export async function acceptSuggestions(
  _: FormState,
  form: FormData,
): Promise<FormState> {
  const payer = text(form, "payer");
  const res = await api<{ applied: number }>(
    `/v1/operator/payers/${encodeURIComponent(payer)}/codes/accept`,
    {
      method: "POST",
      body: { min_confidence: Number(text(form, "min_confidence")) || 0.9 },
    },
  );
  if (!res.ok) return { error: res.error.message };
  revalidatePath("/console/operator", "layout");
  return res.data.applied
    ? {
        ok: `${res.data.applied} ${res.data.applied === 1 ? "match" : "matches"} accepted.`,
      }
    : { error: "No match was sure enough. Map the rest by hand." };
}

export type ListState = FormState & {
  skipped?: { line: number; reason: string }[];
};

// savePayerRules sets how strictly a payer's list applies to claims.
export async function savePayerRules(
  _: FormState,
  form: FormData,
): Promise<FormState> {
  const payer = text(form, "payer");
  const res = await api(
    `/v1/operator/payers/${encodeURIComponent(payer)}/rules`,
    {
      method: "PUT",
      body: {
        price_policy: text(form, "price_policy"),
        unlisted_policy: text(form, "unlisted_policy"),
      },
    },
  );
  if (!res.ok) return { error: res.error.message };
  revalidatePath(`/console/operator/${payer}`);
  return { ok: "Saved. Claims are checked with these rules from now on." };
}

export async function loadPayerList(
  _: ListState,
  form: FormData,
): Promise<ListState> {
  const payer = text(form, "payer");
  const file = form.get("file");
  let csv = text(form, "csv");
  if (file instanceof File && file.size > 0) csv = await file.text();
  if (!csv) return { error: "Choose a CSV file or paste the list." };
  const res = await api<{
    entries: number;
    skipped: { line: number; reason: string }[];
  }>(`/v1/operator/payers/${encodeURIComponent(payer)}/list`, {
    method: "PUT",
    body: { csv },
  });
  if (!res.ok) return { error: res.error.message };
  revalidatePath("/console/operator", "layout");
  return {
    ok: `${res.data.entries.toLocaleString("en-GB")} codes loaded. This replaces the earlier list.`,
    skipped: res.data.skipped,
  };
}

// Price list. The file goes to the API as base64; prices are typed in major
// units here and sent in minor units.

export type ImportState = FormState & {
  skipped?: { line: number; reason: string }[];
  changes?: {
    added: number;
    changed: number;
    unchanged: number;
    removed: number;
  };
};

export async function importPrices(
  _: ImportState,
  form: FormData,
): Promise<ImportState> {
  const file = form.get("file");
  if (!(file instanceof File) || file.size === 0)
    return { error: "Choose a CSV or Excel file." };
  if (file.size > 16 << 20)
    return { error: "The file is over 16 MB. Split it and import each part." };
  const res = await api<{
    rows: number;
    skipped: { line: number; reason: string }[];
    changes: ImportState["changes"];
  }>("/v1/price-list/import", {
    method: "POST",
    body: {
      file: Buffer.from(await file.arrayBuffer()).toString("base64"),
      mode: text(form, "mode") === "replace" ? "replace" : "merge",
      currency: text(form, "currency") || "GHS",
    },
  });
  if (!res.ok) return { error: res.error.message };
  revalidatePath("/console", "layout");
  return {
    ok: `${res.data.rows.toLocaleString("en-GB")} prices read.`,
    skipped: res.data.skipped,
    changes: res.data.changes,
  };
}

export async function addPrice(
  _: FormState,
  form: FormData,
): Promise<FormState> {
  const price = Number(text(form, "price").replace(/,/g, ""));
  if (!Number.isFinite(price) || price < 0 || text(form, "price") === "")
    return { error: "Enter the price, like 45.00." };
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
    return {
      error: details
        ? `This line has ${Object.values(details).join(", ")}.`
        : res.error.message,
    };
  }
  revalidatePath("/console", "layout");
  return { ok: "Saved." };
}

export async function deletePrice(form: FormData) {
  await api(`/v1/price-list/${encodeURIComponent(text(form, "code"))}`, {
    method: "DELETE",
  });
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
    const res = await api("/v1/item-mappings", {
      method: "PUT",
      body: { mappings: mappings.slice(i, i + 500) },
    });
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
export type PayerImportState = FormState & {
  rows?: PayerImportRow[];
  file?: string;
  saved?: number;
};

// importPayers checks a file first (nothing saved) and keeps it, so the
// operator can import exactly what they saw.
export async function importPayers(
  _: PayerImportState,
  form: FormData,
): Promise<PayerImportState> {
  const upload = form.get("upload");
  let file = text(form, "file");
  if (upload instanceof File && upload.size > 0) {
    if (upload.size > 6 << 20) return { error: "The file is over 6 MB." };
    file = Buffer.from(await upload.arrayBuffer()).toString("base64");
  }
  if (!file)
    return { error: "Choose the CSV or Excel export of Serenity's payers." };
  const dryRun = text(form, "intent") !== "import";
  const res = await api<{ rows: PayerImportRow[]; saved: number }>(
    "/v1/operator/payers/import",
    {
      method: "POST",
      body: { file, dry_run: dryRun },
    },
  );
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
  issues: {
    code: string;
    severity: "error" | "warning" | "review";
    field: string;
    message: string;
  }[];
  state?: "draft" | "needs_review" | "ready";
  claim_id?: string;
  replayed?: boolean;
  queued?: boolean;
};
export type ClaimUploadState = FormState & {
  claims?: ImportedClaim[];
  file?: string;
  saved?: boolean;
};

// uploadClaims checks a file of claim lines first (nothing saved) and keeps
// it, so what is saved is exactly what the person saw.
export async function uploadClaims(
  _: ClaimUploadState,
  form: FormData,
): Promise<ClaimUploadState> {
  const upload = form.get("upload");
  let file = text(form, "file");
  if (upload instanceof File && upload.size > 0) {
    if (upload.size > 16 << 20)
      return {
        error: "The file is over 16 MB. Split it and upload each part.",
      };
    file = Buffer.from(await upload.arrayBuffer()).toString("base64");
  }
  if (!file) return { error: "Choose a CSV or Excel file of claim lines." };
  const intent = text(form, "intent");
  const res = await api<{ claims: ImportedClaim[]; ready: number }>(
    "/v1/claims/import",
    {
      method: "POST",
      body: { file, dry_run: intent === "check", submit: intent === "send" },
    },
  );
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

// Money typed in cedis becomes pesewas; anything else is refused rather
// than guessed.
function cedis(raw: string): number | null {
  const s = raw.replace(/[,\s]/g, "").replace(/^GHS/i, "");
  if (!/^\d+(\.\d{1,2})?$/.test(s)) return null;
  return Math.round(parseFloat(s) * 100);
}

async function origin(): Promise<string> {
  const h = await headers();
  const host = h.get("x-forwarded-host") ?? h.get("host") ?? "localhost:3000";
  const proto =
    h.get("x-forwarded-proto") ??
    (host.startsWith("localhost") ? "http" : "https");
  return `${proto}://${host}`;
}

// addFunds opens a checkout at the chosen provider and sends the browser
// there; the provider sends it back to the wallet page, which settles it.
export async function addFunds(
  _: FormState,
  form: FormData,
): Promise<FormState> {
  const amount = cedis(text(form, "amount"));
  if (amount === null)
    return {
      fields: { amount: "Enter an amount in cedis, like 500 or 500.00." },
    };
  const res = await api<Funding>("/v1/wallet/fundings", {
    method: "POST",
    body: {
      amount,
      provider: text(form, "provider"),
      callback_url: `${await origin()}/console/wallet`,
    },
  });
  if (!res.ok) {
    const d = res.error.details as Record<string, string> | undefined;
    return {
      error: d && typeof d === "object" ? undefined : res.error.message,
      fields: d && typeof d === "object" ? d : undefined,
    };
  }
  if (!res.data.checkout_url)
    return { error: "The provider did not give a payment page. Try again." };
  redirect(res.data.checkout_url);
}

export async function addPayoutAccount(
  _: FormState,
  form: FormData,
): Promise<FormState> {
  const res = await api<PayoutAccount>("/v1/wallet/payout-accounts", {
    method: "POST",
    body: {
      provider: text(form, "provider"),
      type: text(form, "type"),
      name: text(form, "name"),
      bank_code: text(form, "bank_code"),
      account_number: text(form, "account_number"),
    },
  });
  if (!res.ok) {
    const d = res.error.details as Record<string, string> | undefined;
    return {
      error: res.error.message,
      fields: d && typeof d === "object" ? d : undefined,
    };
  }
  revalidatePath("/console/wallet");
  return {
    ok: `${res.data.bank_name || res.data.bank_code} ${res.data.account_number} added.`,
  };
}

export async function removePayoutAccount(form: FormData) {
  await api(
    `/v1/wallet/payout-accounts/${encodeURIComponent(text(form, "id"))}`,
    { method: "DELETE" },
  );
  revalidatePath("/console/wallet");
}

export async function requestWithdrawal(
  _: FormState,
  form: FormData,
): Promise<FormState> {
  const amount = cedis(text(form, "amount"));
  if (amount === null)
    return {
      fields: { amount: "Enter an amount in cedis, like 250 or 250.00." },
    };
  const res = await api<Withdrawal>("/v1/wallet/withdrawals", {
    method: "POST",
    body: {
      amount,
      payout_account_id: text(form, "payout_account_id"),
      reason: text(form, "reason"),
    },
  });
  if (!res.ok) {
    const d = res.error.details as Record<string, string> | undefined;
    return {
      error: res.error.message,
      fields: d && typeof d === "object" ? d : undefined,
    };
  }
  revalidatePath("/console/wallet");
  return {
    ok: "Requested. The amount is held in your wallet until it is paid; most are paid the same working day.",
  };
}

export async function decideWithdrawal(
  _: FormState,
  form: FormData,
): Promise<FormState> {
  const id = encodeURIComponent(text(form, "id"));
  const approve = text(form, "decision") === "approve";
  const res = await api<Withdrawal>(
    `/v1/operator/withdrawals/${id}/${approve ? "approve" : "reject"}`,
    {
      method: "POST",
      body: approve ? undefined : { reason: text(form, "reason") },
    },
  );
  if (!res.ok) {
    const d = res.error.details as Record<string, string> | undefined;
    return {
      error: res.error.message,
      fields: d && typeof d === "object" ? d : undefined,
    };
  }
  revalidatePath("/console/operator/withdrawals");
  return {
    ok: approve
      ? "Approved; the payout is on its way."
      : "Rejected; the amount is back in their wallet.",
  };
}

// saveAIProvider creates or changes a model provider. The key field is left
// empty to keep the sealed key.
export async function saveAIProvider(
  _: FormState,
  form: FormData,
): Promise<FormState> {
  const id = text(form, "id").toLowerCase();
  const values = {
    id,
    kind: text(form, "kind"),
    name: text(form, "name"),
    base_url: text(form, "base_url"),
    model: text(form, "model"),
    effort: text(form, "effort"),
    json_mode: text(form, "json_mode"),
    tasks: form.getAll("tasks").map(String),
    priority: text(form, "priority"),
    enabled: form.get("enabled") === "on",
  };
  const res = await api<AIProvider>(
    `/v1/operator/ai-providers/${encodeURIComponent(id || "_")}`,
    {
      method: "PUT",
      body: {
        kind: text(form, "kind"),
        name: text(form, "name"),
        base_url: text(form, "base_url"),
        model: text(form, "model"),
        effort: text(form, "effort"),
        json_mode: text(form, "json_mode"),
        tasks: form.getAll("tasks").map(String),
        priority: Number(text(form, "priority") || 0),
        enabled: form.get("enabled") === "on",
        api_key: text(form, "api_key"),
      },
    },
  );
  if (!res.ok) {
    const d = res.error.details as Record<string, string> | undefined;
    return {
      error: res.error.message,
      fields: d && typeof d === "object" ? d : undefined,
      values,
    };
  }
  revalidatePath("/console/operator/ai");
  redirect(`/console/operator/ai?saved=${encodeURIComponent(res.data.id)}`);
}

export async function checkAIProvider(form: FormData) {
  await api(
    `/v1/operator/ai-providers/${encodeURIComponent(text(form, "id"))}/check`,
    { method: "POST" },
  );
  revalidatePath("/console/operator/ai");
}

export async function deleteAIProvider(form: FormData) {
  await api(
    `/v1/operator/ai-providers/${encodeURIComponent(text(form, "id"))}`,
    { method: "DELETE" },
  );
  revalidatePath("/console/operator/ai");
  redirect("/console/operator/ai");
}

export type ReadState = FormState & {
  extraction?: Extraction;
  key?: string;
  name?: string;
};

const formTypes = ["application/pdf", "image/png", "image/jpeg", "image/webp"];

// readClaimForm sends a scanned claim form to the API, which has a model read
// it into a proposed claim. Nothing is saved until the person saves it.
export async function readClaimForm(
  _: ReadState,
  form: FormData,
): Promise<ReadState> {
  const file = form.get("form");
  if (!(file instanceof File) || file.size === 0)
    return { error: "Choose a PDF or a photo of the claim form." };
  if (file.size > 10 << 20)
    return { error: "The file is over 10 MB. Scan it at a lower resolution." };
  if (!formTypes.includes(file.type))
    return { error: "Use a PDF, or a JPEG, PNG or WebP photo." };
  const res = await api<Extraction>(
    "/v1/claims/extract?include=redacted_pages",
    {
      method: "POST",
      file: { data: await file.arrayBuffer(), type: file.type },
    },
  );
  if (!res.ok) return { error: res.error.message };
  return { extraction: res.data, key: crypto.randomUUID(), name: file.name };
}

// saveReadClaim saves the claim as the person checked it. Every value on it
// is now theirs: the model's reading only filled the form.
export async function saveReadClaim(
  prev: ReadState,
  form: FormData,
): Promise<ReadState> {
  const ids = (name: string) => form.getAll(name).map(String);
  const fields: Record<string, string> = {};
  // Where each saved line and diagnosis came from on the form, to put the
  // API's complaints back next to the right field.
  const lineAt: string[] = [];
  const dxAt: string[] = [];
  const lines: ClaimInput["lines"] = [];
  for (const i of ids("line")) {
    if (form.get(`line_${i}_remove`) === "on") continue;
    const price = text(form, `line_${i}_price`);
    const unit = price ? cedis(price) : 0;
    if (unit === null)
      fields[`lines.${i}.unit_price`] =
        "Enter the price in cedis, like 150 or 150.00.";
    const qty = Number(text(form, `line_${i}_qty`) || 1);
    if (!Number.isInteger(qty) || qty < 1)
      fields[`lines.${i}.quantity`] = "A whole number, 1 or more.";
    const code = text(form, `line_${i}_code`);
    lineAt.push(i);
    lines.push({
      description: text(form, `line_${i}_desc`),
      quantity: qty,
      unit_price: unit ?? 0,
      ...(code ? { code: { system: "payahub", value: code } } : {}),
      ...(text(form, `line_${i}_date`)
        ? { service_date: text(form, `line_${i}_date`) }
        : {}),
    });
  }
  const diagnoses: ClaimInput["diagnoses"] = [];
  for (const i of ids("dx")) {
    if (form.get(`dx_${i}_remove`) === "on") continue;
    const code = text(form, `dx_${i}_code`);
    if (!code)
      fields[`diagnoses.${i}.code`] =
        "Enter the ICD-10 code, or leave this diagnosis out.";
    dxAt.push(i);
    diagnoses.push({
      code: { system: "icd-10", value: code },
      description: text(form, `dx_${i}_desc`) || undefined,
      role: text(form, "dx_primary") === i ? "primary" : "secondary",
    });
  }
  if (!text(form, "payer_id")) fields.payer_id = "Choose the insurer.";
  if (Object.keys(fields).length)
    return { ...prev, error: "Check the highlighted fields.", fields };
  const end = text(form, "end_date");
  const claim: ClaimInput = {
    payer_id: text(form, "payer_id"),
    currency: "GHS",
    member: {
      member_number: text(form, "member_number"),
      policy_number: text(form, "policy_number") || undefined,
      first_name: text(form, "first_name") || undefined,
      last_name: text(form, "last_name") || undefined,
      birth_date: text(form, "birth_date") || undefined,
      gender: text(form, "gender") || undefined,
    },
    provider: { name: text(form, "provider_name") },
    encounter: {
      start_date: text(form, "start_date"),
      ...(end ? { end_date: end, type: "inpatient" } : {}),
    },
    lines,
    diagnoses,
    authorization_code: text(form, "authorization_code") || undefined,
  };
  const res = await api<{ id: string }>("/v1/claims", {
    method: "POST",
    body: claim,
    // Saving twice (a double click, a retry) makes one claim.
    headers: prev.key ? { "Idempotency-Key": `read-${prev.key}` } : undefined,
  });
  if (!res.ok) {
    const back: Record<string, string> = {};
    const details = Array.isArray(res.error.details)
      ? (res.error.details as { field: string; message: string }[])
      : [];
    for (const d of details) {
      // lines[2].unit_price -> lines.<form row>.unit_price
      const path = d.field
        .replace(/^lines\[(\d+)\]/, (_, n) => `lines.${lineAt[Number(n)]}`)
        .replace(
          /^diagnoses\[(\d+)\]/,
          (_, n) => `diagnoses.${dxAt[Number(n)]}`,
        )
        .replace(/^payer_id$/, "payer_id");
      back[path] = d.message;
    }
    const list = details.map((d) => `${d.field} ${d.message}`).join("; ");
    return {
      ...prev,
      error: list ? `${res.error.message} ${list}.` : res.error.message,
      fields: back,
    };
  }
  revalidatePath("/console/claims");
  redirect(`/console/claims/${res.data.id}`);
}

export type SendState = FormState & {
  outcome?: {
    state: string;
    final: boolean;
    message: string;
    payer_claim_number?: string;
    error?: { message: string; payer_message?: string };
  };
};

// sendClaim sends a claim and waits up to 20 seconds for the insurer's
// answer, so the person sees it here rather than on a later visit.
export async function sendClaim(
  _: SendState,
  form: FormData,
): Promise<SendState> {
  const id = text(form, "id");
  const res = await api<{ outcome?: SendState["outcome"] | null }>(
    `/v1/claims/${encodeURIComponent(id)}/submit?wait=20`,
    {
      method: "POST",
      headers: { "Idempotency-Key": text(form, "key") || crypto.randomUUID() },
    },
  );
  revalidatePath(`/console/claims/${id}`);
  if (!res.ok) return { error: res.error.message };
  if (!res.data.outcome)
    return {
      ok: "Sent. The insurer has not answered yet; this page updates when it does.",
    };
  return { outcome: res.data.outcome };
}

// confirmIssues records that a person checked the issues awaiting review.
export async function confirmIssues(
  _: FormState,
  form: FormData,
): Promise<FormState> {
  const id = text(form, "id");
  const confirm = form
    .getAll("confirm")
    .map(String)
    .map((v) => {
      const at = v.indexOf("|");
      return { code: v.slice(0, at), field: v.slice(at + 1) };
    });
  if (confirm.length === 0) return { error: "Tick what you checked." };
  const res = await api(`/v1/claims/${encodeURIComponent(id)}/review`, {
    method: "POST",
    body: { confirm, note: text(form, "note") },
  });
  if (!res.ok) return { error: res.error.message };
  revalidatePath(`/console/claims/${id}`);
  revalidatePath("/console/claims/review");
  return {
    ok: confirm.length === 1 ? "Confirmed." : `${confirm.length} confirmed.`,
  };
}

// mapHeldCode maps one of the clinic's codes from the review queue; the API
// checks every claim waiting on it again.
export async function mapHeldCode(
  _: FormState,
  form: FormData,
): Promise<FormState> {
  const item = text(form, "item");
  if (!item) return { error: "Choose the standard item." };
  const res = await api<{
    rechecked: {
      claims: number;
      ready: number;
      needs_review: number;
      to_fix: number;
    };
  }>("/v1/item-mappings", {
    method: "PUT",
    body: {
      mappings: [
        {
          local_code: text(form, "code"),
          local_name: text(form, "name"),
          item_id: item,
        },
      ],
    },
  });
  if (!res.ok) {
    const details = res.error.details as Record<string, string> | undefined;
    return {
      error: details ? Object.values(details).join(" ") : res.error.message,
    };
  }
  revalidatePath("/console", "layout");
  // The code leaves the queue, so the page says what happened.
  const r = res.data.rechecked;
  const q = new URLSearchParams({
    mapped: text(form, "name") || text(form, "code"),
    claims: String(r.claims),
    ready: String(r.ready),
    review: String(r.needs_review),
    fix: String(r.to_fix),
  });
  const env = text(form, "env");
  if (env) q.set("env", env);
  redirect(`/console/claims/review?${q}`);
}

export type HookState = FormState & { secret?: string; url?: string };

// addWebhook makes an endpoint; its signing secret is shown once, here.
export async function addWebhook(
  _: HookState,
  form: FormData,
): Promise<HookState> {
  const values = {
    url: text(form, "url"),
    description: text(form, "description"),
    environment: text(form, "environment") || "sandbox",
  };
  const res = await api<{ secret: string; endpoint: { url: string } }>(
    "/v1/webhook-endpoints",
    { method: "POST", body: values },
  );
  if (!res.ok) {
    const d = res.error.details as Record<string, string> | undefined;
    return { error: res.error.message, fields: d, values };
  }
  revalidatePath("/console/developers");
  return { secret: res.data.secret, url: res.data.endpoint.url };
}

export async function pingWebhook(form: FormData) {
  await api(
    `/v1/webhook-endpoints/${encodeURIComponent(text(form, "id"))}/ping`,
    { method: "POST" },
  );
  revalidatePath("/console/developers");
}

export async function removeWebhook(form: FormData) {
  await api(`/v1/webhook-endpoints/${encodeURIComponent(text(form, "id"))}`, {
    method: "DELETE",
  });
  revalidatePath("/console/developers");
}

// inviteMember emails an invitation link.
export async function inviteMember(
  _: FormState,
  form: FormData,
): Promise<FormState> {
  const values = {
    email: text(form, "email"),
    name: text(form, "name"),
    role: text(form, "role"),
  };
  const res = await api<{ email: string }>("/v1/invitations", {
    method: "POST",
    body: values,
  });
  if (!res.ok)
    return {
      error: res.error.message,
      fields: res.error.details as Record<string, string> | undefined,
      values,
    };
  revalidatePath("/console/team");
  return {
    ok: `Invitation sent to ${res.data.email}. The link works for 7 days.`,
  };
}

export async function changeRole(
  _: FormState,
  form: FormData,
): Promise<FormState> {
  const res = await api(`/v1/members/${encodeURIComponent(text(form, "id"))}`, {
    method: "PUT",
    body: { role: text(form, "role") },
  });
  if (!res.ok) return { error: res.error.message };
  revalidatePath("/console", "layout");
  return { ok: "Saved" };
}

export async function removeMember(
  _: FormState,
  form: FormData,
): Promise<FormState> {
  const res = await api(`/v1/members/${encodeURIComponent(text(form, "id"))}`, {
    method: "DELETE",
  });
  if (!res.ok) return { error: res.error.message };
  revalidatePath("/console/team");
  return {};
}

export async function revokeInvitation(form: FormData) {
  await api(`/v1/invitations/${encodeURIComponent(text(form, "id"))}`, {
    method: "DELETE",
  });
  revalidatePath("/console/team");
}

// joinTeam accepts an invitation and signs the new member in.
export async function joinTeam(
  _: FormState,
  form: FormData,
): Promise<FormState> {
  const token = text(form, "token");
  const res = await api<{ session_token: string }>(
    `/v1/invitations/accept/${encodeURIComponent(token)}`,
    {
      method: "POST",
      token: null,
      body: {
        name: text(form, "name"),
        password: String(form.get("password") ?? ""),
      },
    },
  );
  if (!res.ok)
    return {
      error: res.error.message,
      fields: res.error.details as Record<string, string> | undefined,
    };
  await setSession(res.data.session_token);
  redirect("/console");
}

export type MemberState = FormState & { check?: MemberCheck };

// checkMember asks the insurer about a member and waits for the answer.
export async function checkMember(
  _: MemberState,
  form: FormData,
): Promise<MemberState> {
  const values = {
    payer_id: text(form, "payer_id"),
    member_number: text(form, "member_number"),
    environment: text(form, "environment") || "sandbox",
    otp: text(form, "otp"),
    action: text(form, "action") || "verify",
  };
  const res = await api<MemberCheck>("/v1/member-checks", {
    method: "POST",
    body: values,
    headers: { Prefer: "wait=20", "Idempotency-Key": crypto.randomUUID() },
  });
  if (!res.ok)
    return {
      error: res.error.message,
      fields: res.error.details as Record<string, string> | undefined,
      values,
    };
  return { check: res.data, values };
}

// askInsurer asks the insurer now where a claim stands and waits for the answer.
export async function askInsurer(
  _: FormState,
  form: FormData,
): Promise<FormState> {
  const id = text(form, "id");
  const res = await api(`/v1/claims/${encodeURIComponent(id)}/status-check`, {
    method: "POST",
    headers: { Prefer: "wait=20" },
  });
  if (!res.ok) return { error: res.error.message };
  revalidatePath(`/console/claims/${id}`);
  return res.status === 202
    ? {
        ok: "The insurer has not answered yet; this page will show it when it does.",
      }
    : {};
}
