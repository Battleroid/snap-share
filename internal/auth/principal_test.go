package auth

import (
	"context"
	"testing"
)

func TestCan(t *testing.T) {
	session := &Principal{UserID: 1, Role: RoleUser}
	admin := &Principal{UserID: 2, Role: RoleAdmin}
	key := &Principal{UserID: 1, Role: RoleUser, KeyID: 9, Scopes: []Scope{ScopeItemsWrite, ScopeAdmin}}
	var anon *Principal

	cases := []struct {
		p    *Principal
		s    Scope
		want bool
	}{
		{session, ScopeItemsWrite, true},
		{session, ScopeAdmin, false},
		{admin, ScopeAdmin, true},
		{key, ScopeItemsWrite, true},
		{key, ScopeItemsRead, false}, // not granted
		{key, ScopeAdmin, false},     // granted but role forbids
		{anon, ScopeItemsRead, false},
		{session, Scope("bogus"), false},
	}
	for i, c := range cases {
		if got := c.p.Can(c.s); got != c.want {
			t.Errorf("case %d: Can(%s) = %v", i, c.s, got)
		}
	}
	if FromContext(context.Background()) != nil {
		t.Fatal("empty context should be anonymous")
	}
	if FromContext(WithPrincipal(context.Background(), admin)) != admin {
		t.Fatal("round trip")
	}
}
