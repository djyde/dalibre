// Package widgets holds the components of the Figma library, built on the
// toolkit's bases so they keep focus, keyboard activation and
// accessibility.
package widgets

import (
	"strings"
	"unicode"

	"github.com/egoist/mygo/ui"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/app/icons"
	"dalibre/internal/app/theme"
)

type Variant int

const (
	Secondary Variant = iota
	Primary
	Ghost
	Danger
)

// Button is the Button component: a label with an optional leading icon.
func Button(c *ui.Context, v Variant, icon *ui.SVG, label string) ui.Element {
	return ButtonSized(c, v, icon, label, theme.ControlM)
}

func ButtonSized(c *ui.Context, v Variant, icon *ui.SVG, label string, height float32) ui.Element {
	return button(c, v, icon, label, height, nil)
}

// MenuButton is a Button with a chevron after its label, for a button
// that opens a menu.
func MenuButton(c *ui.Context, v Variant, icon *ui.SVG, label string) ui.Element {
	return button(c, v, icon, label, theme.ControlM, icons.ChevronDown)
}

func button(c *ui.Context, v Variant, icon *ui.SVG, label string, height float32, trailing *ui.SVG) ui.Element {
	b := ui.ButtonBase(c).Height(height).Radius(theme.RadiusSM).Gap(theme.Space6).AlignItems(ui.Center).Justify(ui.Center).Label(label)
	pad := float32(theme.Space12)
	if height < theme.ControlM {
		pad = theme.Space10
	}
	b.Padding(0, pad)
	if icon != nil {
		b.PaddingStart(pad - 2)
	}
	var fg, bg ui.Color
	switch v {
	case Primary:
		fg, bg = pal().TextOnAccent, pal().Accent
		if b.Hovered() {
			bg = pal().AccentHover
		}
		b.Shadow(0, 1, 2, 0, ui.RGBA(26, 23, 18, 0.08))
	case Danger:
		fg, bg = pal().TextOnAccent, pal().DangerSolid
		if b.Hovered() {
			bg = pal().DangerSolid.Mix(ui.RGB(0, 0, 0), 0.1)
		}
	case Ghost:
		fg = pal().Text
		if b.Hovered() {
			bg = pal().Hover
		}
	default:
		fg, bg = pal().Text, pal().Surface
		border := pal().Border
		if b.Hovered() {
			bg, border = pal().Hover, pal().BorderStrong
		}
		b.Border(1, border).Shadow(0, 1, 2, 0, ui.RGBA(26, 23, 18, 0.05))
	}
	if b.Pressed() {
		b.Opacity(0.88)
	}
	b.Background(bg).TextColor(fg).Children(func() {
		if icon != nil {
			ui.Icon(c, icon).FontSize(16)
		}
		if label != "" {
			fs := float32(13)
			if height < theme.ControlM {
				fs = 12
			}
			ui.Text(c, label).FontSize(fs).FontWeight(500).SingleLine()
		}
		if trailing != nil {
			ui.Icon(c, trailing).FontSize(14)
		}
	})
	return b
}

// IconButton is the IconButton component, with a tooltip as its name.
func IconButton(c *ui.Context, icon *ui.SVG, label string, active bool) ui.Element {
	b := ui.ButtonBase(c).Size(theme.ControlM, theme.ControlM).Radius(theme.RadiusSM).Center().Label(label).Tooltip(label)
	fg := pal().Icon
	switch {
	case active:
		b.Background(pal().Selected)
		fg = pal().TextAccent
	case b.Hovered():
		b.Background(pal().Hover)
		fg = pal().Text
	}
	b.TextColor(fg).Children(func() { ui.Icon(c, icon).FontSize(16) })
	return b
}

// FormatBadge is the FormatBadge component.
func FormatBadge(c *ui.Context, format string, small bool) ui.Element {
	col := pal().Format(format)
	e := ui.Row(c).Radius(theme.RadiusXS).Background(col.BG)
	fs := float32(10)
	if small {
		e.Padding(theme.Space2, theme.Space4)
		fs = 9
	} else {
		e.Padding(theme.Space2+1, theme.Space6)
	}
	return e.Children(func() {
		ui.Text(c, format).Font(theme.FontMono).FontWeight(500).FontSize(fs).LetterSpacing(0.4).TextColor(col.FG)
	})
}

