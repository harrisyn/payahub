// Package sandbox is a deterministic in-process payer for trials, docs and
// tests. Every sandbox API key reaches it, so a first claim can go end to end
// before any real payer credentials exist. It registers twice: as a payer's
// own (direct) interface, and as Sandbox Exchange, a platform that carries
// claims for several sandbox payers, so onboarding can show both kinds.
// The member number picks the path:
//
//	NOTFOUND...   member_not_found
//	OTP...        verification needs OTP 123456, which SendOTP "sends"
//	INACTIVE...   found, but the cover has lapsed
//	REJECT...     the payer refuses the claim
//	FLAKY...      the first send times out before reaching the payer
//	LOSTREPLY...  the first send reaches the payer but the reply is lost
//	SLOW...       the payer takes SlowFor to answer, then acknowledges
//
// Asked later (FetchClaim), an acknowledged claim is approved, unless the
// member number starts PAID (paid), QUERY (the payer asks a question),
// DENY (rejected after review) or WAIT (still under review).
//
//	anything else eligible; claim acknowledged
package sandbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/harrisyn/payahub/internal/claim"
	"github.com/harrisyn/payahub/internal/connector"
)

const (
	Key         = "sandbox"
	ExchangeKey = "sandbox_exchange"
	OTP         = "123456"
)

var manifest = connector.Manifest{
	Key:     Key,
	Name:    "Payahub Sandbox",
	Kind:    connector.KindDirect,
	Country: "ZZ",
	Channel: connector.ChannelAPI,
	Verbs: []connector.Verb{
		connector.VerifyMember, connector.SendOTP, connector.ValidateOTP, connector.SubmitWholeClaim, connector.OpenClaim, connector.AddLine,
		connector.UpdateLine, connector.DeleteLine, connector.AddDiagnosis, connector.DeleteDiagnosis,
		connector.FinalizeClaim, connector.FindClaim, connector.FetchClaim,
	},
	Submission: connector.DefaultSubmissionConfig(),
	Notes:      "Simulated payer. Never reaches a real insurer.",
	Status:     connector.StatusAvailable,
}

// The payer's "database" is shared by every Sandbox value in the process, as
// a real payer's would be across our workers.
var payer = struct {
	sync.Mutex
	claims   map[string]*record // by payer claim number
	attempts map[string]int     // sends per our claim ID
}{claims: map[string]*record{}, attempts: map[string]int{}}

type record struct {
	status    string
	member    string                         // picks how the claim is settled after acknowledgement
	lines     map[string]connector.PayerLine // by payer ref
	seq       int
	diagnoses []string
}

// exchange is the same simulated payer behind a platform: an organisation
// signs in to it once, and each payer on it is told apart by its code.
var exchange = func() connector.Manifest {
	m := manifest
	m.Key, m.Name, m.Kind = ExchangeKey, "Sandbox Exchange", connector.KindPlatform
	m.Notes = "Simulated platform. Any username and password work."
	m.Credentials = []connector.CredentialField{
		{Name: "username", Label: "Exchange username", Required: true},
		{Name: "password", Label: "Exchange password", Secret: true, Required: true},
	}
	m.PayerFields = []connector.CredentialField{{Name: "payer_code", Label: "Payer code on the exchange", Required: true}}
	return m
}()

type Sandbox struct {
	connector.Unsupported
	m connector.Manifest
}

// New builds the direct sandbox payer; every sandbox claim is sent to it.
func New(connector.Account) (connector.Connector, error) {
	return Sandbox{connector.Unsupported{Name: manifest.Name}, manifest}, nil
}

func init() {
	connector.Register(manifest, New)
	connector.Register(exchange, func(connector.Account) (connector.Connector, error) {
		return Sandbox{connector.Unsupported{Name: exchange.Name}, exchange}, nil
	})
}

func (s Sandbox) Manifest() connector.Manifest { return s.m }

func ClaimNumber(claimID string) string {
	sum := sha256.Sum256([]byte(claimID))
	return "SBX-" + strings.ToUpper(hex.EncodeToString(sum[:5]))
}

func has(c *claim.Claim, prefix string) bool {
	return strings.HasPrefix(strings.ToUpper(c.Member.MemberNumber), prefix)
}

