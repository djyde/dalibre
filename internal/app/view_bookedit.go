package app

import (
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/app/icons"
	"dalibre/internal/app/theme"
	"dalibre/internal/app/widgets"
	"dalibre/internal/epub"
)

// view builds the editor window (Figma, Screens › 14 · Edit book).
func (ed *bookEditor) view(c *ui.Context) {
	sys := c.Theme()
	c.SetTheme(theme.Use(sys.Dark).Theme(sys))
	c.SetMenuPresentation(ui.PopoverMenus)
	for _, t := range ed.a.editorToasts[ed.book.ID] {
		typ := "success"
		if t.error {
			typ = "error"
		}
		c.AddToast(ui.Toast{Title: t.text, Type: typ})
	}
	delete(ed.a.editorToasts, ed.book.ID)

	c.OnWindowShortcut(ui.Cmd, ui.KeyS, func() { ed.save() })
	glanceOK, editOK := ed.modes()
	if ed.effectiveMode(glanceOK, editOK) == modeGlance && editOK {
		c.OnWindowShortcut(ui.Cmd, ui.KeyF, func() { ed.find() })
	}

	w := ed.a.settings.EditorSidebar
	if w == 0 {
		w = theme.SidebarWidth
	}
	split := ui.Split(c, &w, func() { ed.sidebar(c) }, func() {
		ui.Column(c.Key("main")).Fill().MinWidth(0).AlignItems(ui.Stretch).Children(func() {
			ed.toolbar(c, glanceOK, editOK)
			ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() { ed.body(c, glanceOK, editOK) })
			ed.statusBar(c, glanceOK, editOK)
		})
	}).SplitReset(theme.SidebarWidth).Fill().Background(pal().BgApp).TextColor(pal().Text)
	if split.Changed() {
		ed.a.settings.EditorSidebar = min(max(w, editorSidebarMin), editorSidebarMax)
		ed.saveSettingsSoon()
	}
}

// The editor's sidebar keeps between these widths as it is dragged.
const editorSidebarMin, editorSidebarMax = 200, 420

// saveSettingsSoon saves the sidebar's width once the drag rests, rather
// than on every move.
func (ed *bookEditor) saveSettingsSoon() {
	if ed.savingSettings || ed.win == nil {
		return
	}
	ed.savingSettings = true
	time.AfterFunc(400*time.Millisecond, func() {
		ed.win.Update(func() {
			ed.savingSettings = false
			_ = ed.a.settings.Save()
		})
	})
}

// modes says whether the file shown can show in Glance and in Edit.
func (ed *bookEditor) modes() (glance, edit bool) {
	media := ed.bk.Type(ed.cur)
	if !ed.bk.Packaged() {
		return true, true
	}
	return epub.IsPage(media), epub.IsTextType(media)
}

func (ed *bookEditor) effectiveMode(glanceOK, editOK bool) editorMode {
	switch {
	case ed.mode == modeGlance && !glanceOK && editOK:
		return modeEdit
	case ed.mode == modeEdit && !editOK:
		return modeGlance
	}
	return ed.mode
}

// find opens the editor's find bar on the file shown.
func (ed *bookEditor) find() {
	if _, editOK := ed.modes(); !editOK {
		return
	}
	ed.mode = modeEdit
	if st := ed.state(ed.cur); st != nil {
		st.Find(st.SelectedText())
	}
}

// ---- Sidebar

