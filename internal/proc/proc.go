// Package proc runs programs in a process group of their own, so that
// stopping one stops what it started too.
package proc

import (
	"bufio"
	"bytes"
	"io"
	"os/exec"
)

// Group puts cmd in a process group of its own and makes cancelling its
// context kill the whole group. Call before cmd starts.
func Group(cmd *exec.Cmd) {
	setGroup(cmd)
	cmd.Cancel = func() error { return killGroup(cmd) }
}

// Lines calls fn with each line r gives, a carriage return ending one as
// a newline does (progress bars redraw with it), until r ends.
func Lines(r io.Reader, fn func(string)) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	sc.Split(func(data []byte, atEOF bool) (int, []byte, error) {
		if i := bytes.IndexAny(data, "\r\n"); i >= 0 {
			adv := i + 1
			if data[i] == '\r' && i+1 < len(data) && data[i+1] == '\n' {
				adv++
			}
			return adv, data[:i], nil
		}
		if atEOF && len(data) > 0 {
			return len(data), data, nil
		}
		return 0, nil, nil
	})
	for sc.Scan() {
		fn(sc.Text())
	}
}
