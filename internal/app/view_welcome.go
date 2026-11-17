package app

import (
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/app/icons"
	"dalibre/internal/app/theme"
	"dalibre/internal/app/widgets"
)

// The wall's covers, as the CoverWall component: BookCover's size, tilted.
const (
	wallCoverW, wallCoverH = 132, 196
	wallGap                = 20
	wallTilt               = 14 // degrees
	wallDim                = 0.12
	wallShift              = 300 // the wall sits right of center, behind the fade
)

// wallStagger offsets the wall's columns, so that their covers don't line up.
var wallStagger = []float32{0, 110, 40, 150, 70}

// The welcome screen is dark whatever the theme, as a poster: white type
// on black, covers fading in from the right.
func onDark(a float32) ui.Color { return ui.RGBA(255, 255, 255, a) }

var (
	darkAmber = ui.Hex("#FFC53D")
	darkRed   = ui.Hex("#FF9592")
)

// welcomeScreen fills the window while no library is open (Figma:
// 09 · Onboarding, Welcome v2): a wall of covers fading in from the right
// and, on the left, the way in.
func (a *App) welcomeScreen(c *ui.Context) {
	bar := c.TitleBar()
	top := max(bar.Height, theme.Space32)
	ui.Box(c).Fill().Background(ui.RGBA(0, 0, 0, 1)).TextColor(onDark(1)).Children(func() {
		a.coverWall(c)
		ui.Box(c).Absolute().Left(0).Top(0).Right(0).Bottom(0).PassThrough().LinearGradient(ui.LinearGradient{
			Angle: 90,
			Stops: []ui.GradientStop{
				{Color: ui.RGBA(0, 0, 0, 1), At: 0}, {Color: ui.RGBA(0, 0, 0, 0.97), At: 0.4},
				{Color: ui.RGBA(0, 0, 0, 0.6), At: 0.6}, {Color: ui.RGBA(0, 0, 0, 0.1), At: 0.82}, {Color: ui.Transparent, At: 1},
			},
		})
		ui.Box(c).Absolute().Left(0).Right(0).Bottom(0).Height(260).PassThrough().LinearGradient(ui.LinearGradient{
			Angle: 180,
			Stops: []ui.GradientStop{{Color: ui.Transparent, At: 0}, {Color: ui.RGBA(0, 0, 0, 0.75), At: 1}},
		})
		ui.Column(c).Absolute().Left(120).Top(top).Bottom(96).Width(560).Justify(ui.Center).AlignItems(ui.Stretch).Children(func() {
			if a.loading {
				a.openingLibrary(c)
			} else {
				a.welcomeContent(c)
			}
		})
		ui.Row(c).Absolute().Left(120).Bottom(theme.Space24).Gap(theme.Space20).AlignItems(ui.Center).Children(func() {
			a.welcomeLanguage(c)
			ui.Text(c, locales.WelcomeShortcutHint()).FontSize(11).TextColor(onDark(0.32))
		})
		// A place to drag the window by.
		ui.Box(c).Absolute().Left(0).Top(0).Right(0).Height(top).DragWindow()
	})
}

// coverWall draws the covers, tilted, across the window.
func (a *App) coverWall(c *ui.Context) {
	a.loadWelcomeCovers()
	w := &a.welcome
	ui.Box(c).Absolute().Left(0).Top(0).Right(0).Bottom(0).Clip().PassThrough().AccessHidden(true).Bind(&w.box).Children(func() {
		r := w.box.Bounds(c)
		W, H := r.W, r.H
		if W == 0 || H == 0 {
			W, H = 1440, 900
		}
		// The tilted wall must still cover the window's corners.
		s, co := math.Sincos(wallTilt * math.Pi / 180)
		needW := W*float32(co) + H*float32(s) + 2*wallCoverW
		needH := W*float32(s) + H*float32(co) + 2*wallCoverH
		cols := int(math.Ceil(float64(needW / (wallCoverW + wallGap))))
		rows := int(math.Ceil(float64(needH/(wallCoverH+wallGap)))) + 1
		wallW := float32(cols)*(wallCoverW+wallGap) - wallGap
		wallH := float32(rows)*(wallCoverH+wallGap) - wallGap
		n := len(welcomeShelf)
		ui.Row(c).Absolute().Left((W-wallW)/2 + wallShift).Top((H-wallH)/2 - wallStagger[len(wallStagger)-2]).Gap(wallGap).AlignItems(ui.Start).
			Rotate(wallTilt).Cache().Children(func() {
			for col := range cols {
				ui.Column(c.Key(col)).Gap(wallGap).Padding(wallStagger[col%len(wallStagger)], 0, 0, 0).Children(func() {
					for row := range rows {
						i := (col*7 + row*11 + (col%3)*5) % n
						coverOf(c.Key(row), welcomeShelf[i], w.bitmaps[i], wallCoverW, wallCoverH, false)
					}
				})
			}
		})
		ui.Box(c).Absolute().Left(0).Top(0).Right(0).Bottom(0).Background(ui.RGBA(0, 0, 0, wallDim))
	})
}

