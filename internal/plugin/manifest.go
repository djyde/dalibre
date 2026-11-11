// Package plugin runs Dalibre's plugins, which anyone may write: folders
// holding a plugin.json manifest and a JavaScript file, run in an
// embedded interpreter (no cgo, nothing for the user to install).
//
// Plugins are untrusted. A script has no file, process or network access
// of its own: it reaches the network only through the host's fetch, only
// to the hosts its manifest lists and the user granted, and never to the
// user's own machine or network unless granted that too. Every call has a
// deadline and a request budget, and what a plugin returns is checked
// field by field before the host uses it.
//
// A plugin declares what it adds to Dalibre in its manifest's
// capabilities. API version 1 has three: "bookSource" ("source" in the
// array form), a plugin that searches and browses a catalogue and says
// where a book's file is (the host downloads it), shown in the Get books
// page; "metadata", a plugin that looks up a book's details for the
// Edit metadata dialog's Fetch metadata; and "fileType", a plugin that
// works on books' files as they are added or when the user asks. Plugins
// never draw UI.
package plugin

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// DalibreVersion is the version of the Dalibre running plugins, which a
// manifest's dalibre field is held to; "" in a development build, which
// holds them to nothing. The app and the CLI set it at start.
var DalibreVersion string

var dalibrePattern = regexp.MustCompile(`^>=\s*(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$`)

// MinDalibre is the oldest Dalibre the plugin says it works in, as
// "0.4.0"; "" when it says nothing.
func (m *Manifest) MinDalibre() string {
	return strings.TrimSpace(strings.TrimPrefix(m.Dalibre, ">="))
}

// NeedsDalibre is the Dalibre version the plugin needs when this one is
// older, else "".
func (m *Manifest) NeedsDalibre() string {
	return NeedsDalibre(m.MinDalibre())
}

// NeedsDalibre is min when this Dalibre (DalibreVersion) is older, else
// "".
func NeedsDalibre(min string) string {
	if min == "" || DalibreVersion == "" || !NewerVersion(min, DalibreVersion) {
		return ""
	}
	return min
}

// FeaturesSince is the oldest Dalibre with what the manifest uses of plugin
// API 1: "0.4.0" for actions and the newer kinds of settings, "" for what
// every Dalibre has. A plugin using them should say so in its dalibre
// field.
func (m *Manifest) FeaturesSince() string {
	if len(m.Capabilities.Actions) > 0 {
		return "0.4.0"
	}
	for _, st := range m.Settings {
		if st.Type == SettingToggle || st.Type == SettingSelect || st.Type == SettingNumber {
			return "0.4.0"
		}
	}
	return ""
}

// APIVersion is the plugin API this Dalibre implements. Within a version
// the API only grows: new optional fields and functions, never a changed
// or removed one. A manifest asking for a newer version is refused.
const APIVersion = 1

// Icons a plugin may pick for itself, from Dalibre's own set: never an
// image, so a plugin can't dress up as another app.
var Icons = []string{"globe", "library", "book-open", "database", "network", "building", "bookmark", "layers"}

// Filter types a book source may declare.
const (
	FilterSelect   = "select"   // one of options
	FilterLanguage = "language" // one of options, which are ISO 639 codes; Dalibre names them
)

// Limits on what a plugin may be.
const (
	MaxScriptSize = 4 << 20
	MaxSettings   = 32
)

