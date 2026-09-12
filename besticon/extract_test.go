package besticon

import (
	"net/url"
	"slices"
	"sort"
	"strings"
	"testing"
)

func mustFindIconLinks(html []byte) []string {
	doc, e := docFromHTML(html)
	check(e)
	links := extractIconTags(doc)
	sort.Strings(links)
	return links
}

func TestFindIconLinksIncludesPageDirectoryDefaults(t *testing.T) {
	siteURL, err := url.Parse("http://10.7.21.1:4965/ui/zashboard/#/proxies")
	if err != nil {
		t.Fatal(err)
	}

	links, err := findIconLinks(siteURL, []byte("<html><head></head></html>"))
	if err != nil {
		t.Fatal(err)
	}

	for _, expected := range []string{
		"http://10.7.21.1:4965/favicon.ico",
		"http://10.7.21.1:4965/ui/zashboard/favicon.ico",
	} {
		if !slices.Contains(links, expected) {
			t.Errorf("findIconLinks() = %v, want it to contain %q", links, expected)
		}
	}
	for _, link := range links {
		if strings.Contains(link, "#") {
			t.Errorf("findIconLinks() returned URL with fragment: %q", link)
		}
	}
}

func TestDetermineBaseURL(t *testing.T) {
	siteURL, err := url.Parse("http://example.com/ui/app/")
	if err != nil {
		t.Fatal(err)
	}

	tests := map[string]string{
		"//cdn.example.com/assets/":      "http://cdn.example.com/assets/",
		"/assets/":                       "http://example.com/assets/",
		"assets/":                        "http://example.com/ui/app/assets/",
		"https://cdn.example.com/icons/": "https://cdn.example.com/icons/",
	}
	for href, expected := range tests {
		doc, err := docFromHTML([]byte("<html><head><base href=\"" + href + "\"></head></html>"))
		if err != nil {
			t.Fatal(err)
		}
		if actual := determineBaseURL(siteURL, doc).String(); actual != expected {
			t.Errorf("determineBaseURL(%q) = %q, want %q", href, actual, expected)
		}
	}
}

func TestLinkExtraction(t *testing.T) {
	// invalid links
	invalid := []string{
		"<link rel='nope'>",
		"<link rel='icon'>",
		"<link rel='icon' href=''>",
		"<link rel='mask-icon' href='a.png'>",
		"<link rel='xxiconxx' href='a.png'>",
	}
	for _, html := range invalid {
		links := mustFindIconLinks([]byte(html))
		if len(links) != 0 {
			t.Fatalf("%s shouldn't contain links", html)
		}
	}

	// test rel case
	valid := []string{
		"<link rel='icon' href='xx'>",
		"<link rel='shortcut icon' href='xx'>",
		"<link REL='Shortcut Icon' href='xx'>",
		"<link rel='apple-touch-icon' href='xx'>",
		"<link rel='apple-touch-icon-precomposed' href='xx'>",
	}
	for _, html := range valid {
		links := mustFindIconLinks([]byte(html))
		if len(links) != 1 {
			t.Fatalf("%s should contain one link", html)
		}
	}

	// test some files
	links := mustFindIconLinks(mustReadFile("testdata/daringfireball.html"))
	assertEquals(t, []string{
		"/graphics/apple-touch-icon.png",
		"/graphics/favicon.ico?v=005",
	}, links)
	links = mustFindIconLinks(mustReadFile("testdata/newyorker.html"))
	assertEquals(t, []string{
		"/wp-content/assets/dist/img/icon/apple-touch-icon-114x114-precomposed.png",
		"/wp-content/assets/dist/img/icon/apple-touch-icon-144x144-precomposed.png",
		"/wp-content/assets/dist/img/icon/apple-touch-icon-57x57-precomposed.png",
		"/wp-content/assets/dist/img/icon/apple-touch-icon-precomposed.png",
		"/wp-content/assets/dist/img/icon/apple-touch-icon.png",
		"/wp-content/assets/dist/img/icon/favicon.ico",
	}, links)
}
