package phantom

import "math/rand"

// Site represents a target website for phantom browsing.
type Site struct {
	Domain  string
	Weight  int      // visit probability weight
	Paths   []string // URL paths to visit
	Pattern BrowsePattern
}

// RandomURL returns a random URL for this site.
func (s *Site) RandomURL(rng *rand.Rand) string {
	path := "/"
	if len(s.Paths) > 0 {
		path = s.Paths[rng.Intn(len(s.Paths))]
	}
	return "https://" + s.Domain + path
}

// RegionalProfile defines browsing targets for a geographic region.
type RegionalProfile struct {
	Name           string
	Sites          []Site
	TotalWeight    int
	AcceptLanguage string
}

// PickSite returns a weighted random site from the profile.
func (p *RegionalProfile) PickSite(rng *rand.Rand) *Site {
	r := rng.Intn(p.TotalWeight)
	cumulative := 0
	for i := range p.Sites {
		cumulative += p.Sites[i].Weight
		if r < cumulative {
			return &p.Sites[i]
		}
	}
	return &p.Sites[0]
}

// ProfileByRegion returns the regional profile by name.
func ProfileByRegion(region string) *RegionalProfile {
	switch region {
	case "turkey":
		return profileTurkey
	case "europe":
		return profileEurope
	default:
		return profileGlobal
	}
}

var profileTurkey = buildProfile("turkey", "tr-TR,tr;q=0.9,en-US;q=0.8,en;q=0.7", []Site{
	{Domain: "google.com.tr", Weight: 20, Pattern: PatternSearchBrowse, Paths: []string{
		"/", "/search?q=hava+durumu", "/search?q=haberler", "/search?q=puan+durumu",
		"/search?q=doviz+kuru", "/search?q=altin+fiyatlari",
	}},
	{Domain: "youtube.com", Weight: 18, Pattern: PatternVideoWatch, Paths: []string{
		"/", "/feed/trending", "/feed/subscriptions", "/results?search_query=muzik",
	}},
	{Domain: "hurriyet.com.tr", Weight: 10, Pattern: PatternNewsRead, Paths: []string{
		"/", "/gundem/", "/ekonomi/", "/spor/", "/teknoloji/",
	}},
	{Domain: "trendyol.com", Weight: 12, Pattern: PatternShoppingBrowse, Paths: []string{
		"/", "/butpijamaalt", "/sr?q=telefon", "/erkek-giyim",
	}},
	{Domain: "eksisozluk.com", Weight: 8, Pattern: PatternSocialScroll, Paths: []string{
		"/", "/debe", "/basliklar/gundem",
	}},
	{Domain: "instagram.com", Weight: 10, Pattern: PatternSocialScroll, Paths: []string{
		"/", "/explore/", "/reels/",
	}},
	{Domain: "twitter.com", Weight: 8, Pattern: PatternSocialScroll, Paths: []string{
		"/", "/explore", "/search",
	}},
	{Domain: "hepsiburada.com", Weight: 7, Pattern: PatternShoppingBrowse, Paths: []string{
		"/", "/laptoplar-c-98", "/cep-telefonlari-c-371965",
	}},
	{Domain: "sahibinden.com", Weight: 5, Pattern: PatternShoppingBrowse, Paths: []string{
		"/", "/kategori/vasita", "/kategori/emlak",
	}},
	{Domain: "milliyet.com.tr", Weight: 2, Pattern: PatternNewsRead, Paths: []string{
		"/", "/gundem/", "/spor/",
	}},
})

var profileEurope = buildProfile("europe", "en-GB,en;q=0.9,de;q=0.8,fr;q=0.7", []Site{
	{Domain: "google.com", Weight: 20, Pattern: PatternSearchBrowse, Paths: []string{
		"/", "/search?q=weather", "/search?q=news+today", "/search?q=euro+exchange+rate",
	}},
	{Domain: "youtube.com", Weight: 15, Pattern: PatternVideoWatch, Paths: []string{
		"/", "/feed/trending", "/feed/subscriptions",
	}},
	{Domain: "bbc.com", Weight: 8, Pattern: PatternNewsRead, Paths: []string{
		"/", "/news", "/sport", "/weather",
	}},
	{Domain: "amazon.de", Weight: 10, Pattern: PatternShoppingBrowse, Paths: []string{
		"/", "/gp/bestsellers/", "/gp/new-releases/",
	}},
	{Domain: "reddit.com", Weight: 12, Pattern: PatternSocialScroll, Paths: []string{
		"/", "/r/all/", "/r/europe/", "/r/worldnews/",
	}},
	{Domain: "instagram.com", Weight: 8, Pattern: PatternSocialScroll, Paths: []string{
		"/", "/explore/",
	}},
	{Domain: "twitter.com", Weight: 7, Pattern: PatternSocialScroll, Paths: []string{
		"/", "/explore",
	}},
	{Domain: "theguardian.com", Weight: 5, Pattern: PatternNewsRead, Paths: []string{
		"/", "/world", "/uk-news",
	}},
	{Domain: "wikipedia.org", Weight: 8, Pattern: PatternNewsRead, Paths: []string{
		"/wiki/Main_Page", "/wiki/Special:Random",
	}},
	{Domain: "github.com", Weight: 7, Pattern: PatternSearchBrowse, Paths: []string{
		"/", "/trending", "/explore",
	}},
})

var profileGlobal = buildProfile("global", "en-US,en;q=0.9", []Site{
	{Domain: "google.com", Weight: 20, Pattern: PatternSearchBrowse, Paths: []string{
		"/", "/search?q=weather", "/search?q=latest+news",
	}},
	{Domain: "youtube.com", Weight: 18, Pattern: PatternVideoWatch, Paths: []string{
		"/", "/feed/trending", "/results?search_query=music",
	}},
	{Domain: "github.com", Weight: 10, Pattern: PatternSearchBrowse, Paths: []string{
		"/", "/trending", "/explore", "/topics",
	}},
	{Domain: "stackoverflow.com", Weight: 8, Pattern: PatternSearchBrowse, Paths: []string{
		"/", "/questions", "/questions?tab=Active",
	}},
	{Domain: "reddit.com", Weight: 12, Pattern: PatternSocialScroll, Paths: []string{
		"/", "/r/all/", "/r/technology/", "/r/programming/",
	}},
	{Domain: "twitter.com", Weight: 8, Pattern: PatternSocialScroll, Paths: []string{
		"/", "/explore",
	}},
	{Domain: "amazon.com", Weight: 8, Pattern: PatternShoppingBrowse, Paths: []string{
		"/", "/gp/bestsellers/", "/gp/new-releases/",
	}},
	{Domain: "wikipedia.org", Weight: 6, Pattern: PatternNewsRead, Paths: []string{
		"/wiki/Main_Page", "/wiki/Special:Random",
	}},
	{Domain: "instagram.com", Weight: 5, Pattern: PatternSocialScroll, Paths: []string{
		"/", "/explore/",
	}},
	{Domain: "linkedin.com", Weight: 5, Pattern: PatternSocialScroll, Paths: []string{
		"/", "/feed/", "/jobs/",
	}},
})

func buildProfile(name, lang string, sites []Site) *RegionalProfile {
	total := 0
	for _, s := range sites {
		total += s.Weight
	}
	return &RegionalProfile{
		Name:           name,
		Sites:          sites,
		TotalWeight:    total,
		AcceptLanguage: lang,
	}
}
