// Package notify provides email sender
package notify

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"mime/quotedprintable"
	"net"
	"net/smtp"
	"strings"
	"time"

	log "github.com/go-pkgz/lgr"
	"github.com/pkg/errors"
)

// Email implements sender interface for VerifyHandler
// Uses common subject line and "from" for all messages
type Email struct {
	SMTPClient
	EmailParams
}

// EmailParams  with all needed to make new Email client with smtp
type EmailParams struct {
	Host         string // SMTP host
	Port         int    // SMTP port
	From         string // From email field
	ContentType  string // Content type, optional. Will trigger MIME and Content-Type headers
	To           []string
	TLS          bool   // TLS auth
	SMTPUserName string // user name
	SMTPPassword string // password
	TimeOut      time.Duration

	OnError      bool
	OnCompletion bool
}

// SMTPClient interface defines subset of net/smtp used by email client
type SMTPClient interface {
	Mail(string) error
	Auth(smtp.Auth) error
	Rcpt(string) error
	Data() (io.WriteCloser, error)
	Quit() error
	Close() error
}

// NewEmailClient creates email client with prepared smtp
func NewEmailClient(p EmailParams) *Email {
	return &Email{EmailParams: p, SMTPClient: nil}
}

// Send email with given text and subject
// If SMTPClient defined in Email struct it will be used, if not - new smtp.Client on each send.
// Always closes client on completion or failure.
//nolint gocyclo
func (em *Email) Send(subj, text string) error {
	log.Printf("[DEBUG] send %q to %+v", subj, em.To)
	client := em.SMTPClient
	if client == nil { // if client not set make new net/smtp
		c, err := em.client()
		if err != nil {
			return errors.Wrap(err, "failed to makre smtp client")
		}
		client = c
	}

	var quit bool
	defer func() {
		if quit { // quit set if Quit() call passed because it's closing connection as well.
			return
		}
		if err := client.Close(); err != nil {
			log.Printf("[WARN] can't close smtp connection, %v", err)
		}
	}()

	if em.SMTPUserName != "" && em.SMTPPassword != "" {
		auth := smtp.PlainAuth("", em.SMTPUserName, em.SMTPPassword, em.Host)
		if err := client.Auth(auth); err != nil {
			return errors.Wrapf(err, "failed to auth to smtp %s:%d", em.Host, em.Port)
		}
	}

	if err := client.Mail(em.From); err != nil {
		return errors.Wrapf(err, "bad from address %q", em.From)
	}

	for _, rcpt := range em.To {
		if err := client.Rcpt(rcpt); err != nil {
			return errors.Wrapf(err, "bad to address %q", em.To)
		}
	}

	writer, err := client.Data()
	if err != nil {
		return errors.Wrap(err, "can't make email writer")
	}

	msg, err := em.buildMessage(subj, text)
	if err != nil {
		return errors.Wrap(err, "can't make email message")
	}
	buf := bytes.NewBufferString(msg)
	if _, err = buf.WriteTo(writer); err != nil {
		return errors.Wrapf(err, "failed to send email body to %q", em.To)
	}
	if err = writer.Close(); err != nil {
		log.Printf("[WARN] can't close smtp body writer, %v", err)
	}

	if err = client.Quit(); err != nil {
		log.Printf("[WARN] failed to send quit command to %s:%d, %v", em.Host, em.Port, err)
	} else {
		quit = true
	}
	return nil
}

// IsOnError status enabling on-error notification
func (em *Email) IsOnError() bool { return em.OnError }

// IsOnCompletion status enabling on-passed notification
func (em *Email) IsOnCompletion() bool { return em.OnCompletion }

func (em *Email) client() (c *smtp.Client, err error) {
	srvAddress := fmt.Sprintf("%s:%d", em.Host, em.Port)
	if em.TLS {
		tlsConf := &tls.Config{
			InsecureSkipVerify: false,
			ServerName:         em.Host,
		}
		conn, e := tls.Dial("tcp", srvAddress, tlsConf)
		if e != nil {
			return nil, errors.Wrapf(e, "failed to dial smtp tls to %s", srvAddress)
		}
		if c, err = smtp.NewClient(conn, em.Host); err != nil {
			return nil, errors.Wrapf(err, "failed to make smtp client for %s", srvAddress)
		}
		return c, nil
	}

	conn, err := net.DialTimeout("tcp", srvAddress, em.TimeOut)
	if err != nil {
		return nil, errors.Wrapf(err, "timeout connecting to %s", srvAddress)
	}

	c, err = smtp.NewClient(conn, em.Host)
	if err != nil {
		return nil, errors.Wrap(err, "failed to dial")
	}
	return c, nil
}

func (em *Email) buildMessage(subj, msg string) (message string, err error) {
	addHeader := func(msg, h, v string) string {
		msg += fmt.Sprintf("%s: %s\n", h, v)
		return msg
	}
	message = addHeader(message, "From", em.From)
	message = addHeader(message, "To", strings.Join(em.To, ","))
	message = addHeader(message, "Subject", subj)
	message = addHeader(message, "Content-Transfer-Encoding", "quoted-printable")

	if em.ContentType != "" {
		message = addHeader(message, "MIME-version", "1.0")
		message = addHeader(message, "Content-Type", em.ContentType+`; charset="UTF-8"`)
	}
	message = addHeader(message, "Date", time.Now().Format(time.RFC1123Z))

	buff := &bytes.Buffer{}
	qp := quotedprintable.NewWriter(buff)
	if _, err := qp.Write([]byte(msg)); err != nil {
		return "", err
	}
	// flush now, must NOT use defer, for small body, defer may cause buff.String() got empty body
	if err := qp.Close(); err != nil {
		return "", errors.Wrapf(err, "quotedprintable Write failed")
	}
	m := buff.String()
	message += "\n" + m
	return message, nil
}
