package inbox

import (
	"regexp"
	"strings"

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

// cssRuleStart matches a leaked stylesheet at the very start of a text: an
// at-rule, or `selector { property: value ...}`. A bare "a { b }" has no
// property:value, so ordinary prose with braces never matches.
var cssRuleStart = regexp.MustCompile(`^\s*(?:@[a-zA-Z-]+[^{};]*\{|[*.#:\[][^{}]{0,120}\{\s*[a-zA-Z-]+\s*:[^{}]*\}|[a-z][a-z0-9 ,>+~:.#_-]{0,80}\{\s*[a-zA-Z-]+\s*:[^{}]*\})`)

// previewText cleans text that was stored with a stylesheet in front of it
// (rows written before stripHTML learned to drop <style>). Only leading rule
// blocks are removed; text after them is untouched.
func previewText(s string) string {
	for i := 0; i < 500; i++ {
		loc := cssRuleStart.FindStringIndex(s)
		if loc == nil {
			break
		}
		open := strings.IndexByte(s[:loc[1]], '{')
		if open < 0 {
			break
		}
		depth, end := 0, -1
		for j := open; j < len(s); j++ {
			switch s[j] {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					end = j + 1
				}
			}
			if end >= 0 {
				break
			}
		}
		if end < 0 {
			return "" // unterminated stylesheet: nothing readable follows
		}
		s = s[end:]
	}
	return strings.TrimSpace(s)
}