func (ed *bookEditor) sidebar(c *ui.Context) {
	bar := c.TitleBar()
	ui.Column(c.Key("sidebar")).Fill().Background(pal().BgSidebar).AlignItems(ui.Stretch).Children(func() {
		top := float32(theme.Space12)
		if bar.Left > 0 {
			top = max(bar.Height-theme.Space16, theme.Space12)
		}
		ui.Box(c).Height(top).DragWindow()
		ui.Row(c).Padding(theme.Space4, theme.Space16, theme.Space16, theme.Space16).Gap(theme.Space12).AlignItems(ui.Center).Children(func() {
			ed.a.coverView(c, ed.book, 36, 54, true)
			ui.Column(c).Grow(1).MinWidth(0).Gap(theme.Space4).AlignItems(ui.Start).Children(func() {
				ui.Text(c, ed.book.Title).FontSize(14).FontWeight(600).MaxLines(2)
				ui.Text(c, strings.Join(ed.book.Authors, ", ")).FontSize(12).TextColor(pal().TextSecondary).SingleLine()
				widgets.FormatBadge(c, ed.format, true)
			})
		})
		ui.Box(c).Height(1).Background(pal().BorderSubtle)
		if ed.bk.Packaged() {
			ui.Box(c).Padding(theme.Space12, theme.Space10, theme.Space8, theme.Space10).AlignItems(ui.Stretch).Children(func() {
				segmented(c, []segment{
					{label: locales.EditorContents(), on: !ed.files, onClick: func() { ed.files = false }},
					{label: locales.EditorFiles(), on: ed.files, onClick: func() { ed.files = true }},
				}, true)
			})
		}
		ui.Scroll(c.Key("list")).Grow(1).Padding(theme.Space4, theme.Space8, theme.Space16, theme.Space8).Gap(theme.Space2).AlignItems(ui.Stretch).Children(func() {
			if ed.files && ed.bk.Packaged() {
				ed.fileTree(c, ed.tree, 0)
				return
			}
			for _, it := range ed.bk.Spine() {
				name := it.Name
				ed.row(c, "c:"+name, 0, nil, icons.File, it.Title, groupDigits(ed.wordsOf(it)), ed.modified(name), name == ed.cur, func() { ed.show(name) })
			}
		})
	})
}

func (ed *bookEditor) fileTree(c *ui.Context, nodes []*treeNode, depth int) {
	for _, n := range nodes {
		n := n
		if !n.dir {
			ed.row(c, "f:"+n.path, depth, nil, icons.File, n.name, "", ed.modified(n.path), n.path == ed.cur, func() { ed.show(n.path) })
			continue
		}
		open := !ed.collapsed[n.path]
		chev, count := icons.ChevronDown, ""
		if !open {
			chev, count = icons.ChevronRight, groupDigits(n.files)
		}
		ed.row(c, "d:"+n.path, depth, chev, icons.Folder, n.name, count, false, false, func() { ed.collapsed[n.path] = open })
		if open {
			ed.fileTree(c, n.children, depth+1)
		}
	}
}

// row is a line of the sidebar, as SidebarItem; chev marks a folder.
func (ed *bookEditor) row(c *ui.Context, key string, depth int, chev, icon *ui.SVG, label, trailing string, dot, selected bool, onClick func()) {
	r := ui.ButtonBase(c.Key(key)).Height(30).Radius(theme.RadiusSM).Padding(0, theme.Space8, 0, float32(theme.Space6+depth*theme.Space16)).Gap(theme.Space6).AlignItems(ui.Center).Label(label)
	fg := pal().Icon
	switch {
	case selected:
		r.Background(pal().SurfaceRaised).Shadow(0, 1, 2, 0, ui.RGBA(26, 23, 18, 0.06)).Shadow(0, 0, 0, 1, pal().BorderSubtle.Alpha(0.6))
		fg = pal().TextAccent
	case r.Hovered():
		r.Background(pal().Hover)
	}
	r.OnClick(onClick).Children(func() {
		if ed.files && ed.bk.Packaged() {
			if chev != nil {
				ui.Icon(c, chev).FontSize(12).TextColor(pal().IconMuted).Width(12)
			} else {
				ui.Box(c).Width(12)
			}
		}
		ui.Icon(c, icon).FontSize(16).TextColor(fg)
		t := ui.Text(c, label).Grow(1).MinWidth(0).SingleLine().FontSize(13).TextColor(pal().Text)
		if selected {
			t.FontWeight(500)
		}
		if dot {
			ui.Box(c).Size(6, 6).Radius(3).Background(pal().WarningSolid)
		}
		if trailing != "" {
			ui.Text(c, trailing).Font(theme.FontMono).FontSize(12).TextColor(pal().TextTertiary)
		}
	})
}

type segment struct {
	icon    *ui.SVG
	label   string
	on      bool
	off     bool // can't be chosen now
	onClick func()
}

