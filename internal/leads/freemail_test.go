package leads

import "testing"

func TestFreeMailDomainsNeverBecomeACompany(t *testing.T) {
	for _, email := range []string{
		"a@gmail.com", "a@GoogleMail.com", "a@outlook.com", "a@hotmail.com", "a@live.com", "a@yahoo.com",
		"a@icloud.com", "a@me.com", "a@aol.com", "a@proton.me", "a@protonmail.com", "a@gmx.de", "a@yandex.ru",
		"a@mail.ru", "a@hotmail.fr", "a@outlook.sa", "a@yahoo.co.uk", "a@live.com.eg", "a@qq.com",
	} {
		if d := CompanyDomain(email); d != "" {
			t.Errorf("%s gave company domain %q; free mail must give none", email, d)
		}
		if !IsFreeMail(EmailDomain(email)) {
			t.Errorf("%s is not recognised as free mail", email)
		}
	}
}

func TestBusinessDomainsDoBecomeACompany(t *testing.T) {
	for email, want := range map[string]string{
		"rana@acme.com":         "acme.com",
		"Rana@Acme-Labs.IO":     "acme-labs.io",
		"x@live.acme.com":       "live.acme.com", // a company's own subdomain, not Microsoft's
		"x@gmail-agency.com":    "gmail-agency.com",
		"x@outlook.company.com": "outlook.company.com",
		"x@www.example.org":     "example.org",
	} {
		if got := CompanyDomain(email); got != want {
			t.Errorf("CompanyDomain(%q) = %q, want %q", email, got, want)
		}
	}
	if CompanyDomain("not-an-email") != "" || CompanyDomain("trailing@") != "" {
		t.Error("an address without a domain must give no company")
	}
}

func TestCompanyNameFromDomainIsReadable(t *testing.T) {
	for domain, want := range map[string]string{
		"acme.com":        "Acme",
		"acme-labs.co.uk": "Acme Labs",
		"blue_river.io":   "Blue River",
		"x.test":          "X",
	} {
		if got := companyNameFromDomain(domain); got != want {
			t.Errorf("companyNameFromDomain(%q) = %q, want %q", domain, got, want)
		}
	}
}

func TestValidEmailTakesOnlyABareAddress(t *testing.T) {
	for _, ok := range []string{"a@b.co", "first.last+tag@example.com"} {
		if !validEmail(ok) {
			t.Errorf("%q refused", ok)
		}
	}
	for _, bad := range []string{"", "a", "a@b", "@b.com", "Name <a@b.com>", "a@b.com, c@d.com", "a b@c.com"} {
		if validEmail(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}
