package research

import (
	"strings"
	"testing"
)

const fintechArticle = `# Nile Pay raises $12 million to expand across Egypt

Published March 3, 2026

Nile Pay has raised $12 million in a Series A round led by Sawari Ventures, the Cairo fintech said on Tuesday.
The company plans to expand into Saudi Arabia and is hiring 40 engineers for its payments platform in Cairo.
Subscribe to our newsletter for the latest fintech news.
Separately, Paymob Ltd signed a partnership with a regional bank to offer merchant payments in Egypt.
The weather in Cairo was pleasant this week and many people went to the beach on the north coast.
[Read more](https://example.com/more)`

func TestSentencesDropsBoilerplateAndKeepsAbbreviations(t *testing.T) {
	got := Sentences(fintechArticle + "\nAcme Inc. announced today that it will open an office in Dubai next quarter.")
	joined := strings.Join(got, "\n")
	if strings.Contains(joined, "Subscribe") {
		t.Errorf("boilerplate kept: %q", joined)
	}
	found := false
	for _, s := range got {
		if strings.HasPrefix(s, "Acme Inc. announced") {
			found = true
		}
	}
	if !found {
		t.Errorf("an abbreviation split a sentence: %q", got)
	}
}

func TestSignal(t *testing.T) {
	cases := map[string]string{
		"Nile Pay has raised $12 million in a Series A round.":              "funding",
		"The company is hiring 40 engineers in Cairo.":                      "hiring",
		"Paymob signed a partnership with a regional bank.":                 "partnership",
		"أعلنت الشركة عن جولة تمويل بقيمة 5 ملايين دولار":                   "funding",
		"شركة نون تعلن عن وظائف جديدة للمهندسين في القاهرة":                 "hiring",
		"The weather was pleasant this week on the coast.":                  "",
		"Acme launched a new platform for small merchants in Riyadh today.": "launch",
	}
	for in, want := range cases {
		if got := Signal(in); got != want {
			t.Errorf("Signal(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestExtractHeuristicQuotesThePageAndNamesTheCompany(t *testing.T) {
	plan := PlanQuery("fintech companies hiring engineers in Egypt")
	got := ExtractHeuristic(plan, Page{URL: "https://techcrunch.com/2026/03/03/nile-pay", Title: "Nile Pay raises $12M | TechCrunch", Text: fintechArticle})
	if len(got) == 0 {
		t.Fatal("no evidence from an on-topic article")
	}
	byCompany := map[string]Extracted{}
	for _, e := range got {
		if !strings.Contains(fintechArticle, e.Quote) {
			t.Errorf("a quote is not verbatim page text: %q", e.Quote)
		}
		if strings.Contains(e.Quote, "weather") {
			t.Errorf("an off-topic sentence became evidence: %q", e.Quote)
		}
		if e.Extractor != "heuristic" || e.Kind != "quote" || e.Claim != "" {
			t.Errorf("a heuristic quote claims to be something else: %+v", e)
		}
		byCompany[e.CompanyName] = e
	}
	if _, ok := byCompany["Nile Pay"]; !ok {
		t.Errorf("Nile Pay not named: %+v", got)
	}
	if e, ok := byCompany["Paymob Ltd"]; !ok || e.SignalType != "partnership" {
		t.Errorf("Paymob Ltd's partnership not found: %+v", got)
	}
	if _, ok := byCompany["TechCrunch"]; ok {
		t.Error("the publisher was taken for the company")
	}
	if got[0].PublishedAt == nil || got[0].PublishedAt.Format("2006-01-02") != "2026-03-03" {
		t.Errorf("published date not read: %v", got[0].PublishedAt)
	}
}

func TestPageCompanyOnlyForCompanySites(t *testing.T) {
	if pc := pageCompany("https://careers.acmepay.com/jobs/1", "Senior Go Engineer | AcmePay"); pc.domain != "acmepay.com" || pc.name != "AcmePay" {
		t.Errorf("company site: %+v", pc)
	}
	if pc := pageCompany("https://jobs.lever.co/nile-logistics/123", "Backend engineer"); pc.name != "Nile Logistics" {
		t.Errorf("job board: %+v", pc)
	}
	for _, u := range []string{"https://en.wikipedia.org/wiki/Fintech", "https://www.linkedin.com/company/x", "https://www.cbe.gov.eg/en"} {
		if pc := pageCompany(u, "Anything"); pc.name != "" {
			t.Errorf("%s was taken for a company: %+v", u, pc)
		}
	}
}

func TestCompanyKeyGroupsNames(t *testing.T) {
	if CompanyKey("Paymob Ltd", "") != CompanyKey("paymob", "") {
		t.Errorf("legal suffix splits one company: %q vs %q", CompanyKey("Paymob Ltd", ""), CompanyKey("paymob", ""))
	}
	if CompanyKey("Anything", "https://www.careers.acme.co.uk") != "" && CompanyKey("x", "careers.acme.co.uk") != "acme.co.uk" {
		t.Errorf("domain key: %q", CompanyKey("x", "careers.acme.co.uk"))
	}
	if RegistrableDomain("jobs.acme.com.eg") != "acme.com.eg" {
		t.Errorf("RegistrableDomain = %q", RegistrableDomain("jobs.acme.com.eg"))
	}
}

func TestArabicCompanyAndRelevance(t *testing.T) {
	plan := PlanQuery("شركات التكنولوجيا المالية في مصر")
	text := "أعلنت شركة فوري للمدفوعات عن جولة تمويل جديدة لدعم التكنولوجيا المالية في مصر خلال العام الجاري.\nكان الطقس معتدلا في الإسكندرية طوال الأسبوع الماضي مع رياح خفيفة."
	got := ExtractHeuristic(plan, Page{URL: "https://www.youm7.com/story/1", Title: "خبر", Text: text})
	if len(got) != 1 {
		t.Fatalf("want the one on-topic sentence, got %+v", got)
	}
	if got[0].CompanyName != "فوري" || got[0].SignalType != "funding" {
		t.Errorf("Arabic company/signal: %+v", got[0])
	}
}

func TestOnPageChecksTheQuote(t *testing.T) {
	page := "Nile Pay has **raised** $12 million in a [Series A](https://x.test) round."
	if !onPage("Nile Pay has raised $12 million in a Series A round.", page) {
		t.Error("a real quote (with markdown on the page) was not found")
	}
	if onPage("Nile Pay raised $20 million.", page) {
		t.Error("an invented quote passed")
	}
}

func TestJobBoardTitleNamesTheEmployer(t *testing.T) {
	plan := PlanQuery("fintech companies in Egypt hiring engineers")
	got := ExtractHeuristic(plan, Page{URL: "https://www.linkedin.com/jobs/view/1", Title: "mylo hiring Full-Stack Engineer in Cairo, Cairo, Egypt | LinkedIn",
		Snippet: "Posted 12:25:10 PM. mylo is a fintech platform dedicated to helping millions of people in Egypt shop and pay."})
	found := false
	for _, e := range got {
		if e.CompanyName == "mylo" && e.SignalType == "hiring" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the employer in a job title was missed: %+v", got)
	}
	if s := Sentences("1,330 fintech jobs in Egypt were posted on the board this month alone."); len(s) != 1 || !strings.HasPrefix(s[0], "1,330") {
		t.Errorf("a number at the start of a line was taken for a list marker: %q", s)
	}
}

func TestDescribedCompaniesAndDataSites(t *testing.T) {
	cases := map[string]string{
		"As of February 2025, MNT Halan, a startup based in Giza, Egypt, led the fintech sector in funding.": "MNT Halan",
		"Blnk is an Egyptian fintech startup providing instant point-of-sale financing to shoppers.":         "Blnk",
	}
	for s, want := range cases {
		if got, _ := companyIn(s, pageCo{}); got != want {
			t.Errorf("companyIn(%q) = %q, want %q", s, got, want)
		}
	}
	// A data site's article carries the site's name in its title; it is not
	// the company the article is about.
	if pc := pageCompany("https://www.statista.com/statistics/1/fintech-egypt/", "Fintech in Egypt | Statista"); pc.name != "" {
		t.Errorf("a data site became a company: %+v", pc)
	}
	if pc := pageCompany("https://unknownsite.io/reports/2026/egypt", "Egypt report | Unknownsite"); pc.name != "" {
		t.Errorf("an article page on an unknown site became a company: %+v", pc)
	}
	if pc := pageCompany("https://acmepay.io/careers", "Careers | AcmePay"); pc.name != "AcmePay" {
		t.Errorf("a careers page: %+v", pc)
	}
}
