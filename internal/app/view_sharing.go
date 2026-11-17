package app

import (
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/app/icons"
	"dalibre/internal/app/theme"
	"dalibre/internal/app/widgets"
	"dalibre/internal/mcp"
	"dalibre/internal/smartdevice"
)

// Sharing and AI agents (Figma "13 · Sharing & AI agents"): what
// Dalibre serves has two buttons in the toolbar. Sharing is the library
// shared with reading apps over OPDS; AI agents is MCP, which shows the
// agent at work and what it did (view_agents.go). Each opens a popover to
// turn its server on and off, copy its address and reach its Settings
// section.

// The servers are named by their protocols, in every language.
const (
	nameOPDS = "OPDS"
	nameMCP  = "MCP"
)

// sharingButton is the SharingIndicator in a toolbar, with its popover:
// OPDS, then wireless devices (device.go). It is on while either is, and
// is named for wireless devices when only they are on.
func (a *App) sharingButton(c *ui.Context) {
	sh, w := a.settings.OPDS.Share, a.settings.Wireless
	failed := sh.On && a.share != nil && a.share.err != nil
	if _, bad := a.deviceStatus(); w.On && bad {
		failed = true
	}
	label := nameOPDS
	if w.On && !sh.On {
		label = locales.WirelessName()
	}
	dot := pal().Success.FG
	if failed {
		dot = pal().Danger.FG
	}
	b := widgets.IndicatorButton(c, icons.Wifi, label, a.sharingOpen, sh.On || w.On, dot)
	if b.Clicked() {
		a.sharingOpen = !a.sharingOpen
	}
	ui.PopoverBase(c, b, &a.sharingOpen, func(panel ui.Element) {
		popoverPanel(panel, b, 360).Children(func() {
			a.shareBlock(c)
			widgets.Hairline(c)
			a.wirelessBlock(c)
		})
	})
}

// popoverPanel gives a toolbar button's popover its look, under the
// button's end.
func popoverPanel(panel, anchor ui.Element, width float32) ui.Element {
	return panel.AttachTo(anchor, ui.AnchorBottomEnd, ui.AnchorTopEnd).Margin(theme.Space6, 0, 0, 0).
		Width(width).Radius(theme.RadiusLG).Background(pal().SurfaceRaised).Border(1, pal().Border).
		Shadow(0, 2, 6, 0, ui.RGBA(26, 23, 18, 0.08)).Shadow(0, 24, 48, -8, ui.RGBA(26, 23, 18, 0.18)).AlignItems(ui.Stretch)
}

// sharingBlock is a server's head in its popover: its name and status,
// its Settings section, its switch, and its address while it runs; close
// closes the popover.
func (a *App) sharingBlock(c *ui.Context, title, status string, statusColor ui.Color, on *bool, section settingsSection, address string, close *bool) {
	ui.Column(c.Key(title)).Padding(theme.Space12, theme.Space12, theme.Space12, theme.Space16).Gap(theme.Space10).AlignItems(ui.Stretch).Children(func() {
		ui.Row(c).Gap(theme.Space10).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(theme.Space2).Children(func() {
				ui.Text(c, title).FontSize(14).FontWeight(600)
				ui.Text(c, status).FontSize(12).TextColor(statusColor).MaxLines(2)
			})
			if widgets.IconButton(c, icons.Settings, locales.SharingSettingsTooltip(title), false).Clicked() {
				*close = false
				a.openSettings(section)
			}
			ui.Switch(c, on).Label(title)
		})
		if address != "" {
			ui.Row(c).Padding(0, 0, 0, theme.Space10).Radius(theme.RadiusSM).Background(pal().Subtle).AlignItems(ui.Center).Children(func() {
				ui.Text(c, address).Font(theme.FontMono).FontSize(12).Selectable().SingleLine().Grow(1).MinWidth(0)
				if widgets.IconButton(c, icons.Copy, locales.SettingsCopyAddress(), false).Clicked() {
					mygo.Clipboard.Write(transfer.TextData(address))
					a.toast("%s", locales.SettingsCopied(address))
				}
			})
		}
	})
}

