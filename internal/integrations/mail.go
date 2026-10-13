package integrations

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"time"
)

type Mailer interface {
	Send(context.Context, string, string, string) error
}
type SMTP struct {
	Address, Username, Password, From string
	ImplicitTLS                       bool
	RootCAs                           *x509.CertPool
}

func (s SMTP) Send(ctx context.Context, to, subject, body string) error {
	from, err := mail.ParseAddress(s.From)
	if err != nil {
		return err
	}
	recipient, err := mail.ParseAddress(to)
	if err != nil {
		return err
	}
	host, _, err := net.SplitHostPort(s.Address)
	if err != nil {
		return err
	}
	cfg := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12, RootCAs: s.RootCAs}
	dialer := net.Dialer{Timeout: 10 * time.Second}
	var conn net.Conn
	if s.ImplicitTLS {
		conn, err = (&tls.Dialer{NetDialer: &dialer, Config: cfg}).DialContext(ctx, "tcp", s.Address)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", s.Address)
	}
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(20 * time.Second))
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return err
	}
	defer client.Close()
	if !s.ImplicitTLS {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return fmt.Errorf("SMTP requires TLS")
		}
		if err = client.StartTLS(cfg); err != nil {
			return err
		}
	}
	if s.Username != "" {
		if err = client.Auth(smtp.PlainAuth("", s.Username, s.Password, host)); err != nil {
			return err
		}
	}
	if err = client.Mail(from.Address); err != nil {
		return err
	}
	if err = client.Rcpt(recipient.Address); err != nil {
		return err
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	message := "From: " + from.String() + "\r\nTo: " + recipient.String() + "\r\nSubject: " + mime.QEncoding.Encode("UTF-8", strings.ReplaceAll(strings.ReplaceAll(subject, "\r", " "), "\n", " ")) + "\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n" + strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\n", "\r\n")
	if _, err = writer.Write([]byte(message)); err != nil {
		return err
	}
	if err = writer.Close(); err != nil {
		return err
	}
	return client.Quit()
}
