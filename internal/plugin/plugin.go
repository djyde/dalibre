package plugin

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"maps"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MoeForge/moejs"
	"github.com/MoeForge/moejs/eventloop"
	"github.com/MoeForge/moejs/node"
	"github.com/MoeForge/moejs/webapi"
)

// Limits on one call into a plugin and on its runtime.
const (
	// CallTimeout bounds one call when the caller's context has no
	// earlier deadline.
	CallTimeout = 60 * time.Second
	// ProcessTimeout bounds one process call, on a book's file.
	ProcessTimeout = 10 * time.Minute
)

//go:embed prelude.js
var prelude string

// preludeName names the prelude in stack traces, which leave its frames
// out.
const preludeName = "<dalibre>"

// preludeScript is the prelude compiled, once for every plugin.
var preludeScript = sync.OnceValues(func() (*moejs.Script, error) {
	return moejs.CompileScript(preludeName, prelude)
})

// Plugin is a loaded plugin. Its methods may be called from any
// goroutine; calls into its script run one at a time.
type Plugin struct {
	Manifest *Manifest
	// Dir is the plugin's folder; "" for one built into Dalibre.
	Dir string
	// Builtin is set for a plugin that ships inside Dalibre.
	Builtin bool

	fsys fs.FS // the plugin's files
	// native is the Go source behind a plugin made by NewNative.
	native Native
	// authorize adds credentials to the plugin's requests.
	authorize func(*http.Request)

	settings map[string]string
	client   *http.Client
	logf     func(format string, args ...any)
	tapMu    sync.Mutex
	taps     map[int]func(line string) // Tap's listeners
	tapSeq   int

	dataDir  string            // the plugin's own folder, the root of its node:fs; "" for none
	tools    map[string]string // its tools' executables, by id
	requests atomic.Int32      // fetches made by the call in progress
	workMu   sync.Mutex        // one Process at a time: it owns /work

	mu       sync.Mutex // guards the fields below and the runtime
	rt       *moejs.Runtime
	loop     *eventloop.Loop          // runs rt: timers, fetches, files
	web      *webapi.API              // fetch and the other Web APIs
	nodeAPI  *node.API                // node:fs, node:buffer
	load     moejs.Value              // the prelude's load(main)
	invokeFn moejs.Value              // the prelude's call(name, args)
	lineFn   moejs.Value              // the prelude's line(id, stream, line), for dalibre.exec's onLine
	modules  map[string]*moejs.Module // compiled, by path in the plugin's folder
	exported []string                 // the functions the script exports
	ctx      context.Context          // of the call in progress
	settled  *string                  // the outcome of the call in progress, as JSON
	host     *ActionHost              // what the action in progress may ask the app
	limit    *deadline                // the time the call in progress has left
	srcSize  int64                    // bytes of script loaded into rt
	closed   bool
}

// Options configure a loaded plugin.
type Options struct {
	// Settings are the user's values for the manifest's settings; missing
	// ones take their defaults.
	Settings map[string]string
	// Logf receives the plugin's log lines; log.Printf when nil.
	Logf func(format string, args ...any)
	// Transport makes the plugin's requests, for tests. When nil requests
	// go through the sandbox's transport, which refuses local addresses
	// unless the manifest asks for the local network.
	Transport http.RoundTripper
	// Authorize, when set, is called on every request the plugin makes,
	// to add credentials: the user's account on an OPDS server.
	Authorize func(*http.Request)
	// DataDir is the plugin's own folder (DataDir(root, id)), which its
	// script sees through node:fs: data/ at /data, what it keeps between
	// runs, and work/ at /work, the files it is handed. It is made when
	// needed. When "", the script has no node:fs and can't process files.
	DataDir string
	// Tools are the executables of the manifest's tools, by id, for
	// dalibre.exec. A tool missing here can't be run.
	Tools map[string]string
}

// Load reads the plugin in dir and runs its script, which defines the
// functions the host calls. Close it when done.
func Load(dir string, opts Options) (*Plugin, error) {
	m, err := ReadManifest(dir)
	if err != nil {
		return nil, err
	}
	return load(m, os.DirFS(dir), dir, opts)
}