// segmented is a segmented control, as the editor's Glance / Edit and
// Contents / Files switches; fill shares its width among the segments.
func segmented(c *ui.Context, segs []segment, fill bool) ui.Element {
	box := ui.Row(c).Padding(theme.Space2).Gap(theme.Space2).Radius(theme.RadiusMD).Background(pal().Subtle).AlignItems(ui.Stretch)
	box.Children(func() {
		for _, s := range segs {
			b := ui.ButtonBase(c.Key(s.label)).Padding(theme.Space4, theme.Space12).Gap(theme.Space6).Radius(theme.RadiusSM).AlignItems(ui.Center).Justify(ui.Center).Label(s.label)
			if fill {
				b.Grow(1)
			}
			fg := pal().TextSecondary
			switch {
			case s.on:
				b.Background(pal().Surface).Shadow(0, 1, 2, 0, ui.RGBA(26, 23, 18, 0.08))
				fg = pal().Text
			case s.off:
				b.Disabled(true)
				fg = pal().TextDisabled
			case b.Hovered():
				fg = pal().Text
			}
			if !s.off && s.onClick != nil {
				b.OnClick(s.onClick)
			}
			b.TextColor(fg).Children(func() {
				if s.icon != nil {
					ui.Icon(c, s.icon).FontSize(14)
				}
				t := ui.Text(c, s.label).FontSize(13).SingleLine()
				if s.on {
					t.FontWeight(500)
				}
			})
		}
	})
	return box
}

// ---- Toolbar

func (ed *bookEditor) title() string {
	for _, it := range ed.bk.Spine() {
		if it.Name == ed.cur {
			return it.Title
		}
	}
	return path.Base(ed.cur)
}

func (ed *bookEditor) toolbar(c *ui.Context, glanceOK, editOK bool) {
	mode := ed.effectiveMode(glanceOK, editOK)
	dirty := ed.dirty()
	right := float32(theme.Space24) + c.TitleBar().Right
	ui.Row(c.Key("toolbar")).Height(56).Padding(0, right, 0, theme.Space24).Gap(theme.Space8).AlignItems(ui.Center).
		BorderWidth(0, 0, 1, 0).BorderColor(pal().BorderSubtle).DragWindow().Children(func() {
		ui.Row(c).Grow(1).MinWidth(0).Gap(theme.Space10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, ed.title()).FontSize(18).FontWeight(600).LetterSpacing(-0.2).SingleLine().Shrink(1)
			if ed.modified(ed.cur) {
				ui.Box(c).Size(6, 6).Radius(3).Background(pal().WarningSolid)
			}
			if ed.bk.Packaged() {
				ui.Text(c, ed.relPath(ed.cur)).Font(theme.FontMono).FontSize(12).TextColor(pal().TextTertiary).SingleLine().Shrink(2)
			}
		})
		segmented(c, []segment{
			{icon: icons.BookOpen, label: locales.EditorGlance(), on: mode == modeGlance, off: !glanceOK, onClick: func() { ed.mode = modeGlance }},
			{icon: icons.Pencil, label: locales.EditorEdit(), on: mode == modeEdit, off: !editOK, onClick: func() { ed.mode = modeEdit }},
		}, false)
		ui.Box(c).Size(1, 20).Background(pal().Border)
		if editOK {
			if widgets.IconButton(c, icons.Search, locales.EditorFind(), false).Clicked() {
				ed.find()
			}
		}
		if dirty {
			if widgets.Button(c, widgets.Ghost, nil, locales.EditorRevert()).Clicked() {
				ed.revert()
			}
		}
		save := widgets.Button(c, widgets.Primary, nil, locales.CommonSave())
		if !dirty {
			save.Disabled(true).Opacity(0.45)
		} else if save.Clicked() {
			ed.save()
		}
	})
}

// relPath is a file's path from the package's folder, as "Text/ch1.xhtml".
func (ed *bookEditor) relPath(name string) string {
	if dir := ed.bk.PackageDir(); dir != "" && dir != "." {
		return strings.TrimPrefix(name, dir+"/")
	}
	return name
}

// ---- Body

func (ed *bookEditor) body(c *ui.Context, glanceOK, editOK bool) {
	if !glanceOK && !editOK {
		ed.fileInfo(c)
		return
	}
	if ed.effectiveMode(glanceOK, editOK) == modeGlance {
		ed.page(c, ed.shownPage(), true)
		return
	}
	st := ed.state(ed.cur)
	if st == nil {
		ed.fileInfo(c)
		return
	}
	st.Theme = editorTheme(epub.IsMarkup(ed.bk.Type(ed.cur)))
	st.Font = ui.Font{Family: theme.FontMono, Size: 12.5}
	st.RenderFindBar = ed.findBar
	editor.View(c.Key("ed:"+ed.cur), st).Grow(1).MinWidth(0).Background(pal().Surface).Bind(&ed.pane)
	if page := ed.shownPage(); page != "" && ed.web != nil {
		ui.Box(c).Width(1).Background(pal().BorderSubtle)
		ed.page(c, page, false)
	}
}

