package queue

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/riverqueue/river"

	"github.com/harrisyn/payahub/internal/access"
	"github.com/harrisyn/payahub/internal/mail"
	"github.com/harrisyn/payahub/internal/store"
)

// MailQueue carries outgoing email. A message is queued in the transaction
// that causes it, so it is sent once the change is saved and never before.
const MailQueue = "mail"

type EmailArgs struct {
	mail.Message
}

func (EmailArgs) Kind() string { return "send_email" }

func (EmailArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: MailQueue, MaxAttempts: 12}
}

type EmailWorker struct {
	river.WorkerDefaults[EmailArgs]
	Sender mail.Sender
}

func (w *EmailWorker) Work(ctx context.Context, job *river.Job[EmailArgs]) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	err := w.Sender.Send(ctx, job.Args.Message)
	if errors.Is(err, mail.ErrInvalid) {
		return river.JobCancel(err) // retrying cannot fix it
	}
	return err
}

// CreateLead saves an enquiry and, in the same transaction, queues an email
// to each operator. A repeated idempotency key saves and sends nothing new.
func CreateLead(ctx context.Context, st *store.Store, rc *Client, l store.Lead, idemKey, consoleURL string) (store.Lead, bool, error) {
	tx, err := st.Pool.Begin(ctx)
	if err != nil {
		return store.Lead{}, false, err
	}
	defer tx.Rollback(ctx)
	lead, replayed, err := store.CreateLead(ctx, tx, l, idemKey)
	if err != nil || replayed {
		return lead, replayed, err
	}
	to, err := store.OperatorEmails(ctx, tx)
	if err != nil {
		return store.Lead{}, false, err
	}
	if len(to) > 0 {
		if _, err := rc.InsertTx(ctx, tx, EmailArgs{LeadAlert(lead, to, consoleURL)}, nil); err != nil {
			return store.Lead{}, false, err
		}
	}
	return lead, false, tx.Commit(ctx)
}

var leadKindText = map[string]string{
	"contact":  "wants to talk",
	"platform": "an insurer or platform wants to connect",
	"payer":    "a clinic asks for a payer",
	"specs":    "a developer asked for the interface spec",
}

// LeadAlert is the email operators get for a new enquiry.
func LeadAlert(l store.Lead, to []string, consoleURL string) mail.Message {
	who := firstOf(l.Organization, l.Name, l.PayerName, l.Email)
	var b strings.Builder
	fmt.Fprintf(&b, "New enquiry: %s.\n\n", leadKindText[l.Kind])
	line := func(label, v string) {
		if v != "" {
			fmt.Fprintf(&b, "%s: %s\n", label, v)
		}
	}
	line("Name", l.Name)
	line("Organisation", l.Organization)
	line("Email", l.Email)
	line("Phone", l.Phone)
	line("Payer", l.PayerName)
	line("Payer contact", l.PayerContact)
	if l.MonthlyClaims != nil {
		line("Claims a month", fmt.Sprint(*l.MonthlyClaims))
	}
	if l.Message != "" {
		fmt.Fprintf(&b, "\n%s\n", l.Message)
	}
	if consoleURL != "" {
		fmt.Fprintf(&b, "\nAll enquiries: %s/console/operator/leads\n", strings.TrimRight(consoleURL, "/"))
	}
	// One line, so a name with a line break cannot reach the headers.
	subject := strings.Join(strings.Fields("New enquiry from "+who), " ")
	return mail.Message{To: to, Subject: subject, Text: b.String()}
}

func firstOf(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

// Invite records an invitation and queues its email in one transaction.
func Invite(ctx context.Context, st *store.Store, rc *Client, in store.NewInvitation, consoleURL string) (store.Invitation, error) {
	tx, err := st.Pool.Begin(ctx)
	if err != nil {
		return store.Invitation{}, err
	}
	defer tx.Rollback(ctx)
	inv, token, orgName, err := store.CreateInvitation(ctx, tx, in)
	if err != nil {
		return store.Invitation{}, err
	}
	if _, err := rc.InsertTx(ctx, tx, EmailArgs{InvitationEmail(inv, orgName, token, consoleURL)}, nil); err != nil {
		return store.Invitation{}, err
	}
	return inv, tx.Commit(ctx)
}

var roleText = map[access.Role]string{
	access.Owner:     "an owner",
	access.Admin:     "an admin",
	access.Claims:    "claims staff",
	access.Finance:   "finance",
	access.Developer: "a developer",
	access.Viewer:    "a viewer",
}

// InvitationEmail is the email that carries an invitation link.
func InvitationEmail(inv store.Invitation, orgName, token, consoleURL string) mail.Message {
	var b strings.Builder
	greet := "Hello"
	if inv.Name != "" {
		greet += " " + inv.Name
	}
	fmt.Fprintf(&b, "%s,\n\n%s has invited you to join %s on Payahub as %s.\n\n", greet, inv.InvitedBy, orgName, roleText[inv.Role])
	fmt.Fprintf(&b, "Accept the invitation and choose your password here:\n%s/join/%s\n\n", strings.TrimRight(consoleURL, "/"), token)
	fmt.Fprintf(&b, "The link works once and expires on %s. If you were not expecting this, you can ignore it.\n", inv.ExpiresAt.Format("2 January 2006"))
	subject := strings.Join(strings.Fields("Join "+orgName+" on Payahub"), " ")
	return mail.Message{To: []string{inv.Email}, Subject: subject, Text: b.String()}
}
