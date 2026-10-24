// Package store is the Postgres system of record. Claims, their audit trail
// and their queued jobs share one database, so a claim change and the job it
// triggers commit (or roll back) together: nothing is lost between them.
package store

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"

	"github.com/harrisyn/payahub/internal/access"
	"github.com/harrisyn/payahub/internal/claim"
)

//go:embed migrations/*.sql
var migrations embed.FS

var (
	ErrNotFound            = errors.New("not found")
	ErrUnauthenticated     = errors.New("unauthenticated")
	ErrIdempotencyConflict = errors.New("idempotency key reused with a different request")
)

type Store struct {
	Pool *pgxpool.Pool

	// keys caches API key lookups for keyTTL, so a revoked key
	// stops working within that window without a database read per request.
	keys sync.Map // sha256 string -> cachedKey

	// catalogVersion is the catalog_changes id the in-memory catalogue was
	// built from; -1 before the first load.
	catalogVersion atomic.Int64
	catalogMu      sync.Mutex

	// wait passes claim_saved notifications to requests waiting on a claim.
	wait waiters
}

const keyTTL = 30 * time.Second

type cachedKey struct {
	org     Org
	expires time.Time
}

func Open(ctx context.Context, url string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	// pgx defaults to max(4, CPUs) connections, which starves a process
	// serving hundreds of requests and workers. Respect pool_max_conns if set.
	if !strings.Contains(url, "pool_max_conns") {
		cfg.MaxConns = int32(max(32, 8*runtime.NumCPU()))
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	st := &Store{Pool: pool}
	st.catalogVersion.Store(-1)
	return st, nil
}

// Close stops the claim listener, if it runs, and closes the pool, which
// waits for every connection to come back.
func (s *Store) Close() {
	s.wait.once.Do(func() {}) // a listener started later would hold the pool open
	if s.wait.stop != nil {
		s.wait.stop()
	}
	s.Pool.Close()
}

// Migrate applies our schema (idempotent SQL, in file order) and River's.
func (s *Store) Migrate(ctx context.Context) error {
	names, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)
	for _, name := range names {
		sql, err := migrations.ReadFile(name)
		if err != nil {
			return err
		}
		if _, err := s.Pool.Exec(ctx, string(sql)); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	migrator, err := rivermigrate.New(riverpgxv5.New(s.Pool), nil)
	if err != nil {
		return err
	}
	if _, err = migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		return err
	}
	return s.SeedCatalog(ctx)
}

func NewID(prefix string) string { return prefix + "_" + uuid.Must(uuid.NewV7()).String() }

func hashKey(raw string) []byte { h := sha256.Sum256([]byte(raw)); return h[:] }

// Org is who a request acts for: an API key (one environment) or a console
// session (a signed-in user, sandbox by default).
type Org struct {
	ID          string
	Environment string
	Session     bool
	UserID      string
	// Operator users run Payahub itself: they edit the shared catalogue.
	Operator bool
	// Role is a console member's job; empty for an API key.
	Role access.Role
}

// Authenticate resolves an API key or a console session token.
func (s *Store) Authenticate(ctx context.Context, raw string) (Org, error) {
	h := hashKey(raw)
	if v, ok := s.keys.Load(string(h)); ok && time.Now().Before(v.(cachedKey).expires) {
		return v.(cachedKey).org, nil
	}
	var o Org
	var err error
	if strings.HasPrefix(raw, sessionPrefix) {
		o, err = s.authenticateSession(ctx, h)
	} else {
		err = s.Pool.QueryRow(ctx, `
			SELECT organization_id, environment FROM api_keys
			WHERE key_sha256 = $1 AND revoked_at IS NULL`, h).Scan(&o.ID, &o.Environment)
	}
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, ErrUnauthenticated) {
		return Org{}, ErrUnauthenticated
	}
	// Sessions are not cached, so a member's new role or removal takes
	// effect on their next request.
	if err == nil && !o.Session {
		s.keys.Store(string(h), cachedKey{org: o, expires: time.Now().Add(keyTTL)})
	}
	return o, err
}

