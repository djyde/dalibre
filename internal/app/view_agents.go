package app

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/app/icons"
	"dalibre/internal/app/theme"
	"dalibre/internal/app/widgets"
	"dalibre/internal/mcp"
)

// The AI agents button (Figma AgentIndicator) is MCP in the toolbar: a
// bot with the server's dot, or, while agents work, the agents. Its
// popover has the server's switch and address, the agents, and what they
// did (the server's call log).

// agentsButton is the AgentIndicator in a toolbar, with its popover.
func (a *App) agentsButton(c *ui.Context) {
	cfg := a.settings.MCP
	st := a.mcpStatus()
	a.wakeForAgents(st.Active)
	var b ui.Element
	switch {
	case len(st.Active) > 0:
		label := a.agentTitle(st.Active[0])
		if len(st.Active) > 1 {
			label = locales.McpAgentsCount(len(st.Active))
		}
		b = widgets.AgentButton(c, locales.McpAgentsWorking(label), label, a.agentsOpen, func() {
			for i, ag := range st.Active {
				if i == 2 {
					break
				}
				widgets.AgentAvatar(c.Key(ag.Name), a.agentTitle(ag), a.agentIcon(ag), 20)
			}
		})
	default:
		label := nameMCP
		b = widgets.IndicatorButton(c, icons.Bot, label, a.agentsOpen, cfg.On, a.mcpDot(st))
	}
	if b.Clicked() {
		a.agentsOpen = !a.agentsOpen
	}
	ui.PopoverBase(c, b, &a.agentsOpen, func(panel ui.Element) {
		a.tickWhileMCPShown()
		popoverPanel(panel, b, 400).Children(func() { a.agentsPopover(c, st) })
	})
}

// mcpDot is the color of the AI agents button's dot.
func (a *App) mcpDot(st mcpStatus) ui.Color {
	if st.Err != nil {
		return pal().Danger.FG
	}
	return pal().Success.FG
}

// wakeForAgents rebuilds the view when the last of active stops being
// active, so the button goes back to the sparkle.
func (a *App) wakeForAgents(active []mcp.Agent) {
	var until time.Time
	for _, ag := range active {
		if t := ag.Last.Add(mcp.ActiveWindow); t.After(until) {
			until = t
		}
	}
	if until.IsZero() || !until.After(a.agentsWake) {
		return
	}
	a.agentsWake = until
	time.AfterFunc(time.Until(until)+100*time.Millisecond, func() { a.post(func() {}) })
}

func (a *App) agentsPopover(c *ui.Context, st mcpStatus) {
	cfg := &a.settings.MCP
	status, color, address := a.mcpStatusLine(false)
	if st.Running {
		status = locales.McpRunningReadOnly()
		if cfg.AllowsChanges() {
			status = locales.McpRunningChanges()
		}
	}
	on := cfg.On
	a.sharingBlock(c, nameMCP, status, color, &on, sectionMCP, address, &a.agentsOpen)
	if on != cfg.On {
		a.setMCPOn(on)
	}
	if !cfg.On || !st.Running {
		return
	}
	agents := a.mcpAgents()
	widgets.Hairline(c)
	if len(agents) == 0 {
		ui.Text(c, locales.McpNoAgentYet()).FontSize(12).TextColor(pal().TextTertiary).Padding(theme.Space12, theme.Space16)
	} else {
		ui.Column(c).Padding(theme.Space10, theme.Space8, theme.Space8, theme.Space8).Gap(theme.Space2).AlignItems(ui.Stretch).Children(func() {
			ui.Box(c).Padding(0, theme.Space8, theme.Space4, theme.Space8).Children(func() { widgets.Overline(c, strings.ToUpper(locales.McpAgentsHeading())) })
			now := time.Now()
			for _, ag := range agents {
				a.agentRow(c.Key(ag.Name), ag, now)
			}
		})
		widgets.Hairline(c)
		a.activity(c)
	}
	if !cfg.AllowsChanges() {
		widgets.Hairline(c)
		ui.Row(c).Padding(theme.Space10, theme.Space12, theme.Space10, theme.Space16).Gap(theme.Space8).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icons.Lock).FontSize(14).TextColor(pal().Icon)
			ui.Text(c, locales.McpReadOnlyNote()).FontSize(12).TextColor(pal().TextSecondary).Grow(1)
			if widgets.ButtonSized(c, widgets.Secondary, nil, locales.McpAllowChangesButton(), theme.ControlS).Clicked() {
				cfg.ReadOnly = false
				a.saveSettings()
				a.applyMCP()
			}
		})
	}
}

