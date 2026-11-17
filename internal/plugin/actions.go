package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MoeForge/moejs"
)

// Actions: run(action, input) on books or on the whole library. While it
// runs, the script may ask the app for things only it has, through the
// dalibre object: a form for the user to fill in (dalibre.form), the
// library's books (dalibre.library.books()), and the job's progress bar
// (dalibre.progress).

// ActionHost is what an action may ask of whoever runs it.
type ActionHost struct {
	// Form shows the user a form and returns what they answered, by field
	// key, or ErrCancelled. Nil where there is no window to show it in:
	// the answers then come from Answers.
	Form func(ctx context.Context, f Form) (map[string]any, error)
	// Answers are answers given ahead, by field key: with every field of
	// a form among them, the form isn't shown.
	Answers map[string]any
	// Books returns up to limit of the library's books with ids over
	// after, in id order; fewer than limit ends the list. Nil gives the
	// script no books.
	Books func(after int64, limit int, comments bool) ([]BookInfo, error)
	// Progress is told how far the action says it is.
	Progress func(done, total int)
	// Covers are the books' covers, by book id, for an action that takes
	// them.
	Covers map[int64]string
}

// ErrCancelled is what ActionHost.Form returns when the user cancelled.
var ErrCancelled = errors.New("cancelled")

// Form is what dalibre.form asks the user.
type Form struct {
	// Submit names the form's button; the action's title when empty.
	Submit string      `json:"submit,omitempty"`
	Fields []FormField `json:"fields"`
	// Remember starts the form with the user's last answers to it, as
	// calibre plugins keep their options.
	Remember bool `json:"remember,omitempty"`
}

// Prefill starts the form's fields with saved answers, by key, where
// they still fit the field: a select's option, a multiselect's options.
func (f *Form) Prefill(saved map[string]any) {
	for i := range f.Fields {
		fd := &f.Fields[i]
		v, ok := saved[fd.Key]
		if !ok {
			continue
		}
		has := func(s string) bool {
			return slices.ContainsFunc(fd.Options, func(o Option) bool { return o.Value == s })
		}
		switch fd.Type {
		case FieldText:
			if s, ok := v.(string); ok {
				fd.Value = s
			}
		case FieldSelect:
			if s, ok := v.(string); ok && has(s) {
				fd.Value = s
			}
		case FieldToggle:
			if b, ok := v.(bool); ok {
				fd.Value = b
			}
		case FieldMultiselect:
			if list, ok := v.([]any); ok {
				out := []string{}
				for _, x := range list {
					if s, ok := x.(string); ok && has(s) {
						out = append(out, s)
					}
				}
				fd.Value = out
			}
		}
	}
}

// FormField is one question of a Form.
type FormField struct {
	Key   string `json:"key"`
	Title string `json:"title"`
	// Type is FieldText, FieldSelect, FieldToggle or FieldMultiselect.
	Type string `json:"type"`
	// Value is what the field starts with: a string (text, select), a
	// boolean (toggle) or a list of strings (multiselect).
	Value   any      `json:"value,omitempty"`
	Options []Option `json:"options,omitempty"`
}

// The types of a FormField.
const (
	FieldText        = "text"
	FieldSelect      = "select"
	FieldToggle      = "toggle"
	FieldMultiselect = "multiselect"
)

// Limits on a form.
const (
	MaxFormFields  = 24
	MaxFormOptions = 500
)

