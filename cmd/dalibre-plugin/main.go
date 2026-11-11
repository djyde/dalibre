// Command dalibre-plugin is for writing Dalibre plugins: it makes a new
// one, runs it as Dalibre would without the app, packs it, and installs
// packages into the plugins folder.
package main

import (
	"bufio"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"maps"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"

	"dalibre/internal/calibre"
	"dalibre/internal/plugin"
	"dalibre/internal/settings"
	"dalibre/sdk"
)

const usage = `dalibre-plugin is for writing Dalibre plugins.

  dalibre-plugin init <dir> [--id ID] [--name NAME]   start a plugin from the template
  dalibre-plugin check <dir>                          validate plugin.json and load main.js
  dalibre-plugin dev <dir>                            add it to the running Dalibre, which reloads it
                                                      as you save; needs Dalibre's MCP server on
  dalibre-plugin search <dir> <text> [--filter key=value] [--page N] [--cursor C] [--json]
  dalibre-plugin collections <dir>                    list what the source offers to browse
  dalibre-plugin list <dir> <collection> [--filter key=value] [--page N] [--json]
  dalibre-plugin get <dir> <text> [--item N] [--json]  the detail panel's view of a result
  dalibre-plugin download <dir> <text> [--item N] [--format EPUB] [-o DIR]
  dalibre-plugin identify <dir> [title] [--author A] [--isbn N] [--id type:value] [--language L] [--json]
                                                      what Fetch metadata shows for a book
  dalibre-plugin process <dir> <file> [--manual] [-o DIR]  run a file type plugin on a book's file
  dalibre-plugin run <dir> <action> [file…] [--library DIR [--book ID…]] [--answer key=value] [-o DIR] [--json]
                                                      run an action: on the files, as books, or on a
                                                      calibre library's books
  dalibre-plugin tool add <dir> <brew:owner/tap/name | gh:owner/repo@tag> [--id ID] [--bin NAME]
                    [--brew F] [--winget ID] [--scoop B/N] [--min V]
                                                      declare a program the plugin runs (tools)
  dalibre-plugin pack <dir> [-o DIR]                  make <id>-<version>.dalibre-plugin
  dalibre-plugin install <package> [--yes]            install into Dalibre's plugins folder
  dalibre-plugin uninstall <id>
  dalibre-plugin installed                            list installed plugins
  dalibre-plugin version                              the Dalibre version it comes with

  dalibre-plugin docs [topic…] [--all]                the plugin API's guide: its topics, or one
  dalibre-plugin types [Name…] [--all]                the API's types, from dalibre.d.ts

search, list, get, download, identify, process and run take --tool id=path to run
a tool from a file; otherwise a package manager's install, or the build, downloaded
once into the cache.

search, list, get, download, identify and process take --set key=value for the
plugin's settings (a file setting takes a file's path, copied into the plugin's
.data folder), and --filter key=value for its filters; the values saved in
Dalibre are used otherwise. The plugins folder is
$DALIBRE_PLUGINS, or Dalibre's in your configuration folder.
`

// version is the Dalibre version this tool comes with, set by the release
// build (-ldflags "-X main.version=0.4.0"); "" in a development build,
// which loads plugins needing any version.
var version string

// pluginAPI is the plugin API version this tool runs plugins with.
const pluginAPI = plugin.APIVersion