// EnsureOrgWithKey creates an organisation and API key if missing (dev seeding and tests).
func (s *Store) EnsureOrgWithKey(ctx context.Context, orgID, name, env, rawKey string) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO organizations (id, name) VALUES ($1, $2) ON CONFLICT DO NOTHING`, orgID, name)
	if err != nil {
		return err
	}
	prefix := rawKey
	if len(prefix) > 12 {
		prefix = prefix[:12]
	}
	_, err = s.Pool.Exec(ctx, `
		INSERT INTO api_keys (id, organization_id, environment, key_sha256, prefix)
		VALUES ($1, $2, $3, $4, $5) ON CONFLICT (key_sha256) DO NOTHING`,
		NewID("key"), orgID, env, hashKey(rawKey), prefix)
	return err
}

// Querier is satisfied by the pool and by a transaction.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// CreateClaim inserts a claim. With an idempotency key, a repeat of the same
// request returns the original claim; a different request under the same key
// is ErrIdempotencyConflict. Safe under concurrent duplicates.
func (s *Store) CreateClaim(ctx context.Context, c *claim.Claim, idemKey string, requestSHA []byte) (*claim.Claim, bool, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback(ctx)

	if err := insertClaim(ctx, tx, c); err != nil {
		return nil, false, err
	}
	if idemKey != "" {
		tag, err := tx.Exec(ctx, `
			INSERT INTO idempotency_keys (organization_id, key, request_sha256, claim_id)
			VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING`, c.OrganizationID, idemKey, requestSHA, c.ID)
		if err != nil {
			return nil, false, err
		}
		if tag.RowsAffected() == 0 {
			_ = tx.Rollback(ctx)
			return s.replay(ctx, c.OrganizationID, idemKey, requestSHA)
		}
	}
	if err := addEvent(ctx, tx, c.ID, "created", "", c.State, nil); err != nil {
		return nil, false, err
	}
	return c, false, tx.Commit(ctx)
}

func (s *Store) replay(ctx context.Context, orgID, key string, requestSHA []byte) (*claim.Claim, bool, error) {
	var claimID string
	var storedSHA []byte
	err := s.Pool.QueryRow(ctx, `
		SELECT claim_id, request_sha256 FROM idempotency_keys WHERE organization_id = $1 AND key = $2`,
		orgID, key).Scan(&claimID, &storedSHA)
	if err != nil {
		return nil, false, err
	}
	if string(storedSHA) != string(requestSHA) {
		return nil, false, ErrIdempotencyConflict
	}
	c, err := s.GetClaim(ctx, orgID, claimID)
	return c, true, err
}

func insertClaim(ctx context.Context, q Querier, c *claim.Claim) error {
	body, err := json.Marshal(c)
	if err != nil {
		return err
	}
	_, err = q.Exec(ctx, `
		INSERT INTO claims (id, organization_id, environment, payer_id, state, payer_claim_number,
		                    currency, total, body, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), $7, $8, $9, $10, $10)`,
		c.ID, c.OrganizationID, c.Environment, c.PayerID, c.State, c.PayerClaimNumber,
		c.Currency, c.Total, body, c.CreatedAt)
	return err
}

func (s *Store) GetClaim(ctx context.Context, orgID, id string) (*claim.Claim, error) {
	return scanClaim(s.Pool.QueryRow(ctx, `SELECT body FROM claims WHERE id = $1 AND organization_id = $2`, id, orgID))
}

func scanClaim(row pgx.Row) (*claim.Claim, error) {
	var body []byte
	if err := row.Scan(&body); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	var c claim.Claim
	return &c, json.Unmarshal(body, &c)
}

// LockClaim reads a claim FOR UPDATE inside tx, with whether a submit job owns it.
func LockClaim(ctx context.Context, tx pgx.Tx, orgID, id string) (*claim.Claim, bool, error) {
	var body []byte
	var queued bool
	err := tx.QueryRow(ctx, `
		SELECT body, submission_queued FROM claims WHERE id = $1 AND organization_id = $2 FOR UPDATE`,
		id, orgID).Scan(&body, &queued)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, ErrNotFound
	}
	if err != nil {
		return nil, false, err
	}
	var c claim.Claim
	return &c, queued, json.Unmarshal(body, &c)
}

// SaveClaim writes the claim and appends an event for the change in one statement pair.
func SaveClaim(ctx context.Context, tx pgx.Tx, c *claim.Claim, from claim.State, queued bool, kind string, detail any) error {
	c.UpdatedAt = time.Now().UTC()
	body, err := json.Marshal(c)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		UPDATE claims SET state = $2, payer_claim_number = NULLIF($3, ''), total = $4, body = $5,
		                  submission_queued = $6, updated_at = $7
		WHERE id = $1`, c.ID, c.State, c.PayerClaimNumber, c.Total, body, queued, c.UpdatedAt)
	if err != nil {
		return err
	}
	// Delivered on commit, to requests waiting for this claim to settle.
	if _, err := tx.Exec(ctx, notifyWaiters, c.ID); err != nil {
		return err
	}
	if from != c.State {
		if err := scheduleStatusCheck(ctx, tx, c); err != nil {
			return err
		}
	}
	if c.Environment == "live" && c.State.Billable() && !from.Billable() {
		if err := chargeClaim(ctx, tx, c); err != nil {
			return err
		}
	}
	return addEvent(ctx, tx, c.ID, kind, from, c.State, detail)
}

