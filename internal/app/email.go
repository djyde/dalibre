package app

import (
	"context"
	"errors"
	"strings"
	"time"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/calibre"
	"dalibre/internal/email"
	"dalibre/internal/settings"
)

// Emailing books to Kindles (Figma "26 · Send to Kindle by email"): a
// stock Kindle reads no OPDS, so Amazon's Send to Kindle address is its
// only wireless way in. Settings › Email holds the Kindles and the account
// mail is sent from; the book menu has Email to each Kindle.

// emailSecret is the credential store's key for the sender's password.
const emailSecret = "email-smtp"

// kindleFormats are the formats Send to Kindle takes, best first, with
// their MIME types. Amazon no longer takes MOBI or AZW3.
var kindleFormats = []struct{ format, contentType string }{
	{"EPUB", "application/epub+zip"},
	{"PDF", "application/pdf"},
	{"DOCX", "application/vnd.openxmlformats-officedocument.wordprocessingml.document"},
	{"DOC", "application/msword"},
	{"RTF", "application/rtf"},
	{"TXT", "text/plain"},
	{"HTML", "text/html"},
	{"HTM", "text/html"},
}

// kindleMaxSize is the largest file Send to Kindle takes.
const kindleMaxSize = 50 << 20

// emailEdit is Settings › Email's fields that aren't settings as they
// are: the port as typed, and the password, read from the store once.
type emailEdit struct {
	loaded   bool
	port, pw string
	// pwLoaded is when pw holds the store's password.
	pwLoaded bool
}

// emailTest is the last test email: sending, or how it went.
type emailTest struct {
	sending bool
	err     error
	to      string
	at      time.Time
}

// emailSecurity is how mail is sent to e's server: as the user chose, else
// by its port.
func emailSecurity(e settings.Email) email.Security {
	if e.Security != "" {
		return email.Security(e.Security)
	}
	if e.ServerPort() == 465 {
		return email.TLS
	}
	return email.StartTLS
}

// emailKindles are the Kindles books can be emailed to now: none until the
// account mail is sent from is set.
func (a *App) emailKindles() []*settings.Kindle {
	e := a.settings.Email
	if strings.TrimSpace(e.Host) == "" || !strings.Contains(e.From, "@") {
		return nil
	}
	var ks []*settings.Kindle
	for _, k := range e.Kindles {
		if strings.Contains(k.Address, "@") {
			ks = append(ks, k)
		}
	}
	return ks
}

// kindleName is what Kindle k is called in the menu.
func kindleName(k settings.Kindle) string {
	if n := strings.TrimSpace(k.Name); n != "" {
		return n
	}
	return strings.TrimSpace(k.Address)
}

// emailServer is the account mail is sent from, with password pw. Any
// goroutine, with a copy of the settings.
func emailServer(e settings.Email, pw string) email.Server {
	return email.Server{Host: strings.TrimSpace(e.Host), Port: e.ServerPort(), Security: emailSecurity(e),
		From: strings.TrimSpace(e.From), Password: pw}
}

// emailPassword is the sender's password: the one in the pane when it was
// read, else the store's. It reads the store, so not on the main thread;
// known and pw are the pane's, taken on the main thread.
func emailPassword(store secretStore, known bool, pw string) string {
	if known || store == nil {
		return pw
	}
	pw, _ = store.Get(emailSecret)
	return pw
}

// saveEmailPassword keeps pw as the sender's password.
func (a *App) saveEmailPassword(pw string) {
	store := a.secrets
	if store == nil {
		return
	}
	a.run(func() {
		var err error
		if pw == "" {
			store.Delete(emailSecret)
		} else {
			err = store.Set(emailSecret, pw)
		}
		if err != nil {
			a.post(func() { a.toastError(errors.New(locales.OpdsKeychainError(err.Error()))) })
		}
	})
}