func main() {
	plugin.DalibreVersion = version
	if len(os.Args) == 2 && (os.Args[1] == "version" || os.Args[1] == "--version") {
		v := version
		if v == "" {
			v = "development build"
		}
		fmt.Printf("dalibre-plugin %s, plugin API %d\n", v, plugin.APIVersion)
		return
	}
	if len(os.Args) < 2 || os.Args[1] == "-h" || os.Args[1] == "--help" || os.Args[1] == "help" {
		fmt.Print(usage)
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	cmds := map[string]func(context.Context, []string) error{
		"init": cmdInit, "check": cmdCheck, "dev": cmdDev, "docs": cmdDocs, "types": cmdTypes, "search": cmdSearch, "download": cmdDownload,
		"collections": cmdCollections, "list": cmdListCollection, "get": cmdGet, "identify": cmdIdentify, "process": cmdProcess, "run": cmdRun,
		"pack": cmdPack, "tool": cmdTool, "install": cmdInstall, "uninstall": cmdUninstall, "installed": cmdList,
	}
	cmd, ok := cmds[os.Args[1]]
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
	if err := cmd(ctx, os.Args[2:]); err != nil {
		report(err)
		os.Exit(1)
	}
}

// report prints an error, with what a plugin's author needs to fix it.
func report(err error) {
	var pe *plugin.Error
	if errors.As(err, &pe) {
		fmt.Fprintf(os.Stderr, "error (%s): %s\n", pe.Kind, pe.Error())
		if pe.Stack != "" {
			fmt.Fprintf(os.Stderr, "\n%s\n", pe.Stack)
		}
		return
	}
	fmt.Fprintln(os.Stderr, "error:", err)
}

// parse parses flags wherever they are among the arguments, and returns
// the arguments that aren't flags, checking there are want of them.
func parse(fs *flag.FlagSet, args []string, want ...string) []string {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			os.Exit(2)
		}
		if fs.NArg() == 0 {
			break
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
	if len(pos) != len(want) {
		fmt.Fprintf(os.Stderr, "usage: dalibre-plugin %s %s\n", fs.Name(), "<"+strings.Join(want, "> <")+">")
		os.Exit(2)
	}
	return pos
}

type setFlags map[string]string

// toolFlag is --tool id=path, for every command that runs a plugin.
var toolFlag = setFlags{}

func (s setFlags) String() string { return "" }
func (s setFlags) Set(v string) error {
	k, val, ok := strings.Cut(v, "=")
	if !ok {
		return errors.New("want key=value")
	}
	s[k] = val
	return nil
}

// load loads the plugin in dir with the settings saved in Dalibre and
// those given, logging to stderr.
func load(dir string, set setFlags) (*plugin.Plugin, error) {
	m, err := plugin.ReadManifest(dir)
	if err != nil {
		return nil, err
	}
	values := map[string]string{}
	if st := settings.Load(settings.DefaultPath()).Plugins[m.ID]; st != nil {
		for k, v := range st.Values {
			values[k] = v
		}
	}
	// Secret settings are in the system's credential store, where the
	// app keeps them; --set is used instead when given.
	for _, st := range m.Settings {
		if _, given := set[st.Key]; st.Type != "secret" || given || values[st.Key] != "" {
			continue
		}
		if v, err := mygo.Secrets.Service(plugin.SecretService).Get(plugin.SecretKey(m.ID, st.Key)); err == nil {
			values[st.Key] = string(v)
		}
	}
	// The plugin's own folder is .data in its folder, which pack leaves
	// out. A file setting given with --set is a file here, copied in.
	own := filepath.Join(dir, ".data")
	for k, v := range set {
		i := slices.IndexFunc(m.Settings, func(s plugin.Setting) bool { return s.Key == k })
		if i < 0 {
			return nil, fmt.Errorf("--set %s: plugin.json has no setting %q", k, k)
		}
		if m.Settings[i].Type == "file" && v != "" {
			p, err := plugin.StoreSettingFile(own, k, v)
			if err != nil {
				return nil, fmt.Errorf("--set %s: %w", k, err)
			}
			v = p
		}
		values[k] = v
	}
	tools, err := toolPaths(context.Background(), m, toolFlag)
	if err != nil {
		return nil, err
	}
	return plugin.Load(dir, plugin.Options{Settings: values, DataDir: own, Tools: tools, Logf: func(f string, a ...any) {
		fmt.Fprintf(os.Stderr, f+"\n", a...)
	}})
}

func cmdInit(_ context.Context, args []string) error {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	id := fs.String("id", "", "the plugin's id (default: the folder's name)")
	name := fs.String("name", "", "the plugin's name (default: the id)")
	dir := parse(fs, args, "dir")[0]
	if *id == "" {
		*id = strings.ToLower(filepath.Base(dir))
	}
	if *name == "" {
		*name = *id
	}
	if _, err := sdk.Init(dir, *id, *name); err != nil {
		return err
	}
	if _, err := plugin.ReadManifest(dir); err != nil {
		return fmt.Errorf("%w (is %q a valid id? pass --id)", err, *id)
	}
	fmt.Printf("Made %s. Edit plugin.json and main.js, then:\n  dalibre-plugin check %s\n  dalibre-plugin dev %s     # try it in Dalibre as you edit\n", dir, dir, dir)
	return nil
}

func cmdCheck(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("check", flag.ExitOnError)
	dir := parse(fs, args, "dir")[0]
	p, err := plugin.Load(dir, plugin.Options{DataDir: filepath.Join(dir, ".data"), Logf: func(string, ...any) {}})
	if err != nil {
		return err
	}
	m := p.Manifest
	fmt.Printf("%s %s (%s), plugin API %d\n", m.Name, m.Version, m.ID, m.API)
	fmt.Printf("capabilities: %s\n", strings.Join(m.Capabilities.Names(), ", "))
	for _, f := range m.Filters() {
		var vals []string
		for _, o := range f.Options {
			vals = append(vals, fmt.Sprintf("%q", o.Value))
		}
		fmt.Printf("filter %s (%s): %s, default %q\n", f.Key, f.Type, strings.Join(vals, " "), f.Value(""))
	}
	if plugin.Reserved(m.ID) {
		fmt.Println("note: a reserved id; only a plugin shipped inside Dalibre may use it")
	}
	if placeholderID(m.ID) {
		fmt.Printf("warning: %q is the template's id; give the plugin its own, such as com.<you>.<plugin>\n", m.ID)
	}
	for _, fn := range []string{"search", "collections", "list", "get", "resolve", "identify"} {
		if p.Defines(fn) {
			fmt.Printf("exports %s\n", fn)
		}
	}
	fmt.Println("asks to:")
	for _, d := range m.Asks() {
		fmt.Println("  " + d)
	}
	for _, id := range m.ToolIDs() {
		fmt.Printf("  Run %s %s on this computer (not sandboxed)\n", id, m.Tools[id].Version)
	}
	if err := checkTools(ctx, m); err != nil {
		return err
	}
	if m.Description == "" {
		fmt.Println("warning: no description; users see it before installing")
	}
	if m.Homepage == "" {
		fmt.Println("warning: no homepage")
	}
	if since := m.FeaturesSince(); since != "" && plugin.NewerVersion(since, m.MinDalibre()) {
		fmt.Printf("warning: uses what Dalibre %s added; say \"dalibre\": \">=%s\" in plugin.json so older ones don’t offer it\n", since, since)
	}
	fmt.Println("ok")
	return nil
}

func cmdSearch(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("search", flag.ExitOnError)
	page := fs.Int("page", 1, "the page")
	cursor := fs.String("cursor", "", "the cursor a previous page returned")
	asJSON := fs.Bool("json", false, "print the results as JSON")
	set, filters := setFlags{}, setFlags{}
	fs.Var(set, "set", "a setting, as key=value (repeatable)")
	fs.Var(toolFlag, "tool", "run tool id from a file, as id=path (repeatable)")
	fs.Var(filters, "filter", "a filter's value, as key=value (repeatable)")
	pos := parse(fs, args, "dir", "text")
	p, err := load(pos[0], set)
	if err != nil {
		return err
	}
	if err := checkFilters(p, filters); err != nil {
		return err
	}
	res, err := p.Search(ctx, plugin.Query{Text: pos[1], Page: *page, Cursor: *cursor, Filters: filters})
	if err != nil {
		return err
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(res)
	}
	printPage(res)
	return nil
}

// checkFilters checks --filter values against the plugin's filters.
func checkFilters(p *plugin.Plugin, filters map[string]string) error {
	for k, v := range filters {
		i := slices.IndexFunc(p.Manifest.Filters(), func(f plugin.Filter) bool { return f.Key == k })
		if i < 0 {
			return fmt.Errorf("--filter %s: plugin.json declares no filter %q", k, k)
		}
		if f := p.Manifest.Filters()[i]; f.Value(v) != v {
			return fmt.Errorf("--filter %s=%s: not one of the filter's options", k, v)
		}
	}
	return nil
}

func cmdCollections(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("collections", flag.ExitOnError)
	set := setFlags{}
	fs.Var(set, "set", "a setting, as key=value (repeatable)")
	fs.Var(toolFlag, "tool", "run tool id from a file, as id=path (repeatable)")
	p, err := load(parse(fs, args, "dir")[0], set)
	if err != nil {
		return err
	}
	if !p.Manifest.HasCollections() {
		return errors.New("plugin.json doesn’t declare collections")
	}
	cs, err := p.Collections(ctx)
	if err != nil {
		return err
	}
	for _, c := range cs {
		fmt.Printf("%s\t%s\n", c.ID, c.Title)
	}
	return nil
}

func cmdListCollection(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	page := fs.Int("page", 1, "the page")
	cursor := fs.String("cursor", "", "the cursor a previous page returned")
	asJSON := fs.Bool("json", false, "print the results as JSON")
	set, filters := setFlags{}, setFlags{}
	fs.Var(set, "set", "a setting, as key=value (repeatable)")
	fs.Var(toolFlag, "tool", "run tool id from a file, as id=path (repeatable)")
	fs.Var(filters, "filter", "a filter's value, as key=value (repeatable)")
	pos := parse(fs, args, "dir", "collection")
	p, err := load(pos[0], set)
	if err != nil {
		return err
	}
	if err := checkFilters(p, filters); err != nil {
		return err
	}
	res, err := p.List(ctx, plugin.Query{Collection: pos[1], Page: *page, Cursor: *cursor, Filters: filters})
	if err != nil {
		return err
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(res)
	}
	printPage(res)
	return nil
}

func cmdGet(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("get", flag.ExitOnError)
	item := fs.Int("item", 1, "which result, from 1")
	asJSON := fs.Bool("json", false, "print the result as JSON")
	set, filters := setFlags{}, setFlags{}
	fs.Var(set, "set", "a setting, as key=value (repeatable)")
	fs.Var(toolFlag, "tool", "run tool id from a file, as id=path (repeatable)")
	fs.Var(filters, "filter", "a filter's value, as key=value (repeatable)")
	pos := parse(fs, args, "dir", "text")
	p, err := load(pos[0], set)
	if err != nil {
		return err
	}
	res, err := p.Search(ctx, plugin.Query{Text: pos[1], Filters: filters})
	if err != nil {
		return err
	}
	if *item < 1 || *item > len(res.Items) {
		return fmt.Errorf("--item %d: search returned %d results", *item, len(res.Items))
	}
	r, err := p.Get(ctx, res.Items[*item-1])
	if err != nil {
		return err
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(r)
	}
	printPage(plugin.Page{Items: []plugin.Result{r}})
	if r.URL != "" {
		fmt.Println("    page", r.URL)
	}
	for _, f := range r.Fields {
		fmt.Printf("    %s: %s\n", f.Label, f.Value)
	}
	if len(r.Tags) > 0 {
		fmt.Println("    tags:", strings.Join(r.Tags, "; "))
	}
	if r.Description != "" {
		fmt.Println("    " + r.Description)
	}
	return nil
}

func cmdIdentify(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("identify", flag.ExitOnError)
	var authors, ids multiFlag
	fs.Var(&authors, "author", "an author (repeatable)")
	fs.Var(&ids, "id", "an identifier, as type:value (repeatable)")
	isbn := fs.String("isbn", "", "the book's ISBN")
	lang := fs.String("language", "", "the book's language, ISO 639")
	asJSON := fs.Bool("json", false, "print the candidates as JSON")
	set := setFlags{}
	fs.Var(set, "set", "a setting, as key=value (repeatable)")
	fs.Var(toolFlag, "tool", "run tool id from a file, as id=path (repeatable)")
	var pos []string
	for rest := args; ; {
		if err := fs.Parse(rest); err != nil {
			os.Exit(2)
		}
		if fs.NArg() == 0 {
			break
		}
		pos = append(pos, fs.Arg(0))
		rest = fs.Args()[1:]
	}
	if len(pos) < 1 || len(pos) > 2 {
		fmt.Fprintln(os.Stderr, "usage: dalibre-plugin identify <dir> [title] [--author A] [--isbn N] [--id type:value]")
		os.Exit(2)
	}
	p, err := load(pos[0], set)
	if err != nil {
		return err
	}
	book := plugin.Book{Authors: authors, Identifiers: map[string]string{}, Language: *lang}
	if len(pos) == 2 {
		book.Title = pos[1]
	}
	for _, id := range ids {
		k, v, ok := strings.Cut(id, ":")
		if !ok {
			return fmt.Errorf("--id %q: want type:value", id)
		}
		book.Identifiers[k] = v
	}
	if *isbn != "" {
		book.Identifiers["isbn"] = *isbn
	}
	cands, err := p.Identify(ctx, book)
	if err != nil {
		return err
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(cands)
	}
	if len(cands) == 0 {
		fmt.Println("no candidates")
	}
	for i, r := range cands {
		fmt.Printf("%2d. %s", i+1, r.Title)
		if len(r.Authors) > 0 {
			fmt.Printf(" — %s", strings.Join(r.Authors, ", "))
		}
		fmt.Println()
		var line []string
		for _, s := range []string{r.Publisher, r.Pubdate, r.Language} {
			if s != "" {
				line = append(line, s)
			}
		}
		if r.Series != "" {
			line = append(line, "series "+r.Series)
		}
		if len(line) > 0 {
			fmt.Println("    " + strings.Join(line, " · "))
		}
		var idents []string
		for _, k := range slices.Sorted(maps.Keys(r.Identifiers)) {
			idents = append(idents, k+":"+r.Identifiers[k])
		}
		if len(idents) > 0 {
			fmt.Println("    " + strings.Join(idents, ", "))
		}
		if r.Cover != "" {
			fmt.Println("    cover", r.Cover)
		}
		if len(r.Tags) > 0 {
			fmt.Println("    tags:", strings.Join(r.Tags, "; "))
		}
	}
	return nil
}

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ", ") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

