// Package connector is the SDK every payer connector implements.
package connector

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/harrisyn/payahub/internal/claim"
)

type MemberVerification struct {
	Eligible    bool         `json:"eligible"`
	Member      claim.Member `json:"member"`
	PlanName    string       `json:"plan_name,omitempty"`
	PlanID      string       `json:"plan_id,omitempty"`
	OTPRequired bool         `json:"otp_required"`
	// Reason is why a member is not eligible: inactive, not_covered_here,
	// expired. Message is the payer's own words.
	Reason    string      `json:"reason,omitempty"`
	Message   string      `json:"message,omitempty"`
	CoverEnds *claim.Date `json:"cover_ends,omitempty"`
}

type Receipt struct {
	PayerClaimNumber string `json:"payer_claim_number"`
	Acknowledged     bool   `json:"acknowledged"`
	// LineReferences maps our Line.ID to the payer's line reference.
	LineReferences map[string]string `json:"line_references,omitempty"`
	Warnings       []string          `json:"warnings,omitempty"`
}

// Connector is the typed verb surface. A connector embeds Unsupported and
// overrides the verbs its manifest lists.
//
// Claim verbs take the Submission (engine.Build's payer-ready view of the
// claim). The incremental verbs are driven by engine.Send, which records each
// step as it completes, so a retry resumes rather than repeats.
type Connector interface {
	Manifest() Manifest

	// Member checks.
	VerifyMember(ctx context.Context, memberNumber, otp string) (MemberVerification, error)
	// SendOTP asks the payer to send the member a one-time code. Some payers
	// return a challenge that must travel with the code on the claim.
	SendOTP(ctx context.Context, memberNumber string) (challenge string, err error)
	ValidateOTP(ctx context.Context, memberNumber, otp string) error

	// Whole-claim path: one call carries every line and diagnosis.
	SubmitWholeClaim(ctx context.Context, s *Submission) (Receipt, error)

	// Incremental path: open, then lines and diagnoses one by one, then finalize.
	OpenClaim(ctx context.Context, s *Submission) (string, error)
	// AddLine may return a ref with an error when the payer stored the line
	// but needs something before it counts (an approval): the ref is kept.
	AddLine(ctx context.Context, s *Submission, number string, l SendLine) (ref string, err error)
	// UpdateLine returns the line's new ref, or "" when it is unchanged.
	UpdateLine(ctx context.Context, s *Submission, number, ref string, l SendLine) (newRef string, err error)
	DeleteLine(ctx context.Context, s *Submission, number, ref string) error
	AddDiagnosis(ctx context.Context, s *Submission, number string, d SendDiagnosis) (ref string, err error)
	DeleteDiagnosis(ctx context.Context, s *Submission, number, ref string) error
	FinalizeClaim(ctx context.Context, s *Submission, number string) (Receipt, error)

	// FindClaim looks a claim up by our claim ID, which every connector sends
	// as the payer-side external reference. The worker calls it before any
	// whole-claim resend, so a crash between "payer accepted" and "we
	// recorded it" never produces a duplicate claim at the payer.
	FindClaim(ctx context.Context, s *Submission) (*Receipt, error)
	// FetchClaim reads a claim the payer holds by its number: its lines for
	// reconciling an interrupted incremental send, and its status after.
	FetchClaim(ctx context.Context, number string) (*PayerClaim, error)

	// ListCatalog downloads the payer's own code list, for provisioning.
	ListCatalog(ctx context.Context, kind CatalogKind) ([]CatalogEntry, error)
}

// Unsupported answers every verb with NOT_SUPPORTED.
// RefKeyer is implemented by connectors whose line refs carry more than the
// payer's id for the line (Medserve keeps the dispensation and the cost the
// insurer accepted beside the prescription row). RefKey gives the part that
// identifies the line, so a ref we recorded and the ref FetchClaim reports
// for the same line compare equal.
type RefKeyer interface {
	RefKey(ref string) string
}

// RefKey is the identity of a line ref for conn.
func RefKey(conn Connector, ref string) string {
	if k, ok := conn.(RefKeyer); ok {
		return k.RefKey(ref)
	}
	return ref
}

type Unsupported struct{ Name string }

func (u Unsupported) err(v Verb) error {
	return Errorf(NotSupported, "%s does not support %s", u.Name, v)
}

