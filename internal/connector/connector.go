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
	OTPRequired bool         `json:"otp_required"`
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
type Connector interface {
	Manifest() Manifest
	VerifyMember(ctx context.Context, memberNumber, otp string) (MemberVerification, error)
	SubmitWholeClaim(ctx context.Context, c *claim.Claim) (Receipt, error)
	OpenClaim(ctx context.Context, c *claim.Claim) (string, error)
	AddLine(ctx context.Context, payerClaimNumber string, l claim.Line) (string, error)
	AddDiagnosis(ctx context.Context, payerClaimNumber string, d claim.Diagnosis) error
	FinalizeClaim(ctx context.Context, payerClaimNumber string, c *claim.Claim) (Receipt, error)
	// FindClaim looks a claim up by our claim ID, which every connector sends
	// as the payer-side external reference. The worker calls it before any
	// resend, so a crash between "payer accepted" and "we recorded it" never
	// produces a duplicate claim at the payer.
	FindClaim(ctx context.Context, c *claim.Claim) (*Receipt, error)
}

// Unsupported answers every verb with NOT_SUPPORTED.
type Unsupported struct{ Name string }

func (u Unsupported) err(v Verb) error {
	return Errorf(NotSupported, "%s does not support %s", u.Name, v)
}

func (u Unsupported) VerifyMember(context.Context, string, string) (MemberVerification, error) {
	return MemberVerification{}, u.err(VerifyMember)
}
func (u Unsupported) SubmitWholeClaim(context.Context, *claim.Claim) (Receipt, error) {
	return Receipt{}, u.err(SubmitWholeClaim)
}
func (u Unsupported) OpenClaim(context.Context, *claim.Claim) (string, error) {
	return "", u.err(OpenClaim)
}
func (u Unsupported) AddLine(context.Context, string, claim.Line) (string, error) {
	return "", u.err(AddLine)
}
func (u Unsupported) AddDiagnosis(context.Context, string, claim.Diagnosis) error {
	return u.err(AddDiagnosis)
}
func (u Unsupported) FinalizeClaim(context.Context, string, *claim.Claim) (Receipt, error) {
	return Receipt{}, u.err(FinalizeClaim)
}
func (u Unsupported) FindClaim(context.Context, *claim.Claim) (*Receipt, error) {
	return nil, u.err(FindClaim)
}

// Factory builds a connector from an organisation's credentials for that payer.
type Factory func(credentials map[string]string) (Connector, error)

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
		if f.Required && creds[f.Name] == "" {
			return Errorf(InvalidCredentials, "%s is missing credential %q", m.Name, f.Name)
		}
	}
	return nil
}

// New builds the connector registered under key.
func New(key string, creds map[string]string) (Connector, error) {
	m, f, ok := Lookup(key)
	if !ok {
		return nil, fmt.Errorf("unknown connector %q", key)
	}
	if err := CheckCredentials(m, creds); err != nil {
		return nil, err
	}
	return f(creds)
}
