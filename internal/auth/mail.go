package auth

import (
	"bytes"
	"context"
	"fmt"
	htmltemplate "html/template"
	"log/slog"
	texttemplate "text/template"
	"time"

	"github.com/aarondl/authboss/v3"
	"github.com/aarondl/authboss/v3/recover"
)

var (
	recoverText = texttemplate.Must(texttemplate.New(recover.EmailRecoverTxt).Parse(
		`Someone asked to reset the password of your Meta-Frame account.

Choose a new password here (the link works once and expires in 24 hours):
{{.recover_url}}

If it was not you, ignore this email; your password stays the same.
`))
	recoverHTML = htmltemplate.Must(htmltemplate.New(recover.EmailRecoverHTML).Parse(
		`<p>Someone asked to reset the password of your Meta-Frame account.</p>
<p><a href="{{.recover_url}}">Choose a new password</a> (the link works once and expires in 24 hours).</p>
<p>If it was not you, ignore this email; your password stays the same.</p>
`))
)

// mailRenderer renders the emails authboss sends; recovery is the only one in use.
type mailRenderer struct{}

func (mailRenderer) Load(...string) error { return nil }

func (mailRenderer) Render(_ context.Context, name string, data authboss.HTMLData) ([]byte, string, error) {
	var body bytes.Buffer
	switch name {
	case recover.EmailRecoverTxt:
		err := recoverText.Execute(&body, data)
		return body.Bytes(), "text/plain", err
	case recover.EmailRecoverHTML:
		err := recoverHTML.Execute(&body, data)
		return body.Bytes(), "text/html", err
	}
	return nil, "", fmt.Errorf("auth: unknown email template %q", name)
}

// AsyncMailer sends in the background, so a slow mail server does not hold up the request.
// Failures are logged; the API already answers the same whether or not an email goes out.
type AsyncMailer struct{ Mailer authboss.Mailer }

func (mailer AsyncMailer) Send(ctx context.Context, email authboss.Email) error {
	go func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		defer cancel()
		if err := mailer.Mailer.Send(ctx, email); err != nil {
			slog.Error("sending email failed", "to", email.To, "subject", email.Subject, "err", err)
		}
	}()
	return nil
}

// logger routes authboss's log lines into slog.
type logger struct{}

func (logger) Info(message string)  { slog.Info(message, "component", "authboss") }
func (logger) Error(message string) { slog.Error(message, "component", "authboss") }
