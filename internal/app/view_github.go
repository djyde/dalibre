package app

import (
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/app/icons"
	"dalibre/internal/app/theme"
	"dalibre/internal/app/widgets"
	"dalibre/internal/plugin"
)

// Install from GitHub (Figma "20 · Plugin tools", frames 1 and 2): the
// repository and the version on top; once found, the repository's README
// on the left and what the plugin will be able to do on the right, its
// tools first.

// The sheet's size once a repository is found: room for its README.
const (
	ghWideWidth  = 960
	ghWideHeight = 800
)

// githubSheet is Install from GitHub.
func (a *App) githubSheet(c *ui.Context) {
	g := &a.ghi
	if !g.open {
		return
	}
	header := func() {
		ui.Column(c).Grow(1).Gap(theme.Space4).Children(func() {
			ui.Text(c, locales.SettingsInstallGitHubTitle()).FontSize(16).FontWeight(600)
			ui.Text(c, locales.GithubInstallBody()).FontSize(13).TextColor(pal().TextSecondary)
		})
	}
	footer := func() {
		if g.manifest != nil {
			ui.Icon(c, icons.ShieldCheck).FontSize(14).TextColor(pal().IconMuted)
			note := locales.SettingsGitHubSandboxNote()
			if len(g.manifest.Tools) > 0 {
				note = locales.ToolsSandboxNote(strings.Join(g.manifest.ToolIDs(), ", "))
			}
			ui.Text(c, note).FontSize(12).TextColor(pal().TextTertiary).Grow(1).MaxLines(2)
		} else {
			ui.Spacer(c)
		}
		if widgets.Button(c, widgets.Secondary, nil, locales.CommonCancel()).Clicked() {
			g.open = false
		}
		ready := g.manifest != nil && !g.preparing && (len(g.manifest.Tools) == 0 || a.picksReady(g.manifest))
		if widgets.Button(c, widgets.Primary, nil, locales.SettingsInstall()).Disabled(!ready).Clicked() {
			a.ghInstallChosen()
		}
	}
	if len(g.releases) == 0 && len(g.branches) == 0 {
		// Nothing found yet: the repository box alone.
		widgets.DialogFrame(c, &g.open, 520, header, func() {
			ui.Column(c).Padding(theme.Space16, theme.Space24, theme.Space20, theme.Space24).Gap(theme.Space12).AlignItems(ui.Stretch).Children(func() {
				a.ghRepoBox(c, false)
				a.ghStatus(c)
			})
		}, footer)
	} else {
		widgets.TallDialogFrame(c, &g.open, ghWideWidth, ghWideHeight, header, func() {
			ui.Row(c).Padding(theme.Space16, theme.Space24).Gap(theme.Space8).AlignItems(ui.End).BorderWidth(0, 0, 1, 0).BorderColor(pal().BorderSubtle).Children(func() {
				ui.Column(c).Grow(1).Children(func() { a.ghRepoBox(c, true) })
				a.ghVersionSelect(c)
			})
			ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
				a.ghReadme(c)
				ui.Scroll(c).Width(392).Padding(theme.Space20, theme.Space24).Gap(theme.Space16).AlignItems(ui.Stretch).
					Background(pal().Surface).BorderWidth(0, 0, 0, 1).BorderColor(pal().BorderSubtle).Children(func() {
					a.ghStatus(c)
					a.ghFound(c)
				})
			})
		}, footer)
	}
	if !g.open {
		g.discard()
	}
}

// ghRepoBox is the repository box, with Find.
func (a *App) ghRepoBox(c *ui.Context, compact bool) {
	g := &a.ghi
	labeled(c, locales.SettingsRepositoryLabel(), func() {
		ui.Row(c).Gap(theme.Space8).Children(func() {
			in := ui.TextInput(c, &g.text).Label(locales.SettingsRepositoryLabel()).Placeholder("owner/repo").Grow(1)
			if !compact {
				in.AutoFocus()
			}
			if in.Submitted() {
				a.ghFind()
			}
			if widgets.Button(c, widgets.Secondary, nil, locales.SettingsFind()).Disabled(strings.TrimSpace(g.text) == "" || g.finding).Clicked() {
				a.ghFind()
			}
		})
		if !compact {
			ui.Text(c, locales.SettingsRepoHint()).FontSize(12).TextColor(pal().TextTertiary)
		}
	})
}

