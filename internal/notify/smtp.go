package notify

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"regexp"
	"strings"
	"time"
)

// SMTP sends mail through a relay.
//
// Auth is optional: university relays commonly accept mail from known hosts
// without credentials, so an empty Username means no AUTH is attempted rather
// than an anonymous login.
type SMTP struct {
	Host     string // host:port
	From     string
	Username string
	Password string
}

var _ Notifier = (*SMTP)(nil)

// ErrRejected marks a recipient the relay refused permanently, such as a
// mailbox that no longer exists. Sending again would be refused the same way.
var ErrRejected = errors.New("recipient rejected")

func (s *SMTP) Notify(ctx context.Context, msg Message) error {
	host, _, err := net.SplitHostPort(s.Host)
	if err != nil {
		return fmt.Errorf("invalid SMTP host %q: %w", s.Host, err)
	}

	body, err := buildMessage(s.From, msg, time.Now())
	if err != nil {
		return err
	}

	// Bound the whole exchange so a hung relay cannot stall the caller.
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := s.send(ctx, host, msg.To, body); err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return fmt.Errorf("send mail to %s: %w", msg.To, err)
	}
	return nil
}

// send does what smtp.SendMail does, step by step, so that a refusal of the
// recipient can be told apart from one of the sender or the login. Those mean
// the relay is misconfigured, and must stay retried.
func (s *SMTP) send(ctx context.Context, host, to string, body []byte) error {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", s.Host)
	if err != nil {
		return err
	}
	// smtp.Client has no context support; unblock it when ctx ends.
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Now()) })
	defer stop()

	c, err := smtp.NewClient(conn, host)
	if err != nil {
		conn.Close()
		return err
	}
	defer c.Close()

	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(&tls.Config{ServerName: host}); err != nil {
			return err
		}
	}
	if s.Username != "" {
		if ok, _ := c.Extension("AUTH"); !ok {
			return errors.New("relay does not offer AUTH")
		}
		if err := c.Auth(smtp.PlainAuth("", s.Username, s.Password, host)); err != nil {
			return err
		}
	}
	if err := c.Mail(s.From); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		if rejected(err) {
			return fmt.Errorf("%w: %w", ErrRejected, err)
		}
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(body); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	// The relay has accepted the mail. A failed QUIT must not make the
	// reminder send it again.
	_ = c.Quit()
	return nil
}

// rejected reports whether err is a reply that identifies the recipient as
// gone: a permanent refusal with an enhanced status in the address class,
// 5.1.x (RFC 3463), such as "550 5.1.1 User unknown". Any other 5xx stays
// retried, since relays answer policy refusals at RCPT TO as well, for
// instance "554 5.7.1 Relay access denied" from a relay that no longer trusts
// this host: dropping those would lose every notice. 5.1.0 is left out, as
// Postfix uses it for a rejected sender.
func rejected(err error) bool {
	var tp *textproto.Error
	if !errors.As(err, &tp) || tp.Code < 500 || tp.Code > 599 {
		return false
	}
	m := enhancedStatus.FindStringSubmatch(tp.Msg)
	return m != nil && m[1] != "0"
}

// enhancedStatus matches an RFC 3463 address-status code at the start of a
// reply, capturing its detail.
var enhancedStatus = regexp.MustCompile(`^5\.1\.(\d{1,3})(?:\s|$)`)

// buildMessage writes msg as an RFC 5322 message.
//
// Headers must be ASCII, since SMTPUTF8 is only used when the relay offers it,
// so the subject is sent as RFC 2047 encoded-words: raw UTF-8 there
// ("Schlüssel") shows garbled in some clients. The body is quoted-printable
// for the same reason. Date and Message-ID are set here rather than left to the relay,
// because spam filters score their absence.
func buildMessage(from string, msg Message, now time.Time) ([]byte, error) {
	// To comes from the identity provider. A line break in it would let it
	// add headers of its own.
	for _, v := range []string{from, msg.To} {
		if strings.ContainsAny(v, "\r\n") {
			return nil, fmt.Errorf("line break in mail address %q", v)
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", msg.To)
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("UTF-8", msg.Subject))
	fmt.Fprintf(&b, "Date: %s\r\n", now.Format(time.RFC1123Z))
	fmt.Fprintf(&b, "Message-ID: %s\r\n", messageID(from))
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("Content-Transfer-Encoding: quoted-printable\r\n")
	b.WriteString("\r\n")

	qp := quotedprintable.NewWriter(&b)
	if _, err := qp.Write([]byte(msg.Body)); err != nil {
		return nil, err
	}
	if err := qp.Close(); err != nil {
		return nil, err
	}
	return []byte(b.String()), nil
}

// messageID returns a unique Message-ID in the sender's domain.
func messageID(from string) string {
	domain := "localhost"
	if addr, err := mail.ParseAddress(from); err == nil {
		if i := strings.LastIndex(addr.Address, "@"); i >= 0 {
			domain = addr.Address[i+1:]
		}
	}
	b := make([]byte, 16)
	// Never fails since Go 1.24; a broken entropy source crashes the process.
	_, _ = rand.Read(b)
	return "<" + hex.EncodeToString(b) + "@" + domain + ">"
}
