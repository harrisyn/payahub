package store

import (
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/harrisyn/payahub/internal/claim"
	"github.com/harrisyn/payahub/internal/onboarding"
)

var (
	ErrEmailTaken = errors.New("an account with that email already exists")
	ErrBadLogin   = errors.New("email or password is wrong")
)

const (
	sessionTTL     = 30 * 24 * time.Hour
	pbkdf2Rounds   = 600_000 // OWASP 2023 guidance for PBKDF2-HMAC-SHA256
	sessionPrefix  = "ses_"
	sandboxKeyName = "Sandbox key"
)

func randomToken(prefix string) string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return prefix + base64.RawURLEncoding.EncodeToString(b)
}

// NewAPIKey returns a raw API key for an environment: sk_sandbox_... or sk_live_...
func NewAPIKey(env string) string { return randomToken("sk_" + env + "_") }

func hashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	sum, err := pbkdf2.Key(sha256.New, password, salt, pbkdf2Rounds, 32)
	if err != nil {
		return "", err
	}
	enc := base64.RawStdEncoding
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", pbkdf2Rounds, enc.EncodeToString(salt), enc.EncodeToString(sum)), nil
}

func checkPassword(stored, password string) bool {
	parts := strings.Split(stored, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return false
	}
	rounds, err := strconv.Atoi(parts[1])
	enc := base64.RawStdEncoding
	salt, err1 := enc.DecodeString(parts[2])
	want, err2 := enc.DecodeString(parts[3])
	if err != nil || err1 != nil || err2 != nil {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, rounds, len(want))
	return err == nil && subtle.ConstantTimeCompare(got, want) == 1
}

// dummyHash is checked when no user matches, so a login for an unknown email
// takes as long as one for a known email.
var dummyHash, _ = hashPassword("payahub-timing-equaliser")

type Signup struct {
	Email        string `json:"email"`
	Password     string `json:"password"`
	Name         string `json:"name"`
	Organization string `json:"organization"`
	Country      string `json:"country"`
}

type Account struct {
	OrganizationID string    `json:"organization_id"`
	UserID         string    `json:"user_id"`
	SessionToken   string    `json:"session_token"`
	SandboxKey     string    `json:"sandbox_key"`
	TrialEndsAt    time.Time `json:"trial_ends_at"`
}

