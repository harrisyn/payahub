package access

import "testing"

func TestRoles(t *testing.T) {
	cases := []struct {
		r    Role
		p    Perm
		want bool
	}{
		{Owner, Team, true},
		{Admin, Team, true},
		{Claims, WorkClaims, true},
		{Claims, Billing, false},
		{Finance, Billing, true},
		{Finance, WorkClaims, false},
		{Developer, Developers, true},
		{Developer, Setup, true},
		{Developer, Team, false},
		{Viewer, WorkClaims, false},
		{Role("root"), Team, false},
	}
	for _, c := range cases {
		if got := Can(c.r, c.p); got != c.want {
			t.Errorf("Can(%s, %s) = %v", c.r, c.p, got)
		}
	}
	if !CanAssign(Owner, Viewer, Owner) || CanAssign(Admin, Viewer, Owner) || CanAssign(Admin, Owner, Viewer) || !CanAssign(Admin, Viewer, Claims) {
		t.Error("only owners make or unmake owners; admins manage the rest")
	}
	if CanAssign(Claims, Viewer, Viewer) {
		t.Error("a claims member cannot manage the team")
	}
	for _, r := range Roles {
		if !Valid(r) {
			t.Errorf("%s is listed but not valid", r)
		}
	}
}
