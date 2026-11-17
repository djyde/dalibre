// Package email sends mail over SMTP, with a file attached: books to a
// Kindle's address, as calibre's Sharing books by email does.
package email

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/smtp"
	"net/textproto"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Security is how the connection to the server is encrypted.
type Security string

const (
	// StartTLS connects in the clear and upgrades (port 587).
	StartTLS Security = "starttls"
	// TLS connects encrypted from the start (port 465).
	TLS Security = "tls"
	// None never encrypts; the server may then refuse the password.
	None Security = "none"
)

// Server is the account mail is sent from.
type Server struct {
	Host     string
	Port     int
	Security Security
	// From is the sender's address, also the username signed in with.
	From     string
	Password string
}

// Message is one email.
type Message struct {
	To, Subject, Body string
	// Attachment is the file sent with it, when there is one.
	Attachment *Attachment
}

// Attachment is a file attached to a message.
type Attachment struct {
	// Name is the file's name in the mail; ASCIIName the same for mail
	// readers that don't read names in UTF-8.
	Name, ASCIIName string
	ContentType     string
	Path            string
}

// timeout is how long one step with the server, as sending a file, may
// take.
const timeout = 5 * time.Minute

// Send sends msgs over one connection, in order. progress, when not nil,
// is told how many are sent. It stops at the first error, returning how
// many were sent before it.
func Send(ctx context.Context, s Server, msgs []Message, progress func(done int)) (int, error) {
	c, err := dial(ctx, s)
	if err != nil {
		return 0, err
	}
	defer c.Close()
	stop := context.AfterFunc(ctx, func() { c.Close() })
	defer stop()
	for i, m := range msgs {
		if err := send(c, s.From, m); err != nil {
			if ctx.Err() != nil {
				err = ctx.Err()
			}
			return i, err
		}
		if progress != nil {
			progress(i + 1)
		}
	}
	c.Quit()
	return len(msgs), nil
}

// dial connects and signs in to s.
func dial(ctx context.Context, s Server) (*smtp.Client, error) {
	addr := net.JoinHostPort(s.Host, strconv.Itoa(s.Port))
	d := &net.Dialer{Timeout: 30 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	conn = deadlineConn{conn}
	cfg := &tls.Config{ServerName: s.Host}
	if s.Security == TLS {
		conn = tls.Client(conn, cfg)
	}
	c, err := smtp.NewClient(conn, s.Host)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if err := c.Hello(localName()); err != nil {
		c.Close()
		return nil, err
	}
	if s.Security == StartTLS {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			c.Close()
			return nil, errors.New("the server doesn’t offer STARTTLS")
		}
		if err := c.StartTLS(cfg); err != nil {
			c.Close()
			return nil, err
		}
	}
	if s.Password != "" {
		if err := c.Auth(auth(c, s)); err != nil {
			c.Close()
			return nil, err
		}
	}
	return c, nil
}

// deadlineConn gives each read and write its own deadline, so a server
// that stops answering fails the send instead of hanging it.
type deadlineConn struct{ net.Conn }

func (c deadlineConn) Read(p []byte) (int, error) {
	c.SetReadDeadline(time.Now().Add(timeout))
	return c.Conn.Read(p)
}

func (c deadlineConn) Write(p []byte) (int, error) {
	c.SetWriteDeadline(time.Now().Add(timeout))
	return c.Conn.Write(p)
}

// localName is the name said in HELO; net/smtp's "localhost" is refused by
// some servers.
func localName() string {
	h, err := os.Hostname()
	if err != nil || h == "" || strings.ContainsAny(h, " \t") {
		return "localhost"
	}
	return h
}

// auth signs in with PLAIN, or LOGIN when the server only takes that
// (Outlook).
func auth(c *smtp.Client, s Server) smtp.Auth {
	_, mechs := c.Extension("AUTH")
	if !strings.Contains(" "+strings.ToUpper(mechs)+" ", " PLAIN ") && strings.Contains(strings.ToUpper(mechs), "LOGIN") {
		return loginAuth{s.From, s.Password, s.Host}
	}
	return smtp.PlainAuth("", s.From, s.Password, s.Host)
}