// CreateAccount creates the organisation, its first user, a session and a
// sandbox API key in one transaction, and starts the trial.
func (s *Store) CreateAccount(ctx context.Context, in Signup) (*Account, error) {
	hash, err := hashPassword(in.Password)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	a := &Account{
		OrganizationID: NewID("org"),
		UserID:         NewID("usr"),
		SessionToken:   randomToken(sessionPrefix),
		SandboxKey:     NewAPIKey("sandbox"),
		TrialEndsAt:    onboarding.TrialEnds(now),
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		INSERT INTO organizations (id, name, country, plan, trial_ends_at) VALUES ($1, $2, $3, 'trial', $4)`,
		a.OrganizationID, in.Organization, in.Country, a.TrialEndsAt); err != nil {
		return nil, err
	}
	tag, err := tx.Exec(ctx, `
		INSERT INTO users (id, organization_id, email, name, password_hash) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT DO NOTHING`, a.UserID, a.OrganizationID, in.Email, in.Name, hash)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrEmailTaken
	}
	if err := insertSession(ctx, tx, a.SessionToken, a.UserID, a.OrganizationID); err != nil {
		return nil, err
	}
	if err := insertKey(ctx, tx, a.OrganizationID, "sandbox", sandboxKeyName, a.SandboxKey); err != nil {
		return nil, err
	}
	return a, tx.Commit(ctx)
}

func insertSession(ctx context.Context, q Querier, token, userID, orgID string) error {
	_, err := q.Exec(ctx, `
		INSERT INTO sessions (token_sha256, user_id, organization_id, expires_at) VALUES ($1, $2, $3, $4)`,
		hashKey(token), userID, orgID, time.Now().Add(sessionTTL))
	return err
}

func insertKey(ctx context.Context, q Querier, orgID, env, name, raw string) error {
	_, err := q.Exec(ctx, `
		INSERT INTO api_keys (id, organization_id, environment, key_sha256, prefix, name)
		VALUES ($1, $2, $3, $4, $5, $6)`, NewID("key"), orgID, env, hashKey(raw), raw[:min(len(raw), 16)], name)
	return err
}

// Login checks a password and opens a session.
func (s *Store) Login(ctx context.Context, email, password string) (string, error) {
	var userID, orgID, hash string
	err := s.Pool.QueryRow(ctx, `SELECT id, organization_id, password_hash FROM users WHERE lower(email) = lower($1)`,
		email).Scan(&userID, &orgID, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		checkPassword(dummyHash, password)
		return "", ErrBadLogin
	}
	if err != nil {
		return "", err
	}
	if !checkPassword(hash, password) {
		return "", ErrBadLogin
	}
	token := randomToken(sessionPrefix)
	return token, insertSession(ctx, s.Pool, token, userID, orgID)
}

func (s *Store) Logout(ctx context.Context, token string) error {
	h := hashKey(token)
	s.keys.Delete(string(h))
	_, err := s.Pool.Exec(ctx, `DELETE FROM sessions WHERE token_sha256 = $1`, h)
	return err
}

func (s *Store) authenticateSession(ctx context.Context, h []byte) (Org, error) {
	o := Org{Environment: "sandbox", Session: true}
	err := s.Pool.QueryRow(ctx, `
		SELECT s.organization_id, s.user_id, u.operator
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_sha256 = $1 AND s.expires_at > now()`, h).
		Scan(&o.ID, &o.UserID, &o.Operator)
	if errors.Is(err, pgx.ErrNoRows) {
		return Org{}, ErrUnauthenticated
	}
	return o, err
}

// Organization is the console's view of an organisation.
type Organization struct {
	ID          string              `json:"id"`
	Name        string              `json:"name"`
	Country     string              `json:"country"`
	Plan        onboarding.Plan     `json:"plan"`
	TrialEndsAt *time.Time          `json:"trial_ends_at"`
	Profile     *onboarding.Profile `json:"profile"`
	User        struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Email string `json:"email"`
	} `json:"user"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Store) Organization(ctx context.Context, orgID, userID string) (*Organization, error) {
	var o Organization
	var profile []byte
	err := s.Pool.QueryRow(ctx, `
		SELECT o.id, o.name, o.country, o.plan, o.trial_ends_at, o.profile, o.created_at,
		       coalesce(u.id, ''), coalesce(u.name, ''), coalesce(u.email, '')
		FROM organizations o LEFT JOIN users u ON u.id = $2 AND u.organization_id = o.id
		WHERE o.id = $1`, orgID, userID).
		Scan(&o.ID, &o.Name, &o.Country, &o.Plan, &o.TrialEndsAt, &profile, &o.CreatedAt,
			&o.User.ID, &o.User.Name, &o.User.Email)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if profile != nil {
		o.Profile = &onboarding.Profile{}
		if err := json.Unmarshal(profile, o.Profile); err != nil {
			return nil, err
		}
	}
	return &o, nil
}

func (s *Store) SaveProfile(ctx context.Context, orgID string, p onboarding.Profile) error {
	body, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = s.Pool.Exec(ctx, `UPDATE organizations SET profile = $2 WHERE id = $1`, orgID, body)
	return err
}

func (s *Store) SetPlan(ctx context.Context, orgID string, plan onboarding.Plan) error {
	_, err := s.Pool.Exec(ctx, `UPDATE organizations SET plan = $2 WHERE id = $1`, orgID, plan)
	return err
}

// Usage counts claims this calendar month (UTC). Billable means the payer
// acknowledged, approved or paid it: the one event every plan prices.
type Usage struct {
	Month          string `json:"month"`
	BillableLive   int    `json:"billable_live"`
	SandboxClaims  int    `json:"sandbox_claims"`
	LiveClaims     int    `json:"live_claims"`
	TrialLiveLimit int    `json:"trial_live_limit"`
}

func (s *Store) Usage(ctx context.Context, orgID string) (Usage, error) {
	now := time.Now().UTC()
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	u := Usage{Month: start.Format("2006-01"), TrialLiveLimit: onboarding.TrialLiveClaims}
	err := s.Pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE environment = 'live' AND state = ANY($3)),
		       count(*) FILTER (WHERE environment = 'sandbox'),
		       count(*) FILTER (WHERE environment = 'live')
		FROM claims WHERE organization_id = $1 AND created_at >= $2`,
		orgID, start, billableStates()).Scan(&u.BillableLive, &u.SandboxClaims, &u.LiveClaims)
	return u, err
}

func billableStates() []string {
	var out []string
	for _, st := range []claim.State{claim.Acknowledged, claim.Approved, claim.Paid} {
		if st.Billable() {
			out = append(out, string(st))
		}
	}
	return out
}

type APIKey struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Environment string     `json:"environment"`
	Prefix      string     `json:"prefix"`
	CreatedAt   time.Time  `json:"created_at"`
	RevokedAt   *time.Time `json:"revoked_at"`
	// Key is the raw key, returned once when the key is created.
	Key string `json:"key,omitempty"`
}

func (s *Store) ListAPIKeys(ctx context.Context, orgID string) ([]APIKey, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, name, environment, prefix, created_at, revoked_at FROM api_keys
		WHERE organization_id = $1 ORDER BY created_at`, orgID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (APIKey, error) {
		var k APIKey
		return k, r.Scan(&k.ID, &k.Name, &k.Environment, &k.Prefix, &k.CreatedAt, &k.RevokedAt)
	})
}