// shareBlock is sharing over OPDS in the Sharing popover.
func (a *App) shareBlock(c *ui.Context) {
	sh := &a.settings.OPDS.Share
	status, color, address := locales.SharingOff(), pal().TextTertiary, ""
	if st := a.share; sh.On {
		switch {
		case st != nil && st.err != nil:
			status, color = st.err.Error(), pal().Danger.FG
		case st == nil || st.server == nil:
			status, color = locales.SettingsStarting(), pal().TextSecondary
		default:
			status, color, address = locales.SettingsSharing(), pal().Success.FG, a.shareAddress()
			if a.lib == nil {
				status = locales.SettingsSharingNoLibrary()
			} else if n := st.opds.Readers(); n > 0 {
				status = locales.SettingsSharingReaders(n)
			}
		}
	}
	on := sh.On
	a.sharingBlock(c, nameOPDS, status, color, &on, sectionOPDS, address, &a.sharingOpen)
	if on != sh.On {
		sh.On = on
		a.saveSettings()
		a.applyShare()
	}
}

// wirelessBlock is wireless devices in the Sharing popover.
func (a *App) wirelessBlock(c *ui.Context) {
	w := &a.settings.Wireless
	status, bad := a.deviceStatus()
	color := pal().TextTertiary
	switch {
	case bad:
		color = pal().Danger.FG
	case a.connectedDevice() != nil:
		color = pal().Success.FG
	case w.On:
		color = pal().TextSecondary
	}
	on := w.On
	a.sharingBlock(c, locales.WirelessName(), status, color, &on, sectionWireless, a.deviceAddress(), &a.sharingOpen)
	if on != w.On {
		w.On = on
		a.saveSettings()
		a.applyDevice()
	}
}

// ---- Settings › Wireless devices

// wirelessEdit is Settings › Wireless devices' fields while they are edited.
type wirelessEdit struct {
	loaded   bool
	port, pw string
}

func (a *App) wirelessPane(c *ui.Context) {
	w := &a.settings.Wireless
	ed := &a.wirelessEdit
	if !ed.loaded {
		ed.loaded = true
		ed.port = ""
		if w.Port > 0 {
			ed.port = strconv.Itoa(w.Port)
		}
		if w.Password && a.secrets != nil {
			secrets := a.secrets
			a.run(func() {
				pw, _ := secrets.Get(wirelessSecret)
				a.post(func() { ed.pw = pw })
			})
		}
	}
	pane(c, func() {
		ui.Column(c).Gap(theme.Space10).AlignItems(ui.Stretch).Children(func() {
			ui.Row(c).Gap(theme.Space16).AlignItems(ui.Start).Children(func() {
				heading(c, locales.WirelessName(), locales.WirelessBody())
				on := w.On
				ui.Switch(c, &on).Label(locales.WirelessName())
				if on != w.On {
					w.On = on
					a.saveSettings()
					a.applyDevice()
				}
			})
			if !w.On {
				return
			}
			status, bad := a.deviceStatus()
			color, ic := pal().TextSecondary, icons.Wifi
			switch {
			case bad:
				color, ic = pal().Danger.FG, icons.Alert
			case a.connectedDevice() != nil:
				color = pal().Success.FG
			}
			addr := a.deviceAddress()
			ui.Row(c).Padding(theme.Space6, theme.Space6, theme.Space6, theme.Space12).MinHeight(44).Gap(theme.Space10).Radius(theme.RadiusMD).Background(pal().Subtle).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, ic).FontSize(16).TextColor(color)
				ui.Column(c).Grow(1).MinWidth(0).Gap(theme.Space2).Children(func() {
					if addr != "" {
						ui.Text(c, addr).Font(theme.FontMono).FontSize(12).Selectable()
					}
					ui.Text(c, status).FontSize(12).TextColor(color)
				})
				if addr != "" && widgets.IconButton(c, icons.Copy, locales.SettingsCopyAddress(), false).Clicked() {
					mygo.Clipboard.Write(transfer.TextData(addr))
					a.toast("%s", locales.SettingsCopied(addr))
				}
			})
			req := w.Password
			ui.Checkbox(c, &req, locales.WirelessRequirePassword())
			ui.Row(c).Gap(theme.Space12).AlignItems(ui.End).Children(func() {
				ui.Column(c).Width(120).AlignItems(ui.Stretch).Children(func() {
					labeled(c, locales.SettingsPortLabel(), func() {
						ui.TextInput(c, &ed.port).Label(locales.SettingsPortLabel()).Placeholder(strconv.Itoa(smartdevice.DefaultPort))
					})
				})
				if req {
					widgets.Field(c, locales.CommonPassword(), "", func() { ui.TextInput(c, &ed.pw).Label(locales.CommonPassword()).Password() })
				}
				port := 0
				text := strings.TrimSpace(ed.port)
				var perr error
				if text != "" {
					port, perr = strconv.Atoi(text)
				}
				bad := perr != nil || port < 0 || port > 65535 || req && ed.pw == ""
				changed := req != w.Password || port != w.Port || req
				if widgets.Button(c, widgets.Primary, nil, locales.SettingsApply()).Disabled(bad || !changed).Clicked() {
					w.Port, w.Password = port, req
					a.saveSettings()
					pw, secrets := ed.pw, a.secrets
					a.run(func() {
						if secrets != nil && req {
							secrets.Set(wirelessSecret, pw)
						}
						a.post(func() {
							if a.device != nil && a.device.srv != nil {
								go a.device.srv.Close()
							}
							a.device = nil // restart with the new password
							a.applyDevice()
						})
					})
				}
			})
			ui.Text(c, locales.WirelessNote()).FontSize(12).TextColor(pal().TextTertiary)
		})
	})
}

