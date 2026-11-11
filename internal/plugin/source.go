package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Limits on what a source returns and downloads.
const (
	MaxResults     = 100 // per page
	MaxCollections = 12
	MaxFields      = 12
	MaxDownload    = 2 << 30 // bytes
	MaxCover       = 10 << 20
)

// KnownFormats are the formats a source may offer.
var KnownFormats = []string{
	"EPUB", "KEPUB", "AZW3", "AZW", "MOBI", "PDF", "FB2", "DJVU", "CBZ", "CBR", "CB7",
	"TXT", "RTF", "DOCX", "ODT", "HTMLZ", "LIT", "PRC", "PDB", "LRF", "SNB", "TCR",
}

// Query is what a source searches for, or which page of a collection
// list asks for.
type Query struct {
	Text string `json:"text"`
	Page int    `json:"page"` // from 1
	// Cursor is what the previous page returned as cursor, for APIs that
	// page by token rather than by number; "" on the first page.
	Cursor string `json:"cursor"`
	// Filters are the values of the source's declared filters, by key;
	// every declared filter has one.
	Filters map[string]string `json:"filters"`
	// Collection is the collection list asks for; "" for search.
	Collection string `json:"collection,omitempty"`
}

// Collection is a list of books a source offers to browse.
type Collection struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// Field is a line of the result's details the source adds: "Rights",
// "Downloads".
type Field struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// Page is a page of search results.
type Page struct {
	Items   []Result `json:"items"`
	HasMore bool     `json:"hasMore,omitempty"`
	Cursor  string   `json:"cursor,omitempty"`
	// Warnings say what was wrong with what the plugin returned: results
	// dropped or fields ignored. Shown to the plugin's author, not the user.
	Warnings []string `json:"-"`
}

// Result is a book a source found.
type Result struct {
	// ID is the book's id in the source, unique within it.
	ID          string            `json:"id"`
	Title       string            `json:"title"`
	Authors     []string          `json:"authors,omitempty"`
	Description string            `json:"description,omitempty"` // plain text or simple HTML
	Publisher   string            `json:"publisher,omitempty"`
	Language    string            `json:"language,omitempty"` // ISO 639: "en", "eng", "zh-CN"
	Pubdate     string            `json:"pubdate,omitempty"`  // "2006", "2006-01", "2006-01-02" or RFC 3339
	Series      string            `json:"series,omitempty"`
	SeriesIndex float64           `json:"seriesIndex,omitempty"`
	Tags        []string          `json:"tags,omitempty"`
	Identifiers map[string]string `json:"identifiers,omitempty"` // "isbn": "…"
	Cover       string            `json:"cover,omitempty"`       // an image URL
	// URL is the book's page on the source's site.
	URL     string   `json:"url,omitempty"`
	Fields  []Field  `json:"fields,omitempty"`
	Formats []Format `json:"formats"`
	// Extra is the plugin's own, handed back to it unchanged by resolve.
	Extra json.RawMessage `json:"extra,omitempty"`
}

// Format is a file a result can be downloaded as.
type Format struct {
	Format string `json:"format"` // one of KnownFormats
	Size   int64  `json:"size,omitempty"`
	// URL downloads the file. When empty the host calls the plugin's
	// resolve, for links that must be made at download time.
	URL string `json:"url,omitempty"`
}

