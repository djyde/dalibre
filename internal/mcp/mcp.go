// Package mcp serves a library to the user's own agents over the Model
// Context Protocol: tools to search and read it and, when the user allows
// changes, to edit it. See docs/mcp/design.md.
package mcp

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"dalibre/internal/calibre"
	"dalibre/internal/plugin"
)

// Path is where the server answers on its port.
const Path = "/mcp"

// Host is what the app gives the server. Its methods may be called on
// any goroutine.
type Host interface {
	// Library is the open library; nil when none is.
	Library() *calibre.Library
	// Changed tells the window that books changed, so it reloads; ids are
	// the books whose covers may have changed.
	Changed(ids ...int64)
	// Trash takes removed books' folders, as the Recycle Bin; nil keeps
	// them in the library's trash folder.
	Trash() func(path string) error
}

// Sources is what a Host with plugins gives too: Get books' sources and
// the metadata plugins.
type Sources interface {
	BookSources(ctx context.Context) []*plugin.Plugin
	MetadataSources(ctx context.Context) []*plugin.Plugin
	// Download adds result r of source p to the library as format, as Get
	// books does, and returns the new book.
	Download(ctx context.Context, p *plugin.Plugin, r plugin.Result, format string) (int64, error)
}

// Processor is what a Host with file type plugins gives too: what they
// make of a file an agent adds, before it goes into the library. done
// removes what they made; errs are the plugins that failed, passed over.
type Processor interface {
	ProcessImport(ctx context.Context, file string) (out string, done func(), errs []error)
}

// processImport is what the host's file type plugins make of file; file
// itself when it has none.
func (s *Server) processImport(ctx context.Context, file string) (string, func(), []string) {
	pr, ok := s.host.(Processor)
	if !ok {
		return file, func() {}, nil
	}
	out, done, errs := pr.ProcessImport(ctx, file)
	var msgs []string
	for _, e := range errs {
		msgs = append(msgs, e.Error())
	}
	return out, done, msgs
}

// Events is what a Host with file type plugins' hooks gives too: told
// after an agent added a file or removed a format, it calls them.
type Events interface {
	Added(ctx context.Context, id int64, format string, newBook bool)
	FormatRemoved(ctx context.Context, id int64, format string)
}

// added tells the host's Events, if any, that a file was added.
func (s *Server) added(ctx context.Context, id int64, format string, newBook bool) {
	if ev, ok := s.host.(Events); ok {
		ev.Added(ctx, id, format, newBook)
	}
}

// Server is the MCP server of a Host.
type Server struct {
	host Host
	srv  *sdk.Server

	token atomic.Pointer[string]
	// OnUse, when set, is told when a tool call starts and when it
	// finishes; it may be called on any goroutine.
	OnUse func()

	mu       sync.Mutex
	writable bool
	found    map[string]plugin.Result // find_books' results, by source and id
	order    []string                 // keys of found, oldest first
	// processed is what test_plugin's last process call made, kept for
	// the agent to look at until the next.
	processed *plugin.Processed
	client    string // the agent that called a tool last
	used      time.Time
	agents    map[string]*Agent // by name
	calls     []*Call           // oldest first
	nextCall  int64
}

// clientMeta is the key in a call's _meta under which the stdio bridge
// passes on the agent it serves: its name, title and icon.
const clientMeta = "dalibre/client"

// New makes the server. With allowChanges it has the write tools too.
func New(h Host, version string, allowChanges bool) *Server {
	s := &Server{host: h, found: map[string]plugin.Result{}, agents: map[string]*Agent{}}
	s.srv = sdk.NewServer(&sdk.Implementation{Name: "dalibre", Title: "Dalibre", Version: version}, &sdk.ServerOptions{
		Instructions: "Dalibre is the user's e-book library, a calibre library. Book ids come from search_books. " +
			"To convert a book, convert the file get_book gives to a file outside the library, then add it with add_format. " +
			"To write a plugin for Dalibre (a book source, a metadata source, or one that works on books' files), read " + PluginDocs +
			" first, then use create_plugin, add_plugin and test_plugin; reload_plugins after an edit. " +
			"What the user's plugins can do with books and the library (find duplicates and the like) is in list_actions; run_action runs one.",
		Capabilities: &sdk.ServerCapabilities{Tools: &sdk.ToolCapabilities{ListChanged: true}},
	})
	s.srv.AddReceivingMiddleware(s.noteUse)
	s.addReadTools()
	s.SetAllowChanges(allowChanges)
	return s
}

