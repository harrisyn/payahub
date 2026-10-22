// Package httpx is the HTTP layer connectors share: one tuned client, JSON
// and form helpers with bounded bodies, transport failures translated to the
// connector taxonomy, and a sign-in token cache shared by every connector
// instance in the process.
package httpx

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/harrisyn/payahub/internal/connector"
)

// maxBody bounds what we read from a payer: a full tariff list fits, a
// runaway response does not take the worker down.
const maxBody = 32 << 20

// DefaultClient is shared by every connector so connections to a payer are
// reused across claims. Each call is also bounded by its context.
var DefaultClient = &http.Client{
	Timeout: 45 * time.Second,
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          256,
		MaxIdleConnsPerHost:   32,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 40 * time.Second,
	},
}

// StatusError is a payer reply outside 2xx. Connectors translate it, since
// what a 404 or a 400 means differs by payer and by call.
type StatusError struct {
	Method string
	URL    string
	Status int
	Body   string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("%s %s: HTTP %d: %s", e.Method, e.URL, e.Status, truncate(e.Body, 300))
}

// Request is one call to a payer.
type Request struct {
	Method string
	URL    string
	Header http.Header
	Body   io.Reader
}

// Do sends r and returns the body of a 2xx reply. Transport failures come
// back as connector errors (Timeout, Connection); other statuses as
// *StatusError for the caller to translate.
func Do(ctx context.Context, client *http.Client, r Request) ([]byte, error) {
	if client == nil {
		client = DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, r.Method, r.URL, r.Body)
	if err != nil {
		return nil, connector.Errorf(connector.Validation, "building request: %v", err)
	}
	for k, vs := range r.Header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, transportError(ctx, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, transportError(ctx, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return body, &StatusError{Method: r.Method, URL: redact(r.URL), Status: resp.StatusCode, Body: string(body)}
	}
	return body, nil
}

func transportError(ctx context.Context, err error) error {
	var ne net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) ||
		(errors.As(err, &ne) && ne.Timeout()) {
		return &connector.Error{Code: connector.Timeout, Message: err.Error(),
			UserMessage: "The payer did not answer in time. We will try again."}
	}
	return &connector.Error{Code: connector.Connection, Message: err.Error(),
		UserMessage: "The payer could not be reached. We will try again."}
}

// JSON sends v as a JSON body (nil for none) and decodes a JSON reply into
// out (nil to ignore it). It returns the raw reply as well.
func JSON(ctx context.Context, client *http.Client, method, u string, header http.Header, v, out any) ([]byte, error) {
	var body io.Reader
	h := header.Clone()
	if h == nil {
		h = http.Header{}
	}
	if v != nil {
		b, err := json.Marshal(v)
		if err != nil {
			return nil, connector.Errorf(connector.Validation, "encoding request: %v", err)
		}
		body = bytes.NewReader(b)
		h.Set("Content-Type", "application/json")
	}
	raw, err := Do(ctx, client, Request{Method: method, URL: u, Header: h, Body: body})
	if err != nil {
		return raw, err
	}
	if out != nil && len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return raw, &connector.Error{Code: connector.Unknown, Message: fmt.Sprintf("payer reply is not the JSON we expect: %v", err),
				UserMessage: "The payer sent a reply we could not read.", PayerMessage: truncate(string(raw), 500)}
		}
	}
	return raw, nil
}

// Form sends values as application/x-www-form-urlencoded and decodes JSON.
func Form(ctx context.Context, client *http.Client, method, u string, header http.Header, values url.Values, out any) ([]byte, error) {
	h := header.Clone()
	if h == nil {
		h = http.Header{}
	}
	h.Set("Content-Type", "application/x-www-form-urlencoded")
	raw, err := Do(ctx, client, Request{Method: method, URL: u, Header: h, Body: strings.NewReader(values.Encode())})
	if err != nil {
		return raw, err
	}
	if out != nil && len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return raw, &connector.Error{Code: connector.Unknown, Message: fmt.Sprintf("payer reply is not the JSON we expect: %v", err),
				UserMessage: "The payer sent a reply we could not read.", PayerMessage: truncate(string(raw), 500)}
		}
	}
	return raw, nil
}

// Translate gives a *StatusError the default meaning: 401/403 are bad
// credentials, 408/429/5xx are transient (the request may or may not have
// landed), anything else is the payer refusing the request. Connectors call
// it after handling the statuses that mean something specific for them.
func Translate(name string, err error) error {
	var se *StatusError
	if !errors.As(err, &se) {
		return err
	}
	payer := truncate(se.Body, 500)
	switch {
	case se.Status == http.StatusUnauthorized || se.Status == http.StatusForbidden:
		return &connector.Error{Code: connector.InvalidCredentials, Message: se.Error(), PayerMessage: payer, Cause: se,
			UserMessage: name + " did not accept the account details. Check them in the console."}
	case se.Status == http.StatusRequestTimeout || se.Status == http.StatusTooManyRequests || se.Status >= 500:
		return &connector.Error{Code: connector.Unknown, Message: se.Error(), PayerMessage: payer, Cause: se,
			UserMessage: name + " had a problem on its side. We will try again."}
	}
	return &connector.Error{Code: connector.PayerRefused, Message: se.Error(), PayerMessage: payer, Cause: se,
		UserMessage: name + " refused the request: " + truncate(firstLine(se.Body), 200)}
}

// Status reports the HTTP status of a payer error, or 0.
func Status(err error) int {
	var se *StatusError
	if errors.As(err, &se) {
		return se.Status
	}
	return 0
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// redact drops the query string, where some payers put session ids.
func redact(u string) string {
	if i := strings.IndexByte(u, '?'); i >= 0 {
		return u[:i]
	}
	return u
}

// Token is a payer session: a bearer token or a connection id, with extra
// values the payer returned at sign-in (a user id, a list of insurers).
type Token struct {
	Value   string
	Expires time.Time
	Extra   map[string]string
}

// Tokens caches sign-ins across connector instances, so a worker signs in
// once per account rather than once per claim. Keys are hashes: the cache
// never holds a password.
type Tokens struct {
	mu sync.Mutex
	m  map[string]Token
	// inflight makes concurrent claims for one account share one sign-in.
	inflight map[string]*sync.Mutex
}

var SharedTokens = &Tokens{m: map[string]Token{}, inflight: map[string]*sync.Mutex{}}

// Key derives a cache key from the parts that identify an account.
func Key(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Get returns a valid token for key, signing in with fn when there is none
// or it expires within a minute.
func (t *Tokens) Get(ctx context.Context, key string, fn func(context.Context) (Token, error)) (Token, error) {
	t.mu.Lock()
	if tok, ok := t.m[key]; ok && time.Until(tok.Expires) > time.Minute {
		t.mu.Unlock()
		return tok, nil
	}
	lock, ok := t.inflight[key]
	if !ok {
		lock = &sync.Mutex{}
		t.inflight[key] = lock
	}
	t.mu.Unlock()

	lock.Lock()
	defer lock.Unlock()
	t.mu.Lock()
	if tok, ok := t.m[key]; ok && time.Until(tok.Expires) > time.Minute {
		t.mu.Unlock()
		return tok, nil
	}
	t.mu.Unlock()

	tok, err := fn(ctx)
	if err != nil {
		return Token{}, err
	}
	t.mu.Lock()
	t.m[key] = tok
	t.mu.Unlock()
	return tok, nil
}

// Drop forgets a token the payer stopped accepting.
func (t *Tokens) Drop(key string) {
	t.mu.Lock()
	delete(t.m, key)
	t.mu.Unlock()
}