// Manifest is a plugin's plugin.json.
type Manifest struct {
	Schema      string `json:"$schema,omitempty"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	Version     string `json:"version"` // semver, as "1.2.0"
	API         int    `json:"api"`
	Description string `json:"description,omitempty"`
	Author      string `json:"author,omitempty"`
	Homepage    string `json:"homepage,omitempty"`
	License     string `json:"license,omitempty"`
	Main        string `json:"main,omitempty"` // main.js when empty
	// Dalibre is the oldest Dalibre the plugin works in, as ">=0.4.0":
	// for a plugin using what plugin API 1 gained after it began.
	Dalibre string `json:"dalibre,omitempty"`
	// Icon names the plugin's icon in Dalibre's set (Icons); "globe" when
	// empty.
	Icon         string       `json:"icon,omitempty"`
	Capabilities Capabilities `json:"capabilities"`
	Permissions  Permissions  `json:"permissions"`
	Settings     []Setting    `json:"settings,omitempty"`
	// Tools are programs the plugin runs outside its sandbox, by id
	// (tools.go).
	Tools map[string]Tool `json:"tools,omitempty"`
}

// Permissions are what a plugin may reach. The user grants them when the
// plugin is installed, and again when an update asks for more.
type Permissions struct {
	// Hosts the plugin may fetch and download from, redirects included,
	// as "example.com" or "*.example.com" (any subdomain, not the domain).
	Hosts []string `json:"hosts"`
	// LocalNetwork lets the plugin reach this computer and the local
	// network (loopback, private and link-local addresses), as an OPDS
	// server at home. Without it those addresses are refused, whatever
	// a listed host resolves to.
	LocalNetwork bool `json:"localNetwork,omitempty"`
	// Library is what the plugin sees of the user's library: LibraryRead
	// lets it see the books it is asked about (their title, authors,
	// identifiers and other details), which a metadata plugin and a file
	// type plugin's hooks need; LibraryWrite also lets its hooks change
	// those details, without asking each time.
	Library string `json:"library,omitempty"`
}

// Library access a plugin may ask for.
const (
	LibraryRead  = "read"
	LibraryWrite = "write"
)

// CanRead reports whether the permissions let the plugin see books.
func (p Permissions) CanRead() bool { return p.Library == LibraryRead || p.Library == LibraryWrite }

// Setting is a value the user gives a plugin, as an API key.
type Setting struct {
	Key   string `json:"key"`
	Title string `json:"title"`
	// Type is "text" (default), "secret", "file", "toggle" ("true" or
	// "false"), "select" (one of Options' values) or "number".
	Type        string `json:"type,omitempty"`
	Description string `json:"description,omitempty"`
	Default     string `json:"default,omitempty"`
	Required    bool   `json:"required,omitempty"`
	// Options are a select's choices.
	Options []Option `json:"options,omitempty"`
}

// Setting types.
const (
	SettingText     = "text"
	SettingSecret   = "secret"
	SettingFileType = "file"
	SettingToggle   = "toggle"
	SettingSelect   = "select"
	SettingNumber   = "number"
)

// Capabilities are what a plugin adds to Dalibre. In plugin.json they are
// an object, {"bookSource": {...}}, or, as API 1 began, an array of names:
// ["source"].
type Capabilities struct {
	BookSource *BookSource `json:"bookSource,omitempty"`
	Metadata   *Metadata   `json:"metadata,omitempty"`
	FileType   *FileType   `json:"fileType,omitempty"`
	Actions    []Action    `json:"actions,omitempty"`
}

// Action is something the user runs on books, or on the whole library,
// as calibre's interface action plugins without the interface: the plugin
// exports run(action, input), which returns what it found and what it
// wants changed, and Dalibre shows and applies it.
type Action struct {
	ID string `json:"id"`
	// Title names the action where it is chosen, ending in … when it asks
	// something first: "Find duplicates…".
	Title string `json:"title"`
	// Icon names the action's icon in Dalibre's set (ActionIcons).
	Icon string `json:"icon,omitempty"`
	// Scope is ScopeBooks, to run on the books chosen, from the book menu,
	// or ScopeLibrary, to run on every book, from the sidebar's Tools.
	Scope string `json:"scope"`
	// Books is how many books a ScopeBooks action takes.
	Books *BookCount `json:"books,omitempty"`
	// Formats, when set, are the formats a ScopeBooks action works on:
	// every book must have one.
	Formats []string `json:"formats,omitempty"`
	// Files gives a ScopeBooks action a copy of each book's file, in
	// /work.
	Files bool `json:"files,omitempty"`
	// Covers gives a ScopeBooks action a copy of each book's cover, in
	// /work, for the books that have one.
	Covers bool `json:"covers,omitempty"`
	// In, when InResults, shows the action only among the books this
	// plugin's own results list.
	In string `json:"in,omitempty"`
}

// BookCount bounds how many books an action takes; 0 is no bound.
type BookCount struct {
	Min int `json:"min,omitempty"`
	Max int `json:"max,omitempty"`
}

// What an Action runs on, and where it shows.
const (
	ScopeBooks   = "books"
	ScopeLibrary = "library"
	InResults    = "results"
)

// ActionIcons are the icons an action may pick, from Dalibre's own set.
var ActionIcons = []string{
	"puzzle", "sparkle", "copy", "check", "check-circle", "x", "layers", "file", "pencil", "tag", "search",
	"trash", "refresh", "replace", "bookmark", "book-open", "library", "database", "globe", "external",
}

// MaxActions is how many actions a plugin may have.
const MaxActions = 16

// Takes reports whether the action can run on n books whose formats are
// given, a list for each book.
func (a *Action) Takes(formats [][]string) bool {
	n := len(formats)
	if a.Scope != ScopeBooks || n == 0 {
		return false
	}
	if c := a.Books; c != nil && (n < c.Min || c.Max > 0 && n > c.Max) {
		return false
	}
	if len(a.Formats) == 0 {
		return true
	}
	for _, fs := range formats {
		if !slices.ContainsFunc(fs, func(f string) bool { return slices.Contains(a.Formats, f) }) {
			return false
		}
	}
	return true
}

// Label is the action's title without the … that says it asks first: what
// a job and a toast call it.
func (a *Action) Label() string {
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(strings.TrimSpace(a.Title), "…"), "..."))
}

// HasLibraryAction reports whether one of the plugin's actions runs on the
// whole library, and so sees every book.
func (m *Manifest) HasLibraryAction() bool {
	return slices.ContainsFunc(m.Capabilities.Actions, func(a Action) bool { return a.Scope == ScopeLibrary })
}

// Action is the plugin's action called id, nil when it has none.
func (m *Manifest) Action(id string) *Action {
	for i := range m.Capabilities.Actions {
		if m.Capabilities.Actions[i].ID == id {
			return &m.Capabilities.Actions[i]
		}
	}
	return nil
}

// FileType works on books' files, as calibre's file type plugins, with
// their hooks:
//
//   - process(file), as calibre's on_import, gets a copy of a book's file
//     and returns the path of a new one, or null to leave it. Dalibre
//     calls it for every file it adds (unless OnImport is false), and
//     when the user runs the plugin on books from the book menu.
//   - postimport(event) after a file was added to a book, as on_postimport;
//     postadd(event) after a book was added, as on_postadd; postdelete(event)
//     after a format was removed, as on_postdelete. They may return
//     changes to the book's metadata, which Dalibre writes (with
//     permissions.library "write"), where calibre's get its database.
type FileType struct {
	// Title names what the plugin does, in the book menu: "Clean up
	// EPUB". The plugin's name when empty.
	Title string `json:"title,omitempty"`
	// OnImport, when false, keeps the plugin from running on the files
	// Dalibre adds: it runs only when the user asks. True when absent.
	OnImport *bool `json:"onImport,omitempty"`
	// OnPostImport, OnPostAdd and OnPostDelete call postimport, postadd
	// and postdelete.
	OnPostImport bool `json:"onPostImport,omitempty"`
	OnPostAdd    bool `json:"onPostAdd,omitempty"`
	OnPostDelete bool `json:"onPostDelete,omitempty"`
}

// The hooks a file type plugin may export, after what calibre's do.
const (
	HookPostImport = "postimport"
	HookPostAdd    = "postadd"
	HookPostDelete = "postdelete"
)

// Hooks are the hooks the plugin asks to be called for.
func (m *Manifest) Hooks() []string {
	ft := m.Capabilities.FileType
	if ft == nil {
		return nil
	}
	var out []string
	if ft.OnPostImport {
		out = append(out, HookPostImport)
	}
	if ft.OnPostAdd {
		out = append(out, HookPostAdd)
	}
	if ft.OnPostDelete {
		out = append(out, HookPostDelete)
	}
	return out
}

// HasHook reports whether the plugin asks for hook.
func (m *Manifest) HasHook(hook string) bool { return slices.Contains(m.Hooks(), hook) }

// Metadata looks up books' details for the Edit metadata dialog. The
// plugin exports identify(book), which returns candidates, best first;
// Dalibre shows them next to the book's values and writes only the
// fields the user picks. It needs permissions.library "read".
type Metadata struct {
	// Fields are those the source can fill, as MetadataFields names them;
	// shown to the user. Empty means it doesn't say.
	Fields []string `json:"fields,omitempty"`
}

// MetadataFields are the fields a metadata source may declare.
var MetadataFields = []string{
	"title", "authors", "series", "tags", "publisher", "pubdate", "language", "identifiers", "cover", "description",
}

// BookSource is a catalogue Dalibre's Get books page searches, browses and
// downloads from. The plugin exports search (required), collections and
// list (with Collections), get and resolve.
type BookSource struct {
	// Filters narrow a search; Dalibre draws them and passes their values
	// in Query.Filters.
	Filters []Filter `json:"filters,omitempty"`
	// Collections says the source has collections to browse while the
	// search box is empty ("Most popular", "New"): it exports collections
	// and list.
	Collections bool `json:"collections,omitempty"`
}

// Filter is a control the Get books page shows under the search box.
type Filter struct {
	Key   string `json:"key"`
	Title string `json:"title"`
	// Type is FilterSelect or FilterLanguage.
	Type    string   `json:"type"`
	Options []Option `json:"options"`
	// Default is the option chosen at first; the first option when empty.
	Default string `json:"default,omitempty"`
}

// Option is one value of a Filter. A language option's title may be left
// out: Dalibre names the language.
type Option struct {
	Value string `json:"value"`
	Title string `json:"title,omitempty"`
}

// Names lists the capabilities, as plugin.json names them.
func (c Capabilities) Names() []string {
	var out []string
	if c.BookSource != nil {
		out = append(out, "bookSource")
	}
	if c.Metadata != nil {
		out = append(out, "metadata")
	}
	if c.FileType != nil {
		out = append(out, "fileType")
	}
	if len(c.Actions) > 0 {
		out = append(out, "actions")
	}
	return out
}

func (c *Capabilities) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) > 0 && data[0] == '[' {
		var names []string
		if err := json.Unmarshal(data, &names); err != nil {
			return err
		}
		for _, n := range names {
			switch n {
			case "source", "bookSource":
				if c.BookSource == nil {
					c.BookSource = &BookSource{}
				}
			case "metadata":
				if c.Metadata == nil {
					c.Metadata = &Metadata{}
				}
			default:
				return fmt.Errorf("unknown capability %q", n)
			}
		}
		return nil
	}
	type plain Capabilities
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var p plain
	if err := dec.Decode(&p); err != nil {
		if f, ok := strings.CutPrefix(err.Error(), "json: unknown field "); ok {
			return fmt.Errorf("unknown capability %s", f)
		}
		return err
	}
	*c = Capabilities(p)
	return nil
}

// IsBookSource reports whether the plugin is a book source.
func (m *Manifest) IsBookSource() bool { return m.Capabilities.BookSource != nil }

// IsMetadataSource reports whether the plugin looks up books' metadata.
func (m *Manifest) IsMetadataSource() bool { return m.Capabilities.Metadata != nil }

// IsFileType reports whether the plugin works on books' files.
func (m *Manifest) IsFileType() bool { return m.Capabilities.FileType != nil }

// RunsOnImport reports whether the plugin processes the files Dalibre adds.
func (m *Manifest) RunsOnImport() bool {
	ft := m.Capabilities.FileType
	return ft != nil && (ft.OnImport == nil || *ft.OnImport)
}

// ActionTitle is what the book menu calls running the plugin: its
// fileType title, else its name.
func (m *Manifest) ActionTitle() string {
	if ft := m.Capabilities.FileType; ft != nil && strings.TrimSpace(ft.Title) != "" {
		return strings.TrimSpace(ft.Title)
	}
	return m.Name
}

// Filters are the book source's filters; none when it isn't one.
func (m *Manifest) Filters() []Filter {
	if m.Capabilities.BookSource == nil {
		return nil
	}
	return m.Capabilities.BookSource.Filters
}

// HasCollections reports whether the book source has collections to browse.
func (m *Manifest) HasCollections() bool {
	return m.Capabilities.BookSource != nil && m.Capabilities.BookSource.Collections
}

// IconName is the plugin's icon, "globe" by default.
func (m *Manifest) IconName() string {
	if m.Icon == "" {
		return "globe"
	}
	return m.Icon
}

// Reserved reports whether id is kept for plugins that ship with Dalibre.
func Reserved(id string) bool {
	return slices.ContainsFunc(reservedPrefix, func(p string) bool { return id == p || strings.HasPrefix(id, p+".") })
}

var (
	idPattern      = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$`)
	semverPattern  = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)
	keyPattern     = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)
	hostPattern    = regexp.MustCompile(`^(\*\.)?[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$`)
	reservedPrefix = []string{"dalibre"}
)

