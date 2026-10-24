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

export type Suggestion = { item: Item; confidence: number; source?: "words" | "model"; reason?: string };

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

export type PlanPrice = { monthly_fee: number; per_claim: number; included: number };
export type Wallet = {
  currency: string;
  balance: number;
  reserved: number;
  available: number;
  pending_withdrawals: number;
  plan: string;
  price?: PlanPrice;
  charged_this_month: number;
  can_send_live: boolean;
};
export type WalletEntry = {
  seq: number;
  transaction_id: string;
  kind: "funding" | "charge" | "fee" | "withdrawal" | "withdrawal_returned" | "adjustment";
  memo: string;
  amount: number;
  balance_after: number;
  reference?: string;
  created_at: string;
};
export type PaymentStatus = "pending" | "succeeded" | "failed" | "abandoned";
export type Funding = {
  id: string;
  provider: string;
  amount: number;
  currency: string;
  status: PaymentStatus;
  paid_amount?: number;
  fee?: number;
  checkout_url?: string;
  message?: string;
  created_at: string;
};
export type PaymentProvider = {
  id: string;
  name: string;
  currencies: string[];
  channels: string[];
  payouts: boolean;
  sandbox: boolean;
};
export type Bank = { code: string; name: string; type: "bank" | "mobile_money" };
export type PayoutAccount = {
  id: string;
  provider: string;
  type: "bank" | "mobile_money";
  name: string;
  bank_code: string;
  bank_name: string;
  account_number: string;
  currency: string;
};
export type WithdrawalStatus = "requested" | "approved" | "paying" | "paid" | "failed" | "rejected";
export type Withdrawal = {
  id: string;
  organization_id: string;
  organization_name?: string;
  payout_account_id: string;
  destination: string;
  provider: string;
  amount: number;
  currency: string;
  status: WithdrawalStatus;
  reason: string;
  message?: string;
  created_at: string;
  completed_at?: string;
};
export type LedgerView = {
  accounts: { id: string; kind: string; currency: string; balance: number }[];
  wallets: Record<string, number>;
  balanced: boolean;
};

export type AIKind = "anthropic" | "openai" | "openai_compatible";

export type AIProvider = {
  id: string;
  kind: AIKind;
  name: string;
  base_url: string;
  model: string;
  effort: string;
  json_mode: string;
  tasks: string[];
  priority: number;
  enabled: boolean;
  has_key: boolean;
  key_hint: string;
  checked_at?: string;
  check_ok?: boolean;
  check_message?: string;
  updated_at: string;
};

export type ReadField = {
  confidence: number;
  quote?: string;
  read?: string;
  note?: string;
  needs_review: boolean;
};

export type ClaimInput = {
  payer_id: string;
  currency: string;
  member: {
    member_number: string;
    policy_number?: string;
    first_name?: string;
    last_name?: string;
    birth_date?: string;
    gender?: string;
  };
  provider: { name: string };
  encounter: { type?: string; start_date: string; end_date?: string };
  lines: {
    description: string;
    code?: { system: string; value: string; display?: string };
    quantity: number;
    unit_price: number;
    service_date?: string;
  }[];
  diagnoses: {
    code: { system: string; value: string; display?: string };
    role?: string;
    description?: string;
  }[];
  authorization_code?: string;
};

export type Extraction = {
  claim: ClaimInput;
  fields: Record<string, ReadField>;
  payer_name?: string;
  review: number;
  evidence: { model_version?: string; prompt_version?: string; confidence?: number };
  // What was blacked out on Payahub's server before the model saw the form.
  privacy?: { method: string; removed?: Record<string, number>; pages?: number };
  redacted_pages?: string[];
};

export type AIUsage = {
  task: string;
  provider: string;
  model: string;
  runs: number;
  failed: number;
  input_tokens: number;
  output_tokens: number;
  avg_ms: number;
};

export type WebhookEndpoint = {
  id: string;
  environment: "sandbox" | "live";
  url: string;
  description: string;
  enabled: boolean;
  created_at: string;
  delivered_24h: number;
  failing: number;
  last_error?: string;
  last_delivered_at?: string;
};

export type WebhookDelivery = {
  id: number;
  event_type: string;
  status: "pending" | "delivered" | "failed";
  attempts: number;
  last_status?: number;
  last_error?: string;
  created_at: string;
  delivered_at?: string;
  payload: { id: string; type: string; data?: { claim_id?: string; state?: string } };
};