// noteUse logs each tool call, with the agent behind it.
func (s *Server) noteUse(next sdk.MethodHandler) sdk.MethodHandler {
	return func(ctx context.Context, method string, req sdk.Request) (res sdk.Result, err error) {
		r, ok := req.(*sdk.CallToolRequest)
		if !ok {
			return next(ctx, method, req)
		}
		c := s.begin(caller(r), r.Params.Name, r.Params.Arguments)
		s.tellUse()
		defer func() {
			s.end(c, res, err)
			s.tellUse()
		}()
		return next(context.WithValue(ctx, recorderKey{}, c), method, req)
	}
}

// tellUse calls OnUse, when set.
func (s *Server) tellUse() {
	if s.OnUse != nil {
		s.OnUse()
	}
}

// LastUse is the name the agent that called a tool last gave, as
// "claude-code", and when; a zero time when none has.
func (s *Server) LastUse() (string, time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.client, s.used
}

// MCP is the server, to run on a transport of its own, as stdio.
func (s *Server) MCP() *sdk.Server { return s.srv }

// SetAllowChanges adds or removes the tools that change the library.
func (s *Server) SetAllowChanges(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if on == s.writable {
		return
	}
	s.writable = on
	if on {
		s.addWriteTools()
		if _, ok := s.host.(Sources); ok {
			s.addSourceTools()
		}
		if _, ok := s.host.(Plugins); ok {
			s.addPluginTools()
		}
		if _, ok := s.host.(Actions); ok {
			s.addActionTools()
		}
		return
	}
	s.srv.RemoveTools(writeTools...)
	s.srv.RemoveTools(sourceTools...)
	s.srv.RemoveTools(pluginTools...)
}

func (s *Server) allowsChanges() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writable
}

// SetToken sets the token requests must carry; the one before stops
// working at once.
func (s *Server) SetToken(token string) { s.token.Store(&token) }

// Handler serves the server at Path over Streamable HTTP to requests
// that carry the token, from this computer only.
func (s *Server) Handler() http.Handler {
	h := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return s.srv }, nil)
	mux := http.NewServeMux()
	mux.Handle(Path, h)
	return guard(func() string {
		if t := s.token.Load(); t != nil {
			return *t
		}
		return ""
	}, mux)
}

// guard refuses requests from other computers, from web pages (DNS
// rebinding and cross-origin requests), and without the token.
func guard(token func() string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		port := ""
		if a, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr); ok {
			_, port, _ = net.SplitHostPort(a.String())
		}
		if !localHost(r.Host, port) {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if o := r.Header.Get("Origin"); o != "" {
			u, err := url.Parse(o)
			if err != nil || u.Scheme != "http" || !localHost(u.Host, port) {
				http.Error(w, "forbidden origin", http.StatusForbidden)
				return
			}
		}
		t := token()
		if t == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+t)) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// localHost reports whether hostport names this computer, on port when
// port is known.
func localHost(hostport, port string) bool {
	host, p, err := net.SplitHostPort(hostport)
	if err != nil {
		return false
	}
	if port != "" && p != port {
		return false
	}
	switch strings.ToLower(host) {
	case "127.0.0.1", "localhost", "::1":
		return true
	}
	return false
}

// Listen listens on port of this computer's loopback address only.
func Listen(port int) (net.Listener, error) {
	return net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
}

// Endpoint is the server's address on port.
func Endpoint(port int) string { return "http://127.0.0.1:" + strconv.Itoa(port) + Path }

// NewToken makes a token for the settings.
func NewToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}
