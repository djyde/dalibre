package app

import (
	"slices"
	"testing"

	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"

	"dalibre/cmd/dalibre/locales"
)

// TestLanguages checks every catalog has every message, and that the
// app shows the language it is set to.
func TestLanguages(t *testing.T) {
	langs := languages()
	for _, want := range []string{"en", "zh-Hans", "zh-Hant", "ja", "ko", "de", "fr", "es", "pt-BR", "pt-PT"} {
		if !slices.Contains(langs, want) {
			t.Errorf("no %s catalog: %v", want, langs)
		}
	}
	if got := bestLanguage("zh-TW"); got != "zh-Hant" {
		t.Errorf("bestLanguage(zh-TW) = %s", got)
	}
	if got := bestLanguage("pt-BR"); got != "pt-BR" {
		t.Errorf("bestLanguage(pt-BR) = %s", got)
	}
	if got := bestLanguage("pt-PT"); got != "pt-PT" {
		t.Errorf("bestLanguage(pt-PT) = %s", got)
	}
	if got := bestLanguage("pt"); got != "pt-BR" {
		t.Errorf("bestLanguage(pt) = %s", got)
	}
	if got := bestLanguage("ru"); got != "en" {
		t.Errorf("bestLanguage(ru) = %s", got)
	}

	loc := "zh-Hans"
	i18n.SetLocaleFunc(func() string { return loc })
	t.Cleanup(func() { i18n.SetLocaleFunc(nil) })
	a := newTestApp(t)
	tt := ui.NewTester(a.View, 1440, 900)
	tt.SetLocale("zh-Hans")
	a.openSettings(sectionGeneral)
	tt.Frame()
	zh := locales.In("zh-Hans")
	for _, s := range []string{zh.CommonSettings(), zh.LanguageSystem("简体中文")} {
		if s == locales.In("en").CommonSettings() || !tt.HasText(s) {
			t.Errorf("no %q in %q", s, tt.Texts())
		}
	}

	loc = "pt-BR"
	tt.SetLocale("pt-BR")
	tt.Frame()
	pt := locales.In("pt-BR")
	for _, s := range []string{pt.CommonSettings(), pt.LanguageSystem(autonym("pt-BR"))} {
		if s == locales.In("en").CommonSettings() || !tt.HasText(s) {
			t.Errorf("no %q in %q", s, tt.Texts())
		}
	}

	if autonym("pt-PT") == "" || autonym("pt-PT") == autonym("pt-BR") {
		t.Errorf("autonym(pt-PT) = %q, autonym(pt-BR) = %q", autonym("pt-PT"), autonym("pt-BR"))
	}
	loc = "pt-PT"
	tt.SetLocale("pt-PT")
	tt.Frame()
	pp := locales.In("pt-PT")
	if got := pp.CommonSettings(); got != "Definições" {
		t.Errorf("pt-PT CommonSettings = %q, want Definições", got)
	}
	for _, s := range []string{pp.CommonSettings(), pp.LanguageSystem(autonym("pt-PT"))} {
		if s == locales.In("en").CommonSettings() || !tt.HasText(s) {
			t.Errorf("no %q in %q", s, tt.Texts())
		}
	}
}

// TestPortugueseLocalesPlurals checks that European Portuguese counts by
// its own plural rules: nought is plural, «0 livros».
func TestPortugueseLocalesPlurals(t *testing.T) {
	pp := locales.In("pt-PT")
	for n, want := range map[int]string{0: "0 livros", 1: "1 livro", 2: "2 livros"} {
		if got := pp.CommonBookCount(n); got != want {
			t.Errorf("pt-PT CommonBookCount(%d) = %q, want %q", n, got, want)
		}
	}
}

// msgText says a message that may be unset.
func msgText(f func() string) string {
	if f == nil {
		return ""
	}
	return f()
}

func TestHeadlineSize(t *testing.T) {
	for _, tag := range languages() {
		h := locales.In(tag).WelcomeHeadline()
		if s := headlineSize(h, 560); s < 36 || s > 60 {
			t.Errorf("%s: headline %q at %v", tag, h, s)
		}
	}
	if s := headlineSize("A calm library\nfor your books.", 560); s != 60 {
		t.Errorf("English headline at %v, want 60", s)
	}
}
