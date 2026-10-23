package api_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/harrisyn/payahub/internal/claim"
)

// fund pays amount into the session's wallet through the sandbox provider.
func fund(t *testing.T, ses env, amount int) map[string]any {
	t.Helper()
	s, f := ses.do(t, "POST", "/v1/wallet/fundings", map[string]any{
		"amount": amount, "provider": "sandbox", "callback_url": "http://localhost:3000/console/wallet",
	})
	if s != 201 {
		t.Fatalf("funding: %d %v", s, f)
	}
	_, f = ses.do(t, "GET", "/v1/wallet/fundings/"+f["id"].(string), nil)
	return f
}

func wallet(t *testing.T, e env) map[string]any {
	t.Helper()
	_, w := e.do(t, "GET", "/v1/wallet", nil)
	return w
}

func balanced(t *testing.T) {
	t.Helper()
	ok, err := st.LedgerBalanced(context.Background())
	if err != nil || !ok {
		t.Fatalf("the ledger does not balance: %v", err)
	}
}

func waitWithdrawal(t *testing.T, ses env, id, want string) map[string]any {
	t.Helper()
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		_, out := ses.do(t, "GET", "/v1/wallet/withdrawals", nil)
		for _, w := range out["data"].([]any) {
			if m := w.(map[string]any); m["id"] == id && m["status"] == want {
				return m
			}
		}
	}
	t.Fatalf("withdrawal %s never became %s", id, want)
	return nil
}