// setMCPOn turns the MCP server on or off.
func (a *App) setMCPOn(on bool) {
	a.settings.MCP.On = on
	a.saveSettings()
	a.applyMCP()
}

// mcpStatusLine is how the MCP server is, in a line, its color, and its
// address while it runs. Settings says "Running" first; the popover's
// switch says so already.
func (a *App) mcpStatusLine(running bool) (string, ui.Color, string) {
	if !a.settings.MCP.On {
		return locales.SharingOff(), pal().TextTertiary, ""
	}
	st := a.mcpStatus()
	switch {
	case st.PortInUse:
		return locales.McpPortInUse(strconv.Itoa(st.Port)), pal().Danger.FG, ""
	case st.Err != nil:
		return locales.McpCantStart(st.Err.Error()), pal().Danger.FG, ""
	case !st.Running:
		return locales.SettingsStarting(), pal().TextSecondary, ""
	}
	line := locales.McpNoAgentYet()
	if !st.Used.IsZero() {
		line = locales.McpAgentUsed(agentName(st.Client), relativeAgo(st.Used))
	}
	if running {
		line = locales.McpRunning(line)
	}
	return line, pal().Success.FG, a.mcpEndpoint()
}

// agentName is how people know the agent that gave name in its MCP
// handshake.
func agentName(name string) string {
	switch strings.ToLower(name) {
	case "":
		return locales.McpAnAgent()
	case "claude-code":
		return "Claude Code"
	case "claude-ai", "claude-desktop":
		return "Claude Desktop"
	case "cursor", "cursor-vscode":
		return "Cursor"
	}
	return name
}

// relativeAgo says how long ago t was, to the minute.
func relativeAgo(t time.Time) string {
	d := time.Since(t)
	unit, n := i18n.RelativeMinute, d.Minutes()
	switch {
	case d < time.Minute:
		unit, n = i18n.RelativeSecond, 0
	case d < time.Hour:
	case d < 24*time.Hour:
		unit, n = i18n.RelativeHour, d.Hours()
	default:
		unit, n = i18n.RelativeDay, d.Hours()/24
	}
	return i18n.FormatRelativeTime(i18n.Locale(), -float64(int(n)), unit, i18n.RelativeTimeOptions{Auto: true})
}

// tickWhileMCPShown rebuilds the view now and then while the MCP status
// shows, so "2 minutes ago" stays true.
func (a *App) tickWhileMCPShown() {
	if a.mcpTicking || !a.settings.MCP.On {
		return
	}
	a.mcpTicking = true
	time.AfterFunc(20*time.Second, func() { a.post(func() { a.mcpTicking = false }) })
}

// ---- Settings › MCP

// mcpEdit is Settings › MCP's port field while it is edited.
type mcpEdit struct {
	loaded bool
	port   string
}

// mcpClientHints say where each agent's setup goes, in the order
// of mcpSnippets.
var mcpClientHints = []func() string{locales.McpHintClaudeCode, locales.McpHintClaudeDesktop, locales.McpHintCursor}