func printPage(res plugin.Page) {
	for i, r := range res.Items {
		fmt.Printf("%2d. %s", i+1, r.Title)
		if len(r.Authors) > 0 {
			fmt.Printf(" — %s", strings.Join(r.Authors, ", "))
		}
		fmt.Println()
		var formats []string
		for _, f := range r.Formats {
			s := f.Format
			if f.Size > 0 {
				s += fmt.Sprintf(" %.1f MB", float64(f.Size)/(1<<20))
			}
			formats = append(formats, s)
		}
		fmt.Printf("    id %s · %s", r.ID, strings.Join(formats, ", "))
		if r.Language != "" {
			fmt.Printf(" · %s", r.Language)
		}
		fmt.Println()
	}
	if len(res.Items) == 0 {
		fmt.Println("no results")
	}
	if res.HasMore {
		next := "--page N+1"
		if res.Cursor != "" {
			next = "--cursor " + res.Cursor
		}
		fmt.Printf("more: %s\n", next)
	}
	for _, w := range res.Warnings {
		fmt.Fprintln(os.Stderr, "warning: search returned", w)
	}
}

func cmdProcess(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("process", flag.ExitOnError)
	manual := fs.Bool("manual", false, `call it as the book menu does (reason "manual"), not as an import`)
	out := fs.String("o", ".", "the folder to save the new file in")
	set := setFlags{}
	fs.Var(set, "set", "a setting, as key=value (repeatable)")
	fs.Var(toolFlag, "tool", "run tool id from a file, as id=path (repeatable)")
	pos := parse(fs, args, "dir", "file")
	p, err := load(pos[0], set)
	if err != nil {
		return err
	}
	defer p.Close()
	reason := plugin.ReasonImport
	if *manual {
		reason = plugin.ReasonManual
	}
	start := time.Now()
	r, err := p.Process(ctx, pos[1], reason, nil)
	if err != nil {
		return err
	}
	defer r.Done()
	for _, line := range r.Report {
		fmt.Println("·", line)
	}
	if r.File == "" {
		fmt.Printf("left the file as it was (%s)\n", time.Since(start).Round(time.Millisecond))
		return nil
	}
	dst := filepath.Join(*out, filepath.Base(r.File))
	if same(dst, pos[1]) {
		dst = filepath.Join(*out, strings.TrimSuffix(filepath.Base(r.File), filepath.Ext(r.File))+" (processed)"+filepath.Ext(r.File))
	}
	data, err := os.ReadFile(r.File)
	if err != nil {
		return err
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return err
	}
	fmt.Printf("saved %s (%s, %.1f MB, %s)\n", dst, r.Format, float64(len(data))/(1<<20), time.Since(start).Round(time.Millisecond))
	return nil
}

