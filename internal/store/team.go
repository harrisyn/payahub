package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/harrisyn/payahub/internal/access"
)

var (
	// ErrLastOwner refuses a change that would leave an organisation with no owner.
	ErrLastOwner = errors.New("an organisation keeps at least one owner")
	// ErrNotAllowed refuses a team change the member's own role does not cover.
	ErrNotAllowed = errors.New("only an owner can add, change or remove an owner")
	// ErrAlreadyMember refuses an invitation to someone already in the organisation.
	ErrAlreadyMember = errors.New("that person is already a member")
)

// InvitationTTL is how long an invitation link works.
const InvitationTTL = 7 * 24 * time.Hour

// Member is a person in an organisation.
type Member struct {
	ID        string      `json:"id"`
	Name      string      `json:"name"`
	Email     string      `json:"email"`
	Role      access.Role `json:"role"`
	CreatedAt time.Time   `json:"created_at"`
}

// Invitation is an open invitation to join an organisation.
type Invitation struct {
	ID        string      `json:"id"`
	Email     string      `json:"email"`
	Name      string      `json:"name"`
	Role      access.Role `json:"role"`
	InvitedBy string      `json:"invited_by"`
	CreatedAt time.Time   `json:"created_at"`
	ExpiresAt time.Time   `json:"expires_at"`
}

// Members lists an organisation's members, oldest first.
func (s *Store) Members(ctx context.Context, orgID string) ([]Member, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, name, email, role, created_at FROM users
		WHERE organization_id = $1 AND removed_at IS NULL ORDER BY created_at, id`, orgID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Member])
}

// Invitations lists the invitations still waiting to be accepted.
func (s *Store) Invitations(ctx context.Context, orgID string) ([]Invitation, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT i.id, i.email, i.name, i.role, u.name, i.created_at, i.expires_at
		FROM invitations i JOIN users u ON u.id = i.invited_by
		WHERE i.organization_id = $1 AND i.accepted_at IS NULL AND i.revoked_at IS NULL AND i.expires_at > now()
		ORDER BY i.created_at DESC`, orgID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Invitation])
}

// NewInvitation is what CreateInvitation needs.
type NewInvitation struct {
	OrgID, Email, Name string
	Role               access.Role
	By                 Org
}

// CreateInvitation records an invitation in q (the caller's transaction, so
// its email is queued with it) and returns the raw link token and the
// organisation's name. An earlier open invitation to the same address is
// replaced, so inviting again resends.
func CreateInvitation(ctx context.Context, q Querier, in NewInvitation) (inv Invitation, token, orgName string, err error) {
	if !access.CanAssign(in.By.Role, access.Viewer, in.Role) {
		return Invitation{}, "", "", ErrNotAllowed
	}
	var otherOrg string
	var removed bool
	err = q.QueryRow(ctx, `SELECT organization_id, removed_at IS NOT NULL FROM users WHERE lower(email) = lower($1)`, in.Email).
		Scan(&otherOrg, &removed)
	switch {
	case err == nil && otherOrg == in.OrgID && !removed:
		return Invitation{}, "", "", ErrAlreadyMember
	case err == nil && otherOrg != in.OrgID:
		return Invitation{}, "", "", ErrEmailTaken
	case err != nil && !errors.Is(err, pgx.ErrNoRows):
		return Invitation{}, "", "", err
	}
	if _, err = q.Exec(ctx, `
		UPDATE invitations SET revoked_at = now()
		WHERE organization_id = $1 AND lower(email) = lower($2) AND accepted_at IS NULL AND revoked_at IS NULL`,
		in.OrgID, in.Email); err != nil {
		return Invitation{}, "", "", err
	}
	token = randomToken("inv_")
	inv = Invitation{ID: NewID("inv"), Email: in.Email, Name: in.Name, Role: in.Role}
	err = q.QueryRow(ctx, `
		WITH i AS (
			INSERT INTO invitations (id, organization_id, email, name, role, token_sha256, invited_by, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, now() + make_interval(secs => $8))
			RETURNING created_at, expires_at)
		SELECT i.created_at, i.expires_at, u.name, o.name
		FROM i, users u, organizations o WHERE u.id = $7 AND o.id = $2`,
		inv.ID, in.OrgID, in.Email, in.Name, in.Role, hashKey(token), in.By.UserID, InvitationTTL.Seconds()).
		Scan(&inv.CreatedAt, &inv.ExpiresAt, &inv.InvitedBy, &orgName)
	return inv, token, orgName, err
}

