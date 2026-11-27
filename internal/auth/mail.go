package auth

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	htmltemplate "html/template"
	"log/slog"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strings"
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

var (
	verifyText = texttemplate.Must(texttemplate.New("verify.txt").Parse(
		`Confirm the email address of your Meta-Frame account:
{{.link}}

The link expires in 24 hours.

If you did not create a Meta-Frame account, ignore this email.
`))
	verifyHTML = htmltemplate.Must(htmltemplate.New("verify.html").Parse(
		`<p><a href="{{.link}}">Confirm the email address of your Meta-Frame account</a>.</p>
<p>The link expires in 24 hours.</p>
<p>If you did not create a Meta-Frame account, ignore this email.</p>
`))
)

// verificationEmail builds the email that carries the verification link.
func verificationEmail(from, recipient, link string) (authboss.Email, error) {
	data := map[string]string{"link": link}
	var text, html bytes.Buffer
	if err := verifyText.Execute(&text, data); err != nil {
		return authboss.Email{}, err
	}
	if err := verifyHTML.Execute(&html, data); err != nil {
		return authboss.Email{}, err
	}
	return authboss.Email{
		To:       []string{recipient},
		From:     from,
		FromName: "Meta-Frame",
		Subject:  "[Meta-Frame] Confirm your email address",
		TextBody: text.String(),
		HTMLBody: html.String(),
	}, nil
}

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

// mailSlots bounds the sends in flight, so a slow mail server cannot pile up goroutines.
var mailSlots = make(chan struct{}, 8)

// AsyncMailer sends in the background, so a slow mail server does not hold up the request.
// Failures are logged as a category and SMTP code, never as error text; the API already answers the same whether
// or not an email goes out. When all 8 send slots are busy the email is dropped.
type AsyncMailer struct{ Mailer authboss.Mailer }

func (mailer AsyncMailer) Send(ctx context.Context, email authboss.Email) error {
	select {
	case mailSlots <- struct{}{}:
	default:
		slog.Error("dropping email: all send slots are busy", "subject", email.Subject)
		return nil
	}
	go func() {
		defer func() { <-mailSlots }()
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		defer cancel()
		if err := mailer.Mailer.Send(ctx, email); err != nil {
			category, code := classifyMailError(err)
			// Never log the error text: an SMTP reply can echo the message, link or address.
			slog.Error("sending email failed", "subject", email.Subject, "category", category, "smtp_code", code)
		}
	}()
	return nil
}

// mailError tags a transport failure with the step that failed.
type mailError struct {
	category string
	err      error
}

func (failure *mailError) Error() string { return failure.category + ": " + failure.err.Error() }
func (failure *mailError) Unwrap() error { return failure.err }

// classifyMailError gives a fixed category and the numeric SMTP code (0 when there is none).
func classifyMailError(err error) (category string, code int) {
	var reply *textproto.Error
	if errors.As(err, &reply) {
		code = reply.Code
	}
	var timeout net.Error
	var tagged *mailError
	switch {
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &timeout) && timeout.Timeout()):
		return "timeout", code
	case errors.As(err, &tagged):
		return tagged.category, code
	}
	return "other", code
}

// smtpMailer sends through an SMTP relay. Every read and write stops at the context deadline.
// With requireTLS it refuses to send unless STARTTLS (certificate verified) and AUTH succeed.
type smtpMailer struct {
	addr, username, password string
	requireTLS               bool
	tlsConfig                *tls.Config // tests set RootCAs; ServerName and MinVersion are always set
}

// NewSMTPMailer returns an SMTP mailer that sends in the background. requireTLS is for production.
func NewSMTPMailer(addr, username, password string, requireTLS bool) authboss.Mailer {
	return AsyncMailer{Mailer: &smtpMailer{addr: addr, username: username, password: password, requireTLS: requireTLS}}
}

