"use server";

import { api } from "@/lib/api";

export type LeadKind = "contact" | "platform" | "payer" | "specs";
export type LeadState = {
  ok?: boolean;
  kind?: LeadKind;
  error?: string;
  fields?: Record<string, string>;
  // what was sent, so a form with errors keeps what the person typed
  values?: Record<string, string>;
};

const text = (f: FormData, k: string) => String(f.get(k) ?? "").trim();

// sendLead posts an enquiry from the landing page. The form carries a key made
// when it rendered, so a double submit saves one lead. A filled-in "website"
// field is the honeypot: people never see it, so only bots fill it.
export async function sendLead(_: LeadState, form: FormData): Promise<LeadState> {
  const kind = text(form, "kind") as LeadKind;
  if (text(form, "website")) return { ok: true, kind };
  const volume = text(form, "monthly_claims").replace(/[,\s]/g, "");
  const res = await api<{ id: string }>("/v1/leads", {
    method: "POST",
    token: null,
    headers: { "Idempotency-Key": text(form, "idempotency_key") },
    body: {
      kind,
      name: text(form, "name"),
      email: text(form, "email"),
      phone: text(form, "phone"),
      organization: text(form, "organization"),
      payer_name: text(form, "payer_name"),
      monthly_claims: volume === "" ? null : Number(volume),
      payer_contact: text(form, "payer_contact"),
      message: text(form, "message"),
    },
  });
  if (!res.ok) {
    const details = res.error.details as Record<string, string> | undefined;
    const values: Record<string, string> = {};
    for (const k of ["name", "email", "phone", "organization", "payer_name", "monthly_claims", "payer_contact", "message"]) {
      values[k] = text(form, k);
    }
    return { kind, error: res.error.message, fields: details && typeof details === "object" ? details : undefined, values };
  }
  return { ok: true, kind };
}