// kindleMessage is the email sending b to Kindle address to: its file in
// the format Send to Kindle takes best. It says why when b can't go: none
// of its formats is taken (noFormat), or its file is too big. Any
// goroutine.
func kindleMessage(lib *calibre.Library, b *calibre.Book, to string) (m email.Message, noFormat, tooBig bool) {
	d, err := lib.Details(b)
	if err != nil {
		return m, true, false
	}
	for _, kf := range kindleFormats {
		for _, f := range d.Files {
			if f.Format != kf.format {
				continue
			}
			if f.Size > kindleMaxSize {
				return m, false, true
			}
			name := b.Title
			if len(b.Authors) > 0 {
				name += " - " + b.Authors[0]
			}
			ext := "." + strings.ToLower(f.Format)
			return email.Message{To: to, Subject: b.Title, Body: b.Title, Attachment: &email.Attachment{
				Name:        calibre.SafeFileName(name, 100) + ext,
				ASCIIName:   calibre.ASCIIFilename(name) + ext,
				ContentType: kf.contentType,
				Path:        f.Path,
			}}, false, false
		}
	}
	return m, true, false
}

// emailBooks emails the library's books of ids to Kindle k, each in its
// own email, in the sidebar's busy row, which can stop it.
func (a *App) emailBooks(k settings.Kindle, ids []int64) {
	lib := a.lib
	if lib == nil || a.busy != "" {
		return
	}
	var books []*calibre.Book
	for _, id := range ids {
		if b := a.byID[id]; b != nil {
			books = append(books, b)
		}
	}
	if len(books) == 0 {
		return
	}
	name, to := kindleName(k), strings.TrimSpace(k.Address)
	conf, store, known, pw := a.settings.Email, a.secrets, a.emailEdit.pwLoaded, a.emailEdit.pw
	ctx, cancel := context.WithCancel(context.Background())
	a.busy, a.busyDone, a.busyTotal, a.busyStop = locales.EmailSending(name), 0, len(books), cancel
	a.run(func() {
		var msgs []email.Message
		var noFormat, tooBig []string
		for _, b := range books {
			m, nf, big := kindleMessage(lib, b, to)
			switch {
			case nf:
				noFormat = append(noFormat, b.Title)
			case big:
				tooBig = append(tooBig, b.Title)
			default:
				msgs = append(msgs, m)
			}
		}
		a.post(func() { a.busyTotal = len(msgs) })
		var n int
		var err error
		if len(msgs) > 0 {
			srv := emailServer(conf, emailPassword(store, known, pw))
			n, err = email.Send(ctx, srv, msgs, func(done int) { a.post(func() { a.busyDone = done }) })
		}
		cancel()
		a.post(func() {
			a.busy, a.busyStop = "", nil
			if err != nil && !errors.Is(err, context.Canceled) {
				a.toastError(errors.New(locales.EmailFailed(name, err.Error())))
			}
			if len(noFormat) > 0 {
				a.toastError(errors.New(locales.EmailNoFormat(strings.Join(noFormat, ", "))))
			}
			if len(tooBig) > 0 {
				a.toastError(errors.New(locales.EmailTooBig(strings.Join(tooBig, ", "))))
			}
			if n > 0 {
				a.toast("%s", locales.EmailSent(n, name))
			}
		})
	})
}

// sendTestEmail emails the sender's own address, to show that the account
// is set up right. Mail to a Kindle without a book only comes back as an
// error from Amazon.
func (a *App) sendTestEmail() {
	if a.emailTestState.sending {
		return
	}
	conf, store, known, pw := a.settings.Email, a.secrets, a.emailEdit.pwLoaded, a.emailEdit.pw
	to := strings.TrimSpace(conf.From)
	a.emailTestState = emailTest{sending: true, to: to}
	a.run(func() {
		srv := emailServer(conf, emailPassword(store, known, pw))
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_, err := email.Send(ctx, srv, []email.Message{{To: to, Subject: locales.EmailTestSubject(), Body: locales.EmailTestBody()}}, nil)
		a.post(func() { a.emailTestState = emailTest{err: err, to: to, at: time.Now()} })
	})
}
