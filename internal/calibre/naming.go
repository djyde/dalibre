package calibre

import (
	"fmt"
	"regexp"
	"runtime"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mozillazg/go-unidecode"
)

// The rules below follow calibre's defaults (src/calibre/ebooks/metadata and
// src/calibre/db/backend.py) so that libraries written by Dalibre look like
// libraries written by calibre.

var titleArticles = regexp.MustCompile(`(?i)^(A\s+|The\s+|An\s+)`)

var quotePairs = map[rune]string{
	'"': `"`, '\'': `'`, '“': "”“", '”': "””", '„': "”“", '‚': "’‘", '’': "’‘", '‘': "’‘",
	'‹': "›", '›': "‹", '《': "》", '〈': "〉", '»': "«»", '«': "«»", '「': "」", '『': "』",
}

func stripQuotes(s string) string {
	if s == "" {
		return s
	}
	first, n := utf8.DecodeRuneInString(s)
	closers, ok := quotePairs[first]
	if !ok {
		return s
	}
	s = s[n:]
	if last, m := utf8.DecodeLastRuneInString(s); m > 0 && strings.ContainsRune(closers, last) {
		s = s[:len(s)-m]
	}
	return s
}

// TitleSort moves a leading English article to the end, as calibre's
// title_sort does: "The Three-Body Problem" → "Three-Body Problem, The".
func TitleSort(title string) string {
	title = stripQuotes(strings.TrimSpace(title))
	if m := titleArticles.FindStringSubmatch(title); m != nil {
		prep := m[1]
		title = stripQuotes(title[len(prep):] + ", " + prep)
	}
	return strings.TrimSpace(title)
}

var (
	authorSuffixes  = wordSet("Jr", "Sr", "Inc", "Ph.D", "Phd", "MD", "M.D", "I", "II", "III", "IV", "Junior", "Senior")
	authorPrefixes  = wordSet("Mr", "Mrs", "Ms", "Dr", "Prof")
	authorCopywords = map[string]bool{}
	bracketed       = regexp.MustCompile(`[\[({][^\])}]*[\])}]`)
)

func init() {
	for _, w := range []string{"Agency", "Corporation", "Company", "Co.", "Council", "Committee", "Inc.", "Institute",
		"National", "Society", "Club", "Team", "Software", "Games", "Entertainment", "Media", "Studios"} {
		authorCopywords[strings.ToLower(w)] = true
	}
}

func wordSet(words ...string) map[string]bool {
	m := map[string]bool{}
	for _, w := range words {
		w = strings.ToLower(w)
		m[w], m[w+"."] = true, true
	}
	return m
}

// AuthorSort inverts a name to "Last, First" with calibre's default
// "comma" method: names already containing a comma, single words and
// company names are kept as they are.
func AuthorSort(author string) string {
	if author == "" {
		return ""
	}
	sauthor := strings.TrimSpace(bracketed.ReplaceAllString(author, ""))
	if strings.Contains(sauthor, ",") {
		return author
	}
	tokens := strings.Fields(sauthor)
	if len(tokens) < 2 {
		return author
	}
	for _, t := range tokens {
		if authorCopywords[strings.ToLower(t)] {
			return author
		}
	}
	first := 0
	for first < len(tokens) && authorPrefixes[strings.ToLower(tokens[first])] {
		first++
	}
	if first == len(tokens) {
		return author
	}
	last := len(tokens) - 1
	for last >= first && authorSuffixes[strings.ToLower(tokens[last])] {
		last--
	}
	if last < first {
		return author
	}
	suffix := strings.Join(tokens[last+1:], " ")
	atokens := append([]string{tokens[last]}, tokens[first:last]...)
	numToks := len(atokens)
	if suffix != "" {
		atokens = append(atokens, suffix)
	}
	if numToks > 1 {
		atokens[0] += ","
	}
	return strings.Join(atokens, " ")
}

// AuthorsSort joins the sort strings of several authors as calibre does.
func AuthorsSort(authors []string) string {
	parts := make([]string, len(authors))
	for i, a := range authors {
		parts[i] = AuthorSort(a)
	}
	return strings.Join(parts, " & ")
}

const invalidFilenameChars = `\|?*<":>+/`

func sanitizeFileName(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r < 32 || strings.ContainsRune(invalidFilenameChars, r) {
			b.WriteByte('_')
		} else if unicode.IsSpace(r) {
			b.WriteByte(' ')
		} else {
			b.WriteRune(r)
		}
	}
	one := strings.TrimSpace(b.String())
	if strings.Trim(one, ".") == "" && one != "" {
		one = "_"
	}
	one = strings.ReplaceAll(one, "..", "_")
	if one != "" && (one[len(one)-1] == '.' || one[len(one)-1] == ' ') {
		one = one[:len(one)-1] + "_"
	}
	if strings.HasPrefix(one, ".") {
		one = "_" + one[1:]
	}
	return one
}

// ASCIIFilename is asciiFilename, for the names of the files calibre puts
// on readers.
func ASCIIFilename(s string) string { return asciiFilename(s) }

// asciiFilename transliterates to ASCII (三体 → "San Ti") and makes the
// result safe on every filesystem, like calibre's ascii_filename.
func asciiFilename(s string) string {
	a := strings.ReplaceAll(unidecode.Unidecode(s), "?", "_")
	return sanitizeFileName(strings.Join(strings.Fields(a), " "))
}

var windowsReserved = wordSet("CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
	"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9")

func pathLimit() int {
	if runtime.GOOS == "windows" {
		return 40
	}
	return 100
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] // ASCII after asciiFilename, so byte slicing is safe
}

// BookPath is the folder of a book relative to the library root:
// "Author/Title (id)", always with forward slashes as calibre stores it.
func BookPath(id int64, title, author string) string {
	idPart := fmt.Sprintf(" (%d)", id)
	l := pathLimit() - len(idPart)/2 - 2
	a := truncate(asciiFilename(author), l)
	t := strings.TrimRight(truncate(asciiFilename(strings.TrimLeft(title, " ")), l), " ")
	if t == "" {
		t = "Unknown"
	}
	a = strings.TrimRight(a, " .")
	if a == "" {
		a = "Unknown"
	}
	if windowsReserved[strings.ToLower(a)] {
		a += "w"
	}
	return a + "/" + t + idPart
}

// BookFileName is the base name, without extension, of a book's files:
// "Title - Author".
func BookFileName(title, author string) string {
	const extlen = 14
	var l int
	if runtime.GOOS == "windows" {
		l = pathLimit() - extlen/2 - 2
	} else {
		l = (pathLimit() - extlen - 2) / 2
	}
	a := truncate(asciiFilename(author), l)
	t := strings.TrimRight(truncate(asciiFilename(strings.TrimLeft(title, " ")), l), " ")
	if t == "" {
		t = "Unknown"
	}
	name := strings.TrimRight(t+" - "+a, ".")
	if name == "" {
		name = "Unknown"
	}
	return name
}

// calibre keeps a comma in an author's name as "|", since it joins a
// book's authors with commas elsewhere: "Herbert, Frank" is stored as
// "Herbert| Frank".

// AuthorName is an author's name as stored in authors.name, as calibre
// shows it.
func AuthorName(stored string) string { return strings.ReplaceAll(stored, "|", ",") }

// storedAuthor is an author's name as calibre stores it.
func storedAuthor(name string) string { return strings.ReplaceAll(name, ",", "|") }