func (a *App) mcpPane(c *ui.Context) {
	cfg := &a.settings.MCP
	ed := &a.mcpEdit
	if !ed.loaded {
		ed.loaded, ed.port = true, strconv.Itoa(cfg.ServerPort())
	}
	pane(c, func() {
		ui.Column(c).Gap(theme.Space10).AlignItems(ui.Stretch).Children(func() {
			ui.Row(c).Gap(theme.Space16).AlignItems(ui.Start).Children(func() {
				heading(c, nameMCP, locales.McpBody())
				on := cfg.On
				ui.Switch(c, &on).Label(nameMCP)
				if on != cfg.On {
					a.setMCPOn(on)
				}
			})
			if !cfg.On {
				return
			}
			a.tickWhileMCPShown()
			status, color, address := a.mcpStatusLine(true)
			ui.Row(c).Padding(theme.Space6, theme.Space6, theme.Space6, theme.Space12).MinHeight(44).Gap(theme.Space10).Radius(theme.RadiusMD).Background(pal().Subtle).AlignItems(ui.Center).Children(func() {
				ic := icons.Bot
				if color == pal().Danger.FG {
					ic = icons.Alert
				}
				ui.Icon(c, ic).FontSize(16).TextColor(color)
				ui.Column(c).Grow(1).MinWidth(0).Gap(theme.Space2).Children(func() {
					ui.Text(c, a.mcpEndpoint()).Font(theme.FontMono).FontSize(12).Selectable()
					ui.Text(c, status).FontSize(12).TextColor(color)
				})
				if address != "" && widgets.IconButton(c, icons.Copy, locales.SettingsCopyAddress(), false).Clicked() {
					mygo.Clipboard.Write(transfer.TextData(address))
					a.toast("%s", locales.SettingsCopied(address))
				}
			})
			allow := cfg.AllowsChanges()
			ui.Checkbox(c, &allow, locales.McpAllowChanges())
			if allow != cfg.AllowsChanges() {
				cfg.ReadOnly = !allow
				a.saveSettings()
				a.applyMCP()
			}
			port, perr := strconv.Atoi(strings.TrimSpace(ed.port))
			ui.Row(c).Gap(theme.Space12).AlignItems(ui.End).Children(func() {
				ui.Column(c).Width(120).AlignItems(ui.Stretch).Children(func() {
					labeled(c, locales.SettingsPortLabel(), func() { ui.TextInput(c, &ed.port).Label(locales.SettingsPortLabel()) })
				})
				if perr == nil && port != cfg.ServerPort() {
					bad := port < 1 || port > 65535
					if widgets.Button(c, widgets.Primary, nil, locales.SettingsApply()).Disabled(bad).Clicked() {
						cfg.Port = port
						a.saveSettings()
						a.applyMCP()
					}
				}
				widgets.Field(c, locales.McpToken(), "", func() {
					masked := strings.Repeat("•", 16)
					ui.TextInput(c, &masked).Label(locales.McpToken()).ReadOnly(true)
				})
				if widgets.Button(c, widgets.Secondary, icons.Refresh, locales.McpNewToken()).Clicked() {
					a.regenerateMCPToken()
					a.toast("%s", locales.McpNewTokenMade())
				}
			})
		})
		if !cfg.On {
			return
		}
		widgets.Hairline(c)
		a.mcpConnect(c)
		widgets.Hairline(c)
		a.mcpTools(c)
		ui.Text(c, locales.McpNote()).FontSize(12).TextColor(pal().TextTertiary)
	})
}

// mcpConnect is Connect an agent: how each agent is set up, to copy.
func (a *App) mcpConnect(c *ui.Context) {
	snippets := a.mcpSnippets()
	names := make([]string, len(snippets))
	for i, s := range snippets {
		names[i] = s.Client
	}
	if a.mcpTab < 0 || a.mcpTab >= len(snippets) {
		a.mcpTab = 0
	}
	ui.Column(c).Gap(theme.Space12).AlignItems(ui.Stretch).Children(func() {
		ui.Text(c, locales.McpConnect()).FontSize(13).FontWeight(600)
		ui.Tabs(c, &a.mcpTab, names...)
		s := snippets[a.mcpTab]
		shown := s.Text
		if t := a.mcpToken(); t != "" {
			shown = strings.ReplaceAll(shown, t, strings.Repeat("•", 16))
		}
		ui.Row(c).Padding(theme.Space10, theme.Space6, theme.Space10, theme.Space12).Gap(theme.Space10).Radius(theme.RadiusMD).Background(pal().Subtle).AlignItems(ui.Start).Children(func() {
			ui.Text(c, shown).Font(theme.FontMono).FontSize(12).FixedLineHeight(18).Grow(1).MinWidth(0)
			if widgets.IconButton(c, icons.Copy, locales.McpCopySetup(s.Client), false).Clicked() {
				mygo.Clipboard.Write(transfer.TextData(s.Text))
				a.toast("%s", locales.McpCopiedSetup(s.Client))
			}
		})
		ui.Text(c, mcpClientHints[a.mcpTab]()).FontSize(12).TextColor(pal().TextTertiary)
	})
}