// LoadBuiltin loads a plugin that ships inside Dalibre, from fsys holding
// its plugin.json and scripts. It may use a reserved id.
func LoadBuiltin(fsys fs.FS, opts Options) (*Plugin, error) {
	data, err := fs.ReadFile(fsys, "plugin.json")
	if err != nil {
		return nil, err
	}
	m, err := ParseManifest(data)
	if err != nil {
		return nil, err
	}
	p, err := load(m, fsys, "", opts)
	if p != nil {
		p.Builtin = true
	}
	return p, err
}

func load(m *Manifest, fsys fs.FS, dir string, opts Options) (*Plugin, error) {
	p := &Plugin{Manifest: m, Dir: dir, fsys: fsys, ctx: context.Background(), logf: opts.Logf, settings: map[string]string{}}
	if p.logf == nil {
		p.logf = log.Printf
	}
	base := p.logf
	p.logf = func(format string, args ...any) {
		base(format, args...)
		p.tapped(fmt.Sprintf(format, args...))
	}
	for _, s := range m.Settings {
		v, ok := opts.Settings[s.Key]
		if !ok {
			v = s.Default
		}
		p.settings[s.Key] = v
	}
	p.client = p.httpClient(opts.Transport)
	p.authorize = opts.Authorize
	p.dataDir = opts.DataDir
	p.tools = opts.Tools

	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.start(); err != nil {
		return nil, err
	}
	if err := p.checkExports(); err != nil {
		p.stop()
		return nil, err
	}
	return p, nil
}

// checkExports checks the script exports what its capabilities need.
func (p *Plugin) checkExports() error {
	m := p.Manifest
	need := []string{}
	if m.IsBookSource() {
		need = append(need, "search")
		if m.HasCollections() {
			need = append(need, "collections", "list")
		}
	}
	for _, n := range need {
		if !slices.Contains(p.exported, n) {
			msg := "a book source must export " + n
			if n != "search" {
				msg = "a book source with collections must export " + n
			}
			return &Error{Plugin: m.ID, Kind: KindInvalid, Msg: msg}
		}
	}
	if m.IsMetadataSource() && !slices.Contains(p.exported, "identify") {
		return &Error{Plugin: m.ID, Kind: KindInvalid, Msg: "a metadata source must export identify"}
	}
	if m.RunsOnImport() && !slices.Contains(p.exported, "process") {
		return &Error{Plugin: m.ID, Kind: KindInvalid, Msg: "a file type plugin that runs on import must export process"}
	}
	if len(m.Capabilities.Actions) > 0 && !slices.Contains(p.exported, "run") {
		return &Error{Plugin: m.ID, Kind: KindInvalid, Msg: "a plugin with actions must export run"}
	}
	for _, h := range m.Hooks() {
		if !slices.Contains(p.exported, h) {
			return &Error{Plugin: m.ID, Kind: KindInvalid, Msg: fmt.Sprintf("a file type plugin with %s must export %s", hookFlag(h), h)}
		}
	}
	return nil
}

// hookFlag is the manifest field that asks for hook.
func hookFlag(hook string) string {
	return map[string]string{HookPostImport: "onPostImport", HookPostAdd: "onPostAdd", HookPostDelete: "onPostDelete"}[hook]
}

// Close drops the plugin's runtime. Calls after Close fail.
func (p *Plugin) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	p.stop()
}