func (s *Store) CreateAPIKey(ctx context.Context, orgID, env, name string) (*APIKey, error) {
	raw := NewAPIKey(env)
	if err := insertKey(ctx, s.Pool, orgID, env, name, raw); err != nil {
		return nil, err
	}
	var k APIKey
	err := s.Pool.QueryRow(ctx, `
		SELECT id, name, environment, prefix, created_at, revoked_at FROM api_keys WHERE key_sha256 = $1`,
		hashKey(raw)).Scan(&k.ID, &k.Name, &k.Environment, &k.Prefix, &k.CreatedAt, &k.RevokedAt)
	k.Key = raw
	return &k, err
}

// RevokeAPIKey revokes a key. The auth cache may honour it for up to keyTTL.
func (s *Store) RevokeAPIKey(ctx context.Context, orgID, id string) error {
	tag, err := s.Pool.Exec(ctx, `
		UPDATE api_keys SET revoked_at = coalesce(revoked_at, now()) WHERE id = $1 AND organization_id = $2`, id, orgID)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// PayerSetting is an organisation's use of one catalogue payer.
type PayerSetting struct {
	PayerID     string `json:"payer_id"`
	Environment string `json:"environment"`
	Enabled     bool   `json:"enabled"`
	// Account answers "do you have an account on this payer's platform?":
	// "have", "need", or empty when not asked yet.
	Account   string    `json:"account,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (s *Store) ListPayerSettings(ctx context.Context, orgID string) ([]PayerSetting, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT payer_id, environment, enabled, coalesce(account, ''), updated_at
		FROM payer_settings WHERE organization_id = $1 ORDER BY payer_id, environment`, orgID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (PayerSetting, error) {
		var p PayerSetting
		return p, r.Scan(&p.PayerID, &p.Environment, &p.Enabled, &p.Account, &p.UpdatedAt)
	})
}

