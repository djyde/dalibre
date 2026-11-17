package sdk

import (
	"strings"
	"testing"
)

func TestGuide(t *testing.T) {
	g := Guide()
	if len(g) < 20 {
		t.Fatalf("%d sections", len(g))
	}
	for _, s := range g {
		if !strings.HasPrefix(s.Text, strings.Repeat("#", s.Level)+" "+s.Title) {
			t.Errorf("%s starts with %.40q", s.Slug, s.Text)
		}
		if strings.Contains(s.Text, "](../") {
			t.Errorf("%s keeps a relative link", s.Slug)
		}
	}
	for topic, want := range map[string]string{"plugin.json": "pluginjson", "fetch": "fetch", "Permissions": "permissions-and-limits"} {
		if f := FindSections(topic); len(f) != 1 || f[0].Slug != want {
			t.Errorf("%q found %+v", topic, f)
		}
	}
	// main.js holds its functions' sections, up to Runtime.
	m := FindSections("main.js")[0]
	if !strings.Contains(m.Text, "### search") || strings.Contains(m.Text, "## Runtime") {
		t.Error("main.js's section doesn't stop at the next ##")
	}
	if f := FindSections("no such topic"); len(f) != 0 {
		t.Errorf("found %v", f)
	}
}

func TestTypes(t *testing.T) {
	ts := Types()
	for _, d := range ts {
		if d.Summary == "" {
			t.Errorf("%s has no doc comment", d.Name)
		}
	}
	r, ok := FindType("Dalibre.result")
	if !ok || !strings.HasPrefix(r.Text, "/** A book the source found.") || !strings.HasSuffix(r.Text, "}") || strings.Contains(r.Text, "FormatName =") {
		t.Errorf("Result: %q", r.Text)
	}
	if d, ok := FindType("dalibre"); !ok || !strings.Contains(d.Text, "error(kind") || !strings.HasSuffix(d.Text, "};") {
		t.Errorf("dalibre: %q", d.Text)
	}
	if _, ok := FindType("Nope"); ok {
		t.Error("found Nope")
	}
}