func (u Unsupported) VerifyMember(context.Context, string, string) (MemberVerification, error) {
	return MemberVerification{}, u.err(VerifyMember)
}
func (u Unsupported) SendOTP(context.Context, string) (string, error) { return "", u.err(SendOTP) }
func (u Unsupported) ValidateOTP(context.Context, string, string) error {
	return u.err(ValidateOTP)
}
func (u Unsupported) SubmitWholeClaim(context.Context, *Submission) (Receipt, error) {
	return Receipt{}, u.err(SubmitWholeClaim)
}
func (u Unsupported) OpenClaim(context.Context, *Submission) (string, error) {
	return "", u.err(OpenClaim)
}
func (u Unsupported) AddLine(context.Context, *Submission, string, SendLine) (string, error) {
	return "", u.err(AddLine)
}
func (u Unsupported) UpdateLine(context.Context, *Submission, string, string, SendLine) (string, error) {
	return "", u.err(UpdateLine)
}
func (u Unsupported) DeleteLine(context.Context, *Submission, string, string) error {
	return u.err(DeleteLine)
}
func (u Unsupported) AddDiagnosis(context.Context, *Submission, string, SendDiagnosis) (string, error) {
	return "", u.err(AddDiagnosis)
}
func (u Unsupported) DeleteDiagnosis(context.Context, *Submission, string, string) error {
	return u.err(DeleteDiagnosis)
}
func (u Unsupported) FinalizeClaim(context.Context, *Submission, string) (Receipt, error) {
	return Receipt{}, u.err(FinalizeClaim)
}
func (u Unsupported) FindClaim(context.Context, *Submission) (*Receipt, error) {
	return nil, u.err(FindClaim)
}
func (u Unsupported) FetchClaim(context.Context, string) (*PayerClaim, error) {
	return nil, u.err(FetchClaim)
}
func (u Unsupported) ListCatalog(context.Context, CatalogKind) ([]CatalogEntry, error) {
	return nil, u.err(ListCatalog)
}

// Account is what a connector is built from: the organisation's credentials
// on the connector and the catalogue's fields for the payer being reached.
type Account struct {
	Credentials map[string]string
	PayerID     string
	PayerFields map[string]string
}

// Factory builds a connector for one organisation and payer.
type Factory func(Account) (Connector, error)

type registration struct {
	manifest Manifest
	factory  Factory
}

var (
	mu       sync.RWMutex
	registry = map[string]registration{}
)

// Register adds a connector; connectors call it from init().
func Register(m Manifest, f Factory) {
	mu.Lock()
	defer mu.Unlock()
	if _, dup := registry[m.Key]; dup {
		panic("connector registered twice: " + m.Key)
	}
	if m.Credentials == nil {
		m.Credentials = []CredentialField{} // JSON [] rather than null
	}
	if m.PayerFields == nil {
		m.PayerFields = []CredentialField{}
	}
	if m.Kind == "" {
		m.Kind = KindPlatform
	}
	registry[m.Key] = registration{manifest: m, factory: f}
}

func Lookup(key string) (Manifest, Factory, bool) {
	mu.RLock()
	defer mu.RUnlock()
	r, ok := registry[key]
	return r.manifest, r.factory, ok
}

func Manifests() []Manifest {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]Manifest, 0, len(registry))
	for _, r := range registry {
		out = append(out, r.manifest)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// CheckCredentials reports required credential fields that are missing.
func CheckCredentials(m Manifest, creds map[string]string) error {
	for _, f := range m.Credentials {
		if f.Required && f.Value(creds) == "" {
			return Errorf(InvalidCredentials, "%s is missing credential %q", m.Name, f.Name)
		}
	}
	return nil
}

// CheckPayerFields reports required payer fields the catalogue has not set.
func CheckPayerFields(m Manifest, fields map[string]string) error {
	for _, f := range m.PayerFields {
		if f.Required && fields[f.Name] == "" {
			return Errorf(InvalidCredentials, "the payer has no %q on %s yet", f.Name, m.Name)
		}
	}
	return nil
}

// Pending is the factory for a connector whose manifest is known but whose
// adapter is not ported yet: every verb answers NOT_SUPPORTED.
func Pending(m Manifest) Factory {
	return func(Account) (Connector, error) {
		return pending{Unsupported{Name: m.Name + " (not yet ported)"}, m}, nil
	}
}

type pending struct {
	Unsupported
	m Manifest
}

func (p pending) Manifest() Manifest { return p.m }

// New builds the connector registered under key for one organisation's account.
func New(key string, a Account) (Connector, error) {
	m, f, ok := Lookup(key)
	if !ok {
		return nil, fmt.Errorf("unknown connector %q", key)
	}
	if err := CheckCredentials(m, a.Credentials); err != nil {
		return nil, err
	}
	if err := CheckPayerFields(m, a.PayerFields); err != nil {
		return nil, err
	}
	return f(a)
}