// bookFlags are --book ids.
type bookFlags []int64

func (b *bookFlags) String() string { return fmt.Sprint(*b) }
func (b *bookFlags) Set(v string) error {
	id, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return fmt.Errorf("a book id is a number: %q", v)
	}
	*b = append(*b, id)
	return nil
}

// cmdRun runs an action as the app does, without its window: the forms
// it asks are answered with --answer, the books it runs on are the files
// given, and the library it reads is the one at --library.
func cmdRun(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	out := fs.String("o", ".", "the folder to save the files it makes in")
	libDir := fs.String("library", "", "a calibre library, for an action that reads the library")
	asJSON := fs.Bool("json", false, "print what it returned as JSON")
	var bookIDs bookFlags
	fs.Var(&bookIDs, "book", "with --library, a book to run on, by its id (repeatable)")
	set, answer := setFlags{}, setFlags{}
	fs.Var(set, "set", "a setting, as key=value (repeatable)")
	fs.Var(toolFlag, "tool", "run tool id from a file, as id=path (repeatable)")
	fs.Var(answer, "answer", "an answer to a form's field, as key=value: true, a text, or a JSON list (repeatable)")
	var pos []string
	for rest := args; ; {
		if err := fs.Parse(rest); err != nil {
			os.Exit(2)
		}
		if fs.NArg() == 0 {
			break
		}
		pos = append(pos, fs.Arg(0))
		rest = fs.Args()[1:]
	}
	if len(pos) < 2 {
		fmt.Fprintln(os.Stderr, "usage: dalibre-plugin run <dir> <action> [file…] [--library DIR] [--answer key=value] [-o DIR] [--json]")
		os.Exit(2)
	}
	p, err := load(pos[0], set)
	if err != nil {
		return err
	}
	defer p.Close()
	host := &plugin.ActionHost{Answers: map[string]any{}, Progress: func(done, total int) {
		if total > 0 {
			fmt.Fprintf(os.Stderr, "· %d of %d\n", done, total)
		}
	}}
	for k, v := range answer {
		// true, false and lists are given as JSON; anything else is text.
		var val any
		if json.Unmarshal([]byte(v), &val) != nil {
			val = v
		} else if _, num := val.(float64); num || val == nil {
			val = v
		}
		host.Answers[k] = val
	}
	// Each file is a book: its name the title, its extension the format.
	var books []plugin.BookInfo
	files := map[int64]string{}
	for i, f := range pos[2:] {
		if _, err := os.Stat(f); err != nil {
			return err
		}
		id := int64(i + 1)
		ext := filepath.Ext(f)
		books = append(books, plugin.BookInfo{ID: id, Title: strings.TrimSuffix(filepath.Base(f), ext),
			Formats: []string{strings.ToUpper(strings.TrimPrefix(ext, "."))}})
		files[id] = f
	}
	if *libDir != "" {
		lib, err := calibre.Open(*libDir)
		if err != nil {
			return err
		}
		defer lib.Close()
		all, err := lib.Books()
		if err != nil {
			return err
		}
		slices.SortFunc(all, func(a, b *calibre.Book) int { return cmp.Compare(a.ID, b.ID) })
		info := func(b *calibre.Book, comments bool) plugin.BookInfo {
			info := plugin.BookInfo{ID: b.ID, Title: b.Title, Authors: b.Authors, Series: b.Series, SeriesIndex: b.SeriesIndex,
				Tags: b.Tags, Publisher: b.Publisher, Languages: b.Languages, Rating: b.Rating, Formats: b.Formats, Size: b.Size,
				UUID: b.UUID, TitleSort: b.Sort, AuthorSort: b.AuthorSort, HasCover: b.HasCover}
			if !b.Pubdate.IsZero() {
				info.Pubdate = b.Pubdate.Format("2006-01-02")
			}
			if !b.Timestamp.IsZero() {
				info.Added = b.Timestamp.Format(time.RFC3339)
			}
			if !b.LastModified.IsZero() {
				info.Modified = b.LastModified.Format(time.RFC3339)
			}
			if d, err := lib.Details(b); err == nil {
				info.Identifiers = d.Identifiers
				if comments {
					info.Comments = d.Comments
				}
			}
			return info
		}
		host.Books = func(after int64, limit int, comments bool) ([]plugin.BookInfo, error) {
			var page []plugin.BookInfo
			for _, b := range all {
				if b.ID <= after {
					continue
				}
				if len(page) == limit {
					break
				}
				page = append(page, info(b, comments))
			}
			return page, nil
		}
		// --book: books of the library to run on, with their first file
		// in Dalibre's order.
		for _, id := range bookIDs {
			i := slices.IndexFunc(all, func(b *calibre.Book) bool { return b.ID == id })
			if i < 0 {
				return fmt.Errorf("the library has no book %d", id)
			}
			b := all[i]
			books = append(books, info(b, true))
			if b.HasCover {
				if host.Covers == nil {
					host.Covers = map[int64]string{}
				}
				host.Covers[b.ID] = lib.CoverPath(b)
			}
			if d, err := lib.Details(b); err == nil {
				for _, want := range []string{"EPUB", "KEPUB", "AZW3", "MOBI", "AZW", "PDF", "FB2", "DOCX", "TXT"} {
					if j := slices.IndexFunc(d.Files, func(f calibre.FormatFile) bool { return f.Format == want }); j >= 0 {
						files[b.ID] = filepath.Join(lib.Dir(b), d.Files[j].Name+"."+strings.ToLower(want))
						break
					}
				}
			}
		}
	} else if len(bookIDs) > 0 {
		return errors.New("--book needs --library")
	}
	start := time.Now()
	res, err := p.RunAction(ctx, pos[1], books, files, host)
	if err != nil {
		return err
	}
	defer res.Done()
	save := func(src string) (string, error) {
		data, err := os.ReadFile(src)
		if err != nil {
			return "", err
		}
		dst := filepath.Join(*out, filepath.Base(src))
		return dst, os.WriteFile(dst, data, 0o644)
	}
	type saved struct {
		Book     int64           `json:"book,omitempty"`
		Path     string          `json:"path"`
		Metadata *plugin.Changes `json:"metadata,omitempty"`
	}
	var added, formats []saved
	for _, nb := range res.Add {
		dst, err := save(nb.File)
		if err != nil {
			return err
		}
		added = append(added, saved{Path: dst, Metadata: nb.Metadata})
	}
	for _, f := range res.Formats {
		dst, err := save(f.File)
		if err != nil {
			return err
		}
		formats = append(formats, saved{Book: f.Book, Path: dst})
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{"message": res.Message, "groups": res.Groups, "changes": res.Changes,
			"add": added, "formats": formats, "open": res.Open})
	}
	if res.Message != "" {
		fmt.Println(res.Message)
	}
	for _, g := range res.Groups {
		fmt.Printf("%s (%d)\n", g.Title, len(g.Books))
		for _, b := range g.Books {
			fmt.Printf("  book %d  %s\n", b.ID, b.Note)
		}
	}
	for _, c := range res.Changes {
		data, _ := json.Marshal(c.Metadata)
		fmt.Printf("change book %d: %s\n", c.Book, data)
	}
	for _, s := range added {
		fmt.Printf("new book: %s\n", s.Path)
	}
	for _, s := range formats {
		fmt.Printf("new format of book %d: %s\n", s.Book, s.Path)
	}
	if res.Open != "" {
		fmt.Printf("open: %s\n", res.Open)
	}
	if res.Empty() {
		fmt.Println("returned nothing")
	}
	fmt.Fprintf(os.Stderr, "(%s)\n", time.Since(start).Round(time.Millisecond))
	return nil
}

