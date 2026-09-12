package besticon

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

var iconPaths = []string{
	"/favicon.ico",
	"/apple-touch-icon.png",
	"/apple-touch-icon-precomposed.png",
}

const (
	favIcon                   = "icon"
	appleTouchIcon            = "apple-touch-icon"
	appleTouchIconPrecomposed = "apple-touch-icon-precomposed"
)

type empty struct{}

// Find all icons in this html. We use siteURL as the base url unless we detect
// another base url in <head>
func findIconLinks(siteURL *url.URL, html []byte) ([]string, error) {
	doc, e := docFromHTML(html)
	if e != nil {
		return nil, e
	}

	baseURL := determineBaseURL(siteURL, doc)

	// Use a map to avoid dups
	links := make(map[string]empty)

	// Add common, hard coded icon paths
	for _, path := range iconPaths {
		links[urlFromBase(baseURL, path)] = empty{}
	}

	// Apps mounted below the origin root often keep their favicon beside the
	// page (for example /ui/app/favicon.ico). Probe that directory as well as
	// the traditional root paths. Treat a path without a trailing slash as an
	// application directory too; the extra candidates are harmless when it is
	// actually a document.
	directoryURL := pageDirectoryURL(baseURL)
	for _, path := range iconPaths {
		absoluteURL, e := resolveURLReference(directoryURL, strings.TrimPrefix(path, "/"))
		if e == nil {
			links[absoluteURL] = empty{}
		}
	}

	// Add icons found in page
	urls := extractIconTags(doc)
	for _, u := range urls {
		absoluteURL, e := absoluteURL(baseURL, u)
		if e == nil {
			links[absoluteURL] = empty{}
		}
	}

	// Turn unique keys into a sorted array.
	return slices.Sorted(maps.Keys(links)), nil
}

func pageDirectoryURL(baseURL *url.URL) *url.URL {
	u := *baseURL
	u.RawQuery = ""
	u.ForceQuery = false
	u.Fragment = ""
	if u.Path == "" {
		u.Path = "/"
	} else if !strings.HasSuffix(u.Path, "/") {
		u.Path += "/"
	}
	return &u
}

func resolveURLReference(baseURL *url.URL, reference string) (string, error) {
	referenceURL, err := url.Parse(reference)
	if err != nil {
		return "", err
	}
	base := *baseURL
	if base.Scheme == "" {
		base.Scheme = "http"
	}
	return base.ResolveReference(referenceURL).String(), nil
}

// What is the baseURL for this doc?
func determineBaseURL(siteURL *url.URL, doc *goquery.Document) *url.URL {
	baseTagHref := extractBaseTag(doc)
	if baseTagHref == "" {
		return siteURL
	}

	baseTagURL, err := url.Parse(baseTagHref)
	if err != nil {
		return siteURL
	}
	return siteURL.ResolveReference(baseTagURL)
}

// Convert bytes => doc
func docFromHTML(html []byte) (*goquery.Document, error) {
	doc, e := goquery.NewDocumentFromReader(bytes.NewReader(html))
	if e != nil || doc == nil {
		return nil, errParseHTML
	}
	return doc, nil
}

var errParseHTML = errors.New("besticon: could not parse html")

// Find <head><base href="xxx">
func extractBaseTag(doc *goquery.Document) string {
	href := ""
	doc.Find("head base[href]").First().Each(func(i int, s *goquery.Selection) {
		href, _ = s.Attr("href")
	})
	return href
}

var (
	iconTypes   = []string{favIcon, appleTouchIcon, appleTouchIconPrecomposed}
	iconTypesRe = regexp.MustCompile(fmt.Sprintf("^(%s)$", strings.Join(regexpQuoteMetaArray(iconTypes), "|")))
)

// Find icons from doc using goquery
func extractIconTags(doc *goquery.Document) []string {
	var hits []string
	doc.Find("link[href][rel]").Each(func(i int, s *goquery.Selection) {
		href := extractIconTag(s)
		if href != "" {
			hits = append(hits, href)
		}
	})
	return hits
}

func extractIconTag(s *goquery.Selection) string {
	// What sort of iconType is in this <rel>?
	rel, _ := s.Attr("rel")
	if rel == "" {
		return ""
	}
	rel = strings.ToLower(rel)

	var iconType string
	for i := range strings.FieldsSeq(rel) {
		if iconTypesRe.MatchString(i) {
			iconType = i
			break
		}
	}
	if iconType == "" {
		return ""
	}

	href, _ := s.Attr("href")
	if href == "" {
		return ""
	}

	return href
}

// regexp.QuoteMeta an array of strings
func regexpQuoteMetaArray(a []string) []string {
	quoted := make([]string, len(a))
	for i, s := range a {
		quoted[i] = regexp.QuoteMeta(s)
	}
	return quoted
}
