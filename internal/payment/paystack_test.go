package payment

import (
	"context"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func fakePaystack(t *testing.T) (*Paystack, map[string]any) {
	t.Helper()
	seen := map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk_test_x" {
			w.WriteHeader(401)
			io.WriteString(w, `{"status":false,"message":"Invalid key"}`)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		seen[r.Method+" "+r.URL.Path] = body
		switch r.Method + " " + r.URL.Path {
		case "POST /transaction/initialize":
			io.WriteString(w, `{"status":true,"message":"ok","data":{"authorization_url":"https://checkout.paystack.com/abc","reference":"`+body["reference"].(string)+`"}}`)
		case "GET /transaction/verify/fnd-0193":
			io.WriteString(w, `{"status":true,"message":"ok","data":{"id":991,"status":"success","amount":50000,"fees":975,"currency":"GHS","paid_at":"2026-10-23T10:00:00.000Z","gateway_response":"Approved"}}`)
		case "GET /transaction/verify/fnd-missing":
			w.WriteHeader(400)
			io.WriteString(w, `{"status":false,"message":"Transaction reference not found"}`)
		case "POST /transfer":
			io.WriteString(w, `{"status":true,"message":"queued","data":{"status":"otp","transfer_code":"TRF_1"}}`)
		default:
			w.WriteHeader(404)
			io.WriteString(w, `{"status":false,"message":"not found"}`)
		}
	}))
	t.Cleanup(srv.Close)
	p := NewPaystack("sk_test_x")
	p.BaseURL = srv.URL
	return p, seen
}

func TestPaystackCollects(t *testing.T) {
	p, seen := fakePaystack(t)
	ctx := context.Background()
	s, err := p.StartCheckout(ctx, Checkout{Reference: "fnd_0193", Amount: 50000, Currency: "GHS", Email: "a@b.test", CallbackURL: "https://x/cb"})
	if err != nil || s.URL != "https://checkout.paystack.com/abc" {
		t.Fatalf("checkout: %v %+v", err, s)
	}
	if b := seen["POST /transaction/initialize"].(map[string]any); b["reference"] != "fnd-0193" || b["amount"] != 50000.0 {
		t.Fatalf("sent: %v", b)
	}
	c, err := p.VerifyCollection(ctx, "fnd_0193")
	if err != nil || c.Status != Succeeded || c.Amount != 50000 || c.Fee != 975 || c.Currency != "GHS" || c.PaidAt.IsZero() {
		t.Fatalf("verify: %v %+v", err, c)
	}
	if _, err := p.VerifyCollection(ctx, "fnd_missing"); err != ErrNotFound {
		t.Fatalf("unknown reference: %v", err)
	}
	r, err := p.SendPayout(ctx, Payout{Reference: "wdr_1", Amount: 100, Currency: "GHS", Recipient: "RCP_1"})
	if err != nil || r.Status != Pending || r.Message == "" {
		t.Fatalf("an OTP transfer is pending with a reason: %v %+v", err, r)
	}
}

func TestPaystackWebhookSignature(t *testing.T) {
	p := NewPaystack("sk_test_x")
	body := []byte(`{"event":"charge.success","data":{"reference":"fnd-0193-aa"}}`)
	mac := hmac.New(sha512.New, []byte("sk_test_x"))
	mac.Write(body)
	h := http.Header{}
	h.Set("X-Paystack-Signature", hex.EncodeToString(mac.Sum(nil)))
	e, ok, err := p.ParseWebhook(h, body)
	if err != nil || !ok || e.Kind != "collection" || e.Reference != "fnd_0193-aa" {
		t.Fatalf("%v %v %+v", err, ok, e)
	}
	h.Set("X-Paystack-Signature", hex.EncodeToString(make([]byte, 64)))
	if _, _, err := p.ParseWebhook(h, body); err != ErrBadSignature {
		t.Fatalf("forged signature accepted: %v", err)
	}
	h.Del("X-Paystack-Signature")
	if _, _, err := p.ParseWebhook(h, body); err != ErrBadSignature {
		t.Fatalf("missing signature accepted: %v", err)
	}
}
