package auth

import "testing"

func TestPolicyAllows(t *testing.T) {
	admin := &User{Role: RoleAdmin}
	creator := &User{Role: RoleFormCreator}
	basic := &User{Role: RoleBasic}

	tests := []struct {
		name   string
		policy Policy
		user   *User
		want   bool
	}{
		{"public anonymous", Public, nil, true},
		{"public basic", Public, basic, true},
		{"signed in anonymous", SignedIn(), nil, false},
		{"signed in basic", SignedIn(), basic, true},
		{"zero value is signed in", Policy{}, nil, false},
		{"creator only: basic", SignedIn(RoleFormCreator), basic, false},
		{"creator only: creator", SignedIn(RoleFormCreator), creator, true},
		{"creator only: admin", SignedIn(RoleFormCreator), admin, true},
		{"admin only: creator", SignedIn(RoleAdmin), creator, false},
		{"admin only: anonymous", SignedIn(RoleAdmin), nil, false},
	}
	for _, tt := range tests {
		if got := tt.policy.Allows(tt.user); got != tt.want {
			t.Errorf("%s: Allows = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestRoleValid(t *testing.T) {
	for _, r := range AllRoles {
		if !r.Valid() {
			t.Errorf("%s: Valid = false", r)
		}
	}
	for _, r := range []Role{"", "admin", "ROOT"} {
		if r.Valid() {
			t.Errorf("%q: Valid = true", r)
		}
	}
}
