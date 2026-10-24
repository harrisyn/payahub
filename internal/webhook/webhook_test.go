package webhook

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The example from the Standard Webhooks specification.
func TestSignMatchesStandardWebhooks(t *testing.T) {
	sig, err := Sign("whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw", "msg_p5jXN8AQM9LWM0D4loKWxJek", 1614265330, []byte(`{"test": 2432232314}`))
	if err != nil || sig != "v1,g0hM9SsE+OTPJTGt/tmIKtSyZlE3uFJELVlNIOLJ1OE=" {
		t.Fatalf("%s %v", sig, err)
	}
}

func TestCheckURL(t *testing.T) {
	for _, u := range []string{"https://hms.example.com/hooks", "https://203.0.113.9/x"} {
		if err := CheckURL(u, false); err != nil {
			t.Errorf("%s: %v", u, err)
		}
	}
	for _, u := range []string{"http://hms.example.com/hooks", "https://localhost/x", "https://127.0.0.1/x", "https://10.0.0.4/x",
		"https://169.254.169.254/latest", "https://[::1]/x", "https://user:pw@hms.example.com/", "ftp://hms.example.com", "https://db.internal/x", "not a url"} {
		if err := CheckURL(u, false); !errors.Is(err, ErrUnsafeURL) {
			t.Errorf("%s accepted", u)
		}
	}
	if err := CheckURL("http://localhost:4000/hooks", true); err != nil {
		t.Errorf("development: %v", err)
	}
}

// A name that resolves to an internal address is refused when dialled.
func TestClientRefusesInternalAddresses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()
	req, _ := http.NewRequestWithContext(context.Background(), "POST", srv.URL, nil)
	if _, err := Client(false).Do(req); !errors.Is(err, ErrUnsafeURL) {
		t.Fatalf("dialled a loopback address: %v", err)
	}
	if res, err := Client(true).Do(req); err != nil || res.StatusCode != 200 {
		t.Fatalf("development client: %v", err)
	}
}

func TestBackoffSpansDays(t *testing.T) {
	var total float64
	for n := 1; n < MaxAttempts; n++ {
		total += backoff(n).Hours()
	}
	if total < 48 || total > 96 {
		t.Fatalf("retries span %.1f hours", total)
	}
}
