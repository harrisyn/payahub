// Package access says what each member of an organisation may do in the
// console. API keys are not people: a key acts for its whole organisation
// in its own environment, as before.
package access

// Role is a member's job in the organisation.
type Role string

const (
	Owner     Role = "owner"     // everything, and the only role that can add or remove owners
	Admin     Role = "admin"     // everything else
	Claims    Role = "claims"    // creates, checks and sends claims
	Finance   Role = "finance"   // the wallet, withdrawals and the plan
	Developer Role = "developer" // API keys, webhooks, insurer accounts and code mappings
	Viewer    Role = "viewer"    // reads everything, changes nothing
)

// Roles in the order the console lists them.
var Roles = []Role{Owner, Admin, Claims, Finance, Developer, Viewer}

// Perm is something a member may change. Reading is open to every member.
type Perm string

const (
	WorkClaims Perm = "claims"     // create, check, confirm and send claims; read forms
	Setup      Perm = "setup"      // profile, insurer settings and accounts, price list, mappings
	Billing    Perm = "billing"    // plan, fund the wallet, payout accounts, withdrawals
	Developers Perm = "developers" // API keys and webhook endpoints
	Team       Perm = "team"       // invite, change and remove members
)

var grants = map[Role][]Perm{
	Owner:     {WorkClaims, Setup, Billing, Developers, Team},
	Admin:     {WorkClaims, Setup, Billing, Developers, Team},
	Claims:    {WorkClaims},
	Finance:   {Billing},
	Developer: {Developers, Setup},
	Viewer:    nil,
}

// Valid reports whether r is a known role.
func Valid(r Role) bool {
	_, ok := grants[r]
	return ok
}

// Can reports whether a member with role r holds p. An unknown role holds
// nothing.
func Can(r Role, p Perm) bool {
	for _, g := range grants[r] {
		if g == p {
			return true
		}
	}
	return false
}

// Perms lists what r holds, for the console to show or hide controls.
func Perms(r Role) []Perm {
	return append([]Perm{}, grants[r]...)
}

// CanAssign reports whether a member with role by may give a member role to,
// or change a member who now has role from. Only owners touch owners.
func CanAssign(by, from, to Role) bool {
	if !Can(by, Team) {
		return false
	}
	if by != Owner && (from == Owner || to == Owner) {
		return false
	}
	return true
}