// page shows a file of the book in the web view.
func (ed *bookEditor) page(c *ui.Context, name string, glance bool) {
	if ed.web == nil {
		if glance {
			ed.emptyState(c, icons.BookOpen, locales.EditorNoWebViewTitle(), locales.EditorNoWebViewDetail(webEngine()))
		}
		return
	}
	if name == "" {
		return
	}
	if epub.IsMarkup(ed.bk.Type(name)) {
		ed.lastPage = name
	}
	if want := ed.pageURL(name); ed.loaded != want && !strings.HasPrefix(ed.loaded, want+"#") {
		ed.loaded = want
		ed.viewVer = ed.bufVersion()
		ed.web.Page().LoadURL(want)
	} else if ed.bufVersion() != ed.viewVer {
		ed.scheduleReload()
	}
	ui.WebView(c, ed.web).Grow(1).MinWidth(0).Background(ui.Hex("#ffffff"))
}

func (ed *bookEditor) emptyState(c *ui.Context, icon *ui.SVG, title, detail string) {
	ui.Column(c.Key("empty")).Grow(1).MinWidth(0).Background(pal().Surface).AlignItems(ui.Center).Justify(ui.Center).Gap(theme.Space8).Padding(0, 0, theme.Space64, 0).Children(func() {
		ui.Icon(c, icon).FontSize(24).TextColor(pal().IconMuted).Margin(0, 0, theme.Space4, 0)
		ui.Text(c, title).FontSize(16).FontWeight(600)
		if detail != "" {
			ui.Text(c, detail).FontSize(13).TextColor(pal().TextSecondary).TextAlign(ui.Center)
		}
	})
}

// fileInfo stands for a file that neither shows nor edits, as a font.
func (ed *bookEditor) fileInfo(c *ui.Context) {
	var size int64
	for _, f := range ed.bk.Files() {
		if f.Name == ed.cur {
			size = f.Size
		}
	}
	ed.emptyState(c, icons.File, path.Base(ed.cur), humanSize(size))
}

// findBar is the editor's find bar in the design's look. In a pane too
// narrow for it, as beside the view, it spans the pane and its field gives
// up width, rather than running out over the sidebar.
func (ed *bookEditor) findBar(c *ui.Context, bar ui.Element, f *editor.FindBar) {
	w := ed.pane.Bounds(c).W
	narrow := w > 0 && w < 620
	if narrow {
		bar.Left(theme.Space8)
	}
	bar.Padding(theme.Space6).Gap(theme.Space6).AlignItems(ui.Stretch).Background(pal().SurfaceRaised).Border(1, pal().Border).
		Radius(theme.RadiusMD).Shadow(0, 4, 12, 0, ui.RGBA(26, 23, 18, 0.12)).TextColor(pal().Text)
	field := func(value *string, label string) ui.Element {
		in := ui.TextInputBase(c, value).Height(26).Padding(0, theme.Space8).Placeholder(label).Label(label).
			Background(pal().Surface).Border(1, pal().Border).Radius(theme.RadiusSM).FontSize(13)
		if narrow {
			in.Grow(1).MinWidth(60)
		} else {
			in.Width(220)
		}
		return in
	}
	button := func(key string, icon *ui.SVG, text, tip string, on bool) ui.Element {
		b := ui.ButtonBase(c.Key(key)).Height(26).MinWidth(26).Padding(0, theme.Space6).Radius(theme.RadiusSM).
			AlignItems(ui.Center).Justify(ui.Center).Label(tip).Tooltip(tip).KeepFocus()
		fg := pal().Icon
		switch {
		case on:
			b.Background(pal().Selected)
			fg = pal().TextAccent
		case b.Hovered():
			b.Background(pal().Hover)
			fg = pal().Text
		}
		b.TextColor(fg).Children(func() {
			if icon != nil {
				ui.Icon(c, icon).FontSize(14)
			} else {
				ui.Text(c, text).Font(theme.FontMono).FontSize(12)
			}
		})
		return b
	}
	toggle := func(key string, v *bool, text, tip string) {
		if button(key, nil, text, tip, *v).Clicked() {
			*v = !*v
		}
	}
	bar.Children(func() {
		ui.Row(c).Gap(theme.Space2).AlignItems(ui.Center).Children(func() {
			f.QueryInput(field(f.Query(), locales.EditorFind())).Margin(0, theme.Space4, 0, 0)
			toggle("case", f.CaseSensitive(), "Aa", locales.EditorMatchCase())
			toggle("word", f.WholeWord(), "ab", locales.EditorWholeWord())
			toggle("re", f.Regexp(), ".*", locales.EditorRegexp())
			ui.Text(c, f.Status()).FontSize(12).TextColor(pal().TextTertiary).SingleLine().Shrink(1).Margin(0, theme.Space4)
			if button("prev", icons.ChevronUp, "", locales.EditorPrevious(), false).Clicked() {
				f.Previous()
			}
			if button("next", icons.ChevronDown, "", locales.EditorNext(), false).Clicked() {
				f.Next()
			}
			if f.CanReplace() && button("replace", icons.Replace, "", locales.EditorReplace(), f.Replacing()).Clicked() {
				f.SetReplacing(!f.Replacing())
			}
			if button("close", icons.X, "", locales.EditorCloseFind(), false).Clicked() {
				f.Close()
			}
		})
		if f.Replacing() {
			ui.Row(c).Gap(theme.Space6).AlignItems(ui.Center).Children(func() {
				f.ReplacementInput(field(f.Replacement(), locales.EditorReplace()))
				if widgets.ButtonSized(c, widgets.Secondary, nil, locales.EditorReplace(), 26).Clicked() {
					f.Replace()
				}
				if widgets.ButtonSized(c, widgets.Secondary, nil, locales.EditorReplaceAll(), 26).Clicked() {
					f.ReplaceAll()
				}
			})
		}
	})
}

