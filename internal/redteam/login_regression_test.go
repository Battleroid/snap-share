package redteam_test

// Regression tests for the sign-in limiter findings (they failed while the
// bugs existed).

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/Battleroid/snap-share/internal/accounts"
	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/testutil"
)

func (e *env) setPassword(u *accounts.User, pw string) {
	e.t.Helper()
	h, err := auth.HashPassword(e.ctx, pw)
	if err != nil {
		e.t.Fatal(err)
	}
	if err := e.s.App.Accounts.Store().SetPassword(e.ctx, u.ID, h, nil, accounts.AuditEntry{}); err != nil {
		e.t.Fatal(err)
	}
}

// loginRace fires n parallel sign-ins at once; guess(i) is the password of
// attempt i. It returns the status codes.
func (e *env) loginRace(n int, login string, guess func(int) string) []int {
	codes := make([]int, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			form := url.Values{"login": {login}, "password": {guess(i)}}
			req := e.s.Request(e.t, "POST", "/login", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			<-start
			res, err := e.client.Do(req)
			if err != nil {
				return
			}
			_ = res.Body.Close()
			codes[i] = res.StatusCode
		}()
	}
	close(start)
	wg.Wait()
	return codes
}

// The per-name budget is not shared between a username and an address, so
// a locked username says nothing about which address belongs to it.
func TestLoginBudgetDoesNotLinkEmailToUsername(t *testing.T) {
	e := newEnv(t, testutil.Env("RATELIMIT_EXEMPT", "127.0.0.1/32"))
	u := e.user("alice", auth.RoleUser)
	e.setPassword(u, "the-real-password-42")
	for i := range 5 {
		e.loginRace(1, "alice", func(int) string { return fmt.Sprint("wrong", i) })
	}
	if c := e.loginRace(1, "alice", func(int) string { return "wrong" })[0]; c != http.StatusTooManyRequests {
		t.Fatalf("username after 5 failures: %d, want 429", c)
	}
	other := e.loginRace(1, "someone@example.test", func(int) string { return "wrong" })[0]
	hers := e.loginRace(1, "alice@example.test", func(int) string { return "wrong" })[0]
	if other != hers || hers != http.StatusUnauthorized {
		t.Fatalf("oracle: unrelated address %d, alice's address %d", other, hers)
	}
}

// Parallel guesses get at most the budget (5) checked against the hash,
// even from an exempt address; the rest are refused without hashing.
func TestLoginAccountLimiterCannotBeRaced(t *testing.T) {
	e := newEnv(t, testutil.Env("RATELIMIT_EXEMPT", "127.0.0.1/32"))
	u := e.user("victim", auth.RoleUser)
	e.setPassword(u, "the-real-password-42")

	// Sequential: the sixth failure is refused.
	for i := range 6 {
		codes := e.loginRace(1, "victim", func(int) string { return fmt.Sprintf("wrong-seq-%d", i) })
		if i < 5 && codes[0] != http.StatusUnauthorized {
			t.Fatalf("sequential attempt %d: %d", i, codes[0])
		}
		if i == 5 && codes[0] != http.StatusTooManyRequests {
			t.Fatalf("sequential attempt 6: %d, want 429", codes[0])
		}
	}

	// Fresh account, raced: 40 wrong guesses at once.
	v2 := e.user("victim2", auth.RoleUser)
	e.setPassword(v2, "the-real-password-43")
	const n = 40
	codes := e.loginRace(n, "victim2", func(i int) string { return fmt.Sprintf("wrong-%d", i) })
	evaluated, limited := 0, 0
	for _, c := range codes {
		switch c {
		case http.StatusUnauthorized, http.StatusSeeOther:
			evaluated++
		case http.StatusTooManyRequests:
			limited++
		}
	}
	if evaluated > 5 || evaluated+limited != n {
		t.Fatalf("raced %d guesses: %d checked against the hash (budget 5), %d refused", n, evaluated, limited)
	}
	// The right password is refused too now: the budget is spent.
	if c := e.loginRace(1, "victim2", func(int) string { return "the-real-password-43" })[0]; c != http.StatusTooManyRequests {
		t.Fatalf("after the raced guesses: %d, want 429", c)
	}
}

// Parallel successful sign-ins (several browsers, the e2e workers) never
// lock an account out: successes give their token back, and the ones beyond
// the budget wait instead of failing.
func TestParallelSuccessfulSignInsAreNotLimited(t *testing.T) {
	e := newEnv(t, testutil.Env("RATELIMIT_EXEMPT", "127.0.0.1/32"))
	u := e.user("busy", auth.RoleUser)
	e.setPassword(u, "the-real-password-44")
	for round := range 2 {
		for i, c := range e.loginRace(12, "busy", func(int) string { return "the-real-password-44" }) {
			if c != http.StatusSeeOther {
				t.Fatalf("round %d sign-in %d: %d", round, i, c)
			}
		}
	}
}