func (Sandbox) VerifyMember(_ context.Context, memberNumber, otp string) (connector.MemberVerification, error) {
	upper := strings.ToUpper(memberNumber)
	if strings.HasPrefix(upper, "NOTFOUND") {
		return connector.MemberVerification{}, connector.Errorf(connector.MemberNotFound, "no member %s", memberNumber)
	}
	if strings.HasPrefix(upper, "OTP") && otp != OTP {
		return connector.MemberVerification{Member: claim.Member{MemberNumber: memberNumber}, OTPRequired: true}, nil
	}
	if strings.HasPrefix(upper, "INACTIVE") {
		ended := claim.Date{Time: time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)}
		return connector.MemberVerification{
			Member:    claim.Member{MemberNumber: memberNumber, FirstName: "Kojo", LastName: "Asante"},
			PlanName:  "Sandbox Silver",
			Reason:    "inactive",
			Message:   "Cover ended 30 June 2026.",
			CoverEnds: &ended,
		}, nil
	}
	return connector.MemberVerification{
		Eligible: true,
		Member:   claim.Member{MemberNumber: memberNumber, FirstName: "Ama", LastName: "Mensah"},
		PlanName: "Sandbox Gold",
	}, nil
}

// settle is where an acknowledged sandbox claim ends up when asked again:
// the member number picks it, approved by default.
func settle(member string) (connector.PayerStatus, string) {
	switch m := strings.ToUpper(member); {
	case strings.HasPrefix(m, "PAID"):
		return connector.PayerPaid, "Paid in the weekly settlement."
	case strings.HasPrefix(m, "QUERY"):
		return connector.PayerQueried, "Send the lab report for the full blood count."
	case strings.HasPrefix(m, "DENY"):
		return connector.PayerRejected, "Not covered under the member's plan."
	case strings.HasPrefix(m, "WAIT"):
		return connector.PayerSubmitted, ""
	}
	return connector.PayerApproved, ""
}

// SendOTP pretends to text the member the sandbox code.
func (Sandbox) SendOTP(_ context.Context, memberNumber string) (string, error) {
	if strings.HasPrefix(strings.ToUpper(memberNumber), "NOTFOUND") {
		return "", connector.Errorf(connector.MemberNotFound, "no member %s", memberNumber)
	}
	return "A code was sent to the member's phone. In the sandbox it is " + OTP + ".", nil
}

func (Sandbox) ValidateOTP(_ context.Context, _, otp string) error {
	if otp != OTP {
		return connector.Errorf(connector.OTPRequired, "that code is not right")
	}
	return nil
}

// SlowFor is how long a SLOW member's payer takes to answer.
const SlowFor = 3 * time.Second

// send applies the scripted failure paths, then runs accept under the payer lock.
func send(c *claim.Claim, accept func() connector.Receipt) (connector.Receipt, error) {
	if has(c, "SLOW") {
		time.Sleep(SlowFor)
	}
	payer.Lock()
	defer payer.Unlock()
	payer.attempts[c.ID]++
	first := payer.attempts[c.ID] == 1

	switch {
	case has(c, "REJECT"):
		return connector.Receipt{}, &connector.Error{
			Code: connector.PayerRefused, Message: "sandbox payer refused the claim",
			UserMessage: "The payer refused this claim.", PayerMessage: "Member benefit exhausted for this period",
		}
	case has(c, "FLAKY") && first:
		return connector.Receipt{}, connector.Errorf(connector.Timeout, "sandbox payer timed out")
	case has(c, "LOSTREPLY") && first:
		accept()
		return connector.Receipt{}, connector.Errorf(connector.Timeout, "sandbox payer reply lost")
	}
	return accept(), nil
}

func (Sandbox) SubmitWholeClaim(_ context.Context, s *connector.Submission) (connector.Receipt, error) {
	c := s.Claim
	return send(c, func() connector.Receipt {
		number := ClaimNumber(c.ID)
		r := &record{status: "acknowledged", member: c.Member.MemberNumber, lines: map[string]connector.PayerLine{}}
		refs := make(map[string]string, len(s.Lines))
		for i, l := range s.Lines {
			ref := fmt.Sprintf("%s-%d", number, i+1)
			r.lines[ref] = connector.PayerLine{Ref: ref, Code: l.Code, Quantity: l.Quantity, Amount: l.Amount}
			for _, id := range l.LineIDs {
				refs[id] = ref
			}
		}
		payer.claims[number] = r
		return connector.Receipt{PayerClaimNumber: number, Acknowledged: true, LineReferences: refs}
	})
}

func (Sandbox) OpenClaim(_ context.Context, s *connector.Submission) (string, error) {
	payer.Lock()
	defer payer.Unlock()
	number := ClaimNumber(s.Claim.ID)
	if _, ok := payer.claims[number]; !ok {
		payer.claims[number] = &record{status: "open", member: s.Claim.Member.MemberNumber, lines: map[string]connector.PayerLine{}}
	}
	return number, nil
}

