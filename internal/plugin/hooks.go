package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// BookInfo is a book as a hook sees it: what the library knows of it.
type BookInfo struct {
	ID          int64             `json:"id"`
	Title       string            `json:"title"`
	Authors     []string          `json:"authors"`
	Series      string            `json:"series,omitempty"`
	SeriesIndex float64           `json:"seriesIndex,omitempty"`
	Tags        []string          `json:"tags"`
	Publisher   string            `json:"publisher,omitempty"`
	Pubdate     string            `json:"pubdate,omitempty"`   // "2006-01-02"
	Languages   []string          `json:"languages,omitempty"` // ISO 639-2, as "eng"
	Identifiers map[string]string `json:"identifiers"`
	Rating      int               `json:"rating,omitempty"` // 0–10, as calibre keeps it
	Comments    string            `json:"comments,omitempty"`
	Formats     []string          `json:"formats"`
	// Size is the size of the book's largest file, in bytes.
	Size int64 `json:"size,omitempty"`
	// UUID, TitleSort and AuthorSort are calibre's.
	UUID       string `json:"uuid,omitempty"`
	TitleSort  string `json:"titleSort,omitempty"`
	AuthorSort string `json:"authorSort,omitempty"`
	// Added and Modified are when the book was added and last changed,
	// RFC 3339.
	Added    string `json:"added,omitempty"`
	Modified string `json:"modified,omitempty"`
	HasCover bool   `json:"hasCover"`
}

// HookFile is a book's file a hook is handed: a copy in /work.
type HookFile struct {
	Path   string `json:"path"`
	Name   string `json:"name"`
	Format string `json:"format"`
}

// HookEvent is what a hook is called with: postimport gets the format
// added and its file, postadd every file of the new book, postdelete
// the format removed.
type HookEvent struct {
	Book   BookInfo   `json:"book"`
	Format string     `json:"format,omitempty"`
	File   *HookFile  `json:"file,omitempty"`
	Files  []HookFile `json:"files,omitempty"`
}

// Changes are what a hook asks to change in the book's metadata; a nil
// field is left as it is. Identifiers are merged, an empty value
// removing one.
type Changes struct {
	Title       *string           `json:"title"`
	Authors     *[]string         `json:"authors"`
	Series      *string           `json:"series"`
	SeriesIndex *float64          `json:"seriesIndex"`
	Tags        *[]string         `json:"tags"`
	Publisher   *string           `json:"publisher"`
	Pubdate     *string           `json:"pubdate"`
	Languages   *[]string         `json:"languages"`
	Identifiers map[string]string `json:"identifiers"`
	Rating      *int              `json:"rating"`
	Comments    *string           `json:"comments"`
}

// Hook calls hook (HookPostImport, HookPostAdd or HookPostDelete) when
// the plugin asks for it, with ev and copies of files (format → path) in
// its /work. It returns the changes the hook asks for, nil for none; they
// need permissions.library "write".
func (p *Plugin) Hook(ctx context.Context, hook string, ev HookEvent, files map[string]string) (*Changes, error) {
	if !p.Manifest.HasHook(hook) || !p.Defines(hook) {
		return nil, nil
	}
	if len(files) > 0 && p.dataDir == "" {
		return nil, &Error{Plugin: p.Manifest.ID, Call: hook, Kind: KindUnavailable, Msg: "has no folder to work in"}
	}
	// Lists are lists, empty or not, so a hook can read book.formats.length.
	b := &ev.Book
	for _, l := range []*[]string{&b.Authors, &b.Tags, &b.Formats} {
		if *l == nil {
			*l = []string{}
		}
	}
	if b.Identifiers == nil {
		b.Identifiers = map[string]string{}
	}
	p.workMu.Lock()
	defer p.workMu.Unlock()
	if len(files) > 0 {
		workRoot := filepath.Join(p.dataDir, workFolder)
		work := strconv.FormatInt(workSeq.Add(1), 10)
		if err := os.MkdirAll(filepath.Join(workRoot, work), 0o700); err != nil {
			return nil, err
		}
		defer emptyFolder(workRoot)
		for _, format := range slices.Sorted(mapKeys(files)) {
			name := filepath.Base(files[format])
			if _, err := copyFile(files[format], filepath.Join(workRoot, work, name)); err != nil {
				return nil, err
			}
			f := HookFile{Path: path.Join("/", workFolder, work, name), Name: name, Format: format}
			ev.Files = append(ev.Files, f)
		}
		if hook == HookPostImport && len(ev.Files) == 1 {
			ev.File, ev.Files = &ev.Files[0], nil
		}
	}
	raw, err := p.callWithin(ctx, ProcessTimeout, hook, ev)
	if err != nil {
		return nil, err
	}
	ch, err := hookResult(raw)
	if err == nil && ch != nil && p.Manifest.Permissions.Library != LibraryWrite {
		err = fmt.Errorf(`returned changes to the book; it needs permissions.library "write"`)
	}
	if err != nil {
		return nil, &Error{Plugin: p.Manifest.ID, Call: hook, Kind: KindInvalid, Msg: err.Error()}
	}
	return ch, nil
}

// hookResult reads what a hook returned: null, nothing, or { metadata }.
func hookResult(raw json.RawMessage) (*Changes, error) {
	if s := strings.TrimSpace(string(raw)); s == "" || s == "null" {
		return nil, nil
	}
	var o struct {
		Metadata *Changes `json:"metadata"`
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&o); err != nil {
		return nil, fmt.Errorf("returned %s; want null or { metadata }: %s", shorten(string(raw)), jsonProblem(err))
	}
	ch := o.Metadata
	if ch == nil {
		return nil, nil
	}
	if err := checkChanges(ch); err != nil {
		return nil, err
	}
	return ch, nil
}

// checkChanges checks the changes a plugin asks for.
func checkChanges(ch *Changes) error {
	if ch.Title != nil && strings.TrimSpace(*ch.Title) == "" {
		return fmt.Errorf("metadata.title: empty")
	}
	if ch.Pubdate != nil && *ch.Pubdate != "" && ParseDate(*ch.Pubdate).IsZero() {
		return fmt.Errorf("metadata.pubdate: %q isn’t a date like 2006, 2006-01 or 2006-01-02", *ch.Pubdate)
	}
	if ch.Rating != nil && (*ch.Rating < 0 || *ch.Rating > 10) {
		return fmt.Errorf("metadata.rating: %d isn’t from 0 to 10", *ch.Rating)
	}
	return nil
}

func mapKeys(m map[string]string) func(func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}