// SavePayerSetting turns a payer on or off and, when account is not empty,
// records the answer about the platform account.
func (s *Store) SavePayerSetting(ctx context.Context, orgID, payerID, env string, enabled bool, account string) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO payer_settings (organization_id, payer_id, environment, enabled, account)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''))
		ON CONFLICT (organization_id, payer_id, environment)
		DO UPDATE SET enabled = EXCLUDED.enabled, account = coalesce(EXCLUDED.account, payer_settings.account),
		              updated_at = now()`, orgID, payerID, env, enabled, account)
	return err
}

// ConnectorAccount is an organisation's account on a platform (or on a
// standalone payer's own interface). Its credentials are sealed and never
// returned; Fields lists which are set.
type ConnectorAccount struct {
	Connector    string     `json:"connector"`
	Environment  string     `json:"environment"`
	Fields       []string   `json:"credential_fields"`
	CheckedAt    *time.Time `json:"checked_at"`
	CheckOK      *bool      `json:"check_ok"`
	CheckMessage string     `json:"check_message,omitempty"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

func (s *Store) ListConnectorAccounts(ctx context.Context, orgID string) ([]ConnectorAccount, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT connector, environment, credential_fields, checked_at, check_ok, coalesce(check_message, ''), updated_at
		FROM connector_accounts WHERE organization_id = $1 ORDER BY connector, environment`, orgID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (ConnectorAccount, error) {
		var a ConnectorAccount
		return a, r.Scan(&a.Connector, &a.Environment, &a.Fields, &a.CheckedAt, &a.CheckOK, &a.CheckMessage, &a.UpdatedAt)
	})
}

// SaveConnectorAccount replaces the sealed credentials and clears the last check.
func (s *Store) SaveConnectorAccount(ctx context.Context, orgID, connectorKey, env string, sealed []byte, fields []string) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO connector_accounts (organization_id, connector, environment, credentials_sealed, credential_fields)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (organization_id, connector, environment)
		DO UPDATE SET credentials_sealed = EXCLUDED.credentials_sealed, credential_fields = EXCLUDED.credential_fields,
		              checked_at = NULL, check_ok = NULL, check_message = NULL, updated_at = now()`,
		orgID, connectorKey, env, sealed, fields)
	return err
}

// SealedAccount returns the sealed credentials of an account (nil when none).
func SealedAccount(ctx context.Context, q Querier, orgID, connectorKey, env string) ([]byte, error) {
	var sealed []byte
	err := q.QueryRow(ctx, `
		SELECT credentials_sealed FROM connector_accounts
		WHERE organization_id = $1 AND connector = $2 AND environment = $3`, orgID, connectorKey, env).Scan(&sealed)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return sealed, err
}

func (s *Store) RecordAccountCheck(ctx context.Context, orgID, connectorKey, env string, ok bool, message string) error {
	_, err := s.Pool.Exec(ctx, `
		UPDATE connector_accounts SET checked_at = now(), check_ok = $4, check_message = $5
		WHERE organization_id = $1 AND connector = $2 AND environment = $3`, orgID, connectorKey, env, ok, message)
	return err
}

// ItemMapping is an organisation's own item code mapped to a standard item.
type ItemMapping struct {
	LocalCode   string    `json:"local_code"`
	LocalName   string    `json:"local_name"`
	ItemID      string    `json:"item_id"`
	ConfirmedBy string    `json:"confirmed_by"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (s *Store) ListItemMappings(ctx context.Context, orgID string) ([]ItemMapping, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT local_code, local_name, item_id, confirmed_by, updated_at
		FROM item_mappings WHERE organization_id = $1 ORDER BY local_code`, orgID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (ItemMapping, error) {
		var m ItemMapping
		return m, r.Scan(&m.LocalCode, &m.LocalName, &m.ItemID, &m.ConfirmedBy, &m.UpdatedAt)
	})
}