// ReadManifest reads and checks dir/plugin.json.
func ReadManifest(dir string) (*Manifest, error) {
	data, err := os.ReadFile(filepath.Join(dir, "plugin.json"))
	if err != nil {
		return nil, err
	}
	return ParseManifest(data)
}

// ParseManifest parses and checks a plugin.json. Unknown fields are
// errors, so a misspelt one is caught rather than ignored.
func ParseManifest(data []byte) (*Manifest, error) {
	var m Manifest
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("plugin.json: %w", err)
	}
	if err := m.check(); err != nil {
		return nil, fmt.Errorf("plugin.json: %w", err)
	}
	return &m, nil
}

func (m *Manifest) check() error {
	switch {
	case len(m.ID) > 64 || !idPattern.MatchString(m.ID):
		return fmt.Errorf("id %q must be lowercase letters, digits and '-', in dot-separated parts, as \"gutenberg\" or \"com.example.books\"", m.ID)
	case strings.TrimSpace(m.Name) == "" || len(m.Name) > 64:
		return errors.New("name must be 1 to 64 characters")
	case !semverPattern.MatchString(m.Version):
		return fmt.Errorf("version %q must be a semantic version, as \"1.0.0\"", m.Version)
	case m.API < 1:
		return errors.New("api is missing; this Dalibre implements api 1")
	case m.API > APIVersion:
		return fmt.Errorf("needs plugin API %d; this Dalibre has %d, update Dalibre", m.API, APIVersion)
	case m.Dalibre != "" && !dalibrePattern.MatchString(m.Dalibre):
		return fmt.Errorf("dalibre %q must be a version to start from, as \">=0.4.0\"", m.Dalibre)
	case m.NeedsDalibre() != "":
		return fmt.Errorf("needs Dalibre %s or newer; this is %s, update Dalibre", m.NeedsDalibre(), DalibreVersion)
	case len(m.Capabilities.Names()) == 0:
		return errors.New("capabilities are missing")
	case m.Icon != "" && !slices.Contains(Icons, m.Icon):
		return fmt.Errorf("icon %q isn’t one of %s", m.Icon, strings.Join(Icons, ", "))
	case len(m.Settings) > MaxSettings:
		return fmt.Errorf("at most %d settings", MaxSettings)
	}
	if m.Main == "" {
		m.Main = "main.js"
	}
	if main := filepath.ToSlash(m.Main); filepath.IsAbs(m.Main) || strings.HasPrefix(main, "/") ||
		slices.Contains(strings.Split(main, "/"), "..") || !slices.Contains([]string{".js", ".mjs", ".ts", ".mts"}, path.Ext(main)) {
		return fmt.Errorf("main %q must be a .js or .ts file inside the plugin's folder", m.Main)
	}
	if m.IsBookSource() {
		if len(m.Permissions.Hosts) == 0 {
			return errors.New("a book source must list the hosts it reaches in permissions.hosts")
		}
		if err := checkFilters(m.Capabilities.BookSource.Filters); err != nil {
			return fmt.Errorf("capabilities.bookSource.%w", err)
		}
	}
	if md := m.Capabilities.Metadata; md != nil {
		if len(m.Permissions.Hosts) == 0 {
			return errors.New("a metadata source must list the hosts it reaches in permissions.hosts")
		}
		if !m.Permissions.CanRead() {
			return errors.New(`a metadata source sees the books it looks up: it needs permissions.library "read"`)
		}
		for _, f := range md.Fields {
			if !slices.Contains(MetadataFields, f) {
				return fmt.Errorf("capabilities.metadata.fields: %q isn’t one of %s", f, strings.Join(MetadataFields, ", "))
			}
		}
	}
	if ft := m.Capabilities.FileType; ft != nil && len([]rune(ft.Title)) > 64 {
		return errors.New("capabilities.fileType.title is over 64 characters")
	}
	if err := m.checkTools(); err != nil {
		return err
	}
	if err := m.checkActions(); err != nil {
		return fmt.Errorf("capabilities.%w", err)
	}
	if len(m.Hooks()) > 0 && !m.Permissions.CanRead() {
		return errors.New(`a file type plugin's hooks see the books they run for: they need permissions.library "read" (or "write" to change them)`)
	}
	if l := m.Permissions.Library; l != "" && l != LibraryRead && l != LibraryWrite {
		return fmt.Errorf(`permissions.library %q must be "read" or "write" (or left out)`, l)
	}
	for _, h := range m.Permissions.Hosts {
		if !hostPattern.MatchString(h) || !strings.Contains(strings.TrimPrefix(h, "*."), ".") && h != "localhost" {
			return fmt.Errorf("host %q must be a lowercase host name, as \"example.com\" or \"*.example.com\"", h)
		}
	}
	keys := map[string]bool{}
	for _, s := range m.Settings {
		switch {
		case !keyPattern.MatchString(s.Key):
			return fmt.Errorf("setting key %q must be letters, digits and '_', starting with a letter", s.Key)
		case keys[s.Key]:
			return fmt.Errorf("setting %q is defined twice", s.Key)
		case strings.TrimSpace(s.Title) == "":
			return fmt.Errorf("setting %q has no title", s.Key)
		case !slices.Contains([]string{"", SettingText, SettingSecret, SettingFileType, SettingToggle, SettingSelect, SettingNumber}, s.Type):
			return fmt.Errorf("setting %q: type must be \"text\", \"secret\", \"file\", \"toggle\", \"select\" or \"number\"", s.Key)
		case s.Type == SettingSelect && (len(s.Options) == 0 || len(s.Options) > 64):
			return fmt.Errorf("setting %q: a select has 1 to 64 options", s.Key)
		case s.Type != SettingSelect && len(s.Options) > 0:
			return fmt.Errorf("setting %q: only a select has options", s.Key)
		case s.Type == SettingSelect && s.Default != "" && !slices.ContainsFunc(s.Options, func(o Option) bool { return o.Value == s.Default }):
			return fmt.Errorf("setting %q: default %q isn’t one of its options", s.Key, s.Default)
		case s.Type == SettingToggle && s.Default != "" && s.Default != "true" && s.Default != "false":
			return fmt.Errorf("setting %q: a toggle's default is \"true\" or \"false\"", s.Key)
		case s.Type == SettingNumber && s.Default != "" && !numberPattern.MatchString(s.Default):
			return fmt.Errorf("setting %q: default %q isn’t a number", s.Key, s.Default)
		}
		keys[s.Key] = true
	}
	return nil
}

