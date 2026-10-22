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
  onboarding: { id: "account" | "profile" | "payers" | "items" | "first_claim" | "go_live"; done: boolean }[];
  // Operators run Payahub and edit the shared catalogue.
  operator: boolean;
};

export type CredentialField = { name: string; label: string; secret: boolean; required: boolean };

// A connector reaches payers: a platform carries many (XCeli, Medserve...),
// a direct one is a single payer's own interface.
export type Connector = {
  id: string;
  name: string;
  kind: "platform" | "direct";
  country: string;
  channel: "api" | "portal" | "email";
  capabilities: string[];
  member_otp_required: boolean;
  credentials: CredentialField[];
  payer_fields: CredentialField[];
  notes?: string;
  status: "available" | "porting";
};

// A payer from the shared catalogue.
export type Payer = {
  id: string;
  name: string;
  country: string;
  kind: "standalone" | "integrated";
  connector: { id: string; name: string; kind: "platform" | "direct"; status: "available" | "porting" };
  status: "available" | "connecting";
  member_otp_required: boolean;
  capabilities: string[];
  coded_items: number;
  notes?: string;
};

export type PayerSetting = {
  payer_id: string;
  environment: "sandbox" | "live";
  enabled: boolean;
  account?: "have" | "need";
  updated_at: string;
};

export type ConnectorAccount = {
  connector: string;
  environment: "sandbox" | "live";
  credential_fields: string[];
  checked_at: string | null;
  check_ok: boolean | null;
  check_message?: string;
  updated_at: string;
};

export type ItemKind = "service" | "lab" | "imaging" | "procedure" | "medication" | "consumable";

export type Item = {
  id: string;
  kind: ItemKind;
  name: string;
  generic?: string;
  strength?: string;
  form?: string;
  synonyms?: string[];
  codes?: { system: string; value: string }[];
};

export type Suggestion = { item: Item; confidence: number };

export type ItemMapping = { local_code: string; local_name: string; item_id: string; confirmed_by: string; updated_at: string };

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

// The operator's view of a catalogue payer: its settings on its connector and
// how far its codes are mapped.
export type OpPayer = Payer & {
  fields: Record<string, string>;
  inactive: boolean;
  missing_fields: string[];
  list_size: number;
  total_items: number;
};

export type ListEntry = { code: string; name: string; group?: string; price?: number };

export type ItemCode = {
  item: Item;
  code: { code: string; description?: string } | null;
  in_list?: boolean;
  generated?: boolean;
  suggestions: (ListEntry & { confidence: number })[];
};

export type PayerCodes = {
  data: ItemCode[];
  coverage: Record<string, { coded: number; total: number }>;
  list_size: number;
};

export type CatalogChange = {
  kind: "payer_created" | "payer_updated" | "codes_changed" | "list_loaded" | "seed";
  detail: Record<string, unknown>;
  changed_by: string;
  changed_at: string;
};

export type Price = {
  local_code: string;
  name: string;
  kind?: ItemKind;
  unit_price: number;
  currency: string;
  item_id?: string;
  updated_at: string;
  suggestion?: Suggestion;
};

export type Diagnosis = { code: string; title: string; billable: boolean };
