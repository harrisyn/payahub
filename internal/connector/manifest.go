package connector

// Verb is one operation a payer may support. The set is the common surface of
// the five core-api-v2 adapters, with typed signatures instead of
// execute_method(name, dict).
type Verb string

const (
	VerifyMember         Verb = "verify_member"
	SendOTP              Verb = "send_otp"
	RequestAuthorization Verb = "request_authorization"
	SubmitWholeClaim     Verb = "submit_whole_claim"
	OpenClaim            Verb = "open_claim"
	AddLine              Verb = "add_line"
	UpdateLine           Verb = "update_line"
	DeleteLine           Verb = "delete_line"
	AddDiagnosis         Verb = "add_diagnosis"
	FinalizeClaim        Verb = "finalize_claim"
	WithdrawClaim        Verb = "withdraw_claim"
	FindClaim            Verb = "find_claim"
	ListCatalog          Verb = "list_catalog"
	ConvertEncounterType Verb = "convert_encounter_type"
)

type Channel string

const (
	ChannelAPI    Channel = "api"
	ChannelPortal Channel = "portal"
	ChannelEmail  Channel = "email"
)

type CredentialField struct {
	Name     string `json:"name"`
	Label    string `json:"label"`
	Secret   bool   `json:"secret"`
	Required bool   `json:"required"`
}

// SubmissionConfig holds per-payer claim-building rules, overridable per
// organisation. Same keys and meaning as core-api-v2's DEFAULT_SUBMISSION_CONFIG.
type SubmissionConfig struct {
	ItemGroupingOutpatient     string `json:"item_grouping_outpatient"`  // grouped | individual
	ItemGroupingInpatient      string `json:"item_grouping_inpatient"`   // grouped | individual
	UnmappedLinePolicy         string `json:"unmapped_line_policy"`      // block | send_raw
	UnmappedDiagnosisPolicy    string `json:"unmapped_diagnosis_policy"` // block | send_raw
	DiagnosisCodeSource        string `json:"diagnosis_code_source"`     // icd | payer_code
	LineSubmissionMode         string `json:"line_submission_mode"`      // realtime | bulk
	ClaimCreationTiming        string `json:"claim_creation_timing"`     // early | deferred
	SameDayEncounterConversion bool   `json:"same_day_encounter_conversion"`
}

func DefaultSubmissionConfig() SubmissionConfig {
	return SubmissionConfig{
		ItemGroupingOutpatient:  "grouped",
		ItemGroupingInpatient:   "individual",
		UnmappedLinePolicy:      "block",
		UnmappedDiagnosisPolicy: "send_raw",
		DiagnosisCodeSource:     "icd",
		LineSubmissionMode:      "realtime",
		ClaimCreationTiming:     "early",
	}
}

// Kind says who a connector reaches. A platform (XCeli, Medserve, RX...)
// carries claims for many payers, so one organisation account on it serves
// every payer behind it; a direct connector is one payer's own interface.
type Kind string

const (
	KindPlatform Kind = "platform"
	KindDirect   Kind = "direct"
)

// Manifest describes a connector as data: what it can do and how its payers
// want claims built, so the engine picks a strategy without payer branches.
type Manifest struct {
	Key               string           `json:"id"`
	Name              string           `json:"name"`
	Kind              Kind             `json:"kind"`
	Country           string           `json:"country"`
	Channel           Channel          `json:"channel"`
	Verbs             []Verb           `json:"capabilities"`
	MemberOTPRequired bool             `json:"member_otp_required"`
	Submission        SubmissionConfig `json:"submission"`
	Notes             string           `json:"notes,omitempty"`
	// Credentials are the organisation's account on the connector, entered
	// once and used for every payer reached through it.
	Credentials []CredentialField `json:"credentials"`
	// PayerFields are what the payer catalogue records for each payer on this
	// connector (its insurer code or API server), defined once for everyone.
	PayerFields []CredentialField `json:"payer_fields"`
	// Status is "available" once the adapter is ported and tested, "porting"
	// while only the manifest exists. The console shows it next to the payer.
	Status string `json:"status"`
}

const (
	StatusAvailable = "available"
	StatusPorting   = "porting"
)

func (m Manifest) Supports(v Verb) bool {
	for _, x := range m.Verbs {
		if x == v {
			return true
		}
	}
	return false
}

func (m Manifest) IncrementalLines() bool { return m.Supports(AddLine) && m.Supports(UpdateLine) }
func (m Manifest) WholeClaim() bool       { return m.Supports(SubmitWholeClaim) }
