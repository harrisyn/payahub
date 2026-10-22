// Package sandbox is a deterministic in-process payer for trials, docs and
// tests. Every sandbox API key reaches it, so a first claim can go end to end
// before any real payer credentials exist. The member number picks the path:
//
//	NOTFOUND...   member_not_found
//	OTP...        verification needs OTP 123456
//	REJECT...     the payer refuses the claim
//	FLAKY...      the first send times out before reaching the payer
//	LOSTREPLY...  the first send reaches the payer but the reply is lost
//	anything else eligible; claim acknowledged
package sandbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"

	"github.com/harrisyn/payahub/internal/claim"
	"github.com/harrisyn/payahub/internal/connector"
)

const (
	Key = "sandbox"
	OTP = "123456"
)

var manifest = connector.Manifest{
	Key:     Key,
	Name:    "Payahub Sandbox",
	Country: "ZZ",
	Channel: connector.ChannelAPI,
	Verbs: []connector.Verb{
		connector.VerifyMember, connector.SubmitWholeClaim, connector.OpenClaim, connector.AddLine,
		connector.UpdateLine, connector.DeleteLine, connector.AddDiagnosis, connector.FinalizeClaim,
		connector.WithdrawClaim, connector.FindClaim,
	},
	Submission: connector.DefaultSubmissionConfig(),
	Notes:      "Simulated payer. Never reaches a real insurer.",
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
	lines     map[string]string
	diagnoses []string
}

type Sandbox struct{ connector.Unsupported }

func New(map[string]string) (connector.Connector, error) {
	return Sandbox{connector.Unsupported{Name: manifest.Name}}, nil
}

func init() { connector.Register(manifest, New) }

func (Sandbox) Manifest() connector.Manifest { return manifest }

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
	return connector.MemberVerification{
		Eligible: true,
		Member:   claim.Member{MemberNumber: memberNumber, FirstName: "Ama", LastName: "Mensah"},
		PlanName: "Sandbox Gold",
	}, nil
}

// send applies the scripted failure paths, then runs accept under the payer lock.
func send(c *claim.Claim, accept func() connector.Receipt) (connector.Receipt, error) {
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

func (Sandbox) SubmitWholeClaim(_ context.Context, c *claim.Claim) (connector.Receipt, error) {
	return send(c, func() connector.Receipt {
		number := ClaimNumber(c.ID)
		refs := make(map[string]string, len(c.Lines))
		for i, l := range c.Lines {
			refs[l.ID] = fmt.Sprintf("%s-%d", number, i+1)
		}
		payer.claims[number] = &record{status: "acknowledged", lines: refs}
		return connector.Receipt{PayerClaimNumber: number, Acknowledged: true, LineReferences: refs}
	})
}

func (Sandbox) OpenClaim(_ context.Context, c *claim.Claim) (string, error) {
	payer.Lock()
	defer payer.Unlock()
	number := ClaimNumber(c.ID)
	if _, ok := payer.claims[number]; !ok {
		payer.claims[number] = &record{status: "open", lines: map[string]string{}}
	}
	return number, nil
}

func openRecord(number string) (*record, error) {
	r, ok := payer.claims[number]
	if !ok {
		return nil, connector.Errorf(connector.ClaimClosed, "no open claim %s", number)
	}
	return r, nil
}

func (Sandbox) AddLine(_ context.Context, number string, l claim.Line) (string, error) {
	payer.Lock()
	defer payer.Unlock()
	r, err := openRecord(number)
	if err != nil {
		return "", err
	}
	if ref, ok := r.lines[l.ID]; ok {
		// Idempotent, like a payer that answers "already added" with the existing line.
		return ref, nil
	}
	ref := fmt.Sprintf("%s-%d", number, len(r.lines)+1)
	r.lines[l.ID] = ref
	return ref, nil
}

func (Sandbox) AddDiagnosis(_ context.Context, number string, d claim.Diagnosis) error {
	payer.Lock()
	defer payer.Unlock()
	r, err := openRecord(number)
	if err != nil {
		return err
	}
	r.diagnoses = append(r.diagnoses, d.Code.Value)
	return nil
}

func (Sandbox) FinalizeClaim(_ context.Context, number string, c *claim.Claim) (connector.Receipt, error) {
	return send(c, func() connector.Receipt {
		r := payer.claims[number]
		if r == nil {
			r = &record{lines: map[string]string{}}
			payer.claims[number] = r
		}
		r.status = "acknowledged"
		refs := make(map[string]string, len(r.lines))
		for k, v := range r.lines {
			refs[k] = v
		}
		return connector.Receipt{PayerClaimNumber: number, Acknowledged: true, LineReferences: refs}
	})
}

func (Sandbox) FindClaim(_ context.Context, c *claim.Claim) (*connector.Receipt, error) {
	payer.Lock()
	defer payer.Unlock()
	number := ClaimNumber(c.ID)
	r, ok := payer.claims[number]
	if !ok || r.status != "acknowledged" {
		return nil, nil
	}
	return &connector.Receipt{PayerClaimNumber: number, Acknowledged: true, LineReferences: r.lines}, nil
}

// Sends reports how many times a claim was sent to the sandbox payer (tests use
// it to prove a retry reconciled instead of submitting twice).
func Sends(claimID string) int {
	payer.Lock()
	defer payer.Unlock()
	return payer.attempts[claimID]
}