// same reports whether a and b are the same file's path.
func same(a, b string) bool {
	x, err1 := filepath.Abs(a)
	y, err2 := filepath.Abs(b)
	return err1 == nil && err2 == nil && x == y
}

func cmdDownload(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("download", flag.ExitOnError)
	item := fs.Int("item", 1, "which result to download, from 1")
	format := fs.String("format", "", "the format (default: the result's first)")
	out := fs.String("o", ".", "the folder to save in")
	set := setFlags{}
	fs.Var(set, "set", "a setting, as key=value (repeatable)")
	fs.Var(toolFlag, "tool", "run tool id from a file, as id=path (repeatable)")
	pos := parse(fs, args, "dir", "text")
	p, err := load(pos[0], set)
	if err != nil {
		return err
	}
	res, err := p.Search(ctx, plugin.Query{Text: pos[1]})
	if err != nil {
		return err
	}
	for _, w := range res.Warnings {
		fmt.Fprintln(os.Stderr, "warning: search returned", w)
	}
	if *item < 1 || *item > len(res.Items) {
		return fmt.Errorf("--item %d: search returned %d results", *item, len(res.Items))
	}
	r := res.Items[*item-1]
	if *format == "" {
		*format = r.Formats[0].Format
	}
	req, err := p.Resolve(ctx, r, *format)
	if err != nil {
		return err
	}
	fmt.Printf("%s (%s) from %s\n", r.Title, strings.ToUpper(*format), req.URL)
	path, err := p.Download(ctx, r, *format, *out, func(done, total int64) {
		if total > 0 {
			fmt.Fprintf(os.Stderr, "\r%.1f / %.1f MB", float64(done)/(1<<20), float64(total)/(1<<20))
		} else {
			fmt.Fprintf(os.Stderr, "\r%.1f MB", float64(done)/(1<<20))
		}
	})
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return err
	}
	if _, err := p.Cover(ctx, r); err != nil {
		fmt.Fprintln(os.Stderr, "warning: cover:", err)
	}
	fmt.Println("saved", path)
	return nil
}

