/** The gallery home's shelf order (MH-527) — brand keys grouped the way a person looks for them. */
export const SHELVES: { labelKey: string; brands: string[] }[] = [
  { labelKey: "presets.shelf.google", brands: ["google", "google-news", "google-trends", "gmail"] },
  { labelKey: "presets.shelf.search", brands: ["bing", "duckduckgo", "brave", "yahoo"] },
  {
    labelKey: "presets.shelf.news",
    brands: ["theverge", "techcrunch", "arstechnica", "wired", "technologyreview", "infoq", "github", "devto", "hackernews", "medium", "techwd", "majarra", "hbrarabic", "mittrarabia"],
  },
  { labelKey: "presets.shelf.jobs", brands: ["weworkremotely", "remoteok", "remotive", "himalayas", "wellfound", "greenhouse", "lever", "ashby", "larajobs"] },
  { labelKey: "presets.shelf.social", brands: ["youtube", "x", "facebook", "bluesky", "instagram", "linkedin", "threads", "tiktok"] },
  { labelKey: "presets.shelf.messaging", brands: ["whatsapp", "telegram"] },
  { labelKey: "presets.shelf.other", brands: ["web"] },
]