func TestWalletFundsChargesAndPaysOut(t *testing.T) {
	e := setup(t)
	ses, _ := signup(t, e)
	_, k := ses.do(t, "POST", "/v1/api-keys", map[string]any{"environment": "live"})
	live := env{srv: e.srv, key: k["key"].(string)}

	if s, out := ses.do(t, "PUT", "/v1/organization/plan", map[string]any{"plan": "volume"}); s != 200 || out["charged"] != 0.0 {
		t.Fatalf("volume has no fee: %d %v", s, out)
	}
	id := live.create(t, "M-2001")
	if s, out := live.do(t, "POST", "/v1/claims/"+id+"/submit", nil); s != 402 || out["error"].(map[string]any)["code"] != "wallet_empty" {
		t.Fatalf("an empty wallet sent a live claim: %d %v", s, out)
	}

	body := map[string]any{"amount": 50000, "provider": "sandbox", "callback_url": "http://localhost:3000/console/wallet"}
	s, f := ses.do(t, "POST", "/v1/wallet/fundings", body, "Idempotency-Key", "top-up-1")
	if s != 201 || f["status"] != "pending" || f["checkout_url"] == nil {
		t.Fatalf("funding: %d %v", s, f)
	}
	if s, again := ses.do(t, "POST", "/v1/wallet/fundings", body, "Idempotency-Key", "top-up-1"); s != 200 || again["id"] != f["id"] {
		t.Fatalf("a repeated funding made a second checkout: %d %v", s, again)
	}
	if w := wallet(t, ses); w["balance"] != 0.0 {
		t.Fatalf("credited before the provider confirmed: %v", w)
	}
	for range 3 { // the return page, a reload, and the webhook: one credit
		ses.do(t, "GET", "/v1/wallet/fundings/"+f["id"].(string), nil)
	}
	if s, _ := e.do(t, "POST", "/v1/webhooks/payments/sandbox", map[string]any{"kind": "collection", "reference": f["id"]}); s != 200 {
		t.Fatalf("webhook: %d", s)
	}
	if w := wallet(t, ses); w["balance"] != 50000.0 || w["can_send_live"] != true {
		t.Fatalf("funded once: %v", w)
	}

	if s, _ := live.do(t, "POST", "/v1/claims/"+id+"/submit", nil); s != 202 {
		t.Fatalf("funded wallet blocked: %d", s)
	}
	live.waitFor(t, id, claim.Acknowledged)
	if w := wallet(t, ses); w["balance"] != 49800.0 || w["charged_this_month"] != 200.0 {
		t.Fatalf("one charge of GHS 2.00 per acknowledged claim: %v", w)
	}
	_, stmt := ses.do(t, "GET", "/v1/wallet/transactions", nil)
	if n := len(stmt["data"].([]any)); n != 2 {
		t.Fatalf("statement: %d entries %v", n, stmt)
	}

	s, acct := ses.do(t, "POST", "/v1/wallet/payout-accounts", map[string]any{
		"provider": "sandbox", "type": "mobile_money", "name": "Ridge Clinic", "bank_code": "MTN", "account_number": "024 555 0101",
	})
	if s != 201 || acct["bank_name"] != "MTN Mobile Money" || acct["account_number"] != "0245550101" {
		t.Fatalf("payout account: %d %v", s, acct)
	}
	if s, out := live.do(t, "POST", "/v1/wallet/withdrawals", map[string]any{"amount": 100, "payout_account_id": acct["id"]}); s != 403 {
		t.Fatalf("an API key withdrew: %d %v", s, out)
	}
	if s, out := ses.do(t, "POST", "/v1/wallet/withdrawals", map[string]any{"amount": 60000, "payout_account_id": acct["id"]}); s != 422 ||
		out["error"].(map[string]any)["code"] != "insufficient_funds" {
		t.Fatalf("overdrawn: %d %v", s, out)
	}
	wreq := map[string]any{"amount": 40000, "payout_account_id": acct["id"], "reason": "month end"}
	s, wd := ses.do(t, "POST", "/v1/wallet/withdrawals", wreq, "Idempotency-Key", "w1")
	if s != 201 || wd["status"] != "requested" {
		t.Fatalf("withdrawal: %d %v", s, wd)
	}
	if s, again := ses.do(t, "POST", "/v1/wallet/withdrawals", wreq, "Idempotency-Key", "w1"); s != 200 || again["id"] != wd["id"] {
		t.Fatalf("repeated withdrawal: %d %v", s, again)
	}
	if w := wallet(t, ses); w["balance"] != 9800.0 || w["pending_withdrawals"] != 40000.0 {
		t.Fatalf("held on request: %v", w)
	}
	if s, _ := ses.do(t, "POST", "/v1/operator/withdrawals/"+wd["id"].(string)+"/approve", nil); s != 403 {
		t.Fatalf("a clinic approved its own withdrawal: %d", s)
	}
	op := operatorSession(t, e)
	if s, out := op.do(t, "POST", "/v1/operator/withdrawals/"+wd["id"].(string)+"/approve", nil); s != 200 || out["status"] != "approved" {
		t.Fatalf("approve: %d %v", s, out)
	}
	if s, _ := op.do(t, "POST", "/v1/operator/withdrawals/"+wd["id"].(string)+"/approve", nil); s != 409 {
		t.Fatalf("approved twice: %d", s)
	}
	waitWithdrawal(t, ses, wd["id"].(string), "paid")
	if w := wallet(t, ses); w["balance"] != 9800.0 || w["pending_withdrawals"] != 0.0 {
		t.Fatalf("after payout: %v", w)
	}

	// The provider refuses one (sandbox: amounts ending in .13); it comes back.
	_, bad := ses.do(t, "POST", "/v1/wallet/withdrawals", map[string]any{"amount": 1013, "payout_account_id": acct["id"]})
	op.do(t, "POST", "/v1/operator/withdrawals/"+bad["id"].(string)+"/approve", nil)
	waitWithdrawal(t, ses, bad["id"].(string), "failed")
	// An operator turns one down; it comes back.
	_, no := ses.do(t, "POST", "/v1/wallet/withdrawals", map[string]any{"amount": 500, "payout_account_id": acct["id"]})
	if s, out := op.do(t, "POST", "/v1/operator/withdrawals/"+no["id"].(string)+"/reject", map[string]any{"reason": "Confirm the account by phone first"}); s != 200 || out["status"] != "rejected" {
		t.Fatalf("reject: %d %v", s, out)
	}
	if w := wallet(t, ses); w["balance"] != 9800.0 {
		t.Fatalf("returned withdrawals: %v", w)
	}
	balanced(t)
}

func TestFailedFundingCreditsNothing(t *testing.T) {
	e := setup(t)
	ses, _ := signup(t, e)
	if f := fund(t, ses, 1013); f["status"] != "failed" {
		t.Fatalf("declined checkout: %v", f)
	}
	if w := wallet(t, ses); w["balance"] != 0.0 {
		t.Fatalf("a declined payment credited the wallet: %v", w)
	}
	if s, _ := ses.do(t, "POST", "/v1/wallet/fundings", map[string]any{"amount": 50, "provider": "sandbox"}); s != 422 {
		t.Fatalf("below the minimum: %d", s)
	}
	if s, _ := ses.do(t, "POST", "/v1/wallet/fundings", map[string]any{"amount": 5000, "provider": "nope"}); s != 422 {
		t.Fatalf("unknown provider: %d", s)
	}
}