func (a *App) welcomeContent(c *ui.Context) {
	ui.Row(c).Gap(theme.Space12).AlignItems(ui.Center).Children(func() {
		ui.Box(c).Size(36, 36).Radius(9).Background(onDark(1)).Center().Children(func() {
			ui.Text(c, "D").FontWeight(700).FontSize(18).TextColor(ui.RGBA(0, 0, 0, 1))
		})
		ui.Text(c, "Dalibre").FontWeight(600).FontSize(17).LetterSpacing(-0.2)
	})
	headline := locales.WelcomeHeadline()
	size := headlineSize(headline, 560)
	ui.Text(c, headline).FontWeight(600).FontSize(size).FixedLineHeight(size*64/60).LetterSpacing(-2.2*size/60).Margin(56, 0, 0, 0)
	sub := locales.WelcomeSubtitleFirstRun()
	if len(a.settings.Recent) > 0 {
		sub = locales.WelcomeSubtitleReturning()
	}
	ui.Text(c, sub).FontSize(17).FixedLineHeight(26).TextColor(onDark(0.62)).Margin(theme.Space20, 0, 0, 0)

	if a.loadErr != nil {
		ui.Row(c).Margin(theme.Space24, 0, 0, 0).Padding(theme.Space12, theme.Space16).Gap(theme.Space10).Radius(10).
			Background(ui.RGBA(229, 72, 77, 0.14)).Border(1, ui.RGBA(229, 72, 77, 0.3)).AlignItems(ui.Start).Children(func() {
			ui.Icon(c, icons.Alert).FontSize(16).TextColor(darkRed)
			ui.Column(c).Grow(1).MinWidth(0).Gap(theme.Space2).Children(func() {
				ui.Text(c, a.loadErr()).FontSize(13).FontWeight(500).TextColor(darkRed)
				if a.loadErrDetail != nil {
					ui.Text(c, a.loadErrDetail()).FontSize(12).TextColor(darkRed.Alpha(0.85))
				}
			})
		})
	}

	ui.Row(c).Gap(theme.Space12).Margin(36, 0, 0, 0).Children(func() {
		if darkButton(c, icons.Folder, locales.WelcomeOpenLibrary(), true).Clicked() {
			a.chooseLibrary()
		}
		if darkButton(c, icons.Plus, locales.WelcomeCreateLibrary(), false).Clicked() {
			a.openCreateSheet()
		}
	})

	w := &a.welcome
	switch {
	case len(a.settings.Recent) > 0:
		a.checkRecent()
		libraryList(c, locales.WelcomeRecent(), func() {
			for _, r := range a.settings.Recent {
				a.libraryRow(c, r, "", w.missing[r], false)
			}
		})
	case w.found != "":
		libraryList(c, locales.WelcomeFoundOnThisMac(), func() {
			a.libraryRow(c, w.found, "", false, true)
		})
	}
}