// SaveItemMappings upserts mappings in one transaction: all or none.
func (s *Store) SaveItemMappings(ctx context.Context, orgID string, ms []ItemMapping) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, m := range ms {
		if _, err := tx.Exec(ctx, `
			INSERT INTO item_mappings (organization_id, local_code, local_name, item_id, confirmed_by)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (organization_id, local_code)
			DO UPDATE SET local_name = EXCLUDED.local_name, item_id = EXCLUDED.item_id,
			              confirmed_by = EXCLUDED.confirmed_by, updated_at = now()`,
			orgID, m.LocalCode, m.LocalName, m.ItemID, m.ConfirmedBy); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) DeleteItemMapping(ctx context.Context, orgID, localCode string) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM item_mappings WHERE organization_id = $1 AND local_code = $2`, orgID, localCode)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// MappedItems returns the standard item for each of the given own codes that
// the organisation has mapped.
func MappedItems(ctx context.Context, q Querier, orgID string, codes []string) (map[string]string, error) {
	out := map[string]string{}
	if len(codes) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx, `
		SELECT local_code, item_id FROM item_mappings WHERE organization_id = $1 AND local_code = ANY($2)`, orgID, codes)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var code, item string
		if err := rows.Scan(&code, &item); err != nil {
			return nil, err
		}
		out[code] = item
	}
	return out, rows.Err()
}

// ClaimSummary is one row of the console's claims list.
type ClaimSummary struct {
	ID               string      `json:"id"`
	Environment      string      `json:"environment"`
	PayerID          string      `json:"payer_id"`
	State            claim.State `json:"state"`
	PayerClaimNumber string      `json:"payer_claim_number,omitempty"`
	MemberNumber     string      `json:"member_number"`
	Currency         string      `json:"currency"`
	Total            int64       `json:"total"`
	CreatedAt        time.Time   `json:"created_at"`
	UpdatedAt        time.Time   `json:"updated_at"`
}

// ListClaims returns claims newest first. before is the created_at of the last
// row of the previous page (cursor pagination).
func (s *Store) ListClaims(ctx context.Context, orgID, env, state string, before *time.Time, limit int) ([]ClaimSummary, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, environment, payer_id, state, coalesce(payer_claim_number, ''),
		       coalesce(body->'member'->>'member_number', ''), currency, total, created_at, updated_at
		FROM claims
		WHERE organization_id = $1 AND ($2 = '' OR environment = $2) AND ($3 = '' OR state = $3)
		  AND ($4::timestamptz IS NULL OR created_at < $4)
		ORDER BY created_at DESC LIMIT $5`, orgID, env, state, before, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (ClaimSummary, error) {
		var c ClaimSummary
		return c, r.Scan(&c.ID, &c.Environment, &c.PayerID, &c.State, &c.PayerClaimNumber, &c.MemberNumber,
			&c.Currency, &c.Total, &c.CreatedAt, &c.UpdatedAt)
	})
}

// TrialAllowsLive reports whether an organisation may send one more live
// claim: any paid plan may; a trial may until it ends or hits its cap.
func TrialAllowsLive(ctx context.Context, q Querier, orgID string) (bool, error) {
	var plan string
	var ends *time.Time
	var sent int
	err := q.QueryRow(ctx, `
		SELECT o.plan, o.trial_ends_at,
		       (SELECT count(*) FROM claims c WHERE c.organization_id = o.id AND c.environment = 'live'
		        AND c.state NOT IN ('draft', 'mapped', 'validated', 'needs_review', 'ready'))
		FROM organizations o WHERE o.id = $1`, orgID).Scan(&plan, &ends, &sent)
	if err != nil {
		return false, err
	}
	if plan != string(onboarding.PlanTrial) {
		return true, nil
	}
	return ends != nil && time.Now().Before(*ends) && sent < onboarding.TrialLiveClaims, nil
}

// HasAcknowledgedClaim reports whether any claim of the organisation in env
// reached the payer: the onboarding's activation moment.
func (s *Store) HasAcknowledgedClaim(ctx context.Context, orgID, env string) (bool, error) {
	var ok bool
	err := s.Pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM claims WHERE organization_id = $1 AND environment = $2 AND state = ANY($3))`,
		orgID, env, billableStates()).Scan(&ok)
	return ok, err
}
