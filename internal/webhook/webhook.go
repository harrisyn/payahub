// Package webhook tells an organisation's own system about its claims as
// they move: received, checked, sent, acknowledged, rejected.
//
// Every claim event is written with a delivery row for each endpoint in the
// same transaction (see store.addEvent), so nothing is lost between saving
// and sending. The dispatcher posts each one, signed in the Standard
// Webhooks format, and retries with backoff for about two and a half days. Delivery
// is at least once and not ordered: receivers use the event id to drop
// repeats and created_at to order.
package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/harrisyn/payahub/internal/store"
	"github.com/harrisyn/payahub/internal/vault"
)

// SecretPrefix marks a signing secret, as Standard Webhooks does.
const SecretPrefix = "whsec_"

// NewSecret makes a signing secret: shown once to the organisation, sealed
// in the database.
func NewSecret() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return SecretPrefix + base64.StdEncoding.EncodeToString(b)
}

// Sign is the webhook-signature header value for a message.
func Sign(secret, id string, ts int64, body []byte) (string, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, SecretPrefix))
	if err != nil {
		return "", fmt.Errorf("webhook: bad secret: %w", err)
	}
	m := hmac.New(sha256.New, key)
	fmt.Fprintf(m, "%s.%d.", id, ts)
	m.Write(body)
	return "v1," + base64.StdEncoding.EncodeToString(m.Sum(nil)), nil
}

// Aad binds a sealed secret to its endpoint.
func Aad(endpointID string) string { return "webhook/" + endpointID }

// ErrUnsafeURL is a URL the dispatcher will not post to.
var ErrUnsafeURL = errors.New("webhook: the address must be a public https URL")

// CheckURL accepts a public https URL (or, when allowPrivate is set, any
// http or https URL, for development).
func CheckURL(raw string, allowPrivate bool) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || len(raw) > 2000 {
		return ErrUnsafeURL
	}
	if allowPrivate {
		if u.Scheme != "https" && u.Scheme != "http" {
			return ErrUnsafeURL
		}
		return nil
	}
	if u.Scheme != "https" {
		return ErrUnsafeURL
	}
	if ip, err := netip.ParseAddr(u.Hostname()); err == nil && !public(ip) {
		return ErrUnsafeURL
	}
	if h := strings.ToLower(u.Hostname()); h == "localhost" || strings.HasSuffix(h, ".localhost") || strings.HasSuffix(h, ".internal") {
		return ErrUnsafeURL
	}
	return nil
}

func public(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() &&
		!netip.MustParsePrefix("100.64.0.0/10").Contains(ip) // carrier-grade NAT
}

// Client posts to customers' URLs. Unless allowPrivate, it refuses to connect
// to any address that is not public, checked on the address actually
// dialled, so a name that resolves to an internal address is refused too.
func Client(allowPrivate bool) *http.Client {
	d := &net.Dialer{Timeout: 5 * time.Second}
	if !allowPrivate {
		d.Control = func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip, err := netip.ParseAddr(host)
			if err != nil || !public(ip) {
				return ErrUnsafeURL
			}
			return nil
		}
	}
	return &http.Client{
		Timeout:   10 * time.Second,
		Transport: &http.Transport{DialContext: d.DialContext, MaxIdleConnsPerHost: 4, IdleConnTimeout: 60 * time.Second, TLSHandshakeTimeout: 5 * time.Second},
		// A redirect could lead anywhere; a receiver answers where it is asked.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// Dispatcher sends due deliveries.
type Dispatcher struct {
	Store  *store.Store
	Vault  *vault.Vault
	HTTP   *http.Client
	Logger *slog.Logger
	// Every is how often it looks for due deliveries; Batch how many it takes.
	Every time.Duration
	Batch int
}

// MaxAttempts spans about two and a half days of backoff.
const MaxAttempts = 18

// backoff is the wait after a failed attempt n (1-based): 5s, 30s, 2m, 5m,
// 15m, 30m, 1h, 2h, then every 6h.
func backoff(n int) time.Duration {
	steps := []time.Duration{5 * time.Second, 30 * time.Second, 2 * time.Minute, 5 * time.Minute, 15 * time.Minute,
		30 * time.Minute, time.Hour, 2 * time.Hour}
	if n-1 < len(steps) {
		return steps[n-1]
	}
	return 6 * time.Hour
}

const lease = 60 * time.Second

// Run sends deliveries until ctx ends.
func (d *Dispatcher) Run(ctx context.Context) {
	every, batch := d.Every, d.Batch
	if every == 0 {
		every = 300 * time.Millisecond
	}
	if batch == 0 {
		batch = 100
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		n, err := d.Once(ctx, batch)
		if err != nil && ctx.Err() == nil {
			d.Logger.Warn("webhook deliveries", "err", err)
		}
		if n == batch {
			continue // more are due
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Once sends one batch of due deliveries, 16 at a time.
func (d *Dispatcher) Once(ctx context.Context, batch int) (int, error) {
	due, err := d.Store.LeaseDeliveries(ctx, batch, lease)
	if err != nil || len(due) == 0 {
		return 0, err
	}
	sem := make(chan struct{}, 16)
	var wg sync.WaitGroup
	for _, dl := range due {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			d.send(ctx, dl)
		}()
	}
	wg.Wait()
	return len(due), nil
}

func (d *Dispatcher) send(ctx context.Context, dl store.Delivery) {
	status, err := d.post(ctx, dl)
	ok := err == nil && status >= 200 && status < 300
	var next time.Time
	msg := ""
	if !ok {
		if err != nil {
			msg = err.Error()
		} else {
			msg = "the endpoint answered HTTP " + strconv.Itoa(status)
		}
		if dl.Attempts < MaxAttempts {
			next = time.Now().Add(backoff(dl.Attempts))
		}
	}
	// The result is recorded even if the dispatcher is stopping.
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := d.Store.DeliveryDone(rctx, dl.ID, status, msg, ok, next); err != nil {
		d.Logger.Error("recording a webhook delivery", "id", dl.ID, "err", err)
	}
}

func (d *Dispatcher) post(ctx context.Context, dl store.Delivery) (int, error) {
	opened, err := d.Vault.Open(dl.Sealed, Aad(dl.Endpoint))
	if err != nil {
		return 0, errors.New("the signing secret could not be opened")
	}
	var head struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(dl.Payload, &head)
	ts := time.Now().Unix()
	sig, err := Sign(opened["secret"], head.ID, ts, dl.Payload)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, dl.URL, bytes.NewReader(dl.Payload))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Payahub-Webhooks/1")
	req.Header.Set("webhook-id", head.ID)
	req.Header.Set("webhook-timestamp", strconv.FormatInt(ts, 10))
	req.Header.Set("webhook-signature", sig)
	res, err := d.HTTP.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) && errors.Is(ue.Err, ErrUnsafeURL) {
			return 0, ErrUnsafeURL
		}
		return 0, errors.New("could not reach the endpoint: " + shortErr(err))
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 64<<10))
	res.Body.Close()
	return res.StatusCode, nil
}

func shortErr(err error) string {
	s := err.Error()
	if i := strings.LastIndex(s, ": "); i >= 0 {
		s = s[i+2:]
	}
	return s
}