// darkButton is a button on the welcome screen's dark: white, or glass.
func darkButton(c *ui.Context, icon *ui.SVG, label string, primary bool) ui.Element {
	b := ui.ButtonBase(c.Key(label)).Height(44).Padding(0, 20, 0, 18).Gap(theme.Space8).Radius(10).AlignItems(ui.Center).Label(label)
	hover, fg := b.Hovered(), ui.RGBA(0, 0, 0, 1)
	switch {
	case primary && hover:
		b.Background(onDark(0.88))
	case primary:
		b.Background(onDark(1))
	default:
		fg = onDark(1)
		b.Background(onDark(map[bool]float32{true: 0.16, false: 0.1}[hover])).Border(1, onDark(0.16))
	}
	return b.Children(func() {
		ui.Icon(c, icon).FontSize(16).TextColor(fg)
		ui.Text(c, label).FontSize(14).FontWeight(600).TextColor(fg)
	})
}

// libraryList is a titled list of libraries under the buttons.
func libraryList(c *ui.Context, title string, rows func()) {
	ui.Column(c).Margin(theme.Space48, 0, 0, 0).Gap(theme.Space4).AlignItems(ui.Stretch).Children(func() {
		ui.Text(c, strings.ToUpper(title)).FontSize(11).FontWeight(600).LetterSpacing(1.2).TextColor(onDark(0.42)).Margin(0, 0, theme.Space6, 0)
		rows()
	})
}

// libraryRow is the LibraryRow component, on the dark: a library to open,
// or one whose folder is gone, which can be taken off the list.
func (a *App) libraryRow(c *ui.Context, root, meta string, missing, withOpen bool) {
	name := filepath.Base(root)
	row := ui.ButtonBase(c.Key(root)).Padding(theme.Space10, theme.Space12).Gap(14).Radius(10).AlignItems(ui.Center).Label(name)
	hover := row.Hovered()
	if hover || withOpen {
		row.Background(onDark(0.07))
	}
	row.OnClick(func() {
		if missing {
			a.loadErr = func() string { return locales.WelcomeCouldntOpen(name) }
			a.loadErrDetail = func() string { return locales.WelcomeLibraryMissingDetail(abbreviateHome(root)) }
			return
		}
		a.openLibrary(root)
	}).Children(func() {
		ui.Box(c).Size(32, 32).Radius(8).Background(onDark(0.08)).Center().Children(func() {
			if missing {
				ui.Icon(c, icons.Alert).FontSize(16).TextColor(darkAmber)
			} else {
				ui.Icon(c, icons.Database).FontSize(16).TextColor(onDark(0.7))
			}
		})
		ui.Column(c).Grow(1).MinWidth(0).Gap(theme.Space2).Children(func() {
			ui.Text(c, name).FontSize(14).FontWeight(500).SingleLine().TextColor(onDark(map[bool]float32{true: 0.45, false: 1}[missing]))
			ui.Text(c, abbreviateHome(root)).Font(theme.FontMono).FontSize(12).TextColor(onDark(0.4)).SingleLine()
		})
		switch {
		case missing && hover:
			x := ui.ButtonBase(c).Size(28, 28).Radius(7).Center().Label(locales.WelcomeRemoveFromRecentLabel(name)).Tooltip(locales.WelcomeRemoveFromRecent())
			if x.Hovered() {
				x.Background(onDark(0.12))
			}
			x.OnClick(func() { a.forgetRecent(root) }).Children(func() {
				ui.Icon(c, icons.X).FontSize(14).TextColor(onDark(0.8))
			})
		case missing:
			ui.Text(c, locales.WelcomeNotFound()).FontSize(12).TextColor(darkAmber)
		case meta != "":
			ui.Text(c, meta).FontSize(12).TextColor(onDark(0.45))
		}
		switch {
		case withOpen:
			b := ui.ButtonBase(c).Height(28).Padding(0, 12).Radius(7).Center().Label(locales.CommonOpen()).Background(onDark(1))
			if b.Hovered() {
				b.Background(onDark(0.88))
			}
			b.OnClick(func() { a.openLibrary(root) }).Children(func() {
				ui.Text(c, locales.CommonOpen()).FontSize(13).FontWeight(600).TextColor(ui.RGBA(0, 0, 0, 1))
			})
		case hover && !missing:
			ui.Icon(c, icons.ArrowRight).FontSize(16).TextColor(onDark(0.8))
		}
	})
}

