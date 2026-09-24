package prospects

import (
	"regexp"
	"strings"

	"github.com/fadymondy/moharrik/internal/research"
)

/*
The person to write to, when the pages name one (MH-397).

Only what a page states: a name next to a title ("Omar Ali, CEO of Acme",
"Acme's founder Omar Ali", "co-founder and CEO Omar Ali"), and an email
address on the company's own domain. Nothing is guessed — no
firstname@domain patterns, no lookups. A prospect with no named person is
"write to the team", and the accept step says so.
*/

// ProspectContact is what the pages say about who to write to.
type ProspectContact struct {
	Name      string `json:"name"`
	Title     string `json:"title"`
	Email     string `json:"email"`
	SourceURL string `json:"source_url"`
}

// Page is a page the prospect's evidence came from.
type Page struct {
	URL     string
	Content string
}

const titles = `(?:co-?founder(?: and| &)? (?:ceo|cto|coo)|founder(?: and| &)? (?:ceo|cto)|chief executive officer|chief technology officer|chief operating officer|` +
	`managing director|general manager|head of [a-z]+(?: [a-z]+)?|vp of [a-z]+|vice president of [a-z]+|ceo|cto|coo|cfo|founder|co-?founder|president)`

var (
	person = `([A-Z][a-z]+(?:[ -][A-Z][a-z]+){1,2})`
	// "Omar Ali, CEO of Acme" / "Omar Ali, the co-founder and CEO"
	nameThenTitle = regexp.MustCompile(person + `,\s+(?:the\s+)?(?i:` + titles + `)\b`)
	// "CEO Omar Ali" / "founder and CEO Omar Ali" / "Acme's CEO, Omar Ali"
	titleThenName = regexp.MustCompile(`(?i:\b` + titles + `),?\s+` + person)
	titleOnly     = regexp.MustCompile(`(?i)\b` + titles + `\b`)
	emailRe       = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)
)

// Not a person, whatever the capitals say.
var notPerson = map[string]bool{"Read More": true, "Chief Executive": true, "Series A": true, "Series B": true, "Middle East": true,
	"North Africa": true, "Saudi Arabia": true, "United Arab": true, "Press Release": true}

// FindContact reads the pages for a named person and an address on the
// company's own domain. companyName narrows a name to sentences about that
// company when there is more than one.
func FindContact(pages []Page, companyName, domain string) ProspectContact {
	var best ProspectContact
	key := strings.ToLower(companyName)
	domain = strings.ToLower(research.RegistrableDomain(domain))
	for _, pg := range pages {
		for _, line := range strings.Split(pg.Content, "\n") {
			if best.Name == "" && (key == "" || strings.Contains(strings.ToLower(line), key)) {
				if n, t := namedPerson(line); n != "" {
					best.Name, best.Title, best.SourceURL = n, t, pg.URL
				}
			}
			if best.Email == "" && domain != "" {
				for _, e := range emailRe.FindAllString(line, -1) {
					e = strings.ToLower(strings.TrimRight(e, "."))
					if strings.HasSuffix(e, "@"+domain) || strings.HasSuffix(e, "."+domain) {
						best.Email = e
						if best.SourceURL == "" {
							best.SourceURL = pg.URL
						}
						break
					}
				}
			}
		}
	}
	return best
}

func namedPerson(line string) (string, string) {
	if m := nameThenTitle.FindStringSubmatch(line); m != nil && !notPerson[m[1]] {
		t := titleOnly.FindString(line[strings.Index(line, m[0])+len(m[1]):])
		return m[1], titleCase(t)
	}
	if m := titleThenName.FindStringSubmatch(line); m != nil && !notPerson[m[1]] {
		return m[1], titleCase(titleOnly.FindString(m[0]))
	}
	return "", ""
}

func titleCase(s string) string {
	s = strings.TrimSpace(s)
	switch strings.ToLower(s) {
	case "ceo", "cto", "coo", "cfo":
		return strings.ToUpper(s)
	}
	words := strings.Fields(s)
	for i, w := range words {
		lw := strings.ToLower(w)
		if lw == "and" || lw == "of" {
			words[i] = lw
			continue
		}
		if lw == "ceo" || lw == "cto" || lw == "coo" || lw == "vp" {
			words[i] = strings.ToUpper(lw)
			continue
		}
		words[i] = strings.ToUpper(lw[:1]) + lw[1:]
	}
	return strings.Join(words, " ")
}
