// Package medserve is the Medserve connector: manifest now, adapter ported
// from serenity-health/core-api-v2 backend/app/plugins/insurance/medserve.py.
//
// Payer quirks that code documents, which the port must keep:
//   - One endpoint for everything, the operation chosen by an op parameter;
//     the connid from op=login rides on every later call.
//   - Built around pharmacy batches: a claim is a prescription batch (p.sba),
//     lines are added (p.ap) and dispensed (p.ad); finalising (p.finalb) first
//     bulk-submits every dispensation on the batch (p.bulkfind).
//   - A dispensation that comes back with approvals needs a payer approval
//     request (p.adapreq) before it can be claimed.
//   - The insurer's code is a path segment (insurerCode, "ghi" by default),
//     and whether OTP is needed is asked per insurer (op=qoinfo).
package medserve

import "github.com/harrisyn/payahub/internal/connector"

const Key = "medserve"

var manifest = func() connector.Manifest {
	sub := connector.DefaultSubmissionConfig()
	sub.LineSubmissionMode = "bulk"
	return connector.Manifest{
		Key:     Key,
		Name:    "Medserve",
		Country: "GH",
		Channel: connector.ChannelAPI,
		Verbs: []connector.Verb{
			connector.VerifyMember, connector.SendOTP, connector.RequestAuthorization, connector.OpenClaim,
			connector.AddLine, connector.FinalizeClaim, connector.WithdrawClaim, connector.ListCatalog,
		},
		MemberOTPRequired: true,
		Credentials: []connector.CredentialField{
			{Name: "base_url", Label: "API base URL", Required: true},
			{Name: "username", Label: "POS username", Required: true},
			{Name: "password", Label: "POS password", Secret: true, Required: true},
			{Name: "insurer_code", Label: "Insurer code", Required: false},
		},
		Submission: sub,
		Status:     connector.StatusPorting,
	}
}()

func init() { connector.Register(manifest, connector.Pending(manifest)) }
