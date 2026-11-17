package app

import (
	"net"
	"net/textproto"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"

	"dalibre/internal/calibre"
)

// mailbox is a local SMTP server without TLS or sign-in that keeps what
// it is sent, by recipient.
type mailbox struct {
	port int
	mu   sync.Mutex
	got  map[string][]string
}

func newMailbox(t *testing.T) *mailbox {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	mb := &mailbox{port: ln.Addr().(*net.TCPAddr).Port, got: map[string][]string{}}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				tp := textproto.NewConn(conn)
				tp.PrintfLine("220 fake")
				var to string
				for {
					line, err := tp.ReadLine()
					if err != nil {
						return
					}
					switch cmd := strings.ToUpper(strings.Fields(line + " x")[0]); cmd {
					case "RCPT":
						to = strings.Trim(line[len("RCPT TO:"):], " <>")
						tp.PrintfLine("250 ok")
					case "DATA":
						tp.PrintfLine("354 go on")
						b, _ := tp.ReadDotBytes()
						mb.mu.Lock()
						mb.got[to] = append(mb.got[to], string(b))
						mb.mu.Unlock()
						tp.PrintfLine("250 ok")
					case "QUIT":
						tp.PrintfLine("221 bye")
						return
					default:
						tp.PrintfLine("250 ok")
					}
				}
			}()
		}
	}()
	return mb
}

func (mb *mailbox) to(addr string) []string {
	mb.mu.Lock()
	defer mb.mu.Unlock()
	return mb.got[addr]
}

// TestEmailToKindle sets up a Kindle and the sender in Settings › Email,
// sends a test email to the sender, then emails a book from its menu.
func TestEmailToKindle(t *testing.T) {
	mb := newMailbox(t)
	a := newTestApp(t)
	a.secrets = memorySecrets{}
	tt := ui.NewTester(a.View, 1440, 900)
	tt.Frame()
	if err := tt.RightClick("Dune"); err != nil {
		t.Fatal(err)
	}
	for _, it := range tt.Menu() {
		if strings.HasPrefix(it, "Email to") {
			t.Errorf("menu has %q before email is set up", it)
		}
	}
	tt.CloseMenu()

	a.openSettings(sectionEmail)
	tt.Frame()
	if err := tt.Click("Add Kindle"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	for _, f := range []struct{ field, text string }{
		{"Name", "Paperwhite"}, {"Send to Kindle address", "pw3@kindle.com"},
		{"SMTP server", "127.0.0.1"}, {"Port", strconv.Itoa(mb.port)}, {"Email", "me@example.com"},
	} {
		in, ok := tt.AccessByRole(ui.RoleTextField, f.field)
		if !ok {
			t.Fatalf("no field %q; texts %q", f.field, tt.Texts())
		}
		tt.ClickAt(in.Bounds.X+in.Bounds.W/2, in.Bounds.Y+in.Bounds.H/2)
		tt.Type(f.text)
		tt.Frame()
	}
	a.settings.Email.Security = "none" // the fake server has no TLS
	tt.Frame()
	if err := tt.Click("Send a test email"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(mb.to("me@example.com")) != 1 {
		t.Fatalf("test email not sent to the sender; status %v, texts %q", a.emailTestState.err, tt.Texts())
	}
	if !hasTextPrefix(tt, "Sent to me@example.com") {
		t.Errorf("test email's status not shown; texts %q", tt.Texts())
	}
	shot(t, tt, "email-settings")
	if k := a.settings.Email.Kindles; len(k) != 1 || k[0].Name != "Paperwhite" || k[0].Address != "pw3@kindle.com" || a.settings.Email.Port != mb.port {
		t.Fatalf("settings not kept: %+v", a.settings.Email)
	}

	a.settingsOpen = false
	tt.Frame()
	if err := tt.RightClick("Dune"); err != nil {
		t.Fatal(err)
	}
	shot(t, tt, "email-menu")
	if err := tt.ChooseMenuItem("Email to Paperwhite"); err != nil {
		t.Fatalf("menu %q: %v", tt.Menu(), err)
	}
	tt.Frame()
	got := mb.to("pw3@kindle.com")
	if len(got) != 1 || !strings.Contains(got[0], `filename="Dune - Frank Herbert.epub"`) || !strings.Contains(got[0], "application/epub+zip") {
		t.Fatalf("Kindle got %d mails: %q", len(got), got)
	}
	if !tt.HasText("Emailed 1 book to Paperwhite") || a.busy != "" {
		t.Errorf("no toast or still busy (%q); texts %q", a.busy, tt.Texts())
	}
}

// TestKindleMessage takes EPUB before PDF, and leaves out a book with only
// formats Send to Kindle refuses.
func TestKindleMessage(t *testing.T) {
	a := newTestApp(t)
	add := func(title string, exts ...string) *calibre.Book {
		var id int64
		for i, ext := range exts {
			f := filepath.Join(t.TempDir(), "book."+ext)
			os.WriteFile(f, []byte("x"), 0o644)
			var err error
			if i == 0 {
				id, err = a.lib.AddBook(calibre.NewBook{File: f, Metadata: calibre.Metadata{Title: title, Authors: []string{"A"}}})
			} else {
				err = a.lib.AddFormat(id, f)
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		b, err := a.lib.Book(id)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	m, noFormat, _ := kindleMessage(a.lib, add("Both", "pdf", "epub"), "k@kindle.com")
	if noFormat || m.Attachment == nil || !strings.HasSuffix(m.Attachment.Path, ".epub") {
		t.Errorf("PDF and EPUB: sent %+v, want the EPUB", m.Attachment)
	}
	if _, noFormat, _ := kindleMessage(a.lib, add("Kindle only", "azw3", "mobi"), "k@kindle.com"); !noFormat {
		t.Error("AZW3 and MOBI only: would be sent, but Send to Kindle refuses both")
	}
}

func hasTextPrefix(tt *ui.Tester, prefix string) bool {
	for _, s := range tt.Texts() {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}
