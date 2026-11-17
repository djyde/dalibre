package calibre

import (
	"strings"
	"unicode/utf8"
)

// ExpandTemplate fills one component of a calibre save template (a part
// between slashes) from fields. {name} is the field's value, nothing when
// the book has none. {name:format|prefix|suffix}, as in calibre, writes
// prefix and suffix around the value only when there is one, so that
// "{series:||/}" adds the slash only to books in a series; the format
// part is ignored.
func ExpandTemplate(s string, fields map[string]string) string {
	var b strings.Builder
	for {
		i := strings.IndexByte(s, '{')
		if i < 0 {
			b.WriteString(s)
			break
		}
		j := strings.IndexByte(s[i:], '}')
		if j < 0 {
			b.WriteString(s)
			break
		}
		b.WriteString(s[:i])
		name, rest, _ := strings.Cut(s[i+1:i+j], ":")
		if v := fields[strings.TrimSpace(name)]; v != "" {
			parts := strings.SplitN(rest, "|", 3)
			if len(parts) == 3 {
				b.WriteString(parts[1] + v + parts[2])
			} else {
				b.WriteString(v)
			}
		}
		s = s[i+j+1:]
	}
	return strings.TrimSpace(b.String())
}

// SafeFileName makes s a file name every filesystem takes, keeping
// letters that aren't ASCII: what calibre forbids becomes "_", Windows'
// reserved names get a "_", and the name is cut to at most max bytes
// between letters.
func SafeFileName(s string, max int) string {
	s = sanitizeFileName(strings.Join(strings.Fields(s), " "))
	if base, _, _ := strings.Cut(s, "."); windowsReserved[strings.ToLower(base)] {
		s = "_" + s
	}
	if len(s) > max {
		cut := max
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		s = strings.TrimRight(s[:cut], " .")
	}
	return s
}