type loginAuth struct{ user, password, host string }

func (a loginAuth) Start(info *smtp.ServerInfo) (string, []byte, error) {
	if !info.TLS && info.Name != "localhost" && info.Name != "127.0.0.1" {
		return "", nil, errors.New("unencrypted connection")
	}
	if info.Name != a.host {
		return "", nil, errors.New("wrong host name")
	}
	return "LOGIN", nil, nil
}

func (a loginAuth) Next(from []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	switch strings.ToLower(strings.TrimSpace(string(from))) {
	case "username:":
		return []byte(a.user), nil
	case "password:":
		return []byte(a.password), nil
	}
	return nil, fmt.Errorf("unexpected server challenge %q", from)
}

// send sends m from from.
func send(c *smtp.Client, from string, m Message) error {
	if err := c.Mail(from); err != nil {
		return err
	}
	if err := c.Rcpt(m.To); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if err := Write(w, from, m); err != nil {
		w.Close()
		return err
	}
	return w.Close()
}

// Write writes m from from as a MIME message: its text, and its
// attachment in base64.
func Write(w io.Writer, from string, m Message) error {
	mw := multipart.NewWriter(w)
	var h strings.Builder
	header := func(k, v string) { fmt.Fprintf(&h, "%s: %s\r\n", k, v) }
	header("From", from)
	header("To", m.To)
	header("Subject", mime.QEncoding.Encode("utf-8", m.Subject))
	header("Date", time.Now().Format(time.RFC1123Z))
	header("Message-ID", messageID(from))
	header("MIME-Version", "1.0")
	header("Content-Type", `multipart/mixed; boundary="`+mw.Boundary()+`"`)
	h.WriteString("\r\n")
	if _, err := io.WriteString(w, h.String()); err != nil {
		return err
	}
	part, err := mw.CreatePart(textproto.MIMEHeader{
		"Content-Type":              {"text/plain; charset=utf-8"},
		"Content-Transfer-Encoding": {"base64"},
	})
	if err != nil {
		return err
	}
	if err := writeBase64(part, strings.NewReader(m.Body)); err != nil {
		return err
	}
	if a := m.Attachment; a != nil {
		f, err := os.Open(a.Path)
		if err != nil {
			return err
		}
		defer f.Close()
		part, err := mw.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {mime.FormatMediaType(a.ContentType, map[string]string{"name": a.ASCIIName})},
			"Content-Disposition":       {attachmentDisposition(a)},
			"Content-Transfer-Encoding": {"base64"},
		})
		if err != nil {
			return err
		}
		if err := writeBase64(part, f); err != nil {
			return err
		}
	}
	return mw.Close()
}

// attachmentDisposition names the file twice: in ASCII for every mail
// reader, and in UTF-8 (RFC 2231) for those that read it, which prefer it.
func attachmentDisposition(a *Attachment) string {
	d := mime.FormatMediaType("attachment", map[string]string{"filename": a.ASCIIName})
	if a.Name != a.ASCIIName {
		d += "; filename*=UTF-8''" + strings.ReplaceAll(url.PathEscape(a.Name), "+", "%2B")
	}
	return d
}

// writeBase64 writes r to w in base64, in lines of 76.
func writeBase64(w io.Writer, r io.Reader) error {
	buf := make([]byte, 57*1024) // 57 bytes make one line of 76
	line := make([]byte, base64.StdEncoding.EncodedLen(len(buf))+2*len(buf)/57)
	for {
		n, err := io.ReadFull(r, buf)
		if n > 0 {
			out := line[:0]
			for i := 0; i < n; i += 57 {
				chunk := buf[i:min(i+57, n)]
				out = base64.StdEncoding.AppendEncode(out, chunk)
				out = append(out, '\r', '\n')
			}
			if _, werr := w.Write(out); werr != nil {
				return werr
			}
		}
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func messageID(from string) string {
	b := make([]byte, 12)
	rand.Read(b)
	domain := "dalibre.local"
	if _, d, ok := strings.Cut(from, "@"); ok && d != "" {
		domain = d
	}
	return "<" + hex.EncodeToString(b) + "@" + domain + ">"
}
