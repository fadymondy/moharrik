package inbox

import (
	"html"
	"regexp"
	"strings"
	"unicode"

	xhtml "golang.org/x/net/html"
)

// skipElements are dropped with their content: their text is never prose.
var skipElements = map[string]bool{
	"style": true, "script": true, "head": true, "noscript": true, "template": true, "title": true,
}

// breakElements end a visual line.
var breakElements = map[string]bool{
	"br": true, "p": true, "div": true, "li": true, "tr": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
}

// stripHTML turns an email's HTML into readable text: style, script, head and
// comments vanish with their content, block ends become line breaks, entities
// are decoded. It is a reader, not a renderer.
func stripHTML(s string) string {
	var b strings.Builder
	z := xhtml.NewTokenizer(strings.NewReader(s))
	skip := ""
	for {
		tt := z.Next()
		if tt == xhtml.ErrorToken {
			break
		}
		switch tt {
		case xhtml.StartTagToken, xhtml.SelfClosingTagToken:
			name, _ := z.TagName()
			n := string(name)
			if skip != "" {
				continue
			}
			if skipElements[n] && tt == xhtml.StartTagToken {
				skip = n
				continue
			}
			if breakElements[n] {
				b.WriteByte('\n')
			}
		case xhtml.EndTagToken:
			name, _ := z.TagName()
			n := string(name)
			if skip != "" {
				if n == skip {
					skip = ""
				}
				continue
			}
			if breakElements[n] {
				b.WriteByte('\n')
			}
		case xhtml.TextToken:
			if skip == "" {
				b.WriteString(string(z.Text())) // Text() already unescapes
			}
		}
	}
	return normalizeSpace(b.String())
}

// normalizeSpace collapses runs of spaces (nbsp included) inside a line, trims
// each line, and keeps at most one blank line between paragraphs.
func normalizeSpace(s string) string {
	s = strings.ReplaceAll(s, " ", " ")
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	blank := 0
	for _, l := range lines {
		l = strings.Join(strings.Fields(l), " ")
		if l == "" {
			blank++
			if blank > 1 || len(out) == 0 {
				continue
			}
		} else {
			blank = 0
		}
		out = append(out, l)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// cssRule matches one leaked stylesheet rule: an at-rule, or
// `selector { property: value ...}`. A bare "a { b }" has no property:value,
// so ordinary prose with braces never matches.
var cssRule = regexp.MustCompile(`(?:@[a-zA-Z-]+[^{};]*\{|[*.#:\[][^{}]{0,120}\{\s*[a-zA-Z-]+\s*:[^{}]*\}|[a-z][a-z0-9 ,>+~:.#_*="\[\]-]{0,80}\{\s*[a-zA-Z-]+\s*:[^{}]*\})`)

// previewText cleans text that was stored with a stylesheet inside it (rows
// written before stripHTML learned to drop <style>; the CSS is often behind a
// preheader fragment, not at the very start). A run of rules is removed when
// it is clearly a stylesheet: two or more rules, an at-rule, or a selector
// that prose never starts with (*, ., #, [). Text around the run is kept.
func previewText(s string) string {
	for guard := 0; guard < 50; guard++ {
		loc := cssRule.FindStringIndex(s)
		if loc == nil {
			break
		}
		first := strings.TrimSpace(s[loc[0]:loc[1]])
		start, end, rules := loc[0], loc[0], 0
		for rules < 2000 {
			m := cssRule.FindStringIndex(s[end:])
			if m == nil || strings.TrimSpace(s[end:end+m[0]]) != "" {
				break
			}
			e := blockEnd(s, end+strings.IndexByte(s[end+m[0]:], '{')+m[0])
			if e < 0 {
				end = len(s) // unterminated stylesheet: nothing readable follows
				rules++
				break
			}
			end, rules = e, rules+1
		}
		obvious := rules >= 2 || strings.ContainsAny(first[:1], "@*.#[")
		if !obvious {
			// Not a stylesheet; look for one after this rule only.
			sep := ""
			if end < len(s) && strings.TrimLeft(s[end:], " \t\n") != s[end:] {
				sep = " "
			}
			return strings.TrimSpace(s[:end] + sep + previewText(s[end:]))
		}
		s = strings.TrimSpace(s[:start]) + " " + strings.TrimSpace(s[end:])
	}
	return strings.TrimSpace(s)
}

// blockEnd returns the index just past the brace block opening at open, or -1.
func blockEnd(s string, open int) int {
	depth := 0
	for j := open; j < len(s); j++ {
		switch s[j] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return j + 1
			}
		}
	}
	return -1
}

// previewLine is the one-line list preview: previewText, then the entities
// older rows kept encoded (&#8202;), the invisible filler marketing emails pad
// their preheader with (U+034F, zero-width joiners, soft hyphens), and runs
// of whitespace collapsed to one space.
func previewLine(s string) string {
	s = html.UnescapeString(previewText(s))
	var b strings.Builder
	space := false
	for _, r := range s {
		switch {
		case r == 0x034f || r == 0x00ad || r == 0xfeff || (r >= 0x200b && r <= 0x200f) || r == 0x2060: // CGJ, soft hyphen, BOM, zero-width and direction marks, word joiner
			continue
		case unicode.IsSpace(r):
			space = true
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
	}
	return b.String()
}