// Tag is the Tag component. It reports a click on the chip, and onRemove
// adds a remove Button.
func Tag(c *ui.Context, label string, selected bool, onRemove func()) ui.Element {
	// A long tag stays inside its row and ends in an ellipsis.
	e := ui.ButtonBase(c).Height(24).MaxWidthPercent(100).MinWidth(0).Radius(theme.RadiusFull).AlignItems(ui.Center).Gap(theme.Space4).Label(label)
	e.Padding(0, theme.Space8)
	if onRemove != nil {
		e.PaddingEnd(theme.Space4)
	}
	bg, fg := pal().Subtle, pal().TextSecondary
	switch {
	case selected:
		bg, fg = pal().AccentSubtle, pal().TextAccent
	case e.Hovered():
		bg = pal().Hover
	}
	return e.Background(bg).TextColor(fg).Children(func() {
		ui.Text(c, label).FontSize(12).FontWeight(500).SingleLine().Shrink(1).MinWidth(0)
		if onRemove != nil {
			x := ui.ButtonBase(c).Size(16, 16).Radius(theme.RadiusFull).Center().Label(locales.WidgetsRemoveTag(label))
			if x.Hovered() {
				x.Background(fg.Alpha(0.15))
			}
			x.OnClick(onRemove).Children(func() { ui.Icon(c, icons.X).FontSize(11) })
		}
	})
}

// Stars shows a calibre rating (0–10) as five Stars.
func Stars(c *ui.Context, rating int, size float32) ui.Element {
	return ui.Row(c).Gap(theme.Space2).Label(ratingLabel(rating)).Children(func() {
		for i := 1; i <= 5; i++ {
			if rating >= i*2 {
				ui.Icon(c, icons.Star).FontSize(size).TextColor(pal().WarningSolid)
			} else if rating == i*2-1 {
				ui.Icon(c, icons.Star).FontSize(size).TextColor(pal().WarningSolid.Alpha(0.5))
			} else {
				ui.Icon(c, icons.StarOutline).FontSize(size).TextColor(pal().BorderStrong)
			}
		}
	})
}

func ratingLabel(r int) string {
	if r == 0 {
		return locales.WidgetsNotRated()
	}
	return locales.WidgetsStars(r / 2)
}

// Overline is a small uppercase section heading.
func Overline(c *ui.Context, s string) ui.Element {
	return ui.Text(c, s).FontSize(11).FontWeight(600).LetterSpacing(0.6).TextColor(pal().TextTertiary)
}

// Hairline is a divider across its column.
func Hairline(c *ui.Context) ui.Element {
	return ui.Box(c).Height(1).Background(pal().BorderSubtle)
}

// Card is a raised surface, as the Menu and the dialogs.
func Card(c *ui.Context) ui.Element {
	return ui.Column(c).Background(pal().SurfaceRaised).Border(1, pal().BorderSubtle).Radius(theme.RadiusLG).
		Shadow(0, 2, 6, 0, ui.RGBA(26, 23, 18, 0.08)).Shadow(0, 24, 48, -8, ui.RGBA(26, 23, 18, 0.18))
}

func pal() *theme.Palette { return theme.P() }

// DialogFrame is a dialog in the design's look: a dimmed window, a raised
// panel of the given width, a header with a close button, and a footer.
func DialogFrame(c *ui.Context, open *bool, width float32, header func(), body func(), footer func()) {
	dialogFrame(c, open, width, false, header, body, footer)
}

// CompactDialogFrame is DialogFrame with a slim header and footer, for a
// window whose body carries its own layout (Settings).
func CompactDialogFrame(c *ui.Context, open *bool, width float32, header func(), body func(), footer func()) {
	dialogFrame(c, open, width, true, header, body, footer)
}

// TallDialogFrame is a dialog as tall as height, or as the window allows,
// whose body fills it without scrolling: its panes scroll themselves.
func TallDialogFrame(c *ui.Context, open *bool, width, height float32, header func(), body func(), footer func()) {
	tallDialogFrame(c, open, width, height, header, body, footer)
}

func dialogFrame(c *ui.Context, open *bool, width float32, compact bool, header func(), body func(), footer func()) {
	frame(c, open, width, 0, compact, header, body, footer)
}

