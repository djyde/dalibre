package sdk

import (
	"path"
	"regexp"
	"strings"
	"unicode"

	plugindocs "dalibre/docs/plugins"
)

// Section is a section of the plugin authors' guide: a heading and what
// is under it, its subsections included.
type Section struct {
	Level int    // 2 for ##, 3 for ###
	Title string // as written, without the #s
	Slug  string // as GitHub makes its anchor: "pluginjson", "collections-and-list"
	Text  string // the heading and everything under it, links made absolute
}

// repoURL is where the guide's relative links point.
const repoURL = "https://github.com/djyde/dalibre/blob/main/"

var link = regexp.MustCompile(`\]\(([^)#:\s][^)\s]*)\)`)

// Guide is the guide's sections, in order.
func Guide() []Section {
	text := link.ReplaceAllStringFunc(plugindocs.Guide, func(m string) string {
		rel := link.FindStringSubmatch(m)[1]
		return "](" + repoURL + path.Join("docs/plugins", rel) + ")"
	})
	lines := strings.Split(text, "\n")
	type head struct{ line, level int }
	var heads []head
	fence := false
	for i, l := range lines {
		if strings.HasPrefix(l, "```") {
			fence = !fence
		}
		if fence {
			continue
		}
		if n := len(l) - len(strings.TrimLeft(l, "#")); n >= 2 && n <= 4 && strings.HasPrefix(l[n:], " ") {
			heads = append(heads, head{i, n})
		}
	}
	var out []Section
	for i, h := range heads {
		end := len(lines)
		for _, next := range heads[i+1:] {
			if next.level <= h.level {
				end = next.line
				break
			}
		}
		title := strings.TrimSpace(lines[h.line][h.level:])
		out = append(out, Section{Level: h.level, Title: title, Slug: Slug(title),
			Text: strings.TrimSpace(strings.Join(lines[h.line:end], "\n"))})
	}
	return out
}

// Intro is the guide's text before its first section.
func Intro() string {
	full := plugindocs.Guide
	if i := strings.Index(full, "\n## "); i >= 0 {
		full = full[:i]
	}
	return strings.TrimSpace(full)
}

// Slug is s as a GitHub anchor: lowercase, punctuation dropped, spaces as
// hyphens.
func Slug(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_':
			b.WriteRune(r)
		case r == ' ':
			b.WriteRune('-')
		}
	}
	return b.String()
}

// FindSections is the guide's sections that topic names: the one whose
// slug it is, or else those whose slug or title has it.
func FindSections(topic string) []Section {
	q := Slug(topic)
	all := Guide()
	for _, s := range all {
		if s.Slug == q {
			return []Section{s}
		}
	}
	var out []Section
	for _, s := range all {
		if strings.Contains(s.Slug, q) || strings.Contains(strings.ToLower(s.Title), strings.ToLower(topic)) {
			out = append(out, s)
		}
	}
	return out
}

// Decl is a declaration of dalibre.d.ts: a type of the Dalibre namespace,
// or the dalibre object.
type Decl struct {
	Name    string // "Result", "dalibre"
	Summary string // its doc comment's first line
	Text    string // the declaration with its doc comment
}

var declLine = regexp.MustCompile(`^  (?:interface|type) (\w+)`)

// Types is dalibre.d.ts's declarations, in order.
func Types() []Decl {
	data, _ := Files.ReadFile("dalibre.d.ts")
	lines := strings.Split(string(data), "\n")
	// Each declaration starts at its doc comment.
	type start struct {
		line int
		name string
	}
	var starts []start
	nsEnd := len(lines)
	inNS := false
	for i, l := range lines {
		switch {
		case strings.HasPrefix(l, "declare namespace Dalibre"):
			inNS = true
		case inNS && l == "}":
			inNS, nsEnd = false, i
		case inNS:
			if m := declLine.FindStringSubmatch(l); m != nil {
				s := i
				if j := i - 1; j >= 0 && strings.HasPrefix(lines[j], "  ") && strings.HasSuffix(strings.TrimSpace(lines[j]), "*/") {
					for s = j; s > 0 && !strings.HasPrefix(strings.TrimSpace(lines[s]), "/**"); s-- {
					}
				}
				starts = append(starts, start{s, m[1]})
			}
		}
	}
	var out []Decl
	for i, st := range starts {
		end := nsEnd
		if i+1 < len(starts) {
			end = starts[i+1].line
		}
		out = append(out, decl(st.name, lines[st.line:end], 2))
	}
	// The dalibre object, after the namespace.
	for i := nsEnd; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "declare const dalibre") {
			end := i
			for end < len(lines) && lines[end] != "};" {
				end++
			}
			out = append(out, decl("dalibre", lines[i:min(end+1, len(lines))], 0))
		}
	}
	return out
}

func decl(name string, lines []string, indent int) Decl {
	for i, l := range lines {
		if len(l) >= indent {
			lines[i] = l[indent:]
		}
	}
	text := strings.TrimSpace(strings.Join(lines, "\n"))
	d := Decl{Name: name, Text: text}
	if strings.HasPrefix(text, "/**") {
		c := strings.TrimPrefix(text, "/**")
		c = c[:strings.Index(c, "*/")]
		var words []string
		for _, l := range strings.Split(c, "\n") {
			if l = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(l), "*")); l != "" {
				words = append(words, l)
			}
		}
		d.Summary = strings.Join(words, " ")
		if i := strings.Index(d.Summary, ". "); i >= 0 {
			d.Summary = d.Summary[:i+1]
		}
	} else if name == "dalibre" {
		d.Summary = "The object every plugin is given: its settings, errors, forms, progress and the library."
	}
	return d
}

// FindType is the declaration named name ("Result", "Dalibre.Result",
// "result"); false when there is none.
func FindType(name string) (Decl, bool) {
	name = strings.TrimPrefix(strings.TrimSpace(name), "Dalibre.")
	for _, d := range Types() {
		if strings.EqualFold(d.Name, name) {
			return d, true
		}
	}
	return Decl{}, false
}
