package app

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"dalibre/internal/app/theme"
	"dalibre/internal/settings"
)

// clickSwitch clicks the switch right of the gear named gear, in a
// popover block whose title and switch share a name.
func clickSwitch(t *testing.T, tt *ui.Tester, gear string) {
	t.Helper()
	g, ok := tt.Find(gear)
	if !ok {
		t.Fatalf("no %q: %q", gear, tt.Texts())
	}
	tt.ClickAt(g.X+g.W+theme.Space10+14, g.Y+g.H/2)
	tt.Frame()
}

func TestAgentsPopoverAndMCPSettings(t *testing.T) {
	a := newTestApp(t)
	a.settings.MCP = settings.MCP{Port: freePort(t)}
	defer func() {
		a.settings.MCP.On = false
		a.applyMCP()
	}()
	tt := ui.NewTester(a.View, 1440, 900)
	tt.Frame()
	if err := tt.Click("MCP"); err != nil {
		t.Fatalf("toolbar: %v; %q", err, tt.Texts())
	}
	tt.Frame()
	if !a.agentsOpen || !tt.HasText("Off") {
		t.Fatalf("popover: %q", tt.Texts())
	}
	// Turning MCP on from the popover starts the server.
	clickSwitch(t, tt, "MCP settings")
	tt.Frame()
	if st := a.mcpStatus(); !st.Running || !a.settings.MCP.On {
		t.Fatalf("on %v, status %+v", a.settings.MCP.On, st)
	}
	// Agents have every tool by default.
	for _, s := range []string{"Running · can change books", a.mcpEndpoint(), "No agent has used it yet"} {
		if !tt.HasText(s) {
			t.Fatalf("popover misses %q: %q", s, tt.Texts())
		}
	}
	if tt.HasText("Read only: agents can’t change books.") {
		t.Fatal("read only by default")
	}

	// An agent at work shows in the toolbar and its calls in the popover.
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "claude-code", Title: "Claude Code"}, nil).Connect(context.Background(), &sdk.StreamableClientTransport{
		Endpoint: a.mcpEndpoint(), HTTPClient: &http.Client{Transport: bearerTransport(a.mcpToken())}, MaxRetries: -1,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	if _, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: "search_books", Arguments: map[string]any{"query": "Dune"}}); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	for _, s := range []string{"Claude Code", "Searched “Dune” · 1 book"} {
		if !tt.HasText(s) {
			t.Fatalf("popover misses %q: %q", s, tt.Texts())
		}
	}
	if _, ok := tt.Find("Claude Code at work"); !ok {
		t.Fatalf("toolbar doesn't show the agent: %q", tt.Texts())
	}
	tt.Advance(time.Second)
	tt.Frame()
	shot(t, tt, "agents-popover")
	// A call shows its arguments on click.
	if err := tt.Click("Searched “Dune” · 1 book"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("{\n  \"query\": \"Dune\"\n}") {
		t.Fatalf("arguments: %q", tt.Texts())
	}
	// Read only says so, and Allow changes ends it.
	a.settings.MCP.ReadOnly = true
	a.applyMCP()
	tt.Frame()
	if !tt.HasText("Running · read only") || !tt.HasText("Read only: agents can’t change books.") {
		t.Fatalf("read only: %q", tt.Texts())
	}
	if err := tt.Click("Allow changes"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.settings.MCP.ReadOnly || !tt.HasText("Running · can change books") {
		t.Fatalf("changes: %v %q", a.settings.MCP.ReadOnly, tt.Texts())
	}
	if err := tt.Click("Clear"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(a.mcpCalls(0)) != 0 || !tt.HasText("No calls yet") {
		t.Fatalf("clear: %q", tt.Texts())
	}

	// Its gear opens Settings › MCP.
	if err := tt.Click("MCP settings"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.agentsOpen || !a.settingsOpen || a.section != sectionMCP {
		t.Fatalf("open %v, settings %v %v", a.agentsOpen, a.settingsOpen, a.section)
	}
	for _, s := range []string{"Let agents edit, add and remove books", "Connect an agent", "Run it in a terminal.", "Tools", "Agents can use 21 of 21", "search_books", "find_metadata", "Move books to the Trash."} {
		if !tt.HasText(s) {
			t.Fatalf("Settings › MCP misses %q: %q", s, tt.Texts())
		}
	}
	for _, s := range tt.Texts() {
		if strings.Contains(s, a.mcpToken()) {
			t.Fatalf("the token shows: %q", s)
		}
	}
	shot(t, tt, "settings-mcp")
	if r, ok := tt.Find("Connect an agent"); ok {
		tt.Scroll(r.X, r.Y, 0, 400)
		tt.Frame()
		shot(t, tt, "settings-mcp-tools")
		tt.Scroll(r.X, r.Y, 0, -400)
		tt.Frame()
	}
	if err := tt.Click("Claude Desktop"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Add it to claude_desktop_config.json, then restart Claude Desktop.") {
		t.Fatalf("Claude Desktop: %q", tt.Texts())
	}
	old := a.mcpToken()
	if err := tt.Click("New token"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.mcpToken() == old || a.mcpToken() == "" {
		t.Error("token not renewed")
	}
}

func TestSharingPopover(t *testing.T) {
	a := newTestApp(t)
	tt := ui.NewTester(a.View, 1440, 900)
	tt.Frame()
	if err := tt.Click("OPDS"); err != nil {
		t.Fatalf("toolbar: %v; %q", err, tt.Texts())
	}
	tt.Frame()
	if !a.sharingOpen || !tt.HasText("Off") || tt.HasText("Running · read only") {
		t.Fatalf("popover: %q", tt.Texts())
	}
}

func TestAgentNameAndPortInUse(t *testing.T) {
	if agentName("claude-code") != "Claude Code" || agentName("zed") != "zed" || agentName("") != "An agent" {
		t.Error("agent names")
	}
	a := newTestApp(t)
	port := freePort(t)
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	a.settings.MCP = settings.MCP{On: true, Port: port}
	a.applyMCP()
	line, _, address := a.mcpStatusLine(false)
	if !strings.Contains(line, "is in use") || address != "" {
		t.Errorf("status %q, address %q", line, address)
	}
	tt := ui.NewTester(a.View, 1440, 900)
	tt.Frame()
	if err := tt.Click("MCP"); err != nil {
		t.Fatalf("toolbar: %v; %q", err, tt.Texts())
	}
	tt.Frame()
	if !tt.HasText(line) {
		t.Fatalf("popover: %q", tt.Texts())
	}
}