// mcpToolInfo says what each tool does, for Settings › MCP.
var mcpToolInfo = map[string]func() string{
	"search_books": locales.McpToolSearchBooks, "get_book": locales.McpToolGetBook, "list_facets": locales.McpToolListFacets,
	"update_books": locales.McpToolUpdateBooks, "set_cover": locales.McpToolSetCover, "add_books": locales.McpToolAddBooks,
	"add_format": locales.McpToolAddFormat, "remove_format": locales.McpToolRemoveFormat, "remove_books": locales.McpToolRemoveBooks,
	"find_books": locales.McpToolFindBooks, "download_book": locales.McpToolDownloadBook, "find_metadata": locales.McpToolFindMetadata,
	"list_plugins": locales.McpToolListPlugins, "create_plugin": locales.McpToolCreatePlugin, "add_plugin": locales.McpToolAddPlugin,
	"reload_plugins": locales.McpToolReloadPlugins, "test_plugin": locales.McpToolTestPlugin, "enable_plugin": locales.McpToolEnablePlugin,
	"remove_plugin": locales.McpToolRemovePlugin, "list_actions": locales.McpToolListActions, "run_action": locales.McpToolRunAction,
}

// mcpTools lists the tools agents get, by group; those they don't have
// now are dimmed.
func (a *App) mcpTools(c *ui.Context) {
	groups := []struct {
		group mcp.Group
		title func() string
	}{{mcp.ReadGroup, locales.McpGroupRead}, {mcp.ChangeGroup, locales.McpGroupChange}, {mcp.SourceGroup, locales.McpGroupSources}, {mcp.PluginGroup, locales.McpGroupPlugins}}
	total, have := 0, 0
	for _, g := range groups {
		n := len(mcp.Tools(g.group))
		total += n
		if g.group == mcp.ReadGroup || a.settings.MCP.AllowsChanges() {
			have += n
		}
	}
	ui.Column(c).Gap(theme.Space12).AlignItems(ui.Stretch).Children(func() {
		ui.Row(c).Gap(theme.Space16).AlignItems(ui.Center).Children(func() {
			ui.Text(c, locales.McpTools()).FontSize(13).FontWeight(600).Grow(1)
			ui.Text(c, locales.McpToolsCount(strconv.Itoa(have), strconv.Itoa(total))).FontSize(12).TextColor(pal().TextTertiary)
		})
		for _, g := range groups {
			on := g.group == mcp.ReadGroup || a.settings.MCP.AllowsChanges()
			ui.Column(c.Key(g.group)).Gap(theme.Space4).AlignItems(ui.Stretch).Children(func() {
				ui.Row(c).Gap(theme.Space8).AlignItems(ui.Center).Children(func() {
					widgets.Overline(c, strings.ToUpper(g.title())).Grow(1)
					if !on {
						ui.Text(c, locales.McpToolsOff()).FontSize(12).TextColor(pal().TextTertiary)
					}
				})
				ui.Column(c).Radius(theme.RadiusMD).Border(1, pal().Border).Clip().AlignItems(ui.Stretch).Children(func() {
					for i, name := range mcp.Tools(g.group) {
						row := ui.Row(c.Key(name)).Padding(theme.Space8, theme.Space12).Gap(theme.Space12).AlignItems(ui.Center)
						if i > 0 {
							row.BorderWidth(1, 0, 0, 0).BorderColor(pal().Border)
						}
						if !on {
							row.Opacity(0.5)
						}
						row.Children(func() {
							ui.Icon(c, toolIcon(name)).FontSize(16).TextColor(pal().Icon)
							ui.Text(c, name).Font(theme.FontMono).FontSize(12).Width(120).SingleLine()
							if info := mcpToolInfo[name]; info != nil {
								ui.Text(c, info()).FontSize(13).TextColor(pal().TextSecondary).Grow(1).MinWidth(0)
							}
						})
					}
				})
			})
		}
	})
}
