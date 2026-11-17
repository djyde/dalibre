package plugin

import (
	"strings"
	"testing"
)

func TestHTMLTokens(t *testing.T) {
	doc := `<?xml version="1.0" encoding="utf-8"?>
<!DOCTYPE html>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops" xml:lang="zh-CN">
<head><title>第一章 &amp; 开始</title><style>p &gt; a { color: red }</style><link rel="stylesheet" href="a.css"/></head>
<body class="a" class="b"><!-- note --><h1 epub:type="title">&#36825;&#x662F;&nbsp;软件</h1>
<p>A&B &lt; C<br/><![CDATA[x < y]]></p><svg viewBox="0 0 1 1"><image xlink:href="c.jpg"/></svg>
<script src="s.js"/><p>after</p></body></html>`
	toks := htmlTokens(doc)
	var raw strings.Builder
	for _, tk := range toks {
		raw.WriteString(tk.Raw)
	}
	if raw.String() != doc {
		t.Fatalf("raw tokens joined differ from the document:\n%s", raw.String())
	}
	find := func(typ, name string) *htmlToken {
		for i := range toks {
			if toks[i].Type == typ && toks[i].Name == name {
				return &toks[i]
			}
		}
		t.Fatalf("no %s %s", typ, name)
		return nil
	}
	text := func(after *htmlToken) string {
		for i := range toks {
			if &toks[i] == after && i+1 < len(toks) && toks[i+1].Text != nil {
				return *toks[i+1].Text
			}
		}
		return ""
	}
	if got := text(find("start", "h1")); got != "这是 软件" {
		t.Errorf("h1 text %q", got)
	}
	if got := text(find("start", "title")); got != "第一章 & 开始" {
		t.Errorf("title text %q", got)
	}
	if got := text(find("start", "style")); got != "p &gt; a { color: red }" {
		t.Errorf("style text %q, want it as written", got)
	}
	if h1 := find("start", "h1"); h1.Attrs["epub:type"] != "title" {
		t.Errorf("h1 attrs %v", h1.Attrs)
	}
	if body := find("start", "body"); body.Attrs["class"] != "a" {
		t.Errorf("body attrs %v, want the first class", body.Attrs)
	}
	if br := find("self", "br"); br.Raw != "<br/>" {
		t.Errorf("br raw %q", br.Raw)
	}
	// A self-closed script doesn't swallow what follows as its text.
	find("self", "script")
	if got := text(find("start", "p")); got != "A&B < C" {
		t.Errorf("p text %q", got)
	}
	if got := text(&toks[len(toks)-5]); toks[len(toks)-5].Name != "p" || got != "after" {
		t.Errorf("text after the script: %q in %+v", got, toks[len(toks)-5])
	}
	// Nor does a self-closed title; and noscript holds tags, as in XHTML.
	for _, doc := range []string{`<title/><p>x</p>`, `<noscript><p>x</p></noscript>`} {
		if toks := htmlTokens(doc); len(toks) < 2 || toks[1].Name != "p" {
			t.Errorf("%s: %+v", doc, toks)
		}
	}
	if toks[0].Type != "comment" || toks[0].Raw != `<?xml version="1.0" encoding="utf-8"?>` {
		t.Errorf("xml declaration: %+v", toks[0])
	}
	if len(htmlTokens("")) != 0 {
		t.Error("tokens of nothing")
	}
}

func TestHTMLInPlugin(t *testing.T) {
	dir := write(t, testManifest, itemOf+`
		export async function search(q) {
			const out = dalibre.html.tokens(q.text).map((t) => t.type === "text" ? dalibre.html.escape(t.text.toUpperCase()) : t.raw).join("");
			return item(out);
		}`)
	p, err := Load(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if got, want := firstTitle(t, p, `<p class="x">a &amp; &#98;<br/>"c"</p>`), `<p class="x">A &amp; B<br/>&quot;C&quot;</p>`; got != want {
		t.Errorf("%q, want %q", got, want)
	}
}