func TestMonthlyFeeIsChargedOncePerMonth(t *testing.T) {
	e := setup(t)
	ses, acct := signup(t, e)
	if _, out := ses.do(t, "PUT", "/v1/organization/plan", map[string]any{"plan": "flat_volume"}); out["charged"] != 60000.0 {
		t.Fatalf("flat + volume fee: %v", out)
	}
	if w := wallet(t, ses); w["balance"] != -60000.0 || w["can_send_live"] != false {
		t.Fatalf("an unpaid fee blocks live claims: %v", w)
	}
	// Moving up costs the difference; moving back and again costs nothing.
	if _, out := ses.do(t, "PUT", "/v1/organization/plan", map[string]any{"plan": "flat"}); out["charged"] != 120000.0 {
		t.Fatalf("difference: %v", out)
	}
	ses.do(t, "PUT", "/v1/organization/plan", map[string]any{"plan": "flat_volume"})
	if _, out := ses.do(t, "PUT", "/v1/organization/plan", map[string]any{"plan": "flat"}); out["charged"] != 0.0 {
		t.Fatalf("charged twice in a month: %v", out)
	}
	owing, err := st.OrgsOwingFee(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range owing {
		if o == acct["organization_id"] {
			t.Fatalf("the billing round would charge it again")
		}
	}
	fund(t, ses, 180000)
	if w := wallet(t, ses); w["balance"] != 0.0 || w["can_send_live"] != true {
		t.Fatalf("paid up: %v", w)
	}
	balanced(t)
}

func TestOperatorAdjustsOnce(t *testing.T) {
	e := setup(t)
	ses, acct := signup(t, e)
	op := operatorSession(t, e)
	path := "/v1/operator/wallets/" + acct["organization_id"].(string) + "/adjustments"
	body := map[string]any{"amount": 2500, "memo": "Goodwill credit for the outage on 20 October"}
	if s, _ := op.do(t, "POST", path, body); s != 422 {
		t.Fatalf("an adjustment without a key: %d", s)
	}
	if s, _ := op.do(t, "POST", path, body, "Idempotency-Key", "adj-1"); s != 201 {
		t.Fatalf("adjust: %d", s)
	}
	if s, _ := op.do(t, "POST", path, body, "Idempotency-Key", "adj-1"); s != 200 {
		t.Fatalf("repeat: %d", s)
	}
	if w := wallet(t, ses); w["balance"] != 2500.0 {
		t.Fatalf("credited once: %v", w)
	}
	if s, _ := ses.do(t, "POST", path, body, "Idempotency-Key", "adj-2"); s != 403 {
		t.Fatalf("a clinic credited itself: %d", s)
	}
	_, l := op.do(t, "GET", "/v1/operator/ledger", nil)
	if l["balanced"] != true {
		t.Fatalf("ledger: %v", l)
	}
}

func TestConcurrentWithdrawalsNeverOverdraw(t *testing.T) {
	e := setup(t)
	ses, _ := signup(t, e)
	fund(t, ses, 1000)
	_, acct := ses.do(t, "POST", "/v1/wallet/payout-accounts", map[string]any{
		"provider": "sandbox", "type": "bank", "name": "Ridge Clinic", "bank_code": "SBX-BANK", "account_number": "1002003004",
	})
	var wg sync.WaitGroup
	var ok atomic.Int32
	for range 20 {
		wg.Go(func() {
			if s, _ := ses.do(t, "POST", "/v1/wallet/withdrawals", map[string]any{"amount": 100, "payout_account_id": acct["id"]}); s == 201 {
				ok.Add(1)
			}
		})
	}
	wg.Wait()
	if ok.Load() != 10 {
		t.Fatalf("%d withdrawals of GHS 1.00 from GHS 10.00", ok.Load())
	}
	if w := wallet(t, ses); w["balance"] != 0.0 {
		t.Fatalf("balance: %v", w)
	}
	balanced(t)
}
