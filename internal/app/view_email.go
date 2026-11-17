package app

import (
	"strconv"
	"strings"

	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/app/icons"
	"dalibre/internal/app/theme"
	"dalibre/internal/app/widgets"
	"dalibre/internal/email"
	"dalibre/internal/settings"
)

// ---- Settings › Email

func (a *App) emailPane(c *ui.Context) {
	e := &a.settings.Email
	ed := &a.emailEdit
	if !ed.loaded {
		ed.loaded = true
		ed.port = ""
		if e.Port > 0 {
			ed.port = strconv.Itoa(e.Port)
		}
		if a.secrets != nil && !ed.pwLoaded {
			secrets := a.secrets
			a.run(func() {
				pw, _ := secrets.Get(emailSecret)
				a.post(func() {
					if !ed.pwLoaded {
						ed.pw, ed.pwLoaded = pw, true
					}
				})
			})
		}
	}
	pane(c, func() {
		ui.Column(c).Gap(theme.Space10).AlignItems(ui.Stretch).Children(func() {
			ui.Row(c).Gap(theme.Space16).AlignItems(ui.Start).Children(func() {
				heading(c, locales.EmailKindlesTitle(), locales.EmailKindlesBody())
				if widgets.ButtonSized(c, widgets.Secondary, icons.Plus, locales.EmailAddKindle(), theme.ControlS).Clicked() {
					e.Kindles = append(e.Kindles, &settings.Kindle{})
					a.saveSettings()
				}
			})
			for i, k := range e.Kindles {
				ui.Row(c.Key("kindle" + strconv.Itoa(i))).Gap(theme.Space12).AlignItems(ui.Center).Children(func() {
					ui.Column(c).Width(200).AlignItems(ui.Stretch).Children(func() {
						if ui.TextInput(c, &k.Name).Label(locales.CommonName()).Placeholder("Kindle").Changed() {
							a.saveSettings()
						}
					})
					ui.Column(c).Grow(1).MinWidth(0).AlignItems(ui.Stretch).Children(func() {
						if ui.TextInput(c, &k.Address).Label(locales.EmailKindleAddress()).Placeholder("name@kindle.com").Changed() {
							a.saveSettings()
						}
					})
					if widgets.IconButton(c, icons.Trash, locales.CommonRemove(), false).Clicked() {
						e.Kindles = append(e.Kindles[:i:i], e.Kindles[i+1:]...)
						a.saveSettings()
					}
				})
			}
		})
		widgets.Hairline(c)
		ui.Column(c).Gap(theme.Space12).AlignItems(ui.Stretch).Children(func() {
			heading(c, locales.EmailSenderTitle(), locales.EmailSenderBody())
			ui.Row(c).Gap(theme.Space12).AlignItems(ui.End).Children(func() {
				widgets.Field(c, locales.EmailServer(), "", func() {
					if ui.TextInput(c, &e.Host).Label(locales.EmailServer()).Placeholder("smtp.gmail.com").Changed() {
						a.saveSettings()
					}
				})
				ui.Column(c).Width(120).AlignItems(ui.Stretch).Children(func() {
					labeled(c, locales.SettingsPortLabel(), func() {
						if ui.TextInput(c, &ed.port).Label(locales.SettingsPortLabel()).Placeholder(strconv.Itoa(settings.DefaultEmailPort)).Changed() {
							if p, err := strconv.Atoi(strings.TrimSpace(ed.port)); err == nil && p > 0 && p <= 65535 {
								e.Port = p
							} else {
								e.Port = 0
							}
							a.saveSettings()
						}
					})
				})
				ui.Column(c).Width(160).AlignItems(ui.Stretch).Children(func() {
					labeled(c, locales.EmailSecurity(), func() { a.emailSecuritySelect(c) })
				})
			})
			ui.Row(c).Gap(theme.Space12).AlignItems(ui.End).Children(func() {
				widgets.Field(c, locales.EmailFrom(), "", func() {
					if ui.TextInput(c, &e.From).Label(locales.EmailFrom()).Placeholder("you@gmail.com").Changed() {
						a.saveSettings()
					}
				})
				widgets.Field(c, locales.EmailPassword(), "", func() {
					in := ui.TextInput(c, &ed.pw).Label(locales.EmailPassword()).Password()
					if in.Changed() {
						ed.pwLoaded = true
					}
					// Kept in the store when the user leaves the field, not
					// at each key.
					if in.FocusLeft() {
						a.saveEmailPassword(ed.pw)
					}
				})
			})
			ui.Row(c).Gap(theme.Space12).AlignItems(ui.Center).Children(func() {
				t := a.emailTestState
				ready := strings.TrimSpace(e.Host) != "" && strings.Contains(e.From, "@")
				if widgets.ButtonSized(c, widgets.Secondary, icons.Send, locales.EmailTest(), theme.ControlS).Disabled(!ready || t.sending).Clicked() {
					a.saveEmailPassword(ed.pw)
					a.sendTestEmail()
				}
				switch {
				case t.sending:
					ui.Spinner(c).Size(14, 14).Label(locales.EmailTestSending())
					ui.Text(c, locales.EmailTestSending()).FontSize(12).TextColor(pal().TextSecondary)
				case t.err != nil:
					ui.Text(c, t.err.Error()).FontSize(12).TextColor(pal().Danger.FG).MaxLines(2).Grow(1).MinWidth(0)
				case !t.at.IsZero():
					at := i18n.FormatDate(i18n.Locale(), t.at, i18n.DateOptions{Skeleton: "jm"})
					ui.Text(c, locales.EmailTestSent(t.to, at)).FontSize(12).TextColor(pal().Success.FG)
				}
			})
		})
		ui.Text(c, locales.EmailNote()).FontSize(12).TextColor(pal().TextTertiary)
	})
}

// emailSecuritySelect picks how mail is sent encrypted; it shows the
// choice the port makes until the user makes one.
func (a *App) emailSecuritySelect(c *ui.Context) {
	e := &a.settings.Email
	values := []email.Security{email.StartTLS, email.TLS, email.None}
	names := []string{"STARTTLS", "SSL/TLS", locales.EmailSecurityNone()}
	cur := names[0]
	now := emailSecurity(*e)
	for i, v := range values {
		if v == now {
			cur = names[i]
		}
	}
	if ui.Select(c, &cur, names).Label(locales.EmailSecurity()).Changed() {
		for i, n := range names {
			if n == cur {
				e.Security = string(values[i])
				a.saveSettings()
			}
		}
	}
}

// emailMenuItems are the book menu's Email to items, one for each Kindle.
// It reports whether it added any.
func (a *App) emailMenuItems(m *ui.Menu, ids []int64) bool {
	ks := a.emailKindles()
	for _, k := range ks {
		if m.Item(locales.EmailTo(kindleName(*k))).Icon(icons.Menu(icons.Send)).Disabled(a.busy != "").Chosen() {
			a.emailBooks(*k, ids)
		}
	}
	return len(ks) > 0
}
