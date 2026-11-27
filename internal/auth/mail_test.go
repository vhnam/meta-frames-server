package auth

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"io"
	"log/slog"
	"math/big"
	"mime"
	"mime/multipart"
	"net"
	stdmail "net/mail"
	"net/textproto"
	"strings"
	"sync"
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

func TestVerificationEmail(test *testing.T) {
	link := "https://api.example/auth/verify-email#token=abc"
	email, err := verificationEmail("no-reply@example.com", "ansel@example.com", link)
	if err != nil {
		test.Fatal(err)
	}
	if len(email.To) != 1 || email.To[0] != "ansel@example.com" {
		test.Fatalf("To = %v", email.To)
	}
	for name, body := range map[string]string{"text": email.TextBody, "html": email.HTMLBody} {
		if !strings.Contains(body, link) || !strings.Contains(body, "24 hours") {
			test.Fatalf("%s body lacks the link or expiry: %q", name, body)
		}
		if strings.Contains(body, "hunter2") {
			test.Fatalf("%s body holds a password", name)
		}
	}
}

type failingMailer struct{ err error }

func (mailer failingMailer) Send(context.Context, authboss.Email) error { return mailer.err }

// lockedBuffer lets a test read the log while a send goroutine writes it.
type lockedBuffer struct {
	lock   sync.Mutex
	buffer bytes.Buffer
}

func (output *lockedBuffer) Write(data []byte) (int, error) {
	output.lock.Lock()
	defer output.lock.Unlock()
	return output.buffer.Write(data)
}

func (output *lockedBuffer) String() string {
	output.lock.Lock()
	defer output.lock.Unlock()
	return output.buffer.String()
}

func captureLog(test *testing.T) *lockedBuffer {
	output := &lockedBuffer{}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(output, nil)))
	test.Cleanup(func() { slog.SetDefault(previous) })
	return output
}

func TestAsyncMailerFailureLogHasNoRecipient(test *testing.T) {
	output := captureLog(test)
	failure := &textproto.Error{Code: 550, Msg: "rejected ansel@example.com"}
	email := authboss.Email{To: []string{"ansel@example.com"}, Subject: "s", TextBody: "body-text-secret"}
	if err := (AsyncMailer{Mailer: failingMailer{failure}}).Send(context.Background(), email); err != nil {
		test.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for !strings.Contains(output.String(), "sending email failed") {
		if time.Now().After(deadline) {
			test.Fatal("no failure log")
		}
		time.Sleep(time.Millisecond)
	}
	logged := output.String()
	if strings.Contains(logged, "ansel@example.com") || strings.Contains(logged, "body-text-secret") {
		test.Fatalf("log = %s", logged)
	}
}

func TestAsyncMailerFailureLogOmitsProviderEcho(test *testing.T) {
	output := captureLog(test)
	const link = "https://api.example/auth/verify-email#token=sample-verification-token"
	const body = "synthetic email body " + link
	mailer := AsyncMailer{Mailer: failingMailer{err: &textproto.Error{Code: 554, Msg: body}}}
	if err := mailer.Send(test.Context(), authboss.Email{Subject: "verification", TextBody: body}); err != nil {
		test.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		logged := output.String()
		if strings.Contains(logged, "sending email failed") {
			if strings.Contains(logged, "sample-verification-token") || strings.Contains(logged, "synthetic email body") {
				test.Fatal("mail failure log contains the verification token or email body")
			}
			return
		}
		if time.Now().After(deadline) {
			test.Fatal("no failure log")
		}
		time.Sleep(time.Millisecond)
	}
}

type blockingMailer struct{ release chan struct{} }

func (mailer blockingMailer) Send(context.Context, authboss.Email) error {
	<-mailer.release
	return nil
}

func TestAsyncMailerDropsWhenSlotsAreBusy(test *testing.T) {
	output := captureLog(test)
	inner := blockingMailer{release: make(chan struct{})}
	mailer := AsyncMailer{Mailer: inner}
	for range cap(mailSlots) {
		if err := mailer.Send(context.Background(), authboss.Email{Subject: "s"}); err != nil {
			test.Fatal(err)
		}
	}
	returned := make(chan error, 1)
	go func() {
		returned <- mailer.Send(context.Background(), authboss.Email{To: []string{"ansel@example.com"}, Subject: "extra"})
	}()
	select {
	case err := <-returned:
		if err != nil {
			test.Fatal(err)
		}
	case <-time.After(time.Second):
		test.Fatal("Send blocked with all slots busy")
	}
	logged := output.String()
	if !strings.Contains(logged, "dropping email") || strings.Contains(logged, "ansel@example.com") {
		test.Fatalf("log = %s", logged)
	}
	close(inner.release)
	for len(mailSlots) > 0 {
		time.Sleep(time.Millisecond)
	}
}

// fakeSMTP is a loopback SMTP server for the transport tests.
type fakeSMTP struct {
	addr                string
	offerTLS, offerAuth bool
	stall               bool
	cert                tls.Certificate
	lock                sync.Mutex
	sawMail             bool
	authLine            string
	data                string
}

func (server *fakeSMTP) snapshot() (sawMail bool, authLine, data string) {
	server.lock.Lock()
	defer server.lock.Unlock()
	return server.sawMail, server.authLine, server.data
}

func startFakeSMTP(test *testing.T, server *fakeSMTP) *x509.CertPool {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		test.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "fake"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		test.Fatal(err)
	}
	server.cert = tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	parsed, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(parsed)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		test.Fatal(err)
	}
	server.addr = listener.Addr().String()
	test.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go server.serve(conn)
		}
	}()
	return pool
}

