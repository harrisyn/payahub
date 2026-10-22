// Package xceli is the XCeli Solutions connector: manifest now, adapter ported
// from serenity-health/core-api-v2 backend/app/plugins/insurance/xceli.py.
//
// Payer quirks that code documents, which the port must keep:
//   - Two servers: sign-in goes to a central auth server (authBaseUrl), every
//     member and claim call to the insurer's own API server (insurerApiUrl).
//   - Sign-in returns a JWT and a userDTO whose UserId must be sent on every
//     write; the token's expiry is tracked and refreshed before it lapses.
//   - Claims are built incrementally (createihspclaim, then one call per line
//     and per diagnosis) and only reach the insurer at submitclaim.
//   - Whether a member needs an OTP comes from the member lookup (RequireOTP).
package xceli

import "github.com/harrisyn/payahub/internal/connector"

const Key = "xceli"

var manifest = connector.Manifest{
	Key:     Key,
	Name:    "XCeli",
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
		{Name: "auth_base_url", Label: "Auth server URL", Required: true},
		{Name: "username", Label: "Portal username", Required: true},
		{Name: "password", Label: "Portal password", Secret: true, Required: true},
		{Name: "provider_id", Label: "Provider ID", Required: true},
		{Name: "provider_name", Label: "Provider name", Required: true},
	},
	// Each insurer on XCeli runs its own API server; sign-in is shared.
	PayerFields: []connector.CredentialField{
		{Name: "insurer_api_url", Label: "Insurer API URL", Required: true},
	},
	Submission: connector.DefaultSubmissionConfig(),
	Status:     connector.StatusPorting,
}

func init() { connector.Register(manifest, connector.Pending(manifest)) }
