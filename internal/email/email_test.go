package email

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/mail"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// received is a message the fake server took.
type received struct {
	to   string
	data string
}

// fakeSMTP serves SMTP on a local port without TLS or sign-in, refusing
// recipients in refuse. It returns its port and the messages it took,
// read once the client quit.
func fakeSMTP(t *testing.T, refuse string) (int, <-chan []received) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	out := make(chan []received, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		tp := textproto.NewConn(conn)
		var got []received
		var to string
		defer func() { out <- got }()
		tp.PrintfLine("220 fake ESMTP")
		for {
			line, err := tp.ReadLine()
			if err != nil {
				return
			}
			cmd := strings.ToUpper(strings.Fields(line + " x")[0])
			switch cmd {
			case "EHLO", "HELO":
				tp.PrintfLine("250 fake")
			case "MAIL":
				tp.PrintfLine("250 ok")
			case "RCPT":
				to = strings.Trim(strings.TrimPrefix(line[len("RCPT TO:"):], " "), "<>")
				if to == refuse {
					tp.PrintfLine("550 no such user")
					continue
				}
				tp.PrintfLine("250 ok")
			case "DATA":
				tp.PrintfLine("354 go on")
				b, err := tp.ReadDotBytes()
				if err != nil {
					return
				}
				got = append(got, received{to, string(b)})
				tp.PrintfLine("250 ok")
			case "QUIT":
				tp.PrintfLine("221 bye")
				return
			default:
				tp.PrintfLine("502 no")
			}
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port, out
}

func TestSendAttachesTheBook(t *testing.T) {
	port, out := fakeSMTP(t, "")
	// Bytes base64 has to carry whole: every value, over more than one
	// read of the encoder's buffer.
	book := bytes.Repeat([]byte{0, 1, 2, 0xfe, 0xff, '.', '\r', '\n'}, 20000)
	path := filepath.Join(t.TempDir(), "book.epub")
	if err := os.WriteFile(path, book, 0o644); err != nil {
		t.Fatal(err)
	}
	s := Server{Host: "127.0.0.1", Port: port, Security: None, From: "me@example.com"}
	msgs := []Message{
		{To: "pw3@kindle.com", Subject: "三体", Body: "三体", Attachment: &Attachment{
			Name: "三体 - 刘慈欣.epub", ASCIIName: "San Ti - Liu Ci Xin.epub", ContentType: "application/epub+zip", Path: path}},
		{To: "oasis@kindle.com", Subject: "Dune", Body: "Dune"},
	}
	var progress []int
	n, err := Send(context.Background(), s, msgs, func(done int) { progress = append(progress, done) })
	if err != nil || n != 2 {
		t.Fatalf("Send = %d, %v; want 2, nil", n, err)
	}
	if len(progress) != 2 || progress[1] != 2 {
		t.Errorf("progress = %v, want [1 2]", progress)
	}
	got := <-out
	if len(got) != 2 || got[0].to != "pw3@kindle.com" || got[1].to != "oasis@kindle.com" {
		t.Fatalf("server got %d messages to %v", len(got), got)
	}

	m, err := mail.ReadMessage(strings.NewReader(got[0].data))
	if err != nil {
		t.Fatal(err)
	}
	if subj, _ := new(mime.WordDecoder).DecodeHeader(m.Header.Get("Subject")); subj != "三体" {
		t.Errorf("Subject = %q, want 三体", subj)
	}
	_, params, err := mime.ParseMediaType(m.Header.Get("Content-Type"))
	if err != nil {
		t.Fatal(err)
	}
	mr := multipart.NewReader(m.Body, params["boundary"])
	var file []byte
	var name string
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, dp, err := mime.ParseMediaType(p.Header.Get("Content-Disposition")); err == nil && dp["filename"] != "" {
			// ParseMediaType prefers filename*, as mail readers that read it do.
			name = dp["filename"]
			file, err = io.ReadAll(base64.NewDecoder(base64.StdEncoding, p))
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if name != "三体 - 刘慈欣.epub" {
		t.Errorf("attachment name = %q, want 三体 - 刘慈欣.epub", name)
	}
	if !bytes.Equal(file, book) {
		t.Errorf("attachment is %d bytes, not the book's %d", len(file), len(book))
	}
	if !strings.Contains(got[0].data, `filename="San Ti - Liu Ci Xin.epub"`) {
		t.Error("no ASCII filename for mail readers that don't read filename*")
	}
}

func TestSendStopsAtARefusal(t *testing.T) {
	port, out := fakeSMTP(t, "gone@kindle.com")
	s := Server{Host: "127.0.0.1", Port: port, Security: None, From: "me@example.com"}
	msgs := []Message{{To: "pw3@kindle.com", Subject: "A"}, {To: "gone@kindle.com", Subject: "B"}, {To: "pw3@kindle.com", Subject: "C"}}
	n, err := Send(context.Background(), s, msgs, nil)
	if n != 1 || err == nil || !strings.Contains(err.Error(), "550") {
		t.Fatalf("Send = %d, %v; want 1 and the server's 550", n, err)
	}
	// The client is closed on the error; the server sees the end.
	if got := <-out; len(got) != 1 {
		t.Errorf("server got %d messages, want 1", len(got))
	}
}