func openRecord(number string) (*record, error) {
	r, ok := payer.claims[number]
	if !ok || r.status != "open" {
		return nil, connector.Errorf(connector.ClaimClosed, "no open claim %s", number)
	}
	return r, nil
}

func (Sandbox) AddLine(_ context.Context, _ *connector.Submission, number string, l connector.SendLine) (string, error) {
	payer.Lock()
	defer payer.Unlock()
	r, err := openRecord(number)
	if err != nil {
		return "", err
	}
	r.seq++
	ref := fmt.Sprintf("%s-%d", number, r.seq)
	r.lines[ref] = connector.PayerLine{Ref: ref, Code: l.Code, Quantity: l.Quantity, Amount: l.Amount}
	return ref, nil
}

func (Sandbox) UpdateLine(_ context.Context, _ *connector.Submission, number, ref string, l connector.SendLine) (string, error) {
	payer.Lock()
	defer payer.Unlock()
	r, err := openRecord(number)
	if err != nil {
		return "", err
	}
	if _, ok := r.lines[ref]; !ok {
		return "", connector.Errorf(connector.LineNotFound, "no line %s", ref)
	}
	r.lines[ref] = connector.PayerLine{Ref: ref, Code: l.Code, Quantity: l.Quantity, Amount: l.Amount}
	return "", nil
}

func (Sandbox) DeleteLine(_ context.Context, _ *connector.Submission, number, ref string) error {
	payer.Lock()
	defer payer.Unlock()
	r, err := openRecord(number)
	if err != nil {
		return err
	}
	if _, ok := r.lines[ref]; !ok {
		return connector.Errorf(connector.LineNotFound, "no line %s", ref)
	}
	delete(r.lines, ref)
	return nil
}

func (Sandbox) AddDiagnosis(_ context.Context, _ *connector.Submission, number string, d connector.SendDiagnosis) (string, error) {
	payer.Lock()
	defer payer.Unlock()
	r, err := openRecord(number)
	if err != nil {
		return "", err
	}
	r.diagnoses = append(r.diagnoses, d.ICD)
	return d.ICD, nil
}

func (Sandbox) DeleteDiagnosis(_ context.Context, _ *connector.Submission, number, ref string) error {
	payer.Lock()
	defer payer.Unlock()
	r, err := openRecord(number)
	if err != nil {
		return err
	}
	r.diagnoses = slices.DeleteFunc(r.diagnoses, func(d string) bool { return d == ref })
	return nil
}

func (Sandbox) FinalizeClaim(_ context.Context, s *connector.Submission, number string) (connector.Receipt, error) {
	return send(s.Claim, func() connector.Receipt {
		r := payer.claims[number]
		if r == nil {
			r = &record{lines: map[string]connector.PayerLine{}}
			payer.claims[number] = r
		}
		r.status = "acknowledged"
		return connector.Receipt{PayerClaimNumber: number, Acknowledged: true}
	})
}

func (Sandbox) FindClaim(_ context.Context, s *connector.Submission) (*connector.Receipt, error) {
	payer.Lock()
	defer payer.Unlock()
	number := ClaimNumber(s.Claim.ID)
	r, ok := payer.claims[number]
	if !ok || r.status != "acknowledged" {
		return nil, nil
	}
	return &connector.Receipt{PayerClaimNumber: number, Acknowledged: true}, nil
}

func (Sandbox) FetchClaim(_ context.Context, number string) (*connector.PayerClaim, error) {
	payer.Lock()
	defer payer.Unlock()
	r, ok := payer.claims[number]
	if !ok {
		// Not in this process's memory (the sandbox forgets on restart).
		return &connector.PayerClaim{Number: number, Status: connector.PayerUnknown}, nil
	}
	pc := &connector.PayerClaim{Number: number, Status: connector.PayerOpen}
	if r.status == "acknowledged" {
		pc.Status, pc.Message = settle(r.member)
	}
	for _, ref := range slices.Sorted(maps.Keys(r.lines)) {
		pc.Lines = append(pc.Lines, r.lines[ref])
	}
	return pc, nil
}

// PayerLines lists the lines the sandbox payer holds on a claim (for tests).
func PayerLines(number string) []connector.PayerLine {
	payer.Lock()
	defer payer.Unlock()
	r, ok := payer.claims[number]
	if !ok {
		return nil
	}
	var out []connector.PayerLine
	for _, ref := range slices.Sorted(maps.Keys(r.lines)) {
		out = append(out, r.lines[ref])
	}
	return out
}

// Sends reports how many times a claim was sent to the sandbox payer (tests use
// it to prove a retry reconciled instead of submitting twice).
func Sends(claimID string) int {
	payer.Lock()
	defer payer.Unlock()
	return payer.attempts[claimID]
}
