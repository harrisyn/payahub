package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// WebhookEndpoint is where an organisation's own system hears about its
// claims. The signing secret is sealed; it is shown once, when made.
type WebhookEndpoint struct {
	ID          string    `json:"id"`
	Environment string    `json:"environment"`
	URL         string    `json:"url"`
	Description string    `json:"description"`
	Enabled     bool      `json:"enabled"`
	CreatedAt   time.Time `json:"created_at"`
	// Over the last day.
	Delivered int        `json:"delivered_24h"`
	Failing   int        `json:"failing"` // pending with at least one failed attempt
	LastError string     `json:"last_error,omitempty"`
	LastAt    *time.Time `json:"last_delivered_at,omitempty"`
}

func (s *Store) CreateWebhookEndpoint(ctx context.Context, e WebhookEndpoint, orgID string, sealed []byte) (WebhookEndpoint, error) {
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO webhook_endpoints (id, organization_id, environment, url, description, secret_sealed)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING enabled, created_at`,
		e.ID, orgID, e.Environment, e.URL, e.Description, sealed).Scan(&e.Enabled, &e.CreatedAt)
	return e, err
}

// WebhookEndpoints lists an organisation's endpoints in one environment, with
// how their deliveries are going.
func (s *Store) WebhookEndpoints(ctx context.Context, orgID, env string) ([]WebhookEndpoint, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT w.id, w.environment, w.url, w.description, w.enabled, w.created_at,
		       (SELECT count(*) FROM webhook_deliveries d WHERE d.endpoint_id = w.id AND d.status = 'delivered' AND d.delivered_at > now() - interval '1 day'),
		       (SELECT count(*) FROM webhook_deliveries d WHERE d.endpoint_id = w.id AND d.status = 'pending' AND d.attempts > 0),
		       coalesce((SELECT d.last_error FROM webhook_deliveries d WHERE d.endpoint_id = w.id AND d.last_error <> '' ORDER BY d.id DESC LIMIT 1), ''),
		       (SELECT max(d.delivered_at) FROM webhook_deliveries d WHERE d.endpoint_id = w.id)
		FROM webhook_endpoints w WHERE w.organization_id = $1 AND w.environment = $2 ORDER BY w.created_at`, orgID, env)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (WebhookEndpoint, error) {
		var e WebhookEndpoint
		err := r.Scan(&e.ID, &e.Environment, &e.URL, &e.Description, &e.Enabled, &e.CreatedAt, &e.Delivered, &e.Failing, &e.LastError, &e.LastAt)
		return e, err
	})
}