// agentRow is an agent in the popover: when it last worked and how much.
func (a *App) agentRow(c *ui.Context, ag mcp.Agent, now time.Time) {
	active := ag.Active(now)
	ui.Row(c).Padding(theme.Space6, theme.Space8).Gap(theme.Space10).AlignItems(ui.Center).Children(func() {
		widgets.AgentAvatar(c, a.agentTitle(ag), a.agentIcon(ag), 24)
		ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
			ui.Text(c, a.agentTitle(ag)).FontSize(13).FontWeight(500).SingleLine()
			detail, col := locales.McpAgentLast(ag.Calls, relativeAgo(ag.Last)), pal().TextTertiary
			if active {
				detail, col = locales.McpAgentWorking(ag.Calls), pal().Success.FG
			}
			ui.Text(c, detail).FontSize(12).TextColor(col).SingleLine()
		})
		if active {
			ui.Box(c).Size(8, 8).Radius(4).Background(pal().Success.FG)
		}
	})
}

// activity is the agents' latest calls, newest first.
func (a *App) activity(c *ui.Context) {
	calls := a.mcpCalls(100)
	ui.Column(c).Padding(theme.Space10, theme.Space8, theme.Space8, theme.Space8).Gap(theme.Space2).AlignItems(ui.Stretch).Children(func() {
		ui.Row(c).Padding(0, theme.Space8, theme.Space4, theme.Space8).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Grow(1).Children(func() { widgets.Overline(c, strings.ToUpper(locales.McpActivity())) })
			if len(calls) == 0 {
				return
			}
			clear := ui.ButtonBase(c).Padding(theme.Space2, theme.Space6).Radius(theme.RadiusXS).Label(locales.McpClear())
			if clear.Hovered() {
				clear.Background(pal().Hover)
			}
			if clear.Clicked() {
				a.clearMCPCalls()
				a.agentsCall = 0
			}
			clear.Children(func() {
				ui.Text(c, locales.McpClear()).FontSize(12).FontWeight(500).TextColor(pal().TextAccent)
			})
		})
		if len(calls) == 0 {
			ui.Text(c, locales.McpNoCalls()).FontSize(12).TextColor(pal().TextTertiary).Padding(theme.Space4, theme.Space8)
			return
		}
		titles := map[string]string{}
		for _, ag := range a.mcpAgents() {
			titles[ag.Name] = a.agentTitle(ag)
		}
		ui.Scroll(c).MaxHeight(300).Gap(theme.Space2).AlignItems(ui.Stretch).Children(func() {
			for _, call := range calls {
				a.callRow(c.Key(call.ID), call, titles[call.Agent])
			}
		})
	})
}

// callRow is one call: what it did, in words, by whom and when; a click
// shows its arguments.
func (a *App) callRow(c *ui.Context, call mcp.Call, agent string) {
	open := a.agentsCall == call.ID
	row := ui.ButtonBase(c).Padding(theme.Space6, theme.Space8).Radius(theme.RadiusSM).Justify(ui.Start).AlignItems(ui.Start).Label(a.callSummary(call))
	switch {
	case open:
		row.Background(pal().Subtle)
	case row.Hovered():
		row.Background(pal().Hover)
	}
	if row.Clicked() {
		if open {
			a.agentsCall = 0
		} else {
			a.agentsCall = call.ID
		}
	}
	row.Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(theme.Space6).AlignItems(ui.Stretch).Children(func() { a.callBody(c, call, agent, open) })
	})
}

// callBody is what a call row shows.
func (a *App) callBody(c *ui.Context, call mcp.Call, agent string, open bool) {
	failed := call.Err != ""
	ui.Row(c).Gap(theme.Space10).AlignItems(ui.Start).Children(func() {
		switch {
		case !call.Done:
			ui.Spinner(c).Size(16, 16)
		case failed:
			ui.Icon(c, icons.Alert).FontSize(16).TextColor(pal().Danger.FG)
		default:
			ui.Icon(c, toolIcon(call.Tool)).FontSize(16).TextColor(pal().Icon)
		}
		ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
			t := ui.Text(c, a.callSummary(call)).FontSize(13).MaxLines(2)
			if failed {
				t.TextColor(pal().Danger.FG)
				ui.Text(c, call.Err).FontSize(12).TextColor(pal().Danger.FG).MaxLines(3)
			}
			when := locales.McpNow()
			if call.Done {
				when = i18n.FormatDate(i18n.Locale(), call.Start, i18n.DateOptions{Skeleton: "jm"})
			}
			ui.Text(c, locales.McpCallMeta(agent, when)).FontSize(12).TextColor(pal().TextTertiary).SingleLine()
		})
	})
	if open && len(call.Args) > 0 {
		ui.Text(c, prettyArgs(call)).Font(theme.FontMono).FontSize(11).TextColor(pal().TextSecondary).Selectable().Padding(0, 0, 0, 26)
	}
}

// prettyArgs are a call's arguments, indented when they are whole.
func prettyArgs(call mcp.Call) string {
	if !call.ArgsTruncated {
		var v any
		if json.Unmarshal(call.Args, &v) == nil {
			if b, err := json.MarshalIndent(v, "", "  "); err == nil {
				return string(b)
			}
		}
	}
	return string(call.Args) + "…"
}