// check checks the form and gives every field the value it starts with,
// in its type.
func (f *Form) check() error {
	if len(f.Fields) == 0 || len(f.Fields) > MaxFormFields {
		return fmt.Errorf("a form needs 1 to %d fields", MaxFormFields)
	}
	if len([]rune(f.Submit)) > 40 {
		return errors.New("submit is over 40 characters")
	}
	keys := map[string]bool{}
	for i := range f.Fields {
		fd := &f.Fields[i]
		at := fmt.Sprintf("fields[%d]", i)
		switch {
		case !keyPattern.MatchString(fd.Key):
			return fmt.Errorf("%s.key %q must be letters, digits and '_', starting with a letter", at, fd.Key)
		case keys[fd.Key]:
			return fmt.Errorf("%s.key %q is used twice", at, fd.Key)
		case strings.TrimSpace(fd.Title) == "":
			return fmt.Errorf("%s has no title", at)
		}
		keys[fd.Key] = true
		choice := fd.Type == FieldSelect || fd.Type == FieldMultiselect
		switch {
		case !slices.Contains([]string{FieldText, FieldSelect, FieldToggle, FieldMultiselect}, fd.Type):
			return fmt.Errorf("%s.type must be %q, %q, %q or %q", at, FieldText, FieldSelect, FieldToggle, FieldMultiselect)
		case choice && (len(fd.Options) == 0 || len(fd.Options) > MaxFormOptions):
			return fmt.Errorf("%s needs 1 to %d options", at, MaxFormOptions)
		case !choice && len(fd.Options) > 0:
			return fmt.Errorf("%s: options are for %q and %q", at, FieldSelect, FieldMultiselect)
		}
		vals := map[string]bool{}
		for j := range fd.Options {
			o := &fd.Options[j]
			if vals[o.Value] {
				return fmt.Errorf("%s.options[%d].value %q is used twice", at, j, o.Value)
			}
			vals[o.Value] = true
			if strings.TrimSpace(o.Title) == "" {
				o.Title = o.Value
			}
		}
		if fd.Value == nil {
			switch fd.Type {
			case FieldText:
				fd.Value = ""
			case FieldSelect:
				fd.Value = fd.Options[0].Value
			case FieldToggle:
				fd.Value = false
			case FieldMultiselect:
				fd.Value = []string{}
			}
		}
		v, err := fd.typed(fd.Value)
		if err != nil {
			return fmt.Errorf("%s.value: %w", at, err)
		}
		fd.Value = v
	}
	return nil
}

// typed is v as an answer to the field: a string, a bool or a []string.
func (fd *FormField) typed(v any) (any, error) {
	has := func(s string) bool {
		return slices.ContainsFunc(fd.Options, func(o Option) bool { return o.Value == s })
	}
	switch fd.Type {
	case FieldText:
		if s, ok := v.(string); ok {
			return s, nil
		}
		return nil, errors.New("want a string")
	case FieldSelect:
		if s, ok := v.(string); ok && has(s) {
			return s, nil
		}
		return nil, errors.New("want one of the options’ values")
	case FieldToggle:
		if b, ok := v.(bool); ok {
			return b, nil
		}
		return nil, errors.New("want true or false")
	}
	var list []string
	switch l := v.(type) {
	case []string:
		list = l
	case []any:
		for _, x := range l {
			s, ok := x.(string)
			if !ok {
				return nil, errors.New("want a list of the options’ values")
			}
			list = append(list, s)
		}
	default:
		return nil, errors.New("want a list of the options’ values")
	}
	out := []string{}
	for _, s := range list {
		if !has(s) {
			return nil, errors.New("want a list of the options’ values")
		}
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out, nil
}

// Answer is answers as the form's answers: every field's, in its type.
// A field answers doesn't have is missing: its key is returned.
func (f *Form) Answer(answers map[string]any) (map[string]any, []string, error) {
	out := map[string]any{}
	var missing []string
	for i := range f.Fields {
		fd := &f.Fields[i]
		v, ok := answers[fd.Key]
		if !ok {
			missing = append(missing, fd.Key)
			continue
		}
		t, err := fd.typed(v)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", fd.Key, err)
		}
		out[fd.Key] = t
	}
	return out, missing, nil
}

// describe says what the form asks, for a caller that has to give the
// answers ahead.
func (f *Form) describe() string {
	var parts []string
	for _, fd := range f.Fields {
		s := fd.Key + " (" + fd.Type
		if len(fd.Options) > 0 {
			var vals []string
			for i, o := range fd.Options {
				if i == 8 {
					vals = append(vals, "…")
					break
				}
				vals = append(vals, o.Value)
			}
			s += ": " + strings.Join(vals, " | ")
		}
		parts = append(parts, s+")")
	}
	return strings.Join(parts, ", ")
}

// Group is books an action found, under a title: a set of duplicates, the
// books with one problem.
type Group struct {
	Title string      `json:"title"`
	Books []GroupBook `json:"books"`
}

// GroupBook is a book of a Group, with what the action says about it.
type GroupBook struct {
	ID   int64  `json:"id"`
	Note string `json:"note,omitempty"`
}

// BookChange is a change an action asks for in a book's details.
type BookChange struct {
	Book     int64    `json:"book"`
	Metadata *Changes `json:"metadata"`
}

// NewBook is a file an action made, to add as a book.
type NewBook struct {
	// File is the file, in the result's own folder.
	File     string
	Metadata *Changes
}

// NewFormat is a file an action made, to add to a book.
type NewFormat struct {
	Book   int64
	File   string
	Format string
}

// ActionResult is what an action returned: what it found and what it
// wants changed, which the app shows and applies.
type ActionResult struct {
	// Message is what the action says of how it went.
	Message string
	Groups  []Group
	Changes []BookChange
	Add     []NewBook
	Formats []NewFormat
	// Open is a page on one of the plugin's hosts to open in the browser.
	Open string
	dir  string
}

