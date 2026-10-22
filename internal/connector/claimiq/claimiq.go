// Package claimiq is the ClaimIQ connector: manifest now, adapter ported in
// phase 0 from serenity-health/core-api-v2 backend/app/plugins/insurance/claimiq.py.
//
// Payer quirks that code learned the hard way, which the port must keep:
//   - Filing a claim does not submit it. POST /v1/claims (or incremental
//     item/diagnosis calls) only queues it in ClaimIQ; nothing reaches the
//     payer until POST /v1/batches runs. SubmitWholeClaim therefore creates
//     the claim and sends a batch of one.
//   - Opening an empty claim shell goes through POST /patients/check-in with
//     create_claim=true, not POST /claims, which needs claim data up front.
package claimiq

import (
	"github.com/harrisyn/payahub/internal/connector"
)

const Key = "claimiq"

var manifest = func() connector.Manifest {
	sub := connector.DefaultSubmissionConfig()
	sub.LineSubmissionMode = "bulk"
	sub.ClaimCreationTiming = "deferred"
	return connector.Manifest{
		Key:     Key,
		Name:    "ClaimIQ",
		Kind:    connector.KindPlatform,
		Country: "GH",
		Channel: connector.ChannelAPI,
		Verbs: []connector.Verb{
			connector.VerifyMember, connector.RequestAuthorization, connector.SubmitWholeClaim,
			connector.OpenClaim, connector.AddLine, connector.UpdateLine, connector.DeleteLine,
			connector.AddDiagnosis, connector.FinalizeClaim, connector.WithdrawClaim, connector.FindClaim,
		},
		Credentials: []connector.CredentialField{
			{Name: "base_url", Label: "API base URL", Required: true},
			{Name: "api_key", Label: "API key", Secret: true, Required: true},
		},
		PayerFields: []connector.CredentialField{
			{Name: "payer_id", Label: "ClaimIQ payer ID", Required: true},
		},
		Submission: sub,
		Status:     connector.StatusPorting,
	}
}()

func init() { connector.Register(manifest, connector.Pending(manifest)) }