func addEvent(ctx context.Context, q Querier, claimID, kind string, from, to claim.State, detail any) error {
	var raw []byte
	if detail != nil {
		var err error
		if raw, err = json.Marshal(detail); err != nil {
			return err
		}
	}
	// The event, and a webhook delivery of it to each of the organisation's
	// endpoints for the claim's environment, in one statement.
	_, err := q.Exec(ctx, `
		WITH ev AS (
			INSERT INTO claim_events (claim_id, kind, from_state, to_state, detail)
			VALUES ($1, $2, NULLIF($3, ''), $4, $5)
			RETURNING id, claim_id, kind, from_state, to_state, detail, created_at)
		INSERT INTO webhook_deliveries (endpoint_id, event_type, payload)
		SELECT w.id, 'claim.' || ev.kind, jsonb_build_object(
			'id', 'evt_' || ev.id, 'type', 'claim.' || ev.kind, 'created_at', ev.created_at,
			'data', jsonb_strip_nulls(jsonb_build_object(
				'claim_id', ev.claim_id, 'state', ev.to_state, 'previous_state', ev.from_state,
				'payer_claim_number', c.payer_claim_number, 'detail', ev.detail)))
		FROM ev
		JOIN claims c ON c.id = ev.claim_id
		JOIN webhook_endpoints w ON w.organization_id = c.organization_id AND w.environment = c.environment AND w.enabled`,
		claimID, kind, string(from), string(to), raw)
	return err
}

type Event struct {
	ID        int64           `json:"id"`
	Kind      string          `json:"kind"`
	FromState string          `json:"from_state,omitempty"`
	ToState   string          `json:"to_state"`
	Detail    json.RawMessage `json:"detail,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

func (s *Store) ListEvents(ctx context.Context, orgID, claimID string) ([]Event, error) {
	if _, err := s.GetClaim(ctx, orgID, claimID); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT id, kind, coalesce(from_state, ''), to_state, detail, created_at
		FROM claim_events WHERE claim_id = $1 ORDER BY id`, claimID)
	if err != nil {
		return nil, err
	}
	events := []Event{}
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.Kind, &e.FromState, &e.ToState, &e.Detail, &e.CreatedAt); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// PayerOverrides returns an organisation's submission overrides for a payer ({} when none).
func PayerOverrides(ctx context.Context, q Querier, orgID, payerID, env string) ([]byte, error) {
	var raw []byte
	err := q.QueryRow(ctx, `
		SELECT overrides FROM payer_settings WHERE organization_id = $1 AND payer_id = $2 AND environment = $3`,
		orgID, payerID, env).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return raw, err
}

// LoadProgress reads a claim's incremental-send progress; nil when none.
func LoadProgress(ctx context.Context, q Querier, claimID string) ([]byte, error) {
	var raw []byte
	err := q.QueryRow(ctx, `SELECT submission_progress FROM claims WHERE id = $1`, claimID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return raw, err
}

// SaveProgress records incremental-send progress. It runs outside the claim
// lock on purpose: the job that owns the claim is its only writer, and each
// payer step must be written down the moment it succeeds.
func SaveProgress(ctx context.Context, q Querier, claimID string, progress []byte) error {
	_, err := q.Exec(ctx, `UPDATE claims SET submission_progress = $2 WHERE id = $1`, claimID, progress)
	return err
}