// start makes the plugin's runtime and loads its script.
func (p *Plugin) start() error {
	script, err := preludeScript()
	if err != nil {
		return fmt.Errorf("plugin prelude: %w", err)
	}
	p.srcSize, p.modules = 0, map[string]*moejs.Module{}
	// Plugins get their own builtins, so a bundled polyfill that patches
	// a prototype works as it does in a browser. Memory isn't limited, as
	// calibre doesn't limit its plugins': a plugin works on whole books.
	rt := moejs.NewRuntime(moejs.Options{
		Importer:          node.Importer(&moejs.Importer{Resolve: p.resolveModule}),
		MutableIntrinsics: true,
	})
	p.rt = rt
	if err := p.install(); err != nil {
		p.stop()
		return err
	}
	host := map[string]func([]any) (any, error){
		"__host_info":     p.hostInfo,
		"__host_log":      p.hostLog,
		"__host_settle":   p.hostSettle,
		"__host_flag":     p.hostFlag,
		"__host_progress": p.hostProgress,
		"__host_html":     hostHTML,
	}
	for name, fn := range host {
		if err := rt.SetGlobal(name, hostFunc(fn)); err != nil {
			p.stop()
			return err
		}
	}
	for name, fn := range map[string]moejs.NativeFunc{"__host_ask": p.hostAsk, "__host_exec": p.hostExec} {
		if err := rt.SetGlobal(name, fn); err != nil {
			p.stop()
			return err
		}
	}
	entry, err := rt.RunScript(script)
	if err == nil {
		p.load, err = rt.Get(entry, "load")
	}
	if err == nil {
		p.invokeFn, err = rt.Get(entry, "call")
	}
	if err == nil {
		p.lineFn, err = rt.Get(entry, "line")
	}
	if err != nil {
		p.stop()
		return fmt.Errorf("plugin prelude: %w", err)
	}
	var names []string
	if err := p.invoke(context.Background(), CallTimeout, "", &names, p.load, moejs.String(p.Manifest.Main)); err != nil {
		p.stop()
		return err
	}
	p.exported = names
	return nil
}

// hostFunc makes a host function of the prelude's a JavaScript function:
// its arguments arrive as strings, and a string it returns goes back as
// one.
func hostFunc(fn func([]any) (any, error)) moejs.NativeFunc {
	return func(r *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
		in := make([]any, len(args))
		for i, a := range args {
			s, err := r.ToString(a)
			if err != nil {
				return moejs.Undefined(), err
			}
			in[i] = s.GoString()
		}
		out, err := fn(in)
		if err != nil {
			return moejs.Undefined(), err
		}
		if s, ok := out.(string); ok {
			return moejs.String(s), nil
		}
		return moejs.Undefined(), nil
	}
}

// install gives the runtime its event loop (timers), the Web APIs (fetch
// through the plugin's sandboxed client) and Node's buffer, with node:fs
// over its data folder when it has one.
func (p *Plugin) install() error {
	loop, err := eventloop.New(p.rt, eventloop.Options{})
	if err != nil {
		return err
	}
	p.loop = loop
	if p.web, err = webapi.Install(loop, webapi.Options{Client: p.fetchClient(), Allow: p.allowFetch}); err != nil {
		return err
	}
	opts := node.Options{Buffer: true}
	if p.dataDir != "" {
		for _, d := range []string{dataFolder, workFolder} {
			if err := os.MkdirAll(filepath.Join(p.dataDir, d), 0o700); err != nil {
				return err
			}
		}
		opts.FS = &node.FSOptions{Root: p.dataDir, Mount: "/", Cwd: "/" + dataFolder, Write: true, Allow: p.allowFile}
	}
	p.nodeAPI, err = node.InstallLoop(loop, opts)
	return err
}

// stop drops the runtime, ending what it has in flight; the next call
// starts a new one, so the script's own state starts over.
func (p *Plugin) stop() {
	if p.web != nil {
		p.web.Close()
	}
	if p.nodeAPI != nil {
		p.nodeAPI.Close()
	}
	if p.loop != nil {
		p.loop.Terminate()
	}
	p.loop, p.web, p.nodeAPI = nil, nil, nil
	p.rt, p.modules = nil, nil
	p.load, p.invokeFn, p.lineFn = moejs.Undefined(), moejs.Undefined(), moejs.Undefined()
}