var numberPattern = regexp.MustCompile(`^-?\d+(\.\d+)?$`)

// IsNumber reports whether s is a number setting's value: digits, with
// an optional minus and fraction.
func IsNumber(s string) bool { return numberPattern.MatchString(s) }

var actionIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

func (m *Manifest) checkActions() error {
	as := m.Capabilities.Actions
	if len(as) == 0 {
		return nil
	}
	if len(as) > MaxActions {
		return fmt.Errorf("actions: at most %d", MaxActions)
	}
	if !m.Permissions.CanRead() {
		return errors.New(`actions see the books they run on: they need permissions.library "read" (or "write" to change them without asking)`)
	}
	ids := map[string]bool{}
	for i := range as {
		a := &as[i]
		at := fmt.Sprintf("actions[%d]", i)
		books := a.Scope == ScopeBooks
		switch {
		case !actionIDPattern.MatchString(a.ID):
			return fmt.Errorf("%s.id %q must be lowercase letters, digits and '-', starting with a letter", at, a.ID)
		case ids[a.ID]:
			return fmt.Errorf("%s.id %q is used twice", at, a.ID)
		case a.Label() == "" || len([]rune(a.Title)) > 64:
			return fmt.Errorf("%s.title must be 1 to 64 characters", at)
		case a.Icon != "" && !slices.Contains(ActionIcons, a.Icon):
			return fmt.Errorf("%s.icon %q isn’t one of %s", at, a.Icon, strings.Join(ActionIcons, ", "))
		case a.Scope != ScopeBooks && a.Scope != ScopeLibrary:
			return fmt.Errorf("%s.scope must be %q or %q", at, ScopeBooks, ScopeLibrary)
		case !books && (a.Books != nil || len(a.Formats) > 0 || a.Files || a.In != ""):
			return fmt.Errorf("%s: books, formats, files and in are for an action with scope %q", at, ScopeBooks)
		case a.In != "" && a.In != InResults:
			return fmt.Errorf("%s.in must be %q (or left out)", at, InResults)
		case a.Books != nil && (a.Books.Min < 0 || a.Books.Max < 0 || a.Books.Max > 0 && a.Books.Max < a.Books.Min):
			return fmt.Errorf("%s.books: min and max must be 0 or more, and max not under min", at)
		}
		for j, f := range a.Formats {
			if f == "" || f != strings.ToUpper(f) {
				return fmt.Errorf("%s.formats[%d] %q must be a format in upper case, as \"EPUB\"", at, j, f)
			}
		}
		ids[a.ID] = true
	}
	return nil
}

