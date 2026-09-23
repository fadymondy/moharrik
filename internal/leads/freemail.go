package leads

import (
	"regexp"
	"strings"
)

/*
Free-mail providers: the ONE list (MH-390).

Converting a lead makes a company from the email's domain — somebody writing
from ali@acme.com works at acme.com. Somebody writing from ali@gmail.com works
at no company called gmail.com, and making one would file every free-mail lead
in the workspace under the same fake company. So these domains never become a
company.

Two parts:

  - freeMailDomains: exact domains.
  - freeMailBrands: providers that hand out the same mailbox under many country
    domains (yahoo.co.uk, hotmail.fr, outlook.sa, yandex.ru, gmx.de …). The
    brand label matches with any plain or country suffix — but only directly
    under it, so live.acme.com (a company's own subdomain) is NOT free mail.

When in doubt a domain is left OFF: a missed free-mail domain costs one stray
company somebody can delete, while a business domain wrongly listed here
silently stops its leads from ever getting a company.
*/

var freeMailDomains = map[string]bool{
	// Google, Microsoft, Apple, Yahoo, AOL
	"gmail.com": true, "googlemail.com": true,
	"outlook.com": true, "hotmail.com": true, "live.com": true, "msn.com": true, "passport.com": true,
	"icloud.com": true, "me.com": true, "mac.com": true,
	"yahoo.com": true, "ymail.com": true, "rocketmail.com": true, "aol.com": true, "aim.com": true,
	// Privacy-first providers
	"proton.me": true, "protonmail.com": true, "protonmail.ch": true, "pm.me": true,
	"tutanota.com": true, "tutanota.de": true, "tuta.io": true, "tuta.com": true,
	"fastmail.com": true, "fastmail.fm": true, "hey.com": true, "hushmail.com": true, "mailfence.com": true,
	"skiff.com": true, "startmail.com": true, "posteo.de": true, "posteo.net": true, "runbox.com": true,
	// Generic portals
	"mail.com": true, "email.com": true, "usa.com": true, "gmx.com": true, "gmx.net": true,
	"zoho.com": true, "zohomail.com": true, "inbox.com": true, "lycos.com": true, "excite.com": true,
	"rediffmail.com": true, "maktoob.com": true,
	// Russia and CIS
	"mail.ru": true, "bk.ru": true, "list.ru": true, "inbox.ru": true, "internet.ru": true,
	"rambler.ru": true, "ya.ru": true,
	// Europe
	"web.de": true, "t-online.de": true, "freenet.de": true, "orange.fr": true, "wanadoo.fr": true,
	"free.fr": true, "laposte.net": true, "sfr.fr": true, "libero.it": true, "virgilio.it": true,
	"alice.it": true, "tiscali.it": true, "seznam.cz": true, "wp.pl": true, "o2.pl": true,
	"interia.pl": true, "onet.pl": true, "btinternet.com": true, "sky.com": true, "virginmedia.com": true,
	"blueyonder.co.uk": true, "ntlworld.com": true, "talktalk.net": true, "bluewin.ch": true, "telenet.be": true,
	"skynet.be": true, "ziggo.nl": true, "kpnmail.nl": true, "home.nl": true, "planet.nl": true,
	// Asia
	"qq.com": true, "163.com": true, "126.com": true, "yeah.net": true, "sina.com": true, "sohu.com": true,
	"naver.com": true, "daum.net": true, "hanmail.net": true,
	// North America ISPs
	"comcast.net": true, "verizon.net": true, "att.net": true, "sbcglobal.net": true, "bellsouth.net": true,
	"cox.net": true, "charter.net": true, "earthlink.net": true, "juno.com": true, "shaw.ca": true,
	"rogers.com": true, "sympatico.ca": true,
}

// freeMailBrands match under any plain or country suffix (see freeMailSuffix).
var freeMailBrands = map[string]bool{
	"gmail": true, "googlemail": true, "yahoo": true, "ymail": true, "hotmail": true, "outlook": true,
	"live": true, "msn": true, "windowslive": true, "aol": true, "gmx": true, "yandex": true,
	"proton": true, "protonmail": true, "icloud": true,
}

// freeMailSuffix is what may follow a brand label: com, net, a two-letter
// country code, or a country's own second level (co.uk, com.eg, com.sa …).
var freeMailSuffix = regexp.MustCompile(`^(com|net|org|[a-z]{2}|(co|com|net|org|ne|or)\.[a-z]{2})$`)

// IsFreeMail reports whether an email domain belongs to a free-mail provider.
func IsFreeMail(domain string) bool {
	d := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domain)), ".")
	if d == "" {
		return false
	}
	if freeMailDomains[d] {
		return true
	}
	brand, rest, ok := strings.Cut(d, ".")
	return ok && freeMailBrands[brand] && freeMailSuffix.MatchString(rest)
}

// EmailDomain is the lower-cased domain of an address, or "".
func EmailDomain(email string) string {
	at := strings.LastIndex(email, "@")
	if at < 0 || at == len(email)-1 {
		return ""
	}
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(email[at+1:])), ".")
}

// CompanyDomain is the domain a lead's company is made from: the email's, unless
// it is free mail — in which case there is no company to make.
func CompanyDomain(email string) string {
	d := EmailDomain(email)
	if d == "" || IsFreeMail(d) {
		return ""
	}
	return strings.TrimPrefix(d, "www.")
}

// companyNameFromDomain is the fallback name when the visitor did not type one:
// "acme-labs.co.uk" becomes "Acme Labs". The owner can rename it; a readable
// guess beats a bare domain on every card it appears on.
func companyNameFromDomain(domain string) string {
	label, _, _ := strings.Cut(domain, ".")
	words := strings.FieldsFunc(label, func(r rune) bool { return r == '-' || r == '_' })
	for i, w := range words {
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	if len(words) == 0 {
		return domain
	}
	return strings.Join(words, " ")
}
