package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
)

// Why a file is processed, as process(file) is told.
const (
	ReasonImport = "import" // Dalibre is adding the file
	ReasonManual = "manual" // the user ran the plugin on the book
)

// FileInput is what process(file) is given about a book's file. Path is
// where the script reads the file (a copy in /work), Name the file's
// own name, Format its format in upper case ("EPUB").
type FileInput struct {
	Path   string `json:"path"`
	Name   string `json:"name"`
	Format string `json:"format"`
	Reason string `json:"reason"`
	// Book is the book the file belongs to, when it is in the library.
	Book *Book `json:"book,omitempty"`
}

// Processed is what a plugin made of a file.
type Processed struct {
	// File is the new file, in a temporary folder that Done removes; ""
	// when the plugin left the file as it was.
	File string
	// Format is the new file's format, from its extension ("EPUB").
	Format string
	// Report is what the plugin says it did, a line each.
	Report []string
	dir    string
}

// Done removes the new file's folder.
func (r *Processed) Done() {
	if r != nil && r.dir != "" {
		os.RemoveAll(r.dir)
	}
}

// maxReport bounds what a plugin may report about one file.
const maxReport = 20

var workSeq atomic.Int64

// Process runs the plugin's process(file) on a copy of file and returns
// what it made. The copy is in the plugin's /work, which it may also
// write the new file to; the new file is moved out to a folder of its own
// before Process returns, and /work emptied (calls into a plugin run one
// at a time).
func (p *Plugin) Process(ctx context.Context, file, reason string, book *Book) (*Processed, error) {
	if !p.Manifest.IsFileType() {
		return nil, &Error{Plugin: p.Manifest.ID, Call: "process", Kind: KindInvalid, Msg: "isn’t a file type plugin"}
	}
	if p.dataDir == "" {
		return nil, &Error{Plugin: p.Manifest.ID, Call: "process", Kind: KindUnavailable, Msg: "has no folder to work in"}
	}
	p.workMu.Lock()
	defer p.workMu.Unlock()
	name := filepath.Base(file)
	work := strconv.FormatInt(workSeq.Add(1), 10)
	workRoot := filepath.Join(p.dataDir, workFolder)
	hostWork := filepath.Join(workRoot, work)
	if err := os.MkdirAll(hostWork, 0o700); err != nil {
		return nil, err
	}
	defer emptyFolder(workRoot)
	if _, err := copyFile(file, filepath.Join(hostWork, name)); err != nil {
		return nil, err
	}
	in := FileInput{
		Path: path.Join("/", workFolder, work, name), Name: name, Reason: reason, Book: book,
		Format: strings.ToUpper(strings.TrimPrefix(filepath.Ext(name), ".")),
	}
	raw, err := p.callWithin(ctx, ProcessTimeout, "process", in)
	if err != nil {
		return nil, err
	}
	out, report, err := processResult(raw)
	if err != nil {
		return nil, &Error{Plugin: p.Manifest.ID, Call: "process", Kind: KindInvalid, Msg: err.Error()}
	}
	if len(report) > maxReport {
		report = report[:maxReport]
	}
	if out == "" {
		return &Processed{Report: report}, nil
	}
	src, err := p.hostPath(out)
	if err != nil {
		return nil, &Error{Plugin: p.Manifest.ID, Call: "process", Kind: KindInvalid, Msg: err.Error()}
	}
	ext := strings.ToLower(filepath.Ext(src))
	if ext == "" || ext == "." {
		return nil, &Error{Plugin: p.Manifest.ID, Call: "process", Kind: KindInvalid,
			Msg: fmt.Sprintf("returned %q, a file without an extension: name the new file after its format (book.epub)", out)}
	}
	dir, err := os.MkdirTemp("", "dalibre-processed-")
	if err != nil {
		return nil, err
	}
	dst := filepath.Join(dir, strings.TrimSuffix(name, filepath.Ext(name))+ext)
	if err := moveFile(src, dst); err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	return &Processed{File: dst, Format: strings.ToUpper(ext[1:]), Report: report, dir: dir}, nil
}

// processResult reads what process returned: null (the file as it was),
// the new file's path, or { path, report }.
func processResult(raw json.RawMessage) (string, []string, error) {
	var s *string
	if err := json.Unmarshal(raw, &s); err == nil {
		if s == nil {
			return "", nil, nil
		}
		return *s, nil, nil
	}
	var o struct {
		Path   *string  `json:"path"`
		Report []string `json:"report"`
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&o); err != nil {
		return "", nil, fmt.Errorf("returned %s; want null, the new file’s path, or { path, report }: %s", shorten(string(raw)), jsonProblem(err))
	}
	if o.Path == nil {
		return "", o.Report, nil
	}
	return *o.Path, o.Report, nil
}

// hostPath is where the file the script names at p (as node:fs sees it)
// is: in /work or /data, and a regular file.
func (pl *Plugin) hostPath(p string) (string, error) {
	clean := path.Clean("/" + strings.TrimPrefix(filepath.ToSlash(p), "/"))
	if !strings.HasPrefix(clean, "/"+workFolder+"/") && !strings.HasPrefix(clean, "/"+dataFolder+"/") {
		return "", fmt.Errorf("returned %q: the new file must be in /work or /data", p)
	}
	host := filepath.Join(pl.dataDir, filepath.FromSlash(clean[1:]))
	st, err := os.Lstat(host)
	if err != nil {
		return "", fmt.Errorf("returned %q, which doesn’t exist", p)
	}
	if !st.Mode().IsRegular() {
		return "", fmt.Errorf("returned %q, which isn’t a file", p)
	}
	return host, nil
}

// emptyFolder removes what dir holds, leaving dir.
func emptyFolder(dir string) {
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		os.RemoveAll(filepath.Join(dir, e.Name()))
	}
}

func shorten(s string) string {
	if len(s) > 80 {
		return s[:77] + "…"
	}
	return s
}

func copyFile(src, dst string) (int64, error) {
	in, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return n, err
}

// moveFile renames src to dst, or copies it across volumes.
func moveFile(src, dst string) error {
	if os.Rename(src, dst) == nil {
		return nil
	}
	if _, err := copyFile(src, dst); err != nil {
		os.Remove(dst)
		return err
	}
	return os.Remove(src)
}
