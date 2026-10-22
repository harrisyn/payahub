// Shapes of the Go API's JSON, as the console reads them.

export type Method = "rest" | "batch" | "documents" | "web_form";

export type Profile = {
  billing_system: "emr" | "practice_software" | "spreadsheets" | "paper";
  claims_leave_by: "portal" | "email" | "paper" | "integration";
  monthly_claims: number;
  has_developers: boolean;
  payers: string[];
  method?: Method;
};

export type Recommendation = { method: Method; reason: string; available: boolean; fallback?: Method };

export type Organization = {
  id: string;
  name: string;
  country: string;
  plan: "trial" | "flat" | "flat_volume" | "volume" | "enterprise_requested" | "enterprise";
  trial_ends_at: string | null;
  profile: Profile | null;
  user: { id: string; name: string; email: string };
  created_at: string;
};

export type Usage = { month: string; billable_live: number; sandbox_claims: number; live_claims: number; trial_live_limit: number };

export type OrgView = {
  organization: Organization;
  usage: Usage;
  recommendation?: Recommendation;
  onboarding: { id: "account" | "profile" | "payers" | "first_claim" | "go_live"; done: boolean }[];
};

export type CredentialField = { name: string; label: string; secret: boolean; required: boolean };

export type Manifest = {
  id: string;
  name: string;
  country: string;
  channel: "api" | "portal" | "email";
  capabilities: string[];
  member_otp_required: boolean;
  credentials: CredentialField[];
  notes?: string;
  status: "available" | "porting";
};

export type PayerSetting = {
  payer_id: string;
  environment: "sandbox" | "live";
  enabled: boolean;
  credential_fields: string[];
  checked_at: string | null;
  check_ok: boolean | null;
  check_message?: string;
  updated_at: string;
};

export type APIKey = {
  id: string;
  name: string;
  environment: "sandbox" | "live";
  prefix: string;
  created_at: string;
  revoked_at: string | null;
  key?: string;
};

export type ClaimState =
  | "draft" | "mapped" | "validated" | "needs_review" | "ready" | "submitted"
  | "acknowledged" | "approved" | "rejected" | "paid" | "withdrawn";

export type ClaimSummary = {
  id: string;
  environment: "sandbox" | "live";
  payer_id: string;
  state: ClaimState;
  payer_claim_number?: string;
  member_number: string;
  currency: string;
  total: number;
  created_at: string;
  updated_at: string;
};

export type ClaimEvent = { id: number; kind: string; from_state?: string; to_state: string; detail?: unknown; created_at: string };
