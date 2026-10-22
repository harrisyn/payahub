// Package rxinsurance is the RX Insurance connector: manifest now, adapter
// ported from serenity-health/core-api-v2
// backend/app/plugins/insurance/rx_insurance.py.
//
// Payer quirks that code documents, which the port must keep:
//   - SUBMITCLAIM answers success with code 012 ("Claims Submitted
//     Sucessfully", misspelt) and a resubmission with 013; both mean the claim
//     is submitted. Treating them as errors stranded submitted claims.
//   - The OTP from the second verification round is sent as validation_token.
//   - RX can re-issue the same claim number, so reconciliation matches on our
//     claim reference, not on the number alone.
package rxinsurance

import "github.com/harrisyn/payahub/internal/connector"

const Key = "rx_insurance"

var manifest = connector.Manifest{
	Key:     Key,
	Name:    "RX Insurance",
	Kind:    connector.KindPlatform,
	Country: "GH",
	Channel: connector.ChannelAPI,
	Verbs: []connector.Verb{
		connector.VerifyMember, connector.SendOTP, connector.RequestAuthorization, connector.OpenClaim,
		connector.AddLine, connector.AddDiagnosis, connector.FinalizeClaim,
	},
	MemberOTPRequired: true,
	Credentials: []connector.CredentialField{
		{Name: "base_url", Label: "API base URL", Required: true},
		{Name: "api_key", Label: "API key", Secret: true, Required: true},
		{Name: "username", Label: "Username", Required: true},
		{Name: "password", Label: "Password", Secret: true, Required: true},
		{Name: "provider_id", Label: "Provider ID", Required: true},
		{Name: "user_id", Label: "User ID", Required: true},
	},
	// Sent as INSCOMPANYCODE on every call.
	PayerFields: []connector.CredentialField{
		{Name: "ins_company_code", Label: "Insurance company code", Required: true},
	},
	Submission: connector.DefaultSubmissionConfig(),
	Status:     connector.StatusPorting,
}

func init() { connector.Register(manifest, connector.Pending(manifest)) }