func cmdPack(_ context.Context, args []string) error {
	fs := flag.NewFlagSet("pack", flag.ExitOnError)
	out := fs.String("o", ".", "the folder to write the package in")
	dir := parse(fs, args, "dir")[0]
	path, err := plugin.Pack(dir, *out)
	if err != nil {
		return err
	}
	fmt.Println("packed", path)
	return nil
}

func cmdInstall(_ context.Context, args []string) error {
	fs := flag.NewFlagSet("install", flag.ExitOnError)
	yes := fs.Bool("yes", false, "grant the permissions without asking")
	archive := parse(fs, args, "package")[0]
	m, err := plugin.Inspect(archive)
	if err != nil {
		return err
	}
	s := settings.Load(settings.DefaultPath())
	st := s.Plugin(m.ID)
	have := plugin.Permissions{}
	if st.Granted != nil {
		have = plugin.Permissions{Hosts: st.Granted.Hosts, LocalNetwork: st.Granted.LocalNetwork, Library: st.Granted.Library}
	}
	if !have.Covers(m.Permissions) {
		fmt.Printf("%s %s", m.Name, m.Version)
		if m.Author != "" {
			fmt.Printf(" by %s", m.Author)
		}
		fmt.Printf(" (%s) asks to:\n", m.ID)
		for _, d := range m.Asks() {
			fmt.Println("  " + d)
		}
		if !*yes {
			fmt.Print("Allow? [y/N] ")
			line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
			if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
				return errors.New("not installed")
			}
		}
		st.Granted = &settings.Grant{Hosts: m.Permissions.Hosts, LocalNetwork: m.Permissions.LocalNetwork, Library: m.Permissions.Library}
	}
	if _, err := plugin.Install(archive, plugin.DefaultDir()); err != nil {
		return err
	}
	if err := s.Save(); err != nil {
		return err
	}
	fmt.Printf("installed %s %s in %s; restart Dalibre to use it\n", m.Name, m.Version, plugin.DefaultDir())
	return nil
}