// editorTheme is the code theme in the design's colors; markup is dimmed
// so a chapter's prose reads first.
func editorTheme(markup bool) *editor.Theme {
	p := pal()
	t := &editor.Theme{
		Background:       p.Surface,
		Text:             p.Text,
		LineNumber:       p.TextDisabled,
		ActiveLineNumber: p.TextSecondary,
		ActiveLine:       p.Subtle.Alpha(0.7),
		Cursor:           p.Accent,
		Selection:        p.AccentSubtle,
	}
	if markup {
		for _, k := range []editor.TokenKind{editor.TokenTag, editor.TokenAttribute, editor.TokenString, editor.TokenPunctuation, editor.TokenMeta, editor.TokenOperator} {
			t.Tokens[k] = editor.TokenStyle{Color: p.TextTertiary}
		}
		t.Tokens[editor.TokenComment] = editor.TokenStyle{Color: p.TextDisabled, Italic: true}
	}
	return t
}

// ---- Status bar

func (ed *bookEditor) statusBar(c *ui.Context, glanceOK, editOK bool) {
	var left []string
	spine := ed.bk.Spine()
	for i, it := range spine {
		if it.Name == ed.cur {
			left = append(left, locales.DialogsPosition(i+1, len(spine)), locales.EditorWords(ed.wordsOf(it)))
		}
	}
	if left == nil {
		for _, f := range ed.bk.Files() {
			if f.Name == ed.cur {
				left = append(left, humanSize(f.Size))
			}
		}
	}
	if ed.modified(ed.cur) {
		left = append(left, locales.EditorEdited())
	}
	var right []string
	if ed.effectiveMode(glanceOK, editOK) == modeEdit {
		if st := ed.state(ed.cur); st != nil {
			p := st.CursorPos()
			line := st.Buffer.Line(p.Line)
			col := utf8.RuneCountInString(line[:min(p.Col, len(line))])
			right = append(right, locales.EditorLineCol(p.Line+1, col+1))
			if l := st.Buffer.Language(); l != nil {
				right = append(right, l.Name)
			}
		}
	} else {
		right = append(right, ed.format, humanSize(ed.bk.Size()))
	}
	ui.Row(c.Key("status")).Height(32).Padding(0, theme.Space24).AlignItems(ui.Center).Justify(ui.SpaceBetween).
		BorderWidth(1, 0, 0, 0).BorderColor(pal().BorderSubtle).Children(func() {
		ui.Text(c, strings.Join(left, " · ")).FontSize(12).TextColor(pal().TextTertiary).SingleLine()
		ui.Text(c, strings.Join(right, " · ")).FontSize(12).TextColor(pal().TextTertiary).SingleLine()
	})
}
