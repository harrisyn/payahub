package api_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestThroughput sends PAYAHUB_LOAD claims along the whole path a clinic's
// system uses: lines in the clinic's own codes resolved through its mappings
// to the payer's codes, checks, a durable queue, the payer, and a signed
// webhook for every step. One in ten asks for the payer's answer in the same
// response. It reports the rate and the time each request took.
func TestThroughput(t *testing.T) {
	n, _ := strconv.Atoi(os.Getenv("PAYAHUB_LOAD"))
	if n == 0 {
		t.Skip("set PAYAHUB_LOAD to run")
	}
	e := setup(t)
	http.DefaultTransport.(*http.Transport).MaxIdleConnsPerHost = 200
	ctx := context.Background()

	var heard atomic.Int64
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		heard.Add(1)
	}))
	defer hook.Close()
	if s, out := e.do(t, "POST", "/v1/webhook-endpoints", map[string]any{"url": hook.URL + "/hms"}); s != 201 {
		t.Fatalf("endpoint: %d %v", s, out)
	}
	own := [][3]string{{"OPD1", "OPD consultation", "SRV-0001"}, {"FBC7", "Full blood count", "LAB-0001"},
		{"A1C", "HbA1c", "LAB-0010"}, {"CXR", "Chest X-ray", "IMG-0001"}, {"PCM500", "PCM 500mg", "MED-0001"}}
	var mappings []any
	for _, o := range own {
		mappings = append(mappings, map[string]any{"local_code": o[0], "local_name": o[1], "item_id": o[2]})
	}
	if s, out := e.do(t, "PUT", "/v1/item-mappings", map[string]any{"mappings": mappings}); s != 200 {
		t.Fatalf("mappings: %d %v", s, out)
	}
	body := func(i int) map[string]any {
		b := claimBody(fmt.Sprintf("M-LOAD-%d", i))
		var lines []any
		for j := range 3 {
			o := own[(i+j)%len(own)]
			lines = append(lines, map[string]any{"description": o[1], "unit_price": 1000 + 500*j, "quantity": 1 + j%2,
				"code": map[string]any{"system": "local", "value": o[0]}})
		}
		b["lines"] = lines
		b["encounter"] = map[string]any{"start_date": time.Now().AddDate(0, 0, -(i % 30)).Format("2006-01-02")}
		return b
	}

	ids := make([]string, n)
	took := make([]time.Duration, n)
	waited := make([]bool, n)
	var answered atomic.Int64
	start := time.Now()
	var wg sync.WaitGroup
	sem := make(chan struct{}, 100)
	for i := range n {
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			t0 := time.Now()
			path := "/v1/claims?submit=true"
			if waited[i] = i%10 == 0; waited[i] {
				path += "&wait=30"
			}
			s, out := e.do(t, "POST", path, body(i), "Idempotency-Key", fmt.Sprintf("load-%d", i))
			took[i] = time.Since(t0)
			switch {
			case s == 200 && waited[i]:
				answered.Add(1)
			case s == 201 || s == 202:
			default:
				t.Errorf("create %d: %d %v", i, s, out)
				return
			}
			if c, ok := out["claim"].(map[string]any); ok {
				ids[i], _ = c["id"].(string)
			} else {
				ids[i], _ = out["id"].(string)
			}
		})
	}
	wg.Wait()
	accepted := time.Since(start)
	if t.Failed() {
		return
	}

	for {
		var pending int
		_ = st.Pool.QueryRow(ctx, `SELECT count(*) FROM claims WHERE id = ANY($1) AND state <> 'acknowledged'`, ids).Scan(&pending)
		if pending == 0 {
			break
		}
		if time.Since(start) > 10*time.Minute {
			t.Fatalf("%d claims still pending", pending)
		}
		time.Sleep(100 * time.Millisecond)
	}
	acked := time.Since(start)

	var events int64
	_ = st.Pool.QueryRow(ctx, `SELECT count(*) FROM claim_events WHERE claim_id = ANY($1)`, ids).Scan(&events)
	for heard.Load() < events {
		if time.Since(start) > 15*time.Minute {
			t.Fatalf("webhooks: %d of %d heard", heard.Load(), events)
		}
		time.Sleep(100 * time.Millisecond)
	}
	hooked := time.Since(start)

	var lines int
	_ = st.Pool.QueryRow(ctx, `
		SELECT count(*) FROM claims c CROSS JOIN LATERAL jsonb_array_elements(c.body->'lines') l
		WHERE c.id = ANY($1) AND l->'mapping'->'target'->>'value' IS NOT NULL`, ids).Scan(&lines)
	if lines != 3*n {
		t.Errorf("%d of %d lines carry the payer's code", lines, 3*n)
	}
	if want := int64((n + 9) / 10); answered.Load() != want {
		t.Errorf("%d of %d real-time requests had the payer's answer", answered.Load(), want)
	}

	// These claims are not followed up afterwards: the shared test database
	// would otherwise ask the sandbox about them for the next 120 days.
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(context.Background(), `UPDATE claims SET status_check_at = NULL WHERE id = ANY($1)`, ids)
	})

	rate := func(d time.Duration) float64 { return float64(n) / d.Seconds() }
	t.Logf("%d claims, 3 lines each, 100 at a time", n)
	t.Logf("accepted in %v (%.0f/s); acknowledged by the payer in %v (%.0f/s); %d webhooks heard in %v",
		accepted.Round(time.Millisecond), rate(accepted), acked.Round(time.Millisecond), rate(acked), events, hooked.Round(time.Millisecond))
	t.Logf("create and submit: %s", percentiles(took, waited, false))
	t.Logf("with the payer's answer (wait): %s", percentiles(took, waited, true))
}

func percentiles(took []time.Duration, waited []bool, want bool) string {
	var d []time.Duration
	for i, x := range took {
		if waited[i] == want {
			d = append(d, x)
		}
	}
	if len(d) == 0 {
		return "none"
	}
	slices.Sort(d)
	at := func(p float64) time.Duration { return d[min(len(d)-1, int(p*float64(len(d))))].Round(time.Millisecond) }
	return fmt.Sprintf("p50 %v, p95 %v, p99 %v, max %v", at(0.50), at(0.95), at(0.99), d[len(d)-1].Round(time.Millisecond))
}