// MaxFilters and MaxOptions bound a book source's filters.
const (
	MaxFilters = 4
	MaxOptions = 64
)

func checkFilters(fs []Filter) error {
	if len(fs) > MaxFilters {
		return fmt.Errorf("filters: at most %d", MaxFilters)
	}
	keys := map[string]bool{}
	for i := range fs {
		f := &fs[i]
		at := fmt.Sprintf("filters[%d]", i)
		switch {
		case !keyPattern.MatchString(f.Key):
			return fmt.Errorf("%s.key %q must be letters, digits and '_', starting with a letter", at, f.Key)
		case keys[f.Key]:
			return fmt.Errorf("%s.key %q is used twice", at, f.Key)
		case strings.TrimSpace(f.Title) == "":
			return fmt.Errorf("%s has no title", at)
		case f.Type != FilterSelect && f.Type != FilterLanguage:
			return fmt.Errorf("%s.type must be %q or %q", at, FilterSelect, FilterLanguage)
		case len(f.Options) == 0 || len(f.Options) > MaxOptions:
			return fmt.Errorf("%s needs 1 to %d options", at, MaxOptions)
		}
		keys[f.Key] = true
		vals := map[string]bool{}
		for j, o := range f.Options {
			if vals[o.Value] {
				return fmt.Errorf("%s.options[%d].value %q is used twice", at, j, o.Value)
			}
			vals[o.Value] = true
			if f.Type == FilterSelect && strings.TrimSpace(o.Title) == "" {
				return fmt.Errorf("%s.options[%d] has no title", at, j)
			}
		}
		if f.Default != "" && !vals[f.Default] {
			return fmt.Errorf("%s.default %q isn’t one of its options", at, f.Default)
		}
	}
	return nil
}

