package queue

import (
	"context"
	"errors"
	"time"

	"github.com/riverqueue/river"

	"github.com/harrisyn/payahub/internal/catalog"
	"github.com/harrisyn/payahub/internal/connector"
	"github.com/harrisyn/payahub/internal/connector/sandbox"
	"github.com/harrisyn/payahub/internal/store"
	"github.com/harrisyn/payahub/internal/vault"
)

// A member check is asked while someone waits at the front desk, so it runs
// at once and is not retried later: an answer an hour late helps nobody.
type MemberCheckArgs struct {
	ID string `json:"id"`
	// SealedOTP is the code the member read out, sealed so it is not kept
	// readable in the job table.
	SealedOTP []byte `json:"sealed_otp,omitempty"`
}

func (MemberCheckArgs) Kind() string { return "member_check" }

func (MemberCheckArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: SubmissionsQueue, MaxAttempts: 1}
}

func otpAad(id string) string { return "member-check/" + id }

// CheckMember records a check and queues the payer call in one transaction.
func CheckMember(ctx context.Context, st *store.Store, rc *Client, v *vault.Vault, m store.MemberCheck, otp, idemKey string, sha []byte) (store.MemberCheck, error) {
	args := MemberCheckArgs{ID: m.ID}
	if otp != "" {
		sealed, err := v.Seal(map[string]string{"otp": otp}, otpAad(m.ID))
		if err != nil {
			return m, err
		}
		args.SealedOTP = sealed
	}
	tx, err := st.Pool.Begin(ctx)
	if err != nil {
		return m, err
	}
	defer tx.Rollback(ctx)
	m, replayed, err := store.CreateMemberCheck(ctx, tx, m, idemKey, sha)
	if err != nil || replayed {
		return m, err
	}
	if _, err := rc.InsertTx(ctx, tx, args, nil); err != nil {
		return m, err
	}
	return m, tx.Commit(ctx)
}

type MemberCheckWorker struct {
	river.WorkerDefaults[MemberCheckArgs]
	Store *store.Store
	Vault *vault.Vault
}

const memberCheckTimeout = 30 * time.Second

func (w *MemberCheckWorker) Timeout(*river.Job[MemberCheckArgs]) time.Duration {
	return memberCheckTimeout + 5*time.Second
}

func (w *MemberCheckWorker) Work(ctx context.Context, job *river.Job[MemberCheckArgs]) error {
	m, err := w.Store.GetMemberCheck(ctx, "", job.Args.ID)
	if errors.Is(err, store.ErrNotFound) {
		return river.JobCancel(err)
	}
	if err != nil {
		return err
	}
	if m.Status != "pending" {
		return nil
	}
	result, callErr := w.ask(ctx, m, job.Args.SealedOTP)
	code, msg := "", ""
	if callErr != nil {
		ce := connector.AsError(callErr)
		code, msg = string(ce.Code), ce.UserMessage
	}
	// The answer is recorded even if the worker is stopping.
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return w.Store.FinishMemberCheck(rctx, m.ID, result, code, msg)
}

func (w *MemberCheckWorker) ask(ctx context.Context, m store.MemberCheck, sealedOTP []byte) (any, error) {
	otp := ""
	if sealedOTP != nil {
		opened, err := w.Vault.Open(sealedOTP, otpAad(m.ID))
		if err != nil {
			return nil, connector.Errorf(connector.Validation, "the code could not be read")
		}
		otp = opened["otp"]
	}
	var conn connector.Connector
	var err error
	if m.Environment == "sandbox" {
		conn, err = sandbox.New(connector.Account{})
	} else {
		p, ok := catalog.LookupPayer(m.PayerID)
		if !ok {
			return nil, connector.Errorf(connector.Validation, "unknown payer %q", m.PayerID)
		}
		conn, err = OpenAccount(ctx, w.Store.Pool, w.Vault, m.OrganizationID, p, m.Environment)
	}
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, memberCheckTimeout)
	defer cancel()
	if m.Action == "send_otp" {
		challenge, err := conn.SendOTP(ctx, m.MemberNumber)
		return map[string]string{"challenge": challenge}, err
	}
	return conn.VerifyMember(ctx, m.MemberNumber, otp)
}