func (server *fakeSMTP) serve(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	if server.stall {
		_, _ = io.Copy(io.Discard, conn) // accept, never reply
		return
	}
	reader := bufio.NewReader(conn)
	reply := func(text string) { _, _ = conn.Write([]byte(text + "\r\n")) }
	reply("220 fake ready")
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "EHLO"):
			extensions := ""
			if server.offerTLS {
				extensions += "250-STARTTLS\r\n"
			}
			if server.offerAuth {
				extensions += "250-AUTH PLAIN\r\n"
			}
			reply("250-fake\r\n" + extensions + "250 8BITMIME")
		case line == "STARTTLS":
			reply("220 go ahead")
			secured := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{server.cert}})
			if secured.Handshake() != nil {
				return
			}
			conn, reader = secured, bufio.NewReader(secured)
		case strings.HasPrefix(line, "AUTH PLAIN"):
			server.lock.Lock()
			server.authLine = line
			server.lock.Unlock()
			reply("235 ok")
		case strings.HasPrefix(line, "MAIL FROM"):
			server.lock.Lock()
			server.sawMail = true
			server.lock.Unlock()
			reply("250 ok")
		case strings.HasPrefix(line, "RCPT"):
			reply("250 ok")
		case line == "DATA":
			reply("354 go")
			var data strings.Builder
			for {
				text, err := reader.ReadString('\n')
				if err != nil || text == ".\r\n" {
					break
				}
				data.WriteString(text)
			}
			server.lock.Lock()
			server.data = data.String()
			server.lock.Unlock()
			reply("250 queued")
		case line == "QUIT":
			reply("221 bye")
			return
		}
	}
}

func testEmail() authboss.Email {
	return authboss.Email{To: []string{"ansel@example.com"}, From: "hello@example.com", FromName: "Meta-Frame", Subject: "[Meta-Frame] hi", TextBody: "plain body https://api.example/auth/verify-email#token=abc", HTMLBody: "<p>html body</p>"}
}