// openingLibrary is the welcome screen while a library opens.
func (a *App) openingLibrary(c *ui.Context) {
	ui.Spinner(c).Size(28, 28).Label(locales.WelcomeOpeningLabel())
	ui.Text(c, locales.WelcomeOpening(filepath.Base(a.loadingRoot))).FontWeight(600).FontSize(44).FixedLineHeight(50).LetterSpacing(-1.4).Margin(theme.Space24, 0, 0, 0)
	ui.Text(c, abbreviateHome(a.loadingRoot)).Font(theme.FontMono).FontSize(13).TextColor(onDark(0.45)).SingleLine().Margin(theme.Space12, 0, 0, 0)
	ui.Box(c).Width(320).Height(3).Radius(2).Background(onDark(0.12)).Margin(theme.Space24, 0, 0, 0).Children(func() {
		ui.Progress(c, -1).Fill()
	})
	ui.Row(c).Margin(theme.Space24, 0, 0, 0).Children(func() {
		if darkButton(c, icons.X, locales.CommonCancel(), false).Clicked() {
			a.cancelOpen()
		}
	})
}

// ---- Sheets

// createLibrarySheet is the Create a new library sheet: a name and the
// folder it goes in.
func (a *App) createLibrarySheet(c *ui.Context) {
	s := &a.welcome.create
	if !s.open {
		return
	}
	name := strings.TrimSpace(s.name)
	parent := expandHome(strings.TrimSpace(s.parent))
	target := filepath.Join(parent, name)
	validName := name != "" && !strings.ContainsAny(name, `/\:`) && name != "." && name != ".."
	exists := false
	if validName {
		_, err := os.Stat(filepath.Join(target, "metadata.db"))
		exists = err == nil
	}
	widgets.DialogFrame(c, &s.open, 480, func() {
		ui.Column(c).Grow(1).Gap(theme.Space4).Children(func() {
			ui.Text(c, locales.WelcomeCreateLibrary()).FontSize(16).FontWeight(600)
			ui.Text(c, locales.WelcomeCreateIntro()).FontSize(13).TextColor(pal().TextSecondary)
		})
	}, func() {
		ui.Column(c).Padding(theme.Space16, theme.Space24, theme.Space20, theme.Space24).Gap(14).AlignItems(ui.Stretch).Children(func() {
			labeled(c, locales.CommonName(), func() {
				ui.TextInput(c, &s.name).Label(locales.CommonName()).Placeholder(locales.WelcomeDefaultName()).AutoFocus()
				ui.Text(c, locales.WelcomeNameHelp()).FontSize(12).TextColor(pal().TextTertiary)
			})
			labeled(c, locales.WelcomeLocation(), func() {
				ui.Row(c).Gap(theme.Space8).AlignItems(ui.Center).Children(func() {
					ui.TextInput(c, &s.parent).Label(locales.WelcomeLocation()).Grow(1)
					if widgets.Button(c, widgets.Secondary, nil, locales.WelcomeChoose()).Clicked() {
						a.chooseCreateParent()
					}
				})
			})
			bg, fg, ic, title := pal().Subtle, pal().IconMuted, icons.Database, locales.WelcomeWillBeCreatedAt()
			if exists {
				bg, fg, ic, title = pal().Warning.BG, pal().Warning.FG, icons.Alert, locales.WelcomeLibraryExists()
			}
			ui.Row(c).Padding(theme.Space10, theme.Space12).Gap(theme.Space10).Radius(theme.RadiusMD).Background(bg).AlignItems(ui.Start).Children(func() {
				ui.Icon(c, ic).FontSize(16).TextColor(fg)
				ui.Column(c).Grow(1).MinWidth(0).Gap(theme.Space2).Children(func() {
					ui.Text(c, title).FontSize(12).TextColor(map[bool]ui.Color{true: pal().Warning.FG, false: pal().TextTertiary}[exists])
					shown := abbreviateHome(target)
					if !validName {
						shown = locales.WelcomeGiveName()
					}
					ui.Text(c, shown).Font(theme.FontMono).FontSize(12)
				})
			})
		})
	}, func() {
		ui.Spacer(c)
		if widgets.Button(c, widgets.Secondary, nil, locales.CommonCancel()).Clicked() {
			s.open = false
		}
		if exists {
			if widgets.Button(c, widgets.Primary, nil, locales.WelcomeOpenIt()).Clicked() {
				s.open = false
				a.openLibrary(target)
			}
			return
		}
		if widgets.Button(c, widgets.Primary, nil, locales.WelcomeCreateLibraryButton()).Disabled(!validName || parent == "").Clicked() {
			s.open = false
			a.createLibrary(target)
		}
	})
}

