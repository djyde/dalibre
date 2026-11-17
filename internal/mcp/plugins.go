package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"dalibre/internal/plugin"
	plugsdk "dalibre/sdk"
)

// The plugin tools let the user's agent write a plugin and try it in
// Dalibre: start one from the template, add its folder, call its
// functions, and reload it after an edit. What a plugin may reach is
// still the user's to grant, in Dalibre's own dialog.

var pluginTools = []string{"list_plugins", "create_plugin", "add_plugin", "reload_plugins", "test_plugin", "enable_plugin", "remove_plugin", "list_actions", "run_action"}

// PluginDocs is where writing plugins is explained.
const PluginDocs = "https://dalibre.randynamic.org/docs/plugins/"

// How a plugin is, as PluginInfo.State says.
const (
	PluginLoaded        = "loaded"
	PluginDisabled      = "disabled"
	PluginNeedsApproval = "needs_approval" // the user hasn't allowed what it asks for
	PluginFailed        = "failed"         // it didn't load; the list's errors say why
)

// PluginInfo is a plugin Dalibre knows.
type PluginInfo struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	Description  string   `json:"description,omitempty"`
	Capabilities []string `json:"capabilities" jsonschema:"bookSource, metadata, fileType, actions"`
	State        string   `json:"state" jsonschema:"loaded, disabled, needs_approval (the user must allow it in Dalibre) or failed (see errors)"`
	Builtin      bool     `json:"builtin,omitempty" jsonschema:"ships inside Dalibre"`
	Folder       string   `json:"folder,omitempty" jsonschema:"the folder it loads from, when added from one"`
	Exports      []string `json:"exports,omitempty" jsonschema:"the functions its script exports, when loaded"`
	// MissingSettings are the required settings the user hasn't filled
	// in, by title; the plugin's calls fail until they are.
	MissingSettings []string `json:"missing_settings,omitempty" jsonschema:"required settings the user must fill in, in Dalibre's Plugins"`
	Asks            []string `json:"asks,omitempty" jsonschema:"what it asks the user to allow"`
}

// PluginList is the plugins Dalibre knows and why some didn't load.
type PluginList struct {
	Plugins []PluginInfo `json:"plugins"`
	Errors  []string     `json:"errors,omitempty" jsonschema:"why plugins didn't load"`
}

// Plugins is what a Host that loads plugins gives too. Its methods wait
// for the plugins to be loaded again before they return.
type Plugins interface {
	// Plugins are the plugins Dalibre knows.
	Plugins(ctx context.Context) PluginList
	// AddPlugin adds the plugin in folder, to load from there as it is.
	// The user is asked first when it asks for more than they granted;
	// while they haven't answered, the plugin comes back needing approval.
	AddPlugin(ctx context.Context, folder string) (PluginInfo, error)
	// ReloadPlugins loads the plugins again.
	ReloadPlugins(ctx context.Context)
	SetPluginEnabled(ctx context.Context, id string, on bool) error
	// RemovePlugin removes a plugin added from a folder; the folder stays.
	RemovePlugin(ctx context.Context, id string) error
	// Plugin is the loaded plugin id; nil when it isn't loaded.
	Plugin(ctx context.Context, id string) *plugin.Plugin
}

func (s *Server) plugins() (Plugins, error) {
	if !s.allowsChanges() {
		return nil, errReadOnly
	}
	pl, ok := s.host.(Plugins)
	if !ok {
		return nil, errors.New("plugins need Dalibre running")
	}
	return pl, nil
}

func (s *Server) addPluginTools() {
	sdk.AddTool(s.srv, readTool("list_plugins", "Lists Dalibre's plugins: which are loaded, which wait for the user, and why any failed to load."), s.listPlugins)
	sdk.AddTool(s.srv, writeTool("create_plugin", "Starts a new plugin in an empty folder from Dalibre's template: plugin.json, main.js, and dalibre.d.ts, which describes the API. "+
		"Read "+PluginDocs+" before writing one. Then edit the files, add the folder with add_plugin and try it with test_plugin.", false, false), s.createPlugin)
	sdk.AddTool(s.srv, writeTool("add_plugin", "Adds the plugin in a folder to Dalibre, which loads it from there and reloads it when its files change. "+
		"Dalibre asks the user to allow what plugin.json's permissions ask for; this waits for their answer.", false, true), s.addPlugin)
	sdk.AddTool(s.srv, writeTool("reload_plugins", "Loads the plugins again, as after editing one, and lists them. An edit that asks for new permissions waits for the user.", false, true), s.reloadPlugins)
	sdk.AddTool(s.srv, &sdk.Tool{Name: "test_plugin", Description: "Calls a function of a loaded plugin as Dalibre does, with the user's settings, and returns what it returned, what it logged, and its error with the script's stack. " +
		"Nothing is added to the library.", Annotations: &sdk.ToolAnnotations{DestructiveHint: boolp(false), OpenWorldHint: boolp(true)}}, s.testPlugin)
	sdk.AddTool(s.srv, writeTool("enable_plugin", "Turns a plugin on or off.", false, true), s.setPluginEnabled)
	sdk.AddTool(s.srv, writeTool("remove_plugin", "Removes a plugin that was added from a folder; the folder stays as it is.", true, true), s.removePlugin)
}