// ghStatus says the repository is being looked at, or what went wrong.
func (a *App) ghStatus(c *ui.Context) {
	g := &a.ghi
	switch {
	case g.finding:
		ui.Row(c).Gap(theme.Space8).AlignItems(ui.Center).Children(func() {
			ui.Spinner(c).Size(14, 14).Label(locales.SettingsFindingLabel())
			ui.Text(c, locales.SettingsLookingAt(g.repo.String())).FontSize(13).TextColor(pal().TextSecondary)
		})
	case g.err != "":
		ui.Row(c).Padding(theme.Space10, theme.Space12).Gap(theme.Space10).Radius(theme.RadiusMD).Background(pal().Danger.BG).AlignItems(ui.Start).Children(func() {
			ui.Icon(c, icons.Alert).FontSize(14).TextColor(pal().Danger.FG)
			ui.Text(c, g.err).FontSize(12).TextColor(pal().Danger.FG).Grow(1)
		})
	}
}

// ghVersionSelect picks a release or a branch.
func (a *App) ghVersionSelect(c *ui.Context) {
	g := &a.ghi
	label := locales.SettingsLatestRelease()
	if latest, ok := plugin.Latest(g.releases); !ok || g.tag != latest.Tag {
		label = g.tag
	}
	if g.branch != "" {
		label = locales.SettingsBranchLabel(g.branch)
	}
	b := ui.ButtonBase(c).Width(200).Height(theme.ControlM).Padding(0, theme.Space8, 0, theme.Space10).Gap(theme.Space6).AlignItems(ui.Center).
		Radius(theme.RadiusSM).Background(pal().Surface).Border(1, pal().Border).Label(locales.SettingsVersionLabel(label))
	b.PopoverMenuButton(func(m *ui.Menu) {
		m.MaxWidth(300)
		latest, _ := plugin.Latest(g.releases)
		for i, r := range g.releases {
			if i >= 8 {
				break
			}
			title := r.Tag
			if r.Tag == latest.Tag {
				title = locales.SettingsLatestRelease()
			}
			sub := locales.SettingsReleaseSub(r.Tag, ago(r.Published))
			if r.Prerelease {
				sub = locales.SettingsReleaseSubPre(r.Tag, ago(r.Published))
			}
			if m.Item(title).ID("tag:" + r.Tag).Subtitle(sub).Icon(icons.Menu(icons.Tag)).Checked(g.branch == "" && g.tag == r.Tag).Chosen() {
				a.ghChoose(r.Tag, "")
			}
		}
		if len(g.branches) > 0 && len(g.releases) > 0 {
			m.Separator()
		}
		for i, br := range g.branches {
			if i >= 8 {
				break
			}
			if m.Item(br.Name).ID("branch:" + br.Name).Trailing(plugin.ShortSHA(br.Commit)).Icon(icons.Menu(icons.GitBranch)).Checked(g.branch == br.Name).Chosen() {
				a.ghChoose("", br.Name)
			}
		}
	})
	b.Children(func() {
		ui.Text(c, label).FontSize(13).SingleLine().Grow(1)
		ui.Icon(c, icons.ChevronDown).FontSize(14).TextColor(pal().IconMuted)
	})
}

// ghReadme is the repository's README.md, rendered, at the version
// chosen.
func (a *App) ghReadme(c *ui.Context) {
	g := &a.ghi
	ui.Column(c).Grow(1).MinWidth(0).AlignItems(ui.Stretch).Children(func() {
		ui.Row(c).Padding(theme.Space12, theme.Space16, theme.Space4, theme.Space24).Gap(theme.Space6).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icons.File).FontSize(14).TextColor(pal().IconMuted)
			ui.Text(c, "README.md").FontSize(12).Font(theme.FontMono).TextColor(pal().TextSecondary).Grow(1)
			if g.repo.Owner != "" {
				if widgets.ButtonSized(c, widgets.Ghost, icons.External, locales.PluginsViewOnGithub(), theme.ControlS).Clicked() {
					openURL(g.repo.URL())
				}
			}
		})
		ui.Scroll(c).Grow(1).MinHeight(0).Padding(theme.Space4, theme.Space32, theme.Space32, theme.Space24).AlignItems(ui.Stretch).Children(func() {
			switch {
			case g.preparing || g.readmeLoading:
				ui.Row(c).Gap(theme.Space8).AlignItems(ui.Center).Children(func() {
					ui.Spinner(c).Size(14, 14).Label(locales.CommonLoading())
					ui.Text(c, locales.CommonLoading()).FontSize(13).TextColor(pal().TextSecondary)
				})
			case strings.TrimSpace(g.readme) == "":
				ui.Text(c, locales.GithubNoReadme()).FontSize(13).TextColor(pal().TextTertiary)
			default:
				a.ghMDOptions()
				markdown.View(c, g.readme, &a.ghMD)
			}
		})
	})
}

