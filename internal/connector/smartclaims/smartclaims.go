// Package smartclaims is the SmartClaims (NMHA) connector: manifest now,
// adapter ported from serenity-health/core-api-v2
// backend/app/plugins/insurance/smartclaims.py.
//
// Payer quirks that code documents, which the port must keep:
//   - Asking for authorization (getauthorization) makes the insurer text the
//     member an OTP itself; there is no separate send-OTP call.
//   - startattendance opens the claim but never returns a claim ID; every
//     later /txn/* call is keyed on the item's guid.
//   - success:1 on /txn/additem is not enough: a warning may carry a rule
//     whose real outcome is an error, and the added item must then be rolled
//     back. Validation failures arrive as HTTP 400 with the same shape.
//   - Never surface SmartClaims' failedValidation dump: it holds the whole
//     internal claim document, member details included.
package smartclaims

import "github.com/harrisyn/payahub/internal/connector"

const Key = "smartclaims"

var manifest = connector.Manifest{
	Key:     Key,
	Name:    "SmartClaims",
	Kind:    connector.KindPlatform,
	Country: "GH",
	Channel: connector.ChannelAPI,
	Verbs: []connector.Verb{
		connector.VerifyMember, connector.SendOTP, connector.OpenClaim, connector.AddLine,
		connector.UpdateLine, connector.DeleteLine, connector.AddDiagnosis, connector.FinalizeClaim,
		connector.ListCatalog,
	},
	MemberOTPRequired: true,
	Credentials: []connector.CredentialField{
		{Name: "base_url", Label: "API base URL", Required: true},
		{Name: "username", Label: "Username", Required: true},
		{Name: "password", Label: "Password", Secret: true, Required: true},
	},
	// HICD is the insurer's ID on SmartClaims (core-api-v2 reads it from the
	// payer's external reference), so it belongs to the payer, not the clinic.
	PayerFields: []connector.CredentialField{
		{Name: "hicd", Label: "Health insurance company ID (HICD)", Required: true},
	},
	Submission: connector.DefaultSubmissionConfig(),
	Status:     connector.StatusPorting,
}

func init() { connector.Register(manifest, connector.Pending(manifest)) }