// Done removes the files the result holds.
func (r *ActionResult) Done() {
	if r != nil && r.dir != "" {
		os.RemoveAll(r.dir)
	}
}

// Empty reports whether the action returned nothing.
func (r *ActionResult) Empty() bool {
	return r == nil || r.Message == "" && len(r.Groups) == 0 && len(r.Changes) == 0 && len(r.Add) == 0 && len(r.Formats) == 0 && r.Open == ""
}

// Limits on what an action may return.
const (
	MaxGroups     = 2000
	MaxGroupBooks = 100000
	MaxNewFiles   = 200
)

// actionInput is what run(action, input) is given.
type actionInput struct {
	Books  []BookInfo          `json:"books"`
	Files  map[string]HookFile `json:"files,omitempty"`
	Covers map[string]string   `json:"covers,omitempty"`
}

// RunAction runs the plugin's action id: on books, with a copy of each
// one's file from files (by book id) when the action takes files, or on
// the library, which it reads through host. It may take as long as a
// process call, not counting the time a form is open.
func (p *Plugin) RunAction(ctx context.Context, id string, books []BookInfo, files map[int64]string, host *ActionHost) (*ActionResult, error) {
	fail := func(kind ErrorKind, msg string) error {
		return &Error{Plugin: p.Manifest.ID, Call: "run", Kind: kind, Msg: msg}
	}
	act := p.Manifest.Action(id)
	if act == nil {
		return nil, fail(KindInvalid, fmt.Sprintf("has no action %q", id))
	}
	if host == nil {
		host = &ActionHost{}
	}
	in := actionInput{Books: []BookInfo{}}
	if act.Scope == ScopeBooks {
		for _, b := range books {
			for _, l := range []*[]string{&b.Authors, &b.Tags, &b.Formats} {
				if *l == nil {
					*l = []string{}
				}
			}
			if b.Identifiers == nil {
				b.Identifiers = map[string]string{}
			}
			in.Books = append(in.Books, b)
		}
	}
	p.workMu.Lock()
	defer p.workMu.Unlock()
	if p.dataDir != "" {
		defer emptyFolder(filepath.Join(p.dataDir, workFolder))
	}
	if act.Files && len(files) > 0 {
		if p.dataDir == "" {
			return nil, fail(KindUnavailable, "has no folder to work in")
		}
		work := strconv.FormatInt(workSeq.Add(1), 10)
		in.Files = map[string]HookFile{}
		for _, b := range in.Books {
			src := files[b.ID]
			if src == "" {
				continue
			}
			key := strconv.FormatInt(b.ID, 10)
			name := filepath.Base(src)
			dir := filepath.Join(p.dataDir, workFolder, work, key)
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return nil, err
			}
			if _, err := copyFile(src, filepath.Join(dir, name)); err != nil {
				return nil, err
			}
			in.Files[key] = HookFile{Path: path.Join("/", workFolder, work, key, name), Name: name,
				Format: strings.ToUpper(strings.TrimPrefix(filepath.Ext(name), "."))}
		}
	}
	if act.Covers && len(host.Covers) > 0 {
		if p.dataDir == "" {
			return nil, fail(KindUnavailable, "has no folder to work in")
		}
		work := strconv.FormatInt(workSeq.Add(1), 10)
		in.Covers = map[string]string{}
		for _, b := range in.Books {
			src := host.Covers[b.ID]
			if src == "" {
				continue
			}
			key := strconv.FormatInt(b.ID, 10)
			name := "cover" + strings.ToLower(filepath.Ext(src))
			dir := filepath.Join(p.dataDir, workFolder, work, key)
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return nil, err
			}
			if _, err := copyFile(src, filepath.Join(dir, name)); err != nil {
				return nil, err
			}
			in.Covers[key] = path.Join("/", workFolder, work, key, name)
		}
	}
	raw, err := p.callHosted(ctx, ProcessTimeout, host, "run", id, in)
	if err != nil {
		return nil, err
	}
	res, err := p.actionResult(raw)
	if err != nil {
		return nil, fail(KindInvalid, err.Error())
	}
	return res, nil
}