// DeleteWebhookEndpoint removes an endpoint and its undelivered events.
func (s *Store) DeleteWebhookEndpoint(ctx context.Context, orgID, id string) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM webhook_endpoints WHERE id = $1 AND organization_id = $2`, id, orgID)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// QueueWebhookPing queues a test event to one endpoint.
func (s *Store) QueueWebhookPing(ctx context.Context, orgID, id string) error {
	tag, err := s.Pool.Exec(ctx, `
		INSERT INTO webhook_deliveries (endpoint_id, event_type, payload)
		SELECT w.id, 'ping', jsonb_build_object('id', 'evt_ping_' || floor(extract(epoch FROM now()) * 1000)::bigint,
			'type', 'ping', 'created_at', now(), 'data', jsonb_build_object('endpoint_id', w.id))
		FROM webhook_endpoints w WHERE w.id = $1 AND w.organization_id = $2`, id, orgID)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// Delivery is one event on its way to one endpoint.
type Delivery struct {
	ID       int64
	Endpoint string
	URL      string
	Sealed   []byte
	Payload  json.RawMessage
	Attempts int
}

// LeaseDeliveries takes up to n due deliveries and hides them for lease, so
// another dispatcher does not send them while this one does. A dispatcher
// that dies leaves them to be retried when the lease runs out.
func (s *Store) LeaseDeliveries(ctx context.Context, n int, lease time.Duration) ([]Delivery, error) {
	rows, err := s.Pool.Query(ctx, `
		WITH due AS (
			SELECT id FROM webhook_deliveries
			WHERE status = 'pending' AND next_attempt_at <= now()
			ORDER BY next_attempt_at, id LIMIT $1 FOR UPDATE SKIP LOCKED)
		UPDATE webhook_deliveries d SET attempts = d.attempts + 1, next_attempt_at = now() + make_interval(secs => $2)
		FROM due, webhook_endpoints w
		WHERE d.id = due.id AND w.id = d.endpoint_id
		RETURNING d.id, d.endpoint_id, w.url, w.secret_sealed, d.payload, d.attempts`, n, lease.Seconds())
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Delivery, error) {
		var d Delivery
		err := r.Scan(&d.ID, &d.Endpoint, &d.URL, &d.Sealed, &d.Payload, &d.Attempts)
		return d, err
	})
}

// Attempt is the result of sending one delivery: delivered, retried at
// Next, or (with a zero Next) given up.
type Attempt struct {
	ID        int64
	Status    int
	Err       string
	Delivered bool
	Next      time.Time
}

// DeliveriesDone records a batch of attempts in one statement, so a busy
// dispatcher commits once per batch rather than once per delivery.
func (s *Store) DeliveriesDone(ctx context.Context, done []Attempt) error {
	if len(done) == 0 {
		return nil
	}
	ids := make([]int64, len(done))
	statuses := make([]int32, len(done))
	errs := make([]string, len(done))
	delivered := make([]bool, len(done))
	next := make([]*time.Time, len(done))
	for i, a := range done {
		ids[i], statuses[i], delivered[i] = a.ID, int32(a.Status), a.Delivered
		errs[i] = a.Err
		if len(errs[i]) > 300 {
			errs[i] = errs[i][:300]
		}
		if !a.Next.IsZero() {
			next[i] = &a.Next
		}
	}
	_, err := s.Pool.Exec(ctx, `
		UPDATE webhook_deliveries d SET
			status = CASE WHEN r.delivered THEN 'delivered' WHEN r.next IS NULL THEN 'failed' ELSE d.status END,
			delivered_at = CASE WHEN r.delivered THEN now() ELSE d.delivered_at END,
			last_status = NULLIF(r.status, 0),
			last_error = CASE WHEN r.delivered THEN '' ELSE r.err END,
			next_attempt_at = CASE WHEN NOT r.delivered AND r.next IS NOT NULL THEN r.next ELSE d.next_attempt_at END
		FROM unnest($1::bigint[], $2::int[], $3::text[], $4::bool[], $5::timestamptz[]) AS r(id, status, err, delivered, next)
		WHERE d.id = r.id`, ids, statuses, errs, delivered, next)
	return err
}

// WebhookDelivery is a recent delivery as an organisation sees it.
type WebhookDelivery struct {
	ID          int64           `json:"id"`
	EventType   string          `json:"event_type"`
	Status      string          `json:"status"`
	Attempts    int             `json:"attempts"`
	LastStatus  *int            `json:"last_status,omitempty"`
	LastError   string          `json:"last_error,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	DeliveredAt *time.Time      `json:"delivered_at,omitempty"`
	Payload     json.RawMessage `json:"payload"`
}

func (s *Store) WebhookDeliveries(ctx context.Context, orgID, endpointID string, limit int) ([]WebhookDelivery, error) {
	var ok bool
	if err := s.Pool.QueryRow(ctx, `SELECT true FROM webhook_endpoints WHERE id = $1 AND organization_id = $2`, endpointID, orgID).Scan(&ok); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT id, event_type, status, attempts, last_status, last_error, created_at, delivered_at, payload
		FROM webhook_deliveries WHERE endpoint_id = $1 ORDER BY id DESC LIMIT $2`, endpointID, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (WebhookDelivery, error) {
		var d WebhookDelivery
		err := r.Scan(&d.ID, &d.EventType, &d.Status, &d.Attempts, &d.LastStatus, &d.LastError, &d.CreatedAt, &d.DeliveredAt, &d.Payload)
		return d, err
	})
}