// RevokeInvitation withdraws an open invitation.
func (s *Store) RevokeInvitation(ctx context.Context, orgID, id string) error {
	tag, err := s.Pool.Exec(ctx, `
		UPDATE invitations SET revoked_at = now()
		WHERE id = $1 AND organization_id = $2 AND accepted_at IS NULL AND revoked_at IS NULL`, id, orgID)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// InvitationView is what the person holding an invitation link sees.
type InvitationView struct {
	Organization string      `json:"organization"`
	Email        string      `json:"email"`
	Name         string      `json:"name"`
	Role         access.Role `json:"role"`
	InvitedBy    string      `json:"invited_by"`
	ExpiresAt    time.Time   `json:"expires_at"`
}

// InvitationByToken finds an open invitation from its link.
func (s *Store) InvitationByToken(ctx context.Context, token string) (InvitationView, error) {
	var v InvitationView
	err := s.Pool.QueryRow(ctx, `
		SELECT o.name, i.email, i.name, i.role, u.name, i.expires_at
		FROM invitations i JOIN organizations o ON o.id = i.organization_id JOIN users u ON u.id = i.invited_by
		WHERE i.token_sha256 = $1 AND i.accepted_at IS NULL AND i.revoked_at IS NULL AND i.expires_at > now()`,
		hashKey(token)).Scan(&v.Organization, &v.Email, &v.Name, &v.Role, &v.InvitedBy, &v.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, ErrNotFound
	}
	return v, err
}

// AcceptInvitation makes the member, signs them in and closes the
// invitation, in one transaction. It returns a session token.
func (s *Store) AcceptInvitation(ctx context.Context, token, name, password string) (string, error) {
	hash, err := hashPassword(password)
	if err != nil {
		return "", err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	var id, orgID, email string
	var role access.Role
	err = tx.QueryRow(ctx, `
		SELECT id, organization_id, email, role FROM invitations
		WHERE token_sha256 = $1 AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at > now()
		FOR UPDATE`, hashKey(token)).Scan(&id, &orgID, &email, &role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	// Someone removed from this organisation comes back as the same person,
	// so what they did before stays theirs.
	userID := NewID("usr")
	err = tx.QueryRow(ctx, `
		INSERT INTO users (id, organization_id, email, name, password_hash, role) VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (lower(email)) DO UPDATE SET name = EXCLUDED.name, password_hash = EXCLUDED.password_hash,
			role = EXCLUDED.role, removed_at = NULL
			WHERE users.organization_id = EXCLUDED.organization_id AND users.removed_at IS NOT NULL
		RETURNING id`, userID, orgID, email, strings.TrimSpace(name), hash, role).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrEmailTaken
	}
	if err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `UPDATE invitations SET accepted_at = now() WHERE id = $1`, id); err != nil {
		return "", err
	}
	session := randomToken(sessionPrefix)
	if err := insertSession(ctx, tx, session, userID, orgID); err != nil {
		return "", err
	}
	return session, tx.Commit(ctx)
}

// changeMember locks the organisation's members, so two owners cannot each
// demote the other at once, and applies change to one of them.
func (s *Store) changeMember(ctx context.Context, by Org, userID string, to access.Role, change func(pgx.Tx) error) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `
		SELECT id, role FROM users WHERE organization_id = $1 AND removed_at IS NULL ORDER BY id FOR UPDATE`, by.ID)
	if err != nil {
		return err
	}
	var from access.Role
	owners, found := 0, false
	for rows.Next() {
		var id string
		var r access.Role
		if err := rows.Scan(&id, &r); err != nil {
			return err
		}
		if r == access.Owner {
			owners++
		}
		if id == userID {
			from, found = r, true
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if !found {
		return ErrNotFound
	}
	if !access.CanAssign(by.Role, from, to) {
		return ErrNotAllowed
	}
	if from == access.Owner && to != access.Owner && owners == 1 {
		return ErrLastOwner
	}
	if err := change(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// SetRole changes a member's role.
func (s *Store) SetRole(ctx context.Context, by Org, userID string, role access.Role) error {
	return s.changeMember(ctx, by, userID, role, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE users SET role = $2 WHERE id = $1`, userID, role)
		return err
	})
}

// RemoveMember takes a member out of the organisation and ends their
// sessions. Their past actions keep their name.
func (s *Store) RemoveMember(ctx context.Context, by Org, userID string) error {
	// Removing is treated as a change to the lowest role, for the owner checks.
	return s.changeMember(ctx, by, userID, access.Viewer, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE users SET removed_at = now() WHERE id = $1`, userID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, userID)
		return err
	})
}