type noIn struct{}

func (s *Server) listPlugins(ctx context.Context, _ *sdk.CallToolRequest, _ noIn) (*sdk.CallToolResult, PluginList, error) {
	pl, err := s.plugins()
	if err != nil {
		return nil, PluginList{}, err
	}
	return nil, listed(pl.Plugins(ctx)), nil
}

func listed(l PluginList) PluginList {
	if l.Plugins == nil {
		l.Plugins = []PluginInfo{}
	}
	return l
}

type createIn struct {
	Path string `json:"path" jsonschema:"the new plugin's folder, an absolute path; empty or not there yet"`
	ID   string `json:"id" jsonschema:"permanent and unique: lowercase letters, digits and - in dot-separated parts, as io.github.ann.books"`
	Name string `json:"name,omitempty" jsonschema:"shown to users; the id when not given"`
}

type createOut struct {
	Folder string   `json:"folder"`
	Files  []string `json:"files"`
	Docs   string   `json:"docs" jsonschema:"how to write a plugin"`
	Next   string   `json:"next"`
}

func (s *Server) createPlugin(_ context.Context, _ *sdk.CallToolRequest, in createIn) (*sdk.CallToolResult, createOut, error) {
	if _, err := s.plugins(); err != nil {
		return nil, createOut{}, err
	}
	dir, err := absFolder(in.Path)
	if err != nil {
		return nil, createOut{}, err
	}
	id := strings.TrimSpace(in.ID)
	if id == "" {
		return nil, createOut{}, errors.New("no id")
	}
	if plugin.Reserved(id) {
		return nil, createOut{}, fmt.Errorf("id %q is kept for plugins that ship with Dalibre", id)
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = id
	}
	if info, err := os.Stat(dir); err == nil && !info.IsDir() {
		return nil, createOut{}, fmt.Errorf("%s is a file", dir)
	}
	// The template is filled in elsewhere first, so a bad id leaves
	// nothing behind.
	tmp, err := os.MkdirTemp("", "dalibre-plugin-")
	if err != nil {
		return nil, createOut{}, err
	}
	defer os.RemoveAll(tmp)
	if _, err := plugsdk.Init(tmp, id, name); err != nil {
		return nil, createOut{}, err
	}
	if _, err := plugin.ReadManifest(tmp); err != nil {
		return nil, createOut{}, err
	}
	files, err := plugsdk.Init(dir, id, name)
	if err != nil {
		return nil, createOut{}, err
	}
	return nil, createOut{Folder: dir, Files: files, Docs: PluginDocs,
		Next: "The template is a book source that searches example.com: rewrite main.js and plugin.json (capabilities, permissions.hosts, settings) for what the plugin is to do, " +
			"then add_plugin this folder and try it with test_plugin."}, nil
}

// absFolder is path, which must be absolute, cleaned.
func absFolder(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("no path")
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("%q isn’t an absolute path", path)
	}
	return filepath.Clean(path), nil
}

type addPluginIn struct {
	Path string `json:"path" jsonschema:"the plugin's folder, an absolute path; it holds plugin.json"`
}

type addPluginOut struct {
	Plugin PluginInfo `json:"plugin"`
	Errors []string   `json:"errors,omitempty" jsonschema:"why it didn't load"`
	Note   string     `json:"note,omitempty"`
}

func (s *Server) addPlugin(ctx context.Context, _ *sdk.CallToolRequest, in addPluginIn) (*sdk.CallToolResult, addPluginOut, error) {
	pl, err := s.plugins()
	if err != nil {
		return nil, addPluginOut{}, err
	}
	dir, err := absFolder(in.Path)
	if err != nil {
		return nil, addPluginOut{}, err
	}
	info, err := pl.AddPlugin(ctx, dir)
	if err != nil {
		return nil, addPluginOut{}, err
	}
	out := addPluginOut{Plugin: info}
	switch info.State {
	case PluginNeedsApproval:
		out.Note = "Dalibre is asking the user to allow it. Once they have, list_plugins shows it loaded."
	case PluginFailed:
		out.Errors = pl.Plugins(ctx).Errors
		out.Note = "Fix it and call reload_plugins."
	case PluginLoaded:
		if len(info.MissingSettings) > 0 {
			out.Note = "The user must fill in its settings in Dalibre’s Plugins before it can be called."
		}
	}
	return nil, out, nil
}