// resolveModule compiles the module an import names in the code of
// referrer. Only the plugin's own .js files can be imported, by a path
// relative to the importing module.
func (p *Plugin) resolveModule(referrer moejs.Referrer, name string) (*moejs.Module, error) {
	if !strings.HasPrefix(name, "./") && !strings.HasPrefix(name, "../") {
		return nil, fmt.Errorf("can’t import %q: only the plugin’s own files can be imported, as \"./lib.js\"; bundle packages into main.js (with esbuild)", name)
	}
	dir := "."
	if referrer != nil && referrer.Name() != preludeName {
		dir = path.Dir(referrer.Name())
	}
	resolved := path.Clean(path.Join(dir, name))
	if resolved == ".." || strings.HasPrefix(resolved, "../") || path.IsAbs(resolved) {
		return nil, fmt.Errorf("can’t import %q: it is outside the plugin’s folder", name)
	}
	resolved, err := p.moduleFile(name, resolved)
	if err != nil {
		return nil, err
	}
	if m, ok := p.modules[resolved]; ok {
		return m, nil // a module is evaluated once, however many import it
	}
	src, err := p.loadModule(resolved)
	if err != nil {
		return nil, err
	}
	ts := path.Ext(resolved) == ".ts" || path.Ext(resolved) == ".mts"
	m, err := moejs.CompileWith(resolved, src, moejs.CompileOptions{Context: p.ctx, TypeScript: ts})
	if err != nil {
		return nil, err
	}
	p.modules[resolved] = m
	return m, nil
}

// moduleFile is the file an import of name resolved to path means: a .js,
// .mjs, .ts or .mts file, found as TypeScript finds one: "./lib.js" may be
// lib.ts, and "./lib" lib.ts or lib.js.
func (p *Plugin) moduleFile(name, resolved string) (string, error) {
	exists := func(f string) bool {
		st, err := fs.Stat(p.fsys, f)
		return err == nil && !st.IsDir()
	}
	var try []string
	switch ext := path.Ext(resolved); ext {
	case ".js", ".mjs":
		try = []string{resolved, strings.TrimSuffix(resolved, ext) + map[string]string{".js": ".ts", ".mjs": ".mts"}[ext]}
	case ".ts", ".mts":
		try = []string{resolved}
	case "":
		try = []string{resolved + ".ts", resolved + ".js"}
	default:
		return "", fmt.Errorf("can’t import %q: only .js and .ts files can be imported", name)
	}
	for _, f := range try {
		if exists(f) {
			return f, nil
		}
	}
	return try[0], nil // loadModule says it's missing
}

func (p *Plugin) loadModule(name string) (string, error) {
	st, err := fs.Stat(p.fsys, name)
	if err != nil || st.IsDir() {
		return "", fmt.Errorf("%s: no such file", name)
	}
	if p.srcSize += st.Size(); p.srcSize > MaxScriptSize {
		return "", fmt.Errorf("the plugin’s scripts are over %d MB", MaxScriptSize>>20)
	}
	data, err := fs.ReadFile(p.fsys, name)
	return string(data), err
}

// Discover loads every plugin in the folders inside root that load
// accepts (all when nil). A folder that fails to load gives an error and
// is skipped.
func Discover(root string, load func(m *Manifest) (Options, bool)) ([]*Plugin, []error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, []error{err}
	}
	var plugins []*Plugin
	var errs []error
	seen := map[string]string{}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		dir := filepath.Join(root, e.Name())
		m, err := ReadManifest(dir)
		if errors.Is(err, os.ErrNotExist) {
			continue // not a plugin
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", e.Name(), err))
			continue
		}
		if Reserved(m.ID) {
			errs = append(errs, fmt.Errorf("%s: id %q is kept for plugins that ship with Dalibre", e.Name(), m.ID))
			continue
		}
		if other, ok := seen[m.ID]; ok {
			errs = append(errs, fmt.Errorf("%s: id %q is already used by %s", e.Name(), m.ID, other))
			continue
		}
		seen[m.ID] = e.Name()
		o, ok := Options{}, true
		if load != nil {
			o, ok = load(m)
		}
		if !ok {
			continue
		}
		p, err := Load(dir, o)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		plugins = append(plugins, p)
	}
	sort.Slice(plugins, func(i, j int) bool { return plugins[i].Manifest.Name < plugins[j].Manifest.Name })
	return plugins, errs
}

// DefaultDir is the plugins folder in the user's configuration folder,
// or $DALIBRE_PLUGINS.
func DefaultDir() string {
	if d := os.Getenv("DALIBRE_PLUGINS"); d != "" {
		return d
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "Dalibre", "plugins")
}

