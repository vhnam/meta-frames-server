package testutil

import (
	"context"
	"net/url"
	"regexp"
	"sync"

	"github.com/aarondl/authboss/v3"
)

// Mailbox is an authboss.Mailer that keeps sent emails for tests to read.
type Mailbox struct {
	mutex sync.Mutex
	sent  []authboss.Email
}

func (mailbox *Mailbox) Send(_ context.Context, email authboss.Email) error {
	mailbox.mutex.Lock()
	defer mailbox.mutex.Unlock()
	mailbox.sent = append(mailbox.sent, email)
	return nil
}

// Sent returns a copy of every email sent so far.
func (mailbox *Mailbox) Sent() []authboss.Email {
	mailbox.mutex.Lock()
	defer mailbox.mutex.Unlock()
	return append([]authboss.Email(nil), mailbox.sent...)
}

var recoverLink = regexp.MustCompile(`\S+/recover/end\?token=\S+`)

// RecoverToken returns the reset token linked in the last email, or "" if there is none.
func (mailbox *Mailbox) RecoverToken() string {
	sent := mailbox.Sent()
	if len(sent) == 0 {
		return ""
	}
	link, err := url.Parse(recoverLink.FindString(sent[len(sent)-1].TextBody))
	if err != nil {
		return ""
	}
	return link.Query().Get("token")
}