// toolIcon is the icon of a tool's calls in the activity.
func toolIcon(tool string) *ui.SVG {
	switch tool {
	case "search_books", "find_books":
		return icons.Search
	case "get_book":
		return icons.BookOpen
	case "list_facets":
		return icons.Tag
	case "update_books", "find_metadata":
		return icons.Pencil
	case "set_cover":
		return icons.Image
	case "add_books", "add_format":
		return icons.Plus
	case "remove_books", "remove_format":
		return icons.Trash
	case "download_book":
		return icons.Download
	case "reload_plugins":
		return icons.Refresh
	case "list_plugins", "create_plugin", "add_plugin", "test_plugin", "enable_plugin", "remove_plugin", "list_actions", "run_action":
		return icons.Puzzle
	}
	return icons.Bot
}

// callSummary says what a call did, in words.
func (a *App) callSummary(call mcp.Call) string {
	var args struct {
		Query  string  `json:"query"`
		Kind   string  `json:"kind"`
		ID     int64   `json:"id"`
		IDs    []int64 `json:"ids"`
		Path   string  `json:"path"`
		Format string  `json:"format"`
		Call   string  `json:"call"`
		On     *bool   `json:"enabled"`
	}
	json.Unmarshal(call.Args, &args)
	title := ""
	if len(call.Books) > 0 {
		title = call.Books[0].Title
	} else if b := a.byID[args.ID]; b != nil {
		title = b.Title
	}
	many := len(call.Books)
	if many == 0 {
		many = len(args.IDs)
	}
	book := func(one func(string) string, n func(int) string) string {
		if many > 1 || title == "" {
			return n(max(many, 1))
		}
		return one(title)
	}
	switch call.Tool {
	case "search_books":
		if args.Query == "" {
			return locales.McpSearchedLibrary(max(call.Count, 0))
		}
		return locales.McpSearched(max(call.Count, 0), args.Query)
	case "find_books":
		return locales.McpFoundBooks(max(call.Count, 0), args.Query)
	case "get_book":
		return book(locales.McpRead, locales.McpReadMany)
	case "list_facets":
		return locales.McpListed(args.Kind)
	case "update_books":
		return book(locales.McpEdited, locales.McpEditedMany)
	case "set_cover":
		return book(locales.McpSetCover, locales.McpSetCoverMany)
	case "add_books":
		return book(locales.McpAdded, locales.McpAddedMany)
	case "remove_books":
		return book(locales.McpRemoved, locales.McpRemovedMany)
	case "add_format":
		f := strings.ToUpper(strings.TrimPrefix(filepath.Ext(args.Path), "."))
		return locales.McpAddedFormat(f, title)
	case "remove_format":
		return locales.McpRemovedFormat(strings.ToUpper(args.Format), title)
	case "download_book":
		return book(locales.McpDownloaded, locales.McpDownloadedMany)
	case "find_metadata":
		return locales.McpFoundMetadata(title)
	case "list_plugins":
		return locales.McpListedPlugins()
	case "create_plugin":
		return locales.McpCreatedPlugin(filepath.Base(args.Path))
	case "add_plugin":
		return locales.McpAddedPlugin(filepath.Base(args.Path))
	case "reload_plugins":
		return locales.McpReloadedPlugins()
	case "list_actions":
		return locales.McpListedActions()
	case "run_action":
		var run mcp.ActionRun
		json.Unmarshal(call.Args, &run)
		label := run.Action
		for _, p := range a.plugins {
			if act := p.Manifest.Action(run.Action); p.Manifest.ID == run.Plugin && act != nil {
				label = act.Label()
			}
		}
		return locales.McpRanAction(label)
	}
	// The plugin tools name a plugin by its id, a string.
	var pl struct {
		ID string `json:"id"`
	}
	json.Unmarshal(call.Args, &pl)
	switch name := a.pluginName(pl.ID); call.Tool {
	case "test_plugin":
		return locales.McpTestedPlugin(args.Call, name)
	case "enable_plugin":
		if args.On != nil && !*args.On {
			return locales.McpDisabledPlugin(name)
		}
		return locales.McpEnabledPlugin(name)
	case "remove_plugin":
		return locales.McpRemovedPlugin(name)
	}
	return call.Tool
}

// agentTitle is the name an agent shows with.
func (a *App) agentTitle(ag mcp.Agent) string {
	if ag.Title != "" {
		return ag.Title
	}
	return agentName(ag.Name)
}

// agentIcon is an agent's own icon, decoded once; nil when it sent none
// or it doesn't decode.
func (a *App) agentIcon(ag mcp.Agent) ui.ImageSource {
	if len(ag.Icon) == 0 {
		return nil
	}
	if a.agentIcons == nil {
		a.agentIcons = map[string]ui.ImageSource{}
	}
	src, ok := a.agentIcons[ag.Name]
	if !ok {
		src, _ = ui.DecodeImage(ag.Icon)
		a.agentIcons[ag.Name] = src
	}
	return src
}