func TestSMTPMailerRequiresTLSAndAuthBeforeMailFrom(test *testing.T) {
	cases := map[string]*fakeSMTP{
		"no STARTTLS": {offerAuth: true},
		"no AUTH":     {offerTLS: true},
	}
	for name, server := range cases {
		test.Run(name, func(test *testing.T) {
			pool := startFakeSMTP(test, server)
			mailer := &smtpMailer{addr: server.addr, username: "user", password: "pw", requireTLS: true, tlsConfig: &tls.Config{RootCAs: pool}}
			if err := mailer.Send(test.Context(), testEmail()); err == nil {
				test.Fatal("Send succeeded")
			}
			if sawMail, _, _ := server.snapshot(); sawMail {
				test.Fatal("server saw MAIL FROM")
			}
		})
	}
}

func TestSMTPMailerRequiresCredentialsInProduction(test *testing.T) {
	server := &fakeSMTP{offerTLS: true, offerAuth: true}
	pool := startFakeSMTP(test, server)
	mailer := &smtpMailer{addr: server.addr, requireTLS: true, tlsConfig: &tls.Config{RootCAs: pool}}
	ctx, cancel := context.WithTimeout(test.Context(), 3*time.Second)
	defer cancel()
	err := mailer.Send(ctx, testEmail())
	if sawMail, _, _ := server.snapshot(); sawMail {
		test.Fatal("server received MAIL FROM without credentials")
	}
	if err == nil {
		test.Fatal("Send accepted a configuration without credentials")
	}
}

func TestSMTPMailerDeliversOverTLSWithAuth(test *testing.T) {
	server := &fakeSMTP{offerTLS: true, offerAuth: true}
	pool := startFakeSMTP(test, server)
	mailer := &smtpMailer{addr: server.addr, username: "user", password: "pw", requireTLS: true, tlsConfig: &tls.Config{RootCAs: pool}}
	if err := mailer.Send(test.Context(), testEmail()); err != nil {
		test.Fatal(err)
	}
	_, authLine, data := server.snapshot()
	credentials, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(authLine, "AUTH PLAIN "))
	if string(credentials) != "\x00user\x00pw" {
		test.Fatalf("auth line = %q", authLine)
	}
	message, err := stdmail.ReadMessage(strings.NewReader(data))
	if err != nil {
		test.Fatal(err)
	}
	_, params, _ := mime.ParseMediaType(message.Header.Get("Content-Type"))
	reader := multipart.NewReader(message.Body, params["boundary"])
	var bodies []string
	for {
		part, err := reader.NextPart()
		if err != nil {
			break
		}
		text, _ := io.ReadAll(part)
		bodies = append(bodies, string(text))
	}
	if len(bodies) != 2 || !strings.Contains(bodies[0], "token=abc") || !strings.Contains(bodies[1], "html body") {
		test.Fatalf("parts = %q", bodies)
	}
	if !strings.Contains(message.Header.Get("To"), "ansel@example.com") {
		test.Fatalf("To = %q", message.Header.Get("To"))
	}
}

func TestSMTPMailerRefusesUntrustedCertificate(test *testing.T) {
	server := &fakeSMTP{offerTLS: true, offerAuth: true}
	startFakeSMTP(test, server)
	mailer := &smtpMailer{addr: server.addr, username: "user", password: "pw", requireTLS: true}
	if err := mailer.Send(test.Context(), testEmail()); err == nil {
		test.Fatal("Send trusted an unknown certificate")
	}
	if sawMail, _, _ := server.snapshot(); sawMail {
		test.Fatal("server saw MAIL FROM")
	}
}

// AsyncMailer frees its slot with a defer when the inner send returns, so a bounded send frees it.
func TestStalledSMTPServerReturnsAtTheDeadline(test *testing.T) {
	server := &fakeSMTP{stall: true}
	startFakeSMTP(test, server)
	mailer := &smtpMailer{addr: server.addr}
	ctx, cancel := context.WithTimeout(test.Context(), 200*time.Millisecond)
	defer cancel()
	started := time.Now()
	err := mailer.Send(ctx, testEmail())
	if err == nil || time.Since(started) > 3*time.Second {
		test.Fatalf("Send = %v after %s, want a timeout error", err, time.Since(started))
	}
	if category, _ := classifyMailError(err); category != "timeout" {
		test.Fatalf("category = %q", category)
	}
}
