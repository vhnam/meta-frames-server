package auth

import (
	"context"
	"testing"
	"time"

	"github.com/aarondl/authboss/v3"
)

type channelMailer chan authboss.Email

func (mailer channelMailer) Send(ctx context.Context, email authboss.Email) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	mailer <- email
	return nil
}

func TestAsyncMailerOutlivesTheRequest(test *testing.T) {
	sent := make(channelMailer, 1)
	request, cancel := context.WithCancel(context.Background())
	if err := (AsyncMailer{Mailer: sent}).Send(request, authboss.Email{Subject: "reset"}); err != nil {
		test.Fatal(err)
	}
	cancel() // the response has gone out

	select {
	case email := <-sent:
		if email.Subject != "reset" {
			test.Fatalf("email = %+v", email)
		}
	case <-time.After(time.Second):
		test.Fatal("the email was not sent")
	}
}
