package trends

/*
The source catalog (MH-387): sources a workspace picks from in one click,
instead of hunting for feed URLs and pasting them.

Ported from fadymondy.com db/tech_trends.postgres.sql (the seeded sources) and
internal/trends/tiers.go (their feed URLs), widened past technology: Moharrik's
workspaces are businesses — a coffee roaster watches coffee trade press and
the business pages, in English and Arabic. Every entry was fetched live on
2026-09-23 and answered 200 with a parseable feed (or, for the two API
sources, JSON). Browser-only sources — Google Trends Explore, X trends,
Majarra — are left out until the automation plugins can collect them.

The catalog is code, not a table: it is the same for every workspace, and a
change to it ships with the collector that understands it. A workspace's copy
is an ordinary feed_sources row carrying catalog_key, so renaming it or
changing its interval never touches the catalog.
*/

// FeedCatalogEntry is one source a workspace can add.
type FeedCatalogEntry struct {
	Key         string            `json:"key"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Category    string            `json:"category" enum:"technology,business,world,industry"`
	Language    string            `json:"language" enum:"en,ar"`
	Provider    string            `json:"provider" enum:"feed,hn,devto"`
	URL         string            `json:"url" doc:"The feed, or for an API source the endpoint it reads."`
	Params      map[string]string `json:"params,omitempty"`
	// Interval is the suggested schedule in minutes; fast-moving lists are
	// read more often than trade press that posts twice a day.
	Interval int `json:"interval_minutes"`
}

// Catalog is every source on offer, in the order the page lists them.
var Catalog = []FeedCatalogEntry{
	// ---- technology -------------------------------------------------------------
	{Key: "hackernews", Name: "Hacker News", Description: "The front page, with each story's rank and points (API).", Category: "technology", Language: "en",
		Provider: ProviderHN, Params: map[string]string{"list": "topstories", "limit": "30"}, Interval: 360},
	{Key: "devto-top-week", Name: "DEV: top this week", Description: "The week's most-reacted DEV articles (API).", Category: "technology", Language: "en",
		Provider: ProviderDevto, Params: map[string]string{"top": "7", "limit": "30"}, Interval: 720},
	{Key: "techcrunch", Name: "TechCrunch", Description: "Startups, funding and technology news.", Category: "technology", Language: "en",
		Provider: ProviderFeed, URL: "https://techcrunch.com/feed/", Interval: 360},
	{Key: "theverge", Name: "The Verge", Description: "Technology, science and culture.", Category: "technology", Language: "en",
		Provider: ProviderFeed, URL: "https://www.theverge.com/rss/index.xml", Interval: 360},
	{Key: "arstechnica", Name: "Ars Technica", Description: "Technology, science and policy in depth.", Category: "technology", Language: "en",
		Provider: ProviderFeed, URL: "https://feeds.arstechnica.com/arstechnica/index", Interval: 720},
	{Key: "wired", Name: "WIRED", Description: "Technology and its effect on business and culture.", Category: "technology", Language: "en",
		Provider: ProviderFeed, URL: "https://www.wired.com/feed/rss", Interval: 720},
	{Key: "technologyreview", Name: "MIT Technology Review", Description: "Emerging technology, especially AI.", Category: "technology", Language: "en",
		Provider: ProviderFeed, URL: "https://www.technologyreview.com/feed/", Interval: 1440},
	{Key: "bbc-technology", Name: "BBC News: Technology", Description: "The BBC's technology desk.", Category: "technology", Language: "en",
		Provider: ProviderFeed, URL: "https://feeds.bbci.co.uk/news/technology/rss.xml", Interval: 720},
	{Key: "infoq", Name: "InfoQ", Description: "Software engineering and architecture.", Category: "technology", Language: "en",
		Provider: ProviderFeed, URL: "https://feed.infoq.com/", Interval: 1440},
	{Key: "github-blog", Name: "The GitHub Blog", Description: "GitHub product and engineering news.", Category: "technology", Language: "en",
		Provider: ProviderFeed, URL: "https://github.blog/feed/", Interval: 1440},
	{Key: "medium-ai", Name: "Medium: AI", Description: "Recent Medium posts tagged artificial intelligence.", Category: "technology", Language: "en",
		Provider: ProviderFeed, URL: "https://medium.com/feed/tag/artificial-intelligence", Interval: 1440},
	{Key: "google-news-technology", Name: "Google News: Technology", Description: "Technology headlines from many publishers.", Category: "technology", Language: "en",
		Provider: ProviderFeed, URL: "https://news.google.com/rss/headlines/section/topic/TECHNOLOGY?hl=en-US&gl=US&ceid=US:en",
		Params: map[string]string{"outlet_suffix": "1"}, Interval: 360},
	{Key: "techwd", Name: "عالم التقنية", Description: "أخبار التقنية بالعربية.", Category: "technology", Language: "ar",
		Provider: ProviderFeed, URL: "https://www.tech-wd.com/wd/feed/", Interval: 720},
	{Key: "aitnews", Name: "البوابة العربية للأخبار التقنية", Description: "أخبار التقنية والذكاء الاصطناعي بالعربية.", Category: "technology", Language: "ar",
		Provider: ProviderFeed, URL: "https://www.aitnews.com/feed/", Interval: 720},

	// ---- business -----------------------------------------------------------------
	{Key: "bbc-business", Name: "BBC News: Business", Description: "The BBC's business desk.", Category: "business", Language: "en",
		Provider: ProviderFeed, URL: "https://feeds.bbci.co.uk/news/business/rss.xml", Interval: 360},
	{Key: "guardian-business", Name: "The Guardian: Business", Description: "Business, economics and markets.", Category: "business", Language: "en",
		Provider: ProviderFeed, URL: "https://www.theguardian.com/uk/business/rss", Interval: 360},
	{Key: "npr-business", Name: "NPR: Business", Description: "US business and economy.", Category: "business", Language: "en",
		Provider: ProviderFeed, URL: "https://feeds.npr.org/1006/rss.xml", Interval: 720},
	{Key: "forbes-business", Name: "Forbes: Business", Description: "Companies, markets and leadership.", Category: "business", Language: "en",
		Provider: ProviderFeed, URL: "https://www.forbes.com/business/feed/", Interval: 720},
	{Key: "google-news-business-eg", Name: "أخبار Google: الأعمال (مصر)", Description: "عناوين الاقتصاد والأعمال من ناشرين كثيرين.", Category: "business", Language: "ar",
		Provider: ProviderFeed, URL: "https://news.google.com/rss/headlines/section/topic/BUSINESS?hl=ar&gl=EG&ceid=EG:ar",
		Params: map[string]string{"outlet_suffix": "1"}, Interval: 360},

	// ---- world ----------------------------------------------------------------------
	{Key: "bbc-world", Name: "BBC News: World", Description: "The BBC's world desk.", Category: "world", Language: "en",
		Provider: ProviderFeed, URL: "https://feeds.bbci.co.uk/news/world/rss.xml", Interval: 360},
	{Key: "wsj-world", Name: "The Wall Street Journal: World", Description: "World news headlines.", Category: "world", Language: "en",
		Provider: ProviderFeed, URL: "https://feeds.a.dj.com/rss/RSSWorldNews.xml", Interval: 720},
	{Key: "bbc-arabic", Name: "بي بي سي عربي", Description: "أخبار العالم العربي والعالم.", Category: "world", Language: "ar",
		Provider: ProviderFeed, URL: "https://feeds.bbci.co.uk/arabic/rss.xml", Interval: 360},
	{Key: "dw-arabic", Name: "DW عربية", Description: "أخبار ألمانيا وأوروبا والعالم العربي.", Category: "world", Language: "ar",
		Provider: ProviderFeed, URL: "https://rss.dw.com/xml/rss-ar-all", Interval: 720},
	{Key: "cnn-arabic", Name: "CNN بالعربية", Description: "أخبار الشرق الأوسط والعالم.", Category: "world", Language: "ar",
		Provider: ProviderFeed, URL: "https://arabic.cnn.com/api/v1/rss/rss.xml", Interval: 360},

	// ---- industry: coffee -----------------------------------------------------------
	{Key: "dailycoffeenews", Name: "Daily Coffee News", Description: "Coffee roasting and café business news.", Category: "industry", Language: "en",
		Provider: ProviderFeed, URL: "https://dailycoffeenews.com/feed/", Interval: 720},
	{Key: "sprudge", Name: "Sprudge", Description: "Coffee culture and the café trade.", Category: "industry", Language: "en",
		Provider: ProviderFeed, URL: "https://sprudge.com/feed", Interval: 1440},
	{Key: "perfectdailygrind", Name: "Perfect Daily Grind", Description: "Coffee supply chain, farming and roasting.", Category: "industry", Language: "en",
		Provider: ProviderFeed, URL: "https://www.perfectdailygrind.com/feed/", Interval: 1440},
}

func init() {
	for i := range Catalog {
		if Catalog[i].Provider != ProviderFeed {
			Catalog[i].URL = APIURL(Catalog[i].Provider, Catalog[i].Params)
		}
	}
}

// CatalogEntryFor finds an entry by key.
func CatalogEntryFor(key string) (FeedCatalogEntry, bool) {
	for _, e := range Catalog {
		if e.Key == key {
			return e, true
		}
	}
	return FeedCatalogEntry{}, false
}