func tallDialogFrame(c *ui.Context, open *bool, width, height float32, header func(), body func(), footer func()) {
	frame(c, open, width, height, false, header, body, footer)
}

// frame draws a dialog: with height 0, as tall as its content up to the
// window, its body scrolling; else that tall, its body filling it.
func frame(c *ui.Context, open *bool, width, height float32, compact bool, header func(), body func(), footer func()) {
	headPad := [4]float32{theme.Space16, theme.Space16, theme.Space16, theme.Space24}
	footV, footH := float32(theme.Space16), float32(theme.Space24)
	if compact {
		headPad = [4]float32{theme.Space6, theme.Space8, theme.Space6, theme.Space16}
		footV, footH = theme.Space8, theme.Space16
	}
	// The panel stays below the window's caption buttons: in a short window
	// it would otherwise grow up under them (Windows' sit on the right).
	bar := c.TitleBar()
	ui.DialogBase(c, open, func(backdrop, panel ui.Element) {
		backdrop.Background(pal().Scrim).Padding(bar.Height, 0)
		panel.Width(width).MaxHeight(ui.Auto)
		if height > 0 {
			panel.Height(height).MaxHeightPercent(94).MaxWidthPercent(94)
		}
		panel.Background(pal().SurfaceRaised).Border(1, pal().BorderSubtle).Radius(theme.RadiusXL).Clip().
			Shadow(0, 2, 6, 0, ui.RGBA(26, 23, 18, 0.08)).Shadow(0, 24, 48, -8, ui.RGBA(26, 23, 18, 0.24)).AlignItems(ui.Stretch)
		panel.Children(func() {
			ui.Row(c).Padding(headPad[0], headPad[1], headPad[2], headPad[3]).Gap(theme.Space12).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(pal().BorderSubtle).Children(func() {
				header()
				if IconButton(c, icons.X, locales.WidgetsClose(), false).Clicked() {
					*open = false
				}
			})
			if height > 0 {
				ui.Column(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(body)
			} else {
				ui.Scroll(c).Grow(1).MinHeight(0).Children(body)
			}
			ui.Row(c).Padding(footV, footH).Gap(theme.Space8).AlignItems(ui.Center).Background(pal().Chrome).BorderWidth(1, 0, 0, 0).BorderColor(pal().BorderSubtle).Children(footer)
		})
	})
}

// Field is a labeled control, with a hint at the label's end.
func Field(c *ui.Context, label, hint string, control func()) ui.Element {
	return ui.Column(c.Key(label)).Grow(1).Basis(0).MinWidth(0).Gap(theme.Space6).AlignItems(ui.Stretch).Children(func() {
		ui.Row(c).Children(func() {
			ui.Text(c, label).FontSize(12).FontWeight(500).TextColor(pal().TextSecondary).Grow(1)
			if hint != "" {
				ui.Text(c, hint).FontSize(12).TextColor(pal().TextTertiary)
			}
		})
		control()
	})
}

// FormatOption is the FormatOption component: a radio card choosing an
// output format.
func FormatOption(c *ui.Context, name, title, description string, selected bool) ui.Element {
	e := ui.ButtonBase(c).Grow(1).Basis(0).Padding(theme.Space16).Radius(theme.RadiusMD).AlignItems(ui.Stretch).
		Role(ui.RoleRadio).Label(name + ", " + title)
	switch {
	case selected:
		e.Background(pal().Selected).Border(1.5, pal().BorderSelected)
	case e.Hovered():
		e.Background(pal().Hover).Border(1, pal().BorderStrong)
	default:
		e.Background(pal().Surface).Border(1, pal().Border)
	}
	return e.Children(func() {
		ui.Column(c).Grow(1).Gap(theme.Space12).AlignItems(ui.Stretch).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Children(func() {
				FormatBadge(c, name, false)
				ui.Spacer(c)
				r := ui.Box(c).Size(16, 16).Radius(theme.RadiusFull).Center()
				if selected {
					r.Background(pal().Accent).Children(func() {
						ui.Box(c).Size(6, 6).Radius(theme.RadiusFull).Background(pal().TextOnAccent)
					})
				} else {
					r.Background(pal().Surface).Border(1, pal().BorderStrong)
				}
			})
			ui.Column(c).Gap(theme.Space4).Children(func() {
				ui.Text(c, title).FontSize(13).FontWeight(500)
				ui.Text(c, description).FontSize(12).TextColor(pal().TextSecondary)
			})
		})
	})
}