func cmdUninstall(_ context.Context, args []string) error {
	fs := flag.NewFlagSet("uninstall", flag.ExitOnError)
	id := parse(fs, args, "id")[0]
	if err := plugin.Uninstall(plugin.DefaultDir(), id); err != nil {
		return err
	}
	s := settings.Load(settings.DefaultPath())
	delete(s.Plugins, id)
	return s.Save()
}

func cmdList(_ context.Context, args []string) error {
	parse(flag.NewFlagSet("list", flag.ExitOnError), args)
	root := plugin.DefaultDir()
	s := settings.Load(settings.DefaultPath())
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		fmt.Println("no plugins in", root)
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		m, err := plugin.ReadManifest(filepath.Join(root, e.Name()))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			fmt.Printf("%-24s error: %v\n", e.Name(), err)
			continue
		}
		state := "on"
		st := s.Plugins[m.ID]
		switch {
		case st != nil && st.Disabled:
			state = "off"
		case st == nil || st.Granted == nil ||
			!(plugin.Permissions{Hosts: st.Granted.Hosts, LocalNetwork: st.Granted.LocalNetwork, Library: st.Granted.Library}).Covers(m.Permissions):
			state = "waiting for permission"
		}
		fmt.Printf("%-24s %-10s %s (%s)\n", m.ID, m.Version, m.Name, state)
	}
	return nil
}