// Tap also gives the plugin's log lines (what its script logs, and the
// host's warnings about what it returned) to fn, until stop is called:
// for showing a plugin's author what a call logged.
func (p *Plugin) Tap(fn func(line string)) (stop func()) {
	p.tapMu.Lock()
	defer p.tapMu.Unlock()
	if p.taps == nil {
		p.taps = map[int]func(string){}
	}
	p.tapSeq++
	id := p.tapSeq
	p.taps[id] = fn
	return func() {
		p.tapMu.Lock()
		delete(p.taps, id)
		p.tapMu.Unlock()
	}
}

func (p *Plugin) tapped(line string) {
	p.tapMu.Lock()
	fns := slices.Collect(maps.Values(p.taps))
	p.tapMu.Unlock()
	for _, fn := range fns {
		fn(line)
	}
}

// Defines reports whether the plugin exports a function called name.
func (p *Plugin) Defines(name string) bool {
	if p.native != nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Contains(p.exported, name)
}

// MissingSettings are the required settings the user hasn't given.
func (p *Plugin) MissingSettings() []string {
	var out []string
	for _, s := range p.Manifest.Settings {
		if s.Required && strings.TrimSpace(p.settings[s.Key]) == "" {
			out = append(out, s.Title)
		}
	}
	return out
}

// call runs the exported function name with args and returns its result
// as JSON. Arguments and results cross as JSON so neither side sees the
// other's types. An async function's promise is awaited.
func (p *Plugin) call(ctx context.Context, name string, args ...any) (json.RawMessage, error) {
	return p.callWithin(ctx, CallTimeout, name, args...)
}

// callWithin is call with timeout in place of CallTimeout.
func (p *Plugin) callWithin(ctx context.Context, timeout time.Duration, name string, args ...any) (json.RawMessage, error) {
	return p.callHosted(ctx, timeout, nil, name, args...)
}

// callHosted is callWithin for a call that may ask host: an action's run.
func (p *Plugin) callHosted(ctx context.Context, timeout time.Duration, host *ActionHost, name string, args ...any) (json.RawMessage, error) {
	if missing := p.MissingSettings(); len(missing) > 0 {
		return nil, &Error{Plugin: p.Manifest.ID, Call: name, Kind: KindSettings,
			Msg: "needs " + strings.Join(missing, ", ") + " in its settings"}
	}
	if args == nil {
		args = []any{}
	}
	data, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, &Error{Plugin: p.Manifest.ID, Call: name, Kind: KindUnavailable, Msg: "the plugin was closed"}
	}
	if p.rt == nil {
		// The last call broke the runtime (out of memory, out of time);
		// start afresh.
		if err := p.start(); err != nil {
			return nil, err
		}
	}
	if !slices.Contains(p.exported, name) {
		return nil, &Error{Plugin: p.Manifest.ID, Call: name, Kind: KindInvalid, Msg: "isn’t exported"}
	}
	p.host = host
	defer func() { p.host = nil }()
	var out json.RawMessage
	if err := p.invoke(ctx, timeout, name, &out, p.invokeFn, moejs.String(name), moejs.String(string(data))); err != nil {
		return nil, err
	}
	return out, nil
}

// outcome is how the prelude reports a call.
type outcome struct {
	OK    bool            `json:"ok"`
	Value json.RawMessage `json:"value"`
	Error struct {
		Name, Message, Stack, Kind string
	} `json:"error"`
}

