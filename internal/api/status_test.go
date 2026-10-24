package api_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/harrisyn/payahub/internal/queue"
)

func TestClaimsAreFollowedUntilPaidOrRejected(t *testing.T) {
	e := setup(t)
	send := func(member string) string {
		t.Helper()
		s, out := e.do(t, "POST", "/v1/claims?submit=true&wait=10", claimBody(member))
		if s != 200 || out["claim"].(map[string]any)["state"] != "acknowledged" {
			t.Fatalf("send %s: %d %v", member, s, out)
		}
		return out["claim"].(map[string]any)["id"].(string)
	}
	ask := func(id string) map[string]any {
		t.Helper()
		s, c := e.do(t, "POST", "/v1/claims/"+id+"/status-check", nil)
		if s != 200 {
			t.Fatalf("status check: %d %v", s, c)
		}
		return c
	}
	kinds := func(id string) []string {
		_, out := e.do(t, "GET", "/v1/claims/"+id+"/events", nil)
		var k []string
		for _, ev := range out["data"].([]any) {
			k = append(k, ev.(map[string]any)["kind"].(string))
		}
		return k
	}

	cases := []struct {
		member, state, payerStatus string
		event                      string
	}{
		{"M-STATUS-1", "approved", "approved", "approved"},
		{"PAID-STATUS-1", "paid", "paid", "paid"},
		{"DENY-STATUS-1", "rejected", "rejected", "rejected"},
		{"QUERY-STATUS-1", "acknowledged", "queried", "queried"},
		{"WAIT-STATUS-1", "acknowledged", "submitted", ""},
	}
	for _, c := range cases {
		id := send(c.member)
		got := ask(id)
		if got["state"] != c.state || got["payer_status"] != c.payerStatus || got["status_checked_at"] == nil {
			t.Errorf("%s: %v / %v", c.member, got["state"], got["payer_status"])
		}
		if c.event != "" && !slices.Contains(kinds(id), c.event) {
			t.Errorf("%s: no %s event in %v", c.member, c.event, kinds(id))
		}
		if c.member == "PAID-STATUS-1" {
			// Paid passes through approved, so both are recorded.
			if k := kinds(id); !slices.Contains(k, "approved") {
				t.Errorf("paid without approved: %v", k)
			}
			if s, _ := e.do(t, "POST", "/v1/claims/"+id+"/status-check", nil); s != 409 {
				t.Errorf("a paid claim was asked about again: %d", s)
			}
		}
		if c.member == "QUERY-STATUS-1" {
			if got["payer_message"] == "" {
				t.Errorf("the payer's question is missing: %v", got)
			}
			// Asking again, with nothing new, adds no second event.
			ask(id)
			n := 0
			for _, k := range kinds(id) {
				if k == "queried" {
					n++
				}
			}
			if n != 1 {
				t.Errorf("the same question was recorded %d times", n)
			}
		}
	}

	// A claim whose check falls due is asked by the sweep without anyone calling.
	id := send("M-SWEEP-1")
	if _, err := st.Pool.Exec(context.Background(), `UPDATE claims SET status_check_at = now() WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := testQueue.Insert(context.Background(), queue.StatusSweepArgs{}, nil); err != nil {
		t.Fatal(err)
	}
	var state any
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		_, c := e.do(t, "GET", "/v1/claims/"+id, nil)
		if state = c["state"]; state == "approved" {
			break
		}
	}
	if state != "approved" {
		t.Fatalf("the sweep did not follow the claim: %v", state)
	}
	var next *time.Time
	if err := st.Pool.QueryRow(context.Background(), `SELECT status_check_at FROM claims WHERE id = $1`, id).Scan(&next); err != nil || next == nil || time.Until(*next) < time.Hour {
		t.Fatalf("an approved claim's next check: %v %v", next, err)
	}
}
