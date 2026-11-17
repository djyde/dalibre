package app

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"dalibre/internal/mcp"
	"dalibre/internal/plugin"
	"dalibre/internal/settings"
)

// bearerTransport sends the MCP token.
type bearerTransport string

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+string(b))
	return http.DefaultTransport.RoundTrip(r)
}

func TestMCPServer(t *testing.T) {
	a := newTestApp(t)
	port := freePort(t)
	a.settings.MCP = settings.MCP{On: true, Port: port}
	a.applyMCP()
	defer func() {
		a.settings.MCP.On = false
		a.applyMCP()
	}()
	st := a.mcpStatus()
	if !st.Running || st.Port != port || st.Err != nil || !st.Used.IsZero() {
		t.Fatalf("status %+v", st)
	}
	token := a.mcpToken()
	if len(token) != 64 {
		t.Fatalf("token %q", token)
	}
	if want := "http://127.0.0.1:" + strconv.Itoa(port) + "/mcp"; a.mcpEndpoint() != want {
		t.Errorf("endpoint %q", a.mcpEndpoint())
	}

	ctx := context.Background()
	connect := func(token string) (*sdk.ClientSession, error) {
		return sdk.NewClient(&sdk.Implementation{Name: "claude-code"}, nil).Connect(ctx, &sdk.StreamableClientTransport{
			Endpoint: a.mcpEndpoint(), HTTPClient: &http.Client{Transport: bearerTransport(token)}, MaxRetries: -1,
		}, nil)
	}
	var cs *sdk.ClientSession
	var err error
	for range 20 { // Serve starts on its own goroutine
		if cs, err = connect(token); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	// An agent's edit shows in the window.
	dune := int64(0)
	for _, b := range a.books {
		if b.Title == "Dune" {
			dune = b.ID
		}
	}
	res, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: "update_books", Arguments: map[string]any{"ids": []int64{dune}, "patch": map[string]any{"add_tags": []string{"Desert"}}}})
	if err != nil || res.IsError {
		t.Fatalf("update: %v %+v", err, res)
	}
	if !slices.Contains(a.byID[dune].Tags, "Desert") {
		t.Errorf("window not reloaded: %+v", a.byID[dune])
	}
	if st := a.mcpStatus(); st.Client != "claude-code" || time.Since(st.Used) > time.Minute {
		t.Errorf("last use %+v", st)
	}

	// Read only: the write tools go.
	a.settings.MCP.ReadOnly = true
	a.applyMCP()
	res, err = cs.CallTool(ctx, &sdk.CallToolParams{Name: "update_books", Arguments: map[string]any{"ids": []int64{dune}, "patch": map[string]any{}}})
	if err == nil && !res.IsError {
		t.Error("update allowed while read-only")
	}

	// A new token: the old one is refused at once.
	a.regenerateMCPToken()
	if a.mcpToken() == token {
		t.Fatal("same token")
	}
	if _, err := connect(token); err == nil {
		t.Error("old token let in")
	}
	cs2, err := connect(a.mcpToken())
	if err != nil {
		t.Fatalf("new token: %v", err)
	}
	cs2.Close()

	snippets := a.mcpSnippets()
	if len(snippets) != 3 || !strings.Contains(snippets[0].Text, "--transport http dalibre "+a.mcpEndpoint()+` --header "Authorization: Bearer `+a.mcpToken()+`"`) {
		t.Fatalf("snippets %+v", snippets)
	}
	var desktop, cursor struct {
		MCPServers map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	json.Unmarshal([]byte(snippets[1].Text), &desktop)
	json.Unmarshal([]byte(snippets[2].Text), &cursor)
	if d := desktop.MCPServers["dalibre"]; d.Command == "" || !slices.Equal(d.Args, []string{"mcp"}) || strings.Contains(snippets[1].Text, a.mcpToken()) {
		t.Errorf("Claude Desktop %s", snippets[1].Text)
	}
	if c := cursor.MCPServers["dalibre"]; c.URL != a.mcpEndpoint() || c.Headers["Authorization"] != "Bearer "+a.mcpToken() {
		t.Errorf("Cursor %s", snippets[2].Text)
	}
}