// Value is the filter's option for v: v when it is one, else the default.
func (f Filter) Value(v string) string {
	for _, o := range f.Options {
		if o.Value == v {
			return v
		}
	}
	if f.Default != "" {
		return f.Default
	}
	return f.Options[0].Value
}

// allows reports whether the permissions let the plugin reach host.
func (p *Permissions) allows(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, h := range p.Hosts {
		if suffix, ok := strings.CutPrefix(h, "*"); ok {
			if strings.HasSuffix(host, suffix) && len(host) > len(suffix) {
				return true
			}
		} else if host == h {
			return true
		}
	}
	return false
}

// Covers reports whether p, as granted, allows everything want asks
// for. An update asking for more than was granted needs the user again.
func (p Permissions) Covers(want Permissions) bool {
	if want.LocalNetwork && !p.LocalNetwork {
		return false
	}
	switch want.Library {
	case LibraryRead:
		if !p.CanRead() {
			return false
		}
	case LibraryWrite:
		if p.Library != LibraryWrite {
			return false
		}
	}
	for _, h := range want.Hosts {
		if !slices.Contains(p.Hosts, h) {
			return false
		}
	}
	return true
}

// Describe lists the permissions as the user is asked to grant them.
func (p Permissions) Describe() []string {
	var out []string
	for _, h := range p.Hosts {
		if d, ok := strings.CutPrefix(h, "*."); ok {
			out = append(out, "Connect to any subdomain of "+d)
		} else {
			out = append(out, "Connect to "+h)
		}
	}
	if p.LocalNetwork {
		out = append(out, "Connect to this computer and your local network")
	}
	switch p.Library {
	case LibraryRead:
		out = append(out, "See the title, authors and identifiers of books you look up with it")
	case LibraryWrite:
		out = append(out, "See and change the details of the books it runs for")
	}
	return out
}

// Asks lists what the plugin asks to do, as the user is asked to grant
// it: its permissions, with what its actions see of the library.
func (m *Manifest) Asks() []string {
	out := m.Permissions.Describe()
	if len(m.Capabilities.Actions) == 0 || m.Permissions.Library == "" {
		return out
	}
	books := "the books you run it on"
	if m.HasLibraryAction() {
		books = "every book in your library"
	}
	s := "See the details of " + books
	if m.Permissions.Library == LibraryWrite {
		s += ", and change them"
	}
	out[len(out)-1] = s
	return out
}

// SecretService is the service in the system's credential store under
// which Dalibre keeps plugins' secret settings: the app's identifier.
const SecretService = "org.randynamic.dalibre"

// SecretKey is the credential store's key for secret setting key of
// plugin id.
func SecretKey(id, key string) string { return "plugin-" + id + "-" + key }
