package plugin

import (
	"encoding/json"
	"strings"

	"golang.org/x/net/html"
)

// htmlToken is a token of dalibre.html.tokens: Raw is the token as
// written, so a plugin that rewrites some tokens and joins the rest
// leaves the rest as they were; Text, Name and Attrs are what it means.
type htmlToken struct {
	Type  string            `json:"type"` // text, start, end, self, comment, doctype
	Raw   string            `json:"raw"`
	Text  *string           `json:"text,omitempty"`  // text: decoded; comment, doctype: as written
	Name  string            `json:"name,omitempty"`  // start, end, self: lowercase
	Attrs map[string]string `json:"attrs,omitempty"` // start, self: decoded, the first of a repeated name
}

var htmlTypes = map[html.TokenType]string{
	html.TextToken:           "text",
	html.StartTagToken:       "start",
	html.EndTagToken:         "end",
	html.SelfClosingTagToken: "self",
	html.CommentToken:        "comment",
	html.DoctypeToken:        "doctype",
}

// rawText is the elements whose text isn't tags.
var rawText = map[string]bool{"script": true, "style": true, "title": true, "textarea": true}

// htmlTokens splits HTML or XHTML into tokens, by HTML5's rules: the
// raw tokens joined are s. Text in script and style isn't decoded;
// <?xml …?> and <![CDATA[…]]> are comments. As in XHTML, a self-closed
// tag (<title/>, <script/>) has no text, and only script, style, title
// and textarea hold text rather than tags: in noscript or iframe, HTML5
// would take the tags for text.
func htmlTokens(s string) []htmlToken {
	z := html.NewTokenizer(strings.NewReader(s))
	out := []htmlToken{}
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			return out // the end: reading a string fails no other way
		}
		raw := string(z.Raw())
		t := z.Token()
		if tt == html.SelfClosingTagToken || tt == html.StartTagToken && !rawText[t.Data] {
			z.NextIsNotRawText()
		}
		tok := htmlToken{Type: htmlTypes[tt], Raw: raw}
		switch tt {
		case html.TextToken, html.CommentToken, html.DoctypeToken:
			tok.Text = &t.Data
		default:
			tok.Name = t.Data
			for _, a := range t.Attr {
				if tok.Attrs == nil {
					tok.Attrs = map[string]string{}
				}
				k := a.Key
				if a.Namespace != "" {
					k = a.Namespace + ":" + k
				}
				if _, ok := tok.Attrs[k]; !ok {
					tok.Attrs[k] = a.Val
				}
			}
		}
		out = append(out, tok)
	}
}

// hostHTML is __host_html(s): s's tokens, as JSON.
func hostHTML(args []any) (any, error) {
	s := ""
	if len(args) == 1 {
		s, _ = args[0].(string)
	}
	data, err := json.Marshal(htmlTokens(s))
	return string(data), err
}