// notLibrarySheet asks what to do with a folder that has no library.
func (a *App) notLibrarySheet(c *ui.Context) {
	s := &a.welcome.notLib
	if !s.open {
		return
	}
	widgets.DialogFrame(c, &s.open, 460, func() {
		ui.Row(c).Grow(1).Gap(theme.Space12).AlignItems(ui.Start).Children(func() {
			ui.Box(c).Size(40, 40).Radius(theme.RadiusMD).Background(pal().Warning.BG).Center().Children(func() {
				ui.Icon(c, icons.Folder).FontSize(20).TextColor(pal().Warning.FG)
			})
			ui.Column(c).Grow(1).MinWidth(0).Gap(theme.Space4).Children(func() {
				ui.Text(c, locales.WelcomeNotLibraryTitle(filepath.Base(s.root))).FontSize(16).FontWeight(600)
				ui.Text(c, locales.WelcomeNotLibraryBody()).FontSize(13).TextColor(pal().TextSecondary)
			})
		})
	}, func() {
		ui.Column(c).Padding(theme.Space16, theme.Space24, theme.Space20, theme.Space24).Gap(theme.Space12).AlignItems(ui.Stretch).Children(func() {
			ui.Row(c).Padding(theme.Space10, theme.Space12).Gap(theme.Space10).Radius(theme.RadiusMD).Background(pal().Subtle).AlignItems(ui.Start).Children(func() {
				ui.Icon(c, icons.Folder).FontSize(16).TextColor(pal().IconMuted)
				ui.Column(c).Grow(1).MinWidth(0).Gap(theme.Space2).Children(func() {
					ui.Text(c, locales.WelcomeFolder()).FontSize(12).TextColor(pal().TextTertiary)
					ui.Text(c, abbreviateHome(s.root)).Font(theme.FontMono).FontSize(12)
				})
			})
			if s.files > 0 {
				ui.Row(c).Padding(theme.Space10, theme.Space12).Gap(theme.Space10).Radius(theme.RadiusMD).Background(pal().Warning.BG).AlignItems(ui.Start).Children(func() {
					ui.Icon(c, icons.Alert).FontSize(16).TextColor(pal().Warning.FG)
					ui.Column(c).Grow(1).MinWidth(0).Gap(theme.Space2).Children(func() {
						ui.Text(c, locales.WelcomeFolderNotEmpty()).FontSize(13).FontWeight(500).TextColor(pal().Warning.FG)
						ui.Text(c, locales.WelcomeFolderNotEmptyBody(s.files)).FontSize(12).TextColor(pal().Warning.FG)
					})
				})
			}
		})
	}, func() {
		if widgets.Button(c, widgets.Ghost, nil, locales.WelcomeChooseAnotherFolder()).Clicked() {
			s.open = false
			a.chooseLibrary()
		}
		ui.Spacer(c)
		if widgets.Button(c, widgets.Secondary, nil, locales.CommonCancel()).Clicked() {
			s.open = false
		}
		if widgets.Button(c, widgets.Primary, nil, locales.WelcomeCreateLibraryHere()).Clicked() {
			s.open = false
			a.createLibrary(s.root)
		}
	})
}

// headlineSize is the welcome headline's font size: 60 where its longest
// line fits width, as in English, smaller in languages whose lines are
// longer. Widths are estimated, in ems: a CJK character is one, other
// letters about half of one in Inter Semi Bold, tightened as it is.
func headlineSize(s string, width float32) float32 {
	var longest float32
	for _, line := range strings.Split(s, "\n") {
		var w float32
		for _, r := range line {
			switch {
			case r >= 0x2E80:
				w += 1
			case r == ' ':
				w += 0.25
			default:
				w += 0.52
			}
		}
		longest = max(longest, w)
	}
	if longest == 0 {
		return 60
	}
	return min(60, float32(math.Floor(float64(width/longest))))
}