func (s *Server) reloadPlugins(ctx context.Context, _ *sdk.CallToolRequest, _ noIn) (*sdk.CallToolResult, PluginList, error) {
	pl, err := s.plugins()
	if err != nil {
		return nil, PluginList{}, err
	}
	pl.ReloadPlugins(ctx)
	return nil, listed(pl.Plugins(ctx)), nil
}

type enableIn struct {
	ID      string `json:"id" jsonschema:"the plugin's id"`
	Enabled *bool  `json:"enabled,omitempty" jsonschema:"false turns it off; true when not given"`
}

type pluginOut struct {
	Plugins []PluginInfo `json:"plugins" jsonschema:"the plugins now"`
}

func (s *Server) setPluginEnabled(ctx context.Context, _ *sdk.CallToolRequest, in enableIn) (*sdk.CallToolResult, pluginOut, error) {
	pl, err := s.plugins()
	if err != nil {
		return nil, pluginOut{}, err
	}
	if err := pl.SetPluginEnabled(ctx, in.ID, in.Enabled == nil || *in.Enabled); err != nil {
		return nil, pluginOut{}, err
	}
	return nil, pluginOut{listed(pl.Plugins(ctx)).Plugins}, nil
}

type removePluginIn struct {
	ID string `json:"id" jsonschema:"the plugin's id"`
}

func (s *Server) removePlugin(ctx context.Context, _ *sdk.CallToolRequest, in removePluginIn) (*sdk.CallToolResult, pluginOut, error) {
	pl, err := s.plugins()
	if err != nil {
		return nil, pluginOut{}, err
	}
	if err := pl.RemovePlugin(ctx, in.ID); err != nil {
		return nil, pluginOut{}, err
	}
	return nil, pluginOut{listed(pl.Plugins(ctx)).Plugins}, nil
}

// ---- test_plugin

// TestBook is the book identify is asked to look up.
type TestBook struct {
	Title       string            `json:"title"`
	Authors     []string          `json:"authors,omitempty"`
	Identifiers map[string]string `json:"identifiers,omitempty" jsonschema:"as isbn: 9780441172719"`
	Language    string            `json:"language,omitempty" jsonschema:"an ISO 639 code"`
}

type testIn struct {
	ID         string            `json:"id" jsonschema:"the plugin's id"`
	Call       string            `json:"call" jsonschema:"search, collections, list, get, resolve, identify or process"`
	Text       string            `json:"text,omitempty" jsonschema:"search: what to search for"`
	Collection string            `json:"collection,omitempty" jsonschema:"list: a collection's id, from collections"`
	Page       int               `json:"page,omitempty" jsonschema:"search and list: the page, from 1"`
	Cursor     string            `json:"cursor,omitempty" jsonschema:"search and list: the cursor the page before returned"`
	Filters    map[string]string `json:"filters,omitempty" jsonschema:"search and list: values of the plugin's filters, by key; the others take their defaults"`
	Item       map[string]any    `json:"item,omitempty" jsonschema:"get and resolve: a result, as search or list returned it"`
	Format     string            `json:"format,omitempty" jsonschema:"resolve: the format to download, as EPUB"`
	Book       *TestBook         `json:"book,omitempty" jsonschema:"identify: the book to look up"`
	File       string            `json:"file,omitempty" jsonschema:"process: an e-book file's path; the plugin works on a copy"`
	Manual     bool              `json:"manual,omitempty" jsonschema:"process: as run from the book menu, rather than on adding the file"`
}

// TestError is why a plugin's call failed.
type TestError struct {
	Kind    string `json:"kind" jsonschema:"auth, notFound, rateLimited, unavailable, invalid, settings, timeout, permission, cancelled or script"`
	Message string `json:"message"`
	Stack   string `json:"stack,omitempty" jsonschema:"where the script threw"`
}

type testOut struct {
	OK     bool       `json:"ok"`
	Result any        `json:"result,omitempty" jsonschema:"what the call returned, as Dalibre read it"`
	Error  *TestError `json:"error,omitempty"`
	Logs   []string   `json:"logs,omitempty" jsonschema:"what the script logged, and Dalibre's warnings about what it returned"`
}

// processedFile is what process made of a file.
type processedFile struct {
	File   string   `json:"file,omitempty" jsonschema:"the new file, kept until the next process call; none when the plugin left the file as it was"`
	Format string   `json:"format,omitempty"`
	Report []string `json:"report,omitempty"`
}

// maxTestLogs bounds the log lines one test returns.
const maxTestLogs = 200