func TestMCPPortInUse(t *testing.T) {
	a := newTestApp(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	a.settings.MCP = settings.MCP{On: true, Port: ln.Addr().(*net.TCPAddr).Port}
	a.applyMCP()
	if st := a.mcpStatus(); st.Running || !st.PortInUse {
		t.Fatalf("status %+v", st)
	}
	a.settings.MCP.On = false
	a.applyMCP()
	if a.mcpSrv != nil {
		t.Error("still there")
	}
}

// An agent's plugin loads only once the user allows it in the window; it
// can't take an installed plugin's id, nor remove what the user installed.
func TestMCPPlugins(t *testing.T) {
	a := newTestApp(t)
	a.pluginDir = t.TempDir()
	h := mcpHost{a}
	ctx := context.Background()
	folder := func(id, hosts string) string {
		dir := filepath.Join(t.TempDir(), "src")
		os.MkdirAll(dir, 0o755)
		os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(`{"id":"`+id+`","name":"Dev","version":"0.1.0","api":1,"capabilities":["source"],"permissions":{"hosts":[`+hosts+`]}}`), 0o644)
		os.WriteFile(filepath.Join(dir, "main.js"), []byte(`exports.search = () => ({items: []})`), 0o644)
		return dir
	}
	dir := folder("com.example.dev", `"a.example"`)

	// add adds dir as an agent does while the user answers the dialog.
	// The window's thread is the test's: it waits for the agent's call to
	// have asked before it looks.
	asked := make(chan struct{}, 1)
	post := a.post
	a.post = func(fn func()) {
		post(fn)
		select {
		case asked <- struct{}{}:
		default:
		}
	}
	add := func(dir string, yes bool) (mcp.PluginInfo, error) {
		select {
		case <-asked:
		default:
		}
		type result struct {
			info mcp.PluginInfo
			err  error
		}
		done := make(chan result, 1)
		go func() {
			info, err := h.AddPlugin(ctx, dir)
			done <- result{info, err}
		}()
		<-asked
		if a.consent == nil {
			t.Fatal("added without asking")
		}
		if yes {
			a.grantConsent()
		} else {
			ask := a.consent
			a.consent = nil
			ask.answered(false) // as closing the dialog does
		}
		r := <-done
		return r.info, r.err
	}

	// The user says no: nothing is added.
	if _, err := add(dir, false); err == nil || !strings.Contains(err.Error(), "didn’t allow") {
		t.Fatalf("refused: %v", err)
	}
	if len(a.plugins) != 0 || a.settings.Plugins["com.example.dev"] != nil {
		t.Fatal("added without the user")
	}

	info, err := add(dir, true)
	if err != nil || info.State != mcp.PluginLoaded || info.Folder != dir || !slices.Equal(info.Exports, []string{"search"}) {
		t.Fatalf("added %+v: %v", info, err)
	}
	if h.Plugin(ctx, "com.example.dev") == nil {
		t.Fatal("not loaded")
	}
	// Adding it again asks nothing.
	if info, err = h.AddPlugin(ctx, dir); err != nil || a.consent != nil || info.State != mcp.PluginLoaded {
		t.Fatalf("again %+v: %v", info, err)
	}

	// A broken edit shows as failed, with why.
	os.WriteFile(filepath.Join(dir, "main.js"), []byte(`exports.search = (`), 0o644)
	h.ReloadPlugins(ctx)
	list := h.Plugins(ctx)
	if len(list.Plugins) != 1 || list.Plugins[0].State != mcp.PluginFailed || len(list.Errors) != 1 {
		t.Fatalf("broken: %+v", list)
	}
	os.WriteFile(filepath.Join(dir, "main.js"), []byte(`exports.search = () => ({items: []})`), 0o644)
	// An edit asking for another host waits for the user.
	os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(`{"id":"com.example.dev","name":"Dev","version":"0.2.0","api":1,"capabilities":["source"],"permissions":{"hosts":["a.example","b.example"]}}`), 0o644)
	h.ReloadPlugins(ctx)
	if list = h.Plugins(ctx); list.Plugins[0].State != mcp.PluginNeedsApproval || h.Plugin(ctx, "com.example.dev") != nil {
		t.Fatalf("asking for more: %+v", list)
	}

	if err := h.SetPluginEnabled(ctx, "com.example.dev", false); err != nil || h.Plugins(ctx).Plugins[0].State != mcp.PluginDisabled {
		t.Fatalf("turned off: %v", err)
	}
	if err := h.SetPluginEnabled(ctx, "com.example.none", true); err == nil {
		t.Error("turned on a plugin that isn't there")
	}

	// Another folder with the same id would get its grant and settings.
	if _, err := h.AddPlugin(ctx, folder("com.example.dev", `"a.example"`)); err == nil || !strings.Contains(err.Error(), "its own id") {
		t.Fatalf("the same id from another folder: %v", err)
	}

	if err := h.RemovePlugin(ctx, "com.example.dev"); err != nil || len(h.Plugins(ctx).Plugins) != 0 {
		t.Fatalf("removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "main.js")); err != nil {
		t.Error("removing deleted the folder")
	}

	// What the user installed is theirs to remove.
	pkg, err := plugin.Pack(folder("com.example.installed", `"a.example"`), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a.installPlugin(pkg)
	a.grantConsent()
	if err := h.RemovePlugin(ctx, "com.example.installed"); err == nil || len(a.plugins) != 1 {
		t.Fatalf("removed an installed plugin: %v", err)
	}
}