func (mailer *smtpMailer) Send(ctx context.Context, email authboss.Email) error {
	from, err := mail.ParseAddress(email.From)
	if err != nil || len(email.To) == 0 || (email.TextBody == "" && email.HTMLBody == "") {
		return &mailError{"send", errors.New("invalid email")}
	}
	message, err := buildMessage(from, email)
	if err != nil {
		return &mailError{"send", err}
	}
	host, _, err := net.SplitHostPort(mailer.addr)
	if err != nil {
		return &mailError{"dial", err}
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", mailer.addr)
	if err != nil {
		return &mailError{"dial", err}
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		_ = conn.Close()
		return &mailError{"dial", err}
	}
	defer func() { _ = client.Close() }()

	// Extension sends EHLO. If the server only knows HELO it lists no extensions, so a required
	// STARTTLS check below fails before any MAIL FROM.
	if offered, _ := client.Extension("STARTTLS"); offered {
		config := &tls.Config{}
		if mailer.tlsConfig != nil {
			config = mailer.tlsConfig.Clone()
		}
		config.ServerName, config.MinVersion = host, tls.VersionTLS12
		if err := client.StartTLS(config); err != nil {
			return &mailError{"tls", err}
		}
	} else if mailer.requireTLS {
		return &mailError{"tls", errors.New("server does not offer STARTTLS")}
	}
	if mailer.requireTLS && (mailer.username == "" || mailer.password == "") {
		return &mailError{"auth", errors.New("credentials are required")}
	}
	offered, _ := client.Extension("AUTH")
	if mailer.requireTLS && !offered {
		return &mailError{"auth", errors.New("server does not offer AUTH")}
	}
	if mailer.username != "" && offered {
		if err := client.Auth(smtp.PlainAuth("", mailer.username, mailer.password, host)); err != nil {
			return &mailError{"auth", err}
		}
	}
	if err := client.Mail(from.Address); err != nil {
		return &mailError{"send", err}
	}
	for _, recipient := range email.To {
		if err := client.Rcpt(recipient); err != nil {
			return &mailError{"send", err}
		}
	}
	writer, err := client.Data()
	if err != nil {
		return &mailError{"send", err}
	}
	if _, err := writer.Write(message); err != nil {
		return &mailError{"send", err}
	}
	if err := writer.Close(); err != nil {
		return &mailError{"send", err}
	}
	return client.Quit()
}

// buildMessage makes a multipart/alternative email with CRLF line endings.
func buildMessage(from *mail.Address, email authboss.Email) ([]byte, error) {
	if strings.ContainsAny(email.Subject, "\r\n") {
		return nil, errors.New("subject has a line break")
	}
	var recipients []string
	for _, recipient := range email.To {
		address, err := mail.ParseAddress(recipient)
		if err != nil {
			return nil, err
		}
		recipients = append(recipients, address.String())
	}
	if email.FromName != "" {
		from = &mail.Address{Name: email.FromName, Address: from.Address}
	}
	var body bytes.Buffer
	parts := multipart.NewWriter(&body)
	for _, part := range []struct{ kind, text string }{{"text/plain", email.TextBody}, {"text/html", email.HTMLBody}} {
		if part.text == "" {
			continue
		}
		writer, err := parts.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {part.kind + "; charset=UTF-8"},
			"Content-Transfer-Encoding": {"quoted-printable"},
		})
		if err != nil {
			return nil, err
		}
		encoder := quotedprintable.NewWriter(writer)
		_, _ = encoder.Write([]byte(part.text))
		_ = encoder.Close()
	}
	if err := parts.Close(); err != nil {
		return nil, err
	}
	var message bytes.Buffer
	fmt.Fprintf(&message, "From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMIME-Version: 1.0\r\nContent-Type: multipart/alternative; boundary=%s\r\n\r\n",
		from.String(), strings.Join(recipients, ", "), mime.QEncoding.Encode("utf-8", email.Subject),
		time.Now().Format(time.RFC1123Z), parts.Boundary())
	message.Write(body.Bytes())
	return message.Bytes(), nil
}

// logger routes authboss's log lines into slog.
type logger struct{}

func (logger) Info(message string)  { slog.Info(message, "component", "authboss") }
func (logger) Error(message string) { slog.Error(message, "component", "authboss") }