// ghMDOptions points the README's links and images at the repository, at
// the version chosen.
func (a *App) ghMDOptions() {
	g := &a.ghi
	ref := g.ref
	if ref == "" {
		ref = "HEAD"
	}
	key := g.repo.String() + "@" + ref
	if a.ghMDFor == key {
		return
	}
	a.ghMDFor = key
	repo := g.repo
	blob := func(p string) string {
		return repo.URL() + "/blob/" + ref + "/" + strings.TrimPrefix(path.Clean("/"+p), "/")
	}
	raw := func(p string) string {
		return "https://raw.githubusercontent.com/" + repo.String() + "/" + ref + "/" + strings.TrimPrefix(path.Clean("/"+p), "/")
	}
	a.ghMD = markdown.Options{
		OnLink: func(dest string) {
			switch {
			case strings.HasPrefix(dest, "#"):
				openURL(repo.URL() + "#" + strings.TrimPrefix(dest, "#"))
			case strings.Contains(dest, "://") || strings.HasPrefix(dest, "mailto:"):
				openURL(dest)
			default:
				openURL(blob(dest))
			}
		},
		LoadImage: func(src string) (ui.ImageSource, error) {
			if !strings.Contains(src, "://") {
				src = raw(src)
			}
			return loadWebImage(src)
		},
	}
}

// loadWebImage fetches an image a README shows, through the computer's
// proxy: up to 10 MB of PNG, JPEG, GIF, WebP or SVG.
func loadWebImage(src string) (ui.ImageSource, error) {
	u, err := url.Parse(src)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, http.ErrNotSupported
	}
	client := &http.Client{Transport: plugin.SystemTransport(), Timeout: 30 * time.Second}
	resp, err := client.Get(u.String())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, http.ErrMissingFile
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, err
	}
	if strings.Contains(resp.Header.Get("Content-Type"), "svg") || strings.HasSuffix(strings.ToLower(u.Path), ".svg") {
		return ui.ParseSVG(data)
	}
	return ui.DecodeImage(data)
}

// ghFound is what the version chosen is: the plugin, what it will be able
// to do (its tools first) and whether to keep it up to date.
func (a *App) ghFound(c *ui.Context) {
	g := &a.ghi
	ui.Row(c).Padding(theme.Space12).Gap(theme.Space12).Radius(theme.RadiusMD).Background(pal().Subtle).AlignItems(ui.Center).Children(func() {
		switch {
		case g.preparing:
			ui.Spinner(c).Size(16, 16).Label(locales.SettingsDownloadingLabel())
			ui.Text(c, locales.SettingsDownloading()).FontSize(13).TextColor(pal().TextSecondary).Grow(1)
		case g.manifest != nil:
			pluginTile(c, g.manifest, 32)
			ui.Column(c).Grow(1).MinWidth(0).Gap(theme.Space2).Children(func() {
				ui.Text(c, g.manifest.Name+" "+g.manifest.Version).FontSize(13).FontWeight(500)
				by := g.detail
				if g.manifest.Author != "" {
					by = locales.SettingsByAuthor(g.manifest.Author, by)
				}
				ui.Text(c, by).FontSize(12).TextColor(pal().TextSecondary).MaxLines(2)
			})
		default:
			ui.Text(c, locales.GithubNotAPlugin()).FontSize(13).TextColor(pal().TextSecondary)
		}
	})
	if g.branch != "" {
		ui.Row(c).Padding(theme.Space10, theme.Space12).Gap(theme.Space10).Radius(theme.RadiusMD).Background(pal().Warning.BG).AlignItems(ui.Start).Children(func() {
			ui.Icon(c, icons.Alert).FontSize(14).TextColor(pal().Warning.FG)
			ui.Text(c, locales.SettingsBranchWarning()).FontSize(12).TextColor(pal().Warning.FG).Grow(1)
		})
	}
	m := g.manifest
	if m == nil {
		return
	}
	ui.Column(c).Gap(theme.Space8).AlignItems(ui.Stretch).Children(func() {
		widgets.Overline(c, locales.SettingsAbleTo())
		a.toolPermissions(c, m, nil)
		for _, h := range m.Permissions.Hosts {
			permissionItem(c.Key(h), icons.Globe, locales.SettingsConnectTo(h), "", false)
		}
		if m.Permissions.LocalNetwork {
			permissionItem(c, icons.Network, locales.SettingsConnectLocal(), locales.SettingsConnectLocalNote(), true)
		}
		if m.IsFileType() {
			permissionItem(c, icons.File, locales.PluginsBookFilesAccess(), locales.PluginsBookFilesAccessDetail(), false)
		}
		libraryPermission(c, m, true, nil)
		if len(m.Permissions.Hosts) == 0 && !m.Permissions.LocalNetwork && len(m.Tools) == 0 && m.Permissions.Library == "" && !m.IsFileType() {
			ui.Text(c, locales.SettingsNothing()).FontSize(12).TextColor(pal().TextTertiary)
		}
	})
	ui.Spacer(c)
	keepLabel := locales.SettingsKeepUpToDate()
	if g.branch != "" {
		keepLabel = locales.SettingsFollowCommits(g.branch)
	}
	ui.Checkbox(c, &g.keep, keepLabel)
}