// Request is where the host downloads a file from.
type Request struct {
	URL string `json:"url,omitempty"`
	// Path is a file the plugin made, under /work or /data, as a tool
	// downloads one: the book, in place of URL.
	Path     string            `json:"path,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
	Filename string            `json:"filename,omitempty"`
}

// Search asks a source plugin for a page of books.
func (p *Plugin) Search(ctx context.Context, q Query) (Page, error) {
	q.Collection = ""
	return p.page(ctx, "search", q)
}

// List asks a source plugin for a page of one of its collections.
func (p *Plugin) List(ctx context.Context, q Query) (Page, error) {
	if q.Collection == "" {
		return Page{}, &Error{Plugin: p.Manifest.ID, Call: "list", Kind: KindInvalid, Msg: "no collection asked for"}
	}
	return p.page(ctx, "list", q)
}

func (p *Plugin) page(ctx context.Context, call string, q Query) (Page, error) {
	q.Text = strings.TrimSpace(q.Text)
	if q.Page < 1 {
		q.Page = 1
	}
	q.Filters = p.FilterValues(q.Filters)
	if p.native != nil {
		return p.nativePage(ctx, call, q)
	}
	raw, err := p.call(ctx, call, q)
	if err != nil {
		return Page{}, err
	}
	page, err := p.parsePage(raw)
	if err != nil {
		return Page{}, &Error{Plugin: p.Manifest.ID, Call: call, Kind: KindInvalid, Msg: err.Error()}
	}
	for _, w := range page.Warnings {
		p.logf("[%s] warning: %s returned %s", p.Manifest.ID, call, w)
	}
	return page, nil
}

// FilterValues gives every declared filter a value: the one in values
// when it is one of the filter's options, else the filter's default.
// Keys that aren't declared are dropped.
func (p *Plugin) FilterValues(values map[string]string) map[string]string {
	out := map[string]string{}
	for _, f := range p.Manifest.Filters() {
		out[f.Key] = f.Value(values[f.Key])
	}
	return out
}

// Collections asks a source plugin for the collections it offers.
func (p *Plugin) Collections(ctx context.Context) ([]Collection, error) {
	if p.native != nil {
		cs, err := p.native.Collections(ctx, p.getter())
		if err != nil {
			return nil, p.nativeError("collections", err)
		}
		return cs, nil
	}
	raw, err := p.call(ctx, "collections")
	if err != nil {
		return nil, err
	}
	var cs []Collection
	if err := strictJSON(raw, &cs); err != nil {
		var uf *unknownFieldError
		if !errors.As(err, &uf) {
			return nil, &Error{Plugin: p.Manifest.ID, Call: "collections", Kind: KindInvalid, Msg: "returned " + jsonProblem(err) + "; want [{id, title}]"}
		}
		p.logf("[%s] warning: collections returned %s; ignored", p.Manifest.ID, jsonProblem(err))
	}
	var out []Collection
	seen := map[string]bool{}
	for i, c := range cs {
		c.ID, c.Title = strings.TrimSpace(c.ID), strings.TrimSpace(c.Title)
		if c.ID == "" || c.Title == "" || seen[c.ID] {
			p.logf("[%s] warning: collections[%d] needs a unique id and a title; dropped", p.Manifest.ID, i)
			continue
		}
		seen[c.ID] = true
		out = append(out, c)
	}
	if len(out) > MaxCollections {
		p.logf("[%s] warning: %d collections; only the first %d are kept", p.Manifest.ID, len(out), MaxCollections)
		out = out[:MaxCollections]
	}
	return out, nil
}

// Get asks a source plugin for everything it knows about r, for the
// detail panel. Without a get export, r is returned as it is. What get
// returns keeps r's id, and r's formats and extra when it leaves them out.
func (p *Plugin) Get(ctx context.Context, r Result) (Result, error) {
	if !p.Defines("get") {
		return r, nil
	}
	raw, err := p.call(ctx, "get", r)
	if err != nil {
		return r, err
	}
	var full Result
	if err := strictJSON(raw, &full); err != nil {
		var uf *unknownFieldError
		if !errors.As(err, &uf) {
			return r, &Error{Plugin: p.Manifest.ID, Call: "get", Kind: KindInvalid, Msg: "returned " + jsonProblem(err) + "; want a result"}
		}
		p.logf("[%s] warning: get returned %s; ignored", p.Manifest.ID, jsonProblem(err))
	}
	if full.ID != "" && full.ID != r.ID {
		p.logf("[%s] warning: get returned id %q for %q; kept %q", p.Manifest.ID, full.ID, r.ID, r.ID)
	}
	full.ID = r.ID
	if strings.TrimSpace(full.Title) == "" {
		full.Title = r.Title
	}
	if len(full.Formats) == 0 {
		full.Formats = r.Formats
	}
	if len(full.Extra) == 0 {
		full.Extra = r.Extra
	}
	warn, err := full.check()
	for _, w := range warn {
		p.logf("[%s] warning: get returned %s", p.Manifest.ID, w)
	}
	if err != nil {
		return r, &Error{Plugin: p.Manifest.ID, Call: "get", Kind: KindInvalid, Msg: "returned " + err.Error()}
	}
	return full, nil
}

// parsePage reads what search returned. A malformed result is dropped
// with a warning, so one bad record doesn't hide the rest; a malformed
// page is an error.
func (p *Plugin) parsePage(raw json.RawMessage) (Page, error) {
	var shape struct {
		Items   []json.RawMessage `json:"items"`
		HasMore bool              `json:"hasMore"`
		Cursor  string            `json:"cursor"`
	}
	if err := strictJSON(raw, &shape); err != nil {
		return Page{}, fmt.Errorf("returned %s; want {items: [...], hasMore?, cursor?}", jsonProblem(err))
	}
	if shape.Items == nil {
		return Page{}, errors.New("returned no items array; return {items: []} when nothing is found")
	}
	page := Page{HasMore: shape.HasMore, Cursor: shape.Cursor}
	if len(shape.Items) > MaxResults {
		page.Warnings = append(page.Warnings, fmt.Sprintf("%d items; only the first %d are kept", len(shape.Items), MaxResults))
		shape.Items = shape.Items[:MaxResults]
	}
	seen := map[string]bool{}
	for i, item := range shape.Items {
		at := fmt.Sprintf("items[%d]", i)
		var r Result
		if err := strictJSON(item, &r); err != nil {
			var unknown *unknownFieldError
			if !errors.As(err, &unknown) {
				page.Warnings = append(page.Warnings, fmt.Sprintf("%s: %s; dropped", at, jsonProblem(err)))
				continue
			}
			page.Warnings = append(page.Warnings, fmt.Sprintf("%s: %s; ignored", at, jsonProblem(err)))
		}
		warn, err := r.check()
		for _, w := range warn {
			page.Warnings = append(page.Warnings, at+"."+w)
		}
		if err == nil && seen[r.ID] {
			err = fmt.Errorf("id: %q is used by an earlier item", r.ID)
		}
		if err != nil {
			page.Warnings = append(page.Warnings, fmt.Sprintf("%s.%v; dropped", at, err))
			continue
		}
		seen[r.ID] = true
		page.Items = append(page.Items, r)
	}
	return page, nil
}

// check normalizes a result. It returns warnings for fields it drops,
// and an error when the result can't be used.
func (r *Result) check() (warnings []string, err error) {
	if len(r.Formats) == 0 && strings.TrimSpace(r.ID) != "" && strings.TrimSpace(r.Title) != "" {
		return nil, errors.New("formats: none; a result needs at least one")
	}
	return r.checkDetails()
}

// checkDetails normalizes a result whose formats may be empty: a
// metadata candidate.
func (r *Result) checkDetails() (warnings []string, err error) {
	r.ID, r.Title = strings.TrimSpace(r.ID), strings.TrimSpace(r.Title)
	switch {
	case r.ID == "":
		return nil, errors.New("id: missing")
	case len(r.ID) > 256:
		return nil, errors.New("id: over 256 characters")
	case r.Title == "":
		return nil, errors.New("title: missing")
	}
	r.Authors = slices.DeleteFunc(r.Authors, func(a string) bool { return strings.TrimSpace(a) == "" })
	for i := range r.Formats {
		f := &r.Formats[i]
		f.Format = strings.ToUpper(strings.TrimPrefix(strings.TrimSpace(f.Format), "."))
		if !slices.Contains(KnownFormats, f.Format) {
			return warnings, fmt.Errorf("formats[%d].format: %q isn’t one of %s", i, f.Format, strings.Join(KnownFormats, ", "))
		}
		if f.URL != "" && !isWebURL(f.URL) {
			return warnings, fmt.Errorf("formats[%d].url: %q isn’t an absolute http(s) URL", i, f.URL)
		}
		if f.Size < 0 {
			f.Size = 0
		}
	}
	if r.Cover != "" && !isWebURL(r.Cover) {
		warnings = append(warnings, fmt.Sprintf("cover: %q isn’t an absolute http(s) URL; ignored", r.Cover))
		r.Cover = ""
	}
	if r.Pubdate != "" && ParseDate(r.Pubdate).IsZero() {
		warnings = append(warnings, fmt.Sprintf("pubdate: %q isn’t a date like 2006, 2006-01 or 2006-01-02; ignored", r.Pubdate))
		r.Pubdate = ""
	}
	if r.SeriesIndex < 0 {
		r.SeriesIndex = 0
	}
	if r.URL != "" && !isWebURL(r.URL) {
		warnings = append(warnings, fmt.Sprintf("url: %q isn’t an absolute http(s) URL; ignored", r.URL))
		r.URL = ""
	}
	r.Fields = slices.DeleteFunc(r.Fields, func(f Field) bool {
		return strings.TrimSpace(f.Label) == "" || strings.TrimSpace(f.Value) == ""
	})
	if len(r.Fields) > MaxFields {
		warnings = append(warnings, fmt.Sprintf("fields: %d; only the first %d are kept", len(r.Fields), MaxFields))
		r.Fields = r.Fields[:MaxFields]
	}
	return warnings, nil
}

// ParseDate reads the dates plugins give: a year, a month or a day,
// or an RFC 3339 time. It returns the zero time for anything else.
func ParseDate(s string) time.Time {
	s = strings.TrimSpace(s)
	for _, layout := range []string{time.RFC3339, "2006-01-02", "2006-01", "2006"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

func isWebURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != ""
}

type unknownFieldError struct{ field string }

func (e *unknownFieldError) Error() string { return "unknown field " + e.field }

// strictJSON decodes data into v. Fields v doesn't have are reported as
// an *unknownFieldError, after the rest has been decoded.
func strictJSON(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	err := dec.Decode(v)
	if err != nil && strings.HasPrefix(err.Error(), "json: unknown field ") {
		field := strings.TrimPrefix(err.Error(), "json: unknown field ")
		if err := json.Unmarshal(data, v); err != nil {
			return err
		}
		return &unknownFieldError{field}
	}
	return err
}

// jsonProblem says what was wrong in a plugin's JSON, in its terms.
func jsonProblem(err error) string {
	var te *json.UnmarshalTypeError
	var uf *unknownFieldError
	switch {
	case errors.As(err, &uf):
		return "an unknown field " + uf.field + " (a typo?)"
	case errors.As(err, &te):
		if te.Field == "" {
			return fmt.Sprintf("%s %s, not an object", article(te.Value), te.Value)
		}
		want := jsType(te.Type.Kind().String())
		return fmt.Sprintf("%s as %s %s; it must be %s %s", te.Field, article(te.Value), te.Value, article(want), want)
	}
	return strings.TrimPrefix(err.Error(), "json: ")
}

func article(word string) string {
	if word != "" && strings.ContainsRune("aeiou", rune(word[0])) {
		return "an"
	}
	return "a"
}

func jsType(goKind string) string {
	switch goKind {
	case "string":
		return "string"
	case "slice":
		return "array"
	case "map", "struct":
		return "object"
	case "bool":
		return "boolean"
	}
	return "number"
}

// Resolve says where to download r as format: the format's URL, or what
// the plugin's resolve(result, format) returns. A plugin that exports
// resolve is always asked.
func (p *Plugin) Resolve(ctx context.Context, r Result, format string) (Request, error) {
	format = strings.ToUpper(format)
	i := slices.IndexFunc(r.Formats, func(f Format) bool { return f.Format == format })
	if i < 0 {
		return Request{}, &Error{Plugin: p.Manifest.ID, Kind: KindNotFound, Msg: fmt.Sprintf("%s has no %s", r.Title, format)}
	}
	if !p.Defines("resolve") {
		if r.Formats[i].URL == "" {
			return Request{}, &Error{Plugin: p.Manifest.ID, Call: "search", Kind: KindInvalid,
				Msg: fmt.Sprintf("gave no url for %s and the plugin exports no resolve", format)}
		}
		return Request{URL: r.Formats[i].URL}, nil
	}
	raw, err := p.call(ctx, "resolve", r, format)
	if err != nil {
		return Request{}, err
	}
	invalid := func(msg string) error {
		return &Error{Plugin: p.Manifest.ID, Call: "resolve", Kind: KindInvalid, Msg: msg}
	}
	var req Request
	if err := strictJSON(raw, &req); err != nil {
		var uf *unknownFieldError
		if !errors.As(err, &uf) {
			return Request{}, invalid("returned " + jsonProblem(err) + "; want {url, headers?, filename?} or {path, filename?}")
		}
		p.logf("[%s] warning: resolve returned %s; ignored", p.Manifest.ID, jsonProblem(err))
	}
	if req.Path != "" {
		if _, err := p.hostPath(req.Path); err != nil {
			return Request{}, invalid(strings.Replace(err.Error(), "the new file", "the book's file", 1))
		}
		return req, nil
	}
	if !isWebURL(req.URL) {
		return Request{}, invalid(fmt.Sprintf("url %q isn’t an absolute http(s) URL", req.URL))
	}
	return req, nil
}

// Download saves r as format in dir and returns the file's path.
// progress, when not nil, is told the bytes written and the total (-1
// when unknown).
func (p *Plugin) Download(ctx context.Context, r Result, format, dir string, progress func(done, total int64)) (string, error) {
	req, err := p.Resolve(ctx, r, format)
	if err != nil {
		return "", err
	}
	fail := func(kind ErrorKind, err error) (string, error) {
		switch {
		case errors.Is(err, ErrHostNotAllowed):
			kind = KindPermission
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			kind = KindTimeout
		}
		return "", &Error{Plugin: p.Manifest.ID, Kind: kind, Msg: "download: " + err.Error(), Err: err}
	}
	if req.Path != "" {
		return p.moveMade(req, r, format, dir)
	}
	resp, err := p.get(ctx, req.URL, req.Headers)
	if err != nil {
		return fail(KindUnavailable, err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == 401 || resp.StatusCode == 403:
		return fail(KindAuth, errors.New(resp.Status))
	case resp.StatusCode == 404 || resp.StatusCode == 410:
		return fail(KindNotFound, errors.New(resp.Status))
	case resp.StatusCode == 429:
		return fail(KindRateLimited, errors.New(resp.Status))
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return fail(KindUnavailable, errors.New(resp.Status))
	case resp.ContentLength > MaxDownload:
		return fail(KindInvalid, fmt.Errorf("the file is over %d GB", MaxDownload>>30))
	}
	if ct, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type")); ct == "text/html" && format != "HTMLZ" {
		return fail(KindInvalid, errors.New("got a web page, not a book file (a login or error page?)"))
	}

	name := req.Filename
	if name == "" {
		if _, params, err := mime.ParseMediaType(resp.Header.Get("Content-Disposition")); err == nil {
			name = params["filename"]
		}
	}
	name = fileName(name, r.Title, format)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, name)
	tmp := path + ".part"
	out, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	w := &countingWriter{w: out, total: resp.ContentLength, progress: progress}
	_, err = io.Copy(w, io.LimitReader(resp.Body, MaxDownload+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	switch {
	case err == nil && w.done == 0:
		err = errors.New("the file is empty")
	case err == nil && w.done > MaxDownload:
		err = fmt.Errorf("the file is over %d GB", MaxDownload>>30)
	}
	if err != nil {
		os.Remove(tmp)
		return fail(KindUnavailable, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return path, nil
}

// moveMade moves the book's file the plugin made (resolve's path) into
// dir, named as a download would be.
func (p *Plugin) moveMade(req Request, r Result, format, dir string) (string, error) {
	src, err := p.hostPath(req.Path)
	if err != nil {
		return "", &Error{Plugin: p.Manifest.ID, Call: "resolve", Kind: KindInvalid, Msg: err.Error()}
	}
	st, err := os.Stat(src)
	switch {
	case err != nil:
		return "", err
	case st.Size() == 0:
		return "", &Error{Plugin: p.Manifest.ID, Call: "resolve", Kind: KindInvalid, Msg: "the file is empty"}
	case st.Size() > MaxDownload:
		return "", &Error{Plugin: p.Manifest.ID, Call: "resolve", Kind: KindInvalid, Msg: fmt.Sprintf("the file is over %d GB", MaxDownload>>30)}
	}
	name := req.Filename
	if name == "" {
		name = filepath.Base(src)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	dst := filepath.Join(dir, fileName(name, r.Title, format))
	if err := moveFile(src, dst); err != nil {
		return "", err
	}
	return dst, nil
}

// Cover fetches r's cover image, from a host the plugin may reach.
func (p *Plugin) Cover(ctx context.Context, r Result) ([]byte, error) {
	if r.Cover == "" {
		return nil, nil
	}
	resp, err := p.get(ctx, r.Cover, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("cover %s: %s", resp.Request.URL.Redacted(), resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxCover+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxCover {
		return nil, fmt.Errorf("cover %s is over %d MB", resp.Request.URL.Redacted(), MaxCover>>20)
	}
	return data, nil
}

// get makes a GET request the plugin may make, outside its script.
func (p *Plugin) get(ctx context.Context, rawURL string, headers map[string]string) (*http.Response, error) {
	req, err := p.request(ctx, http.MethodGet, rawURL, headers, nil)
	if err != nil {
		return nil, err
	}
	return p.client.Do(req)
}

// fileName makes a safe file name ending in format's extension, from the
// server's name or else the title.
func fileName(name, title, format string) string {
	ext := "." + strings.ToLower(format)
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	if name == "." || name == ".." {
		name = ""
	}
	if name == "" {
		name = title
	}
	name = strings.Map(func(r rune) rune {
		if r < 32 || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '_'
		}
		return r
	}, name)
	name = strings.TrimSpace(strings.TrimRight(name, ". "))
	if !strings.EqualFold(filepath.Ext(name), ext) {
		name += ext
	}
	if r := []rune(name); len(r) > 120 {
		name = string(r[:120-len(ext)]) + ext
	}
	if name == ext {
		name = "book" + ext
	}
	return name
}

type countingWriter struct {
	w           io.Writer
	done, total int64
	progress    func(done, total int64)
}

func (c *countingWriter) Write(b []byte) (int, error) {
	n, err := c.w.Write(b)
	c.done += int64(n)
	if c.progress != nil {
		c.progress(c.done, c.total)
	}
	return n, err
}