// IndicatorButton is the SharingIndicator component: an IconButton with a
// dot of color at its top right when dot is true.
func IndicatorButton(c *ui.Context, icon *ui.SVG, label string, active, dot bool, color ui.Color) ui.Element {
	b := ui.ButtonBase(c).Size(theme.ControlM, theme.ControlM).Radius(theme.RadiusSM).Center().Label(label).Tooltip(label)
	fg := pal().Icon
	switch {
	case active:
		b.Background(pal().Selected)
		fg = pal().TextAccent
	case b.Hovered():
		b.Background(pal().Hover)
		fg = pal().Text
	}
	b.TextColor(fg).Children(func() {
		ui.Icon(c, icon).FontSize(16)
		if dot {
			ui.Box(c).Absolute().Top(5).Right(4).Size(8, 8).Radius(4).Background(color).Border(1.5, pal().BgApp).PassThrough()
		}
	})
	return b
}

// AgentAvatar is the AgentAvatar component: an agent's own icon when
// there is one, else the initials of its name on a color from it.
func AgentAvatar(c *ui.Context, name string, icon ui.ImageSource, size float32) ui.Element {
	box := ui.Box(c).Size(size, size).Radius(size * 0.28).Center().Clip()
	if icon != nil {
		return box.Children(func() { ui.Image(c, icon).Size(size, size).Fit(ui.Contain) })
	}
	in := initials(name)
	fs := size * 0.55
	if len([]rune(in)) > 1 {
		fs = size * 0.42
	}
	return box.Background(avatarColor(name)).Children(func() {
		ui.Text(c, in).FontSize(fs).FontWeight(600).TextColor(ui.RGB(255, 255, 255)).SingleLine()
	})
}

// initials are the first letters of name's first two words, as "CC" for
// Claude Code, or its first letter.
func initials(name string) string {
	var out []rune
	for _, w := range strings.FieldsFunc(name, func(r rune) bool { return r == ' ' || r == '-' || r == '_' || r == '.' }) {
		r := []rune(w)
		out = append(out, unicode.ToUpper(r[0]))
		if len(out) == 2 {
			break
		}
	}
	if len(out) == 0 {
		return "?"
	}
	return string(out)
}

// avatarColors are the colors AgentAvatar picks from, dark enough for
// white initials.
var avatarColors = []ui.Color{
	ui.RGB(204, 120, 82), ui.RGB(140, 107, 191), ui.RGB(56, 61, 69), ui.RGB(46, 125, 110),
	ui.RGB(52, 101, 164), ui.RGB(176, 74, 94), ui.RGB(130, 110, 52),
}

func avatarColor(name string) ui.Color {
	var h uint32 = 2166136261
	for _, b := range []byte(strings.ToLower(name)) {
		h = (h ^ uint32(b)) * 16777619
	}
	return avatarColors[h%uint32(len(avatarColors))]
}

// AgentButton is the AgentIndicator while agents work: their avatars,
// label and a live dot.
func AgentButton(c *ui.Context, name, label string, open bool, avatars func()) ui.Element {
	b := ui.ButtonBase(c).Height(theme.ControlM).Padding(0, theme.Space10, 0, theme.Space6).Gap(theme.Space6).Radius(theme.RadiusSM).AlignItems(ui.Center).Label(name).Tooltip(name)
	bg := pal().Subtle
	switch {
	case open:
		bg = pal().Selected
	case b.Hovered():
		bg = pal().Hover
	}
	b.Background(bg).Children(func() {
		ui.Row(c).Gap(-6).AlignItems(ui.Center).Children(avatars)
		ui.Text(c, label).FontSize(12).FontWeight(500).TextColor(pal().Text).SingleLine()
		ui.Box(c).Size(14, 14).Radius(7).Background(pal().Success.FG.Alpha(0.25)).Center().Children(func() {
			ui.Box(c).Size(8, 8).Radius(4).Background(pal().Success.FG)
		})
	})
	return b
}