// actionResult reads what run returned: null, or an object of what it
// found and wants changed. The files it names are moved out of the
// plugin's folders into the result's own.
func (p *Plugin) actionResult(raw json.RawMessage) (res *ActionResult, err error) {
	res = &ActionResult{}
	if s := strings.TrimSpace(string(raw)); s == "" || s == "null" {
		return res, nil
	}
	var o struct {
		Message string       `json:"message"`
		Groups  []Group      `json:"groups"`
		Changes []BookChange `json:"changes"`
		Add     []struct {
			Path     string   `json:"path"`
			Metadata *Changes `json:"metadata"`
		} `json:"add"`
		Formats []struct {
			Book int64  `json:"book"`
			Path string `json:"path"`
		} `json:"formats"`
		Open string `json:"open"`
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&o); err != nil {
		return nil, fmt.Errorf("returned %s; want null or { message, groups, changes, add, formats, open }: %s", shorten(string(raw)), jsonProblem(err))
	}
	res.Message = strings.TrimSpace(o.Message)
	if len([]rune(res.Message)) > 200 {
		res.Message = string([]rune(res.Message)[:200])
	}
	if len(o.Groups) > MaxGroups {
		return nil, fmt.Errorf("groups: over %d", MaxGroups)
	}
	n := 0
	for i, g := range o.Groups {
		if strings.TrimSpace(g.Title) == "" {
			return nil, fmt.Errorf("groups[%d] has no title", i)
		}
		if n += len(g.Books); n > MaxGroupBooks {
			return nil, fmt.Errorf("groups: over %d books", MaxGroupBooks)
		}
		if len(g.Books) > 0 {
			res.Groups = append(res.Groups, g)
		}
	}
	for i, c := range o.Changes {
		if c.Metadata == nil {
			continue
		}
		if err := checkChanges(c.Metadata); err != nil {
			return nil, fmt.Errorf("changes[%d].%w", i, err)
		}
		res.Changes = append(res.Changes, c)
	}
	if o.Open != "" {
		u, err := url.Parse(o.Open)
		if err != nil || !u.IsAbs() {
			return nil, fmt.Errorf("open: %q isn’t an absolute URL", o.Open)
		}
		if err := p.checkURL(u); err != nil {
			return nil, fmt.Errorf("open: %w", err)
		}
		res.Open = u.String()
	}
	if len(o.Add)+len(o.Formats) == 0 {
		return res, nil
	}
	if len(o.Add)+len(o.Formats) > MaxNewFiles {
		return nil, fmt.Errorf("add and formats: over %d files", MaxNewFiles)
	}
	if res.dir, err = os.MkdirTemp("", "dalibre-action-"); err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			res.Done()
		}
	}()
	take := func(at, p0 string, i int) (string, string, error) {
		src, err := p.hostPath(p0)
		if err != nil {
			return "", "", fmt.Errorf("%s: %w", at, err)
		}
		ext := strings.ToLower(filepath.Ext(src))
		if ext == "" || ext == "." {
			return "", "", fmt.Errorf("%s: returned %q, a file without an extension: name the file after its format (book.epub)", at, p0)
		}
		dir := filepath.Join(res.dir, strconv.Itoa(i))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", "", err
		}
		dst := filepath.Join(dir, filepath.Base(src))
		if err := moveFile(src, dst); err != nil {
			return "", "", err
		}
		return dst, strings.ToUpper(ext[1:]), nil
	}
	for i, a := range o.Add {
		if a.Metadata != nil {
			if err := checkChanges(a.Metadata); err != nil {
				return nil, fmt.Errorf("add[%d].%w", i, err)
			}
		}
		file, _, err := take(fmt.Sprintf("add[%d]", i), a.Path, i)
		if err != nil {
			return nil, err
		}
		res.Add = append(res.Add, NewBook{File: file, Metadata: a.Metadata})
	}
	for i, f := range o.Formats {
		file, format, err := take(fmt.Sprintf("formats[%d]", i), f.Path, len(o.Add)+i)
		if err != nil {
			return nil, err
		}
		res.Formats = append(res.Formats, NewFormat{Book: f.Book, File: file, Format: format})
	}
	return res, nil
}

// booksPage is how many books dalibre.library.books() fetches at a time.
const booksPage = 500

// hostFlag is __host_flag(name): "1" when the flag is on for the call in
// progress. "interactive" is whether a form would show in a window.
func (p *Plugin) hostFlag(args []any) (any, error) {
	if len(args) == 1 && args[0] == "interactive" && p.host != nil && p.host.Form != nil {
		return "1", nil
	}
	return "", nil
}

// hostProgress is __host_progress(done, total): how far the action in
// progress says it is.
func (p *Plugin) hostProgress(args []any) (any, error) {
	if len(args) == 2 && p.host != nil && p.host.Progress != nil {
		done, _ := strconv.Atoi(fmt.Sprint(args[0]))
		total, _ := strconv.Atoi(fmt.Sprint(args[1]))
		p.host.Progress(max(done, 0), max(total, 0))
	}
	return nil, nil
}