// invoke calls fn, a function of the prelude, under ctx's deadline or
// timeout, and decodes the value it settles with into out. A runtime that
// ran out of time, or failed inside, is stopped, to start afresh on the
// next call.
func (p *Plugin) invoke(ctx context.Context, timeout time.Duration, call string, out any, fn moejs.Value, args ...moejs.Value) (err error) {
	// The call's time is a timer rather than the context's deadline, so
	// that it stops while the user answers a form.
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	p.limit = newDeadline(timeout, func() { cancel(context.DeadlineExceeded) })
	defer p.limit.stop()
	rt, loop := p.rt, p.loop
	rt.ClearInterrupt()
	p.ctx, p.settled = ctx, nil
	p.requests.Store(0)
	p.web.SetContext(ctx) // the call's fetches end with it
	interrupted := make(chan struct{})
	stopTimer := context.AfterFunc(ctx, func() {
		loop.Interrupt(context.Cause(ctx)) // also wakes a loop waiting on a timer
		close(interrupted)
	})
	defer func() {
		if !stopTimer() {
			<-interrupted // done interrupting before the runtime is used again
		}
		p.ctx = context.Background()
		if r := recover(); r != nil {
			p.stop()
			err = &Error{Plugin: p.Manifest.ID, Call: call, Kind: KindScript, Msg: fmt.Sprint("the interpreter failed: ", r)}
		}
		if p.rt == rt {
			rt.ReleaseCallData() // the call's arguments can go
		}
	}()

	// The loop runs until the call settles (hostSettle stops it) or
	// nothing is left that could settle it: no timer, fetch or file
	// operation. Timers the script left keep for its next call.
	jsErr := loop.Run(func(rt *moejs.Runtime) error {
		_, err := rt.CallFunction(fn, moejs.Undefined(), args...)
		return err
	})
	fail := func(kind ErrorKind, msg string) error {
		p.stop()
		cause := context.Cause(ctx)
		if cause != nil && !errors.Is(cause, context.DeadlineExceeded) {
			cause = context.Canceled
		}
		return &Error{Plugin: p.Manifest.ID, Call: call, Kind: kind, Msg: msg, Err: cause}
	}
	var internal *moejs.InternalError
	switch {
	case ctx.Err() != nil:
		if errors.Is(context.Cause(ctx), context.DeadlineExceeded) {
			return fail(KindTimeout, "took too long")
		}
		return fail(KindTimeout, "canceled")
	case errors.As(jsErr, &internal):
		return fail(KindScript, fmt.Sprint("the interpreter failed: ", internal.Value))
	case p.settled == nil && jsErr != nil:
		return fail(KindScript, jsErr.Error())
	case p.settled == nil:
		return &Error{Plugin: p.Manifest.ID, Call: call, Kind: KindInvalid,
			Msg: "returned a promise that never settled: nothing it awaits (a timer, a fetch, a file) is left to settle it"}
	}

	var o outcome
	if err := json.Unmarshal([]byte(*p.settled), &o); err != nil {
		return &Error{Plugin: p.Manifest.ID, Call: call, Kind: KindInvalid, Msg: "returned what isn’t JSON"}
	}
	if !o.OK {
		return p.thrown(call, o)
	}
	if out != nil {
		if err := json.Unmarshal(o.Value, out); err != nil {
			return &Error{Plugin: p.Manifest.ID, Call: call, Kind: KindInvalid, Msg: "returned an unexpected shape: " + jsonProblem(err)}
		}
	}
	return nil
}

// thrown makes an Error of what the script threw: an error made by
// dalibre.error, a host error passed through (a refused fetch), or any
// other value.
func (p *Plugin) thrown(call string, o outcome) *Error {
	// Frames in the prelude mean nothing to the author.
	var stack []string
	for line := range strings.Lines(o.Error.Stack) {
		if !strings.Contains(line, preludeName) {
			stack = append(stack, line)
		}
	}
	e := &Error{Plugin: p.Manifest.ID, Call: call, Kind: KindScript, Msg: o.Error.Message, Stack: strings.TrimSpace(strings.Join(stack, ""))}
	if e.Msg == "" {
		e.Msg = "threw an error with no message"
	}
	if k := ErrorKind(o.Error.Kind); k != "" {
		if slices.Contains(thrownKinds, k) || k == KindPermission || k == KindTimeout || k == KindCancelled {
			e.Kind = k
		} else {
			e.Msg = fmt.Sprintf("%s (unknown error kind %q)", e.Msg, k)
		}
	}
	if e.Kind == KindScript && o.Error.Name != "" && o.Error.Name != "Error" {
		e.Msg = o.Error.Name + ": " + e.Msg // TypeError: …
	}
	return e
}