func (s *Server) testPlugin(ctx context.Context, _ *sdk.CallToolRequest, in testIn) (*sdk.CallToolResult, testOut, error) {
	pl, err := s.plugins()
	if err != nil {
		return nil, testOut{}, err
	}
	p := pl.Plugin(ctx, in.ID)
	if p == nil {
		for _, info := range pl.Plugins(ctx).Plugins {
			if info.ID == in.ID {
				return nil, testOut{}, fmt.Errorf("%s isn’t loaded: it is %s (see list_plugins)", in.ID, info.State)
			}
		}
		return nil, testOut{}, fmt.Errorf("no plugin %q; add its folder with add_plugin", in.ID)
	}
	var mu sync.Mutex
	var logs []string
	stop := p.Tap(func(line string) {
		mu.Lock()
		if len(logs) < maxTestLogs {
			logs = append(logs, strings.TrimPrefix(line, "["+p.Manifest.ID+"] "))
		}
		mu.Unlock()
	})
	res, err := s.callPlugin(ctx, p, in)
	stop()
	mu.Lock()
	out := testOut{Logs: logs}
	mu.Unlock()
	if err != nil {
		var pe *plugin.Error
		if !errors.As(err, &pe) {
			return nil, testOut{}, err // the arguments, not the plugin
		}
		out.Error = &TestError{Kind: string(pe.Kind), Message: pe.Error(), Stack: pe.Stack}
		return nil, out, nil
	}
	out.OK, out.Result = true, res
	return nil, out, nil
}

// callPlugin makes the call in asks for. An error that isn't a
// *plugin.Error is about in.
func (s *Server) callPlugin(ctx context.Context, p *plugin.Plugin, in testIn) (any, error) {
	m := p.Manifest
	query := func() plugin.Query {
		return plugin.Query{Text: in.Text, Page: max(in.Page, 1), Cursor: in.Cursor, Filters: p.FilterValues(in.Filters), Collection: in.Collection}
	}
	for k := range in.Filters {
		if _, ok := p.FilterValues(nil)[k]; !ok {
			return nil, fmt.Errorf("plugin.json declares no filter %q", k)
		}
	}
	item := func() (plugin.Result, error) {
		var r plugin.Result
		if in.Item == nil {
			return r, fmt.Errorf("%s needs item: a result, as search or list returned it", in.Call)
		}
		data, _ := json.Marshal(in.Item)
		if err := json.Unmarshal(data, &r); err != nil {
			return r, fmt.Errorf("item: %w", err)
		}
		return r, nil
	}
	switch in.Call {
	case "search":
		if !m.IsBookSource() {
			return nil, errors.New("not a book source")
		}
		return p.Search(ctx, query())
	case "collections":
		if !m.HasCollections() {
			return nil, errors.New("plugin.json doesn’t declare collections")
		}
		cs, err := p.Collections(ctx)
		if cs == nil {
			cs = []plugin.Collection{}
		}
		return cs, err
	case "list":
		if !m.HasCollections() {
			return nil, errors.New("plugin.json doesn’t declare collections")
		}
		if in.Collection == "" {
			return nil, errors.New("list needs collection")
		}
		return p.List(ctx, query())
	case "get":
		r, err := item()
		if err != nil {
			return nil, err
		}
		return p.Get(ctx, r)
	case "resolve":
		r, err := item()
		if err != nil {
			return nil, err
		}
		format := strings.ToUpper(strings.TrimSpace(in.Format))
		if format == "" && len(r.Formats) > 0 {
			format = r.Formats[0].Format
		}
		return p.Resolve(ctx, r, format)
	case "identify":
		if !m.IsMetadataSource() {
			return nil, errors.New("not a metadata source")
		}
		if in.Book == nil {
			return nil, errors.New("identify needs book")
		}
		b := plugin.Book{Title: in.Book.Title, Authors: nonNil(in.Book.Authors), Identifiers: in.Book.Identifiers, Language: in.Book.Language}
		if b.Identifiers == nil {
			b.Identifiers = map[string]string{}
		}
		rs, err := p.Identify(ctx, b)
		if rs == nil {
			rs = []plugin.Result{}
		}
		return rs, err
	case "process":
		if !m.IsFileType() {
			return nil, errors.New("not a file type plugin")
		}
		info, err := os.Stat(in.File)
		if err != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("process needs file, an e-book file's path: %q isn’t one", in.File)
		}
		reason := plugin.ReasonImport
		if in.Manual {
			reason = plugin.ReasonManual
		}
		res, err := p.Process(ctx, in.File, reason, nil)
		if err != nil {
			return nil, err
		}
		s.mu.Lock()
		old := s.processed
		s.processed = res
		s.mu.Unlock()
		old.Done()
		return processedFile{File: res.File, Format: res.Format, Report: res.Report}, nil
	}
	return nil, fmt.Errorf("no call %q: search, collections, list, get, resolve, identify or process", in.Call)
}