// hostAsk is __host_ask(op, json): it asks the app, on another goroutine,
// for what the call in progress may have of it, and returns a promise of
// the answer as JSON: { ok, value } or { ok: false, kind, message }.
func (p *Plugin) hostAsk(r *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
	var in [2]string
	for i := range in {
		if i < len(args) {
			s, err := r.ToString(args[i])
			if err != nil {
				return moejs.Undefined(), err
			}
			in[i] = s.GoString()
		}
	}
	promise, settle := p.loop.NewPromise()
	host, ctx, limit := p.host, p.ctx, p.limit
	go func() {
		value, err := p.ask(ctx, host, limit, in[0], in[1])
		out := map[string]any{"ok": err == nil, "value": value}
		if err != nil {
			kind := KindInvalid
			var pe *Error
			switch {
			case errors.Is(err, ErrCancelled):
				kind = KindCancelled
			case errors.As(err, &pe):
				kind = pe.Kind
			}
			out["kind"], out["message"] = kind, err.Error()
		}
		data, _ := json.Marshal(out)
		settle(func(*moejs.Runtime) (moejs.Value, error) { return moejs.String(string(data)), nil })
	}()
	return promise, nil
}

// ask answers one __host_ask. Any goroutine.
func (p *Plugin) ask(ctx context.Context, host *ActionHost, limit *deadline, op, arg string) (any, error) {
	if host == nil {
		return nil, fmt.Errorf("dalibre.%s is for an action’s run", map[string]string{"form": "form", "books": "library.books"}[op])
	}
	switch op {
	case "form":
		var f Form
		dec := json.NewDecoder(strings.NewReader(arg))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&f); err != nil {
			return nil, fmt.Errorf("dalibre.form: want { submit, fields }: %s", jsonProblem(err))
		}
		if err := f.check(); err != nil {
			return nil, fmt.Errorf("dalibre.form: %w", err)
		}
		given, missing, err := f.Answer(host.Answers)
		if err != nil {
			return nil, fmt.Errorf("the answer given for %w", err)
		}
		if len(missing) == 0 {
			return given, nil
		}
		if host.Form == nil {
			return nil, fmt.Errorf("asks for answers nobody is there to give; give them ahead: %s", f.describe())
		}
		// The user's time isn't the action's.
		limit.pause()
		defer limit.resume()
		answers, err := host.Form(ctx, f)
		if err != nil {
			return nil, err
		}
		out, missing, err := f.Answer(answers)
		if err == nil && len(missing) > 0 {
			err = fmt.Errorf("no answer for %s", strings.Join(missing, ", "))
		}
		return out, err
	case "books":
		var q struct {
			After    int64 `json:"after"`
			Comments bool  `json:"comments"`
		}
		if err := json.Unmarshal([]byte(arg), &q); err != nil {
			return nil, err
		}
		page := []BookInfo{}
		if host.Books != nil {
			var err error
			if page, err = host.Books(q.After, booksPage, q.Comments); err != nil {
				return nil, err
			}
		}
		for i := range page {
			b := &page[i]
			for _, l := range []*[]string{&b.Authors, &b.Tags, &b.Formats} {
				if *l == nil {
					*l = []string{}
				}
			}
			if b.Identifiers == nil {
				b.Identifiers = map[string]string{}
			}
		}
		return map[string]any{"books": page, "more": len(page) >= booksPage}, nil
	}
	return nil, fmt.Errorf("unknown request %q", op)
}

// deadline is the time a call has: a timer that can be stopped while the
// user is asked something and started again after.
type deadline struct {
	mu     sync.Mutex
	timer  *time.Timer
	left   time.Duration
	at     time.Time
	paused int
	fired  bool
}

func newDeadline(d time.Duration, fire func()) *deadline {
	dl := &deadline{left: d, at: time.Now().Add(d)}
	dl.timer = time.AfterFunc(d, func() {
		dl.mu.Lock()
		dl.fired = true
		dl.mu.Unlock()
		fire()
	})
	return dl
}

func (d *deadline) pause() {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.paused++; d.paused == 1 && !d.fired && d.timer.Stop() {
		d.left = time.Until(d.at)
	}
}

func (d *deadline) resume() {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.paused--; d.paused == 0 && !d.fired {
		d.at = time.Now().Add(d.left)
		d.timer.Reset(d.left)
	}
}

func (d *deadline) stop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.fired = true
	d.timer.Stop()
}
