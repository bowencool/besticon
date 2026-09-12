package besticon

import (
	"encoding/json"
	"maps"
	"net/url"
	"slices"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

const maxManifestLinks = 4

type webManifest struct {
	Icons []struct {
		Src string `json:"src"`
	} `json:"icons"`
}

func findManifestLinks(siteURL *url.URL, html []byte) ([]string, error) {
	doc, err := docFromHTML(html)
	if err != nil {
		return nil, err
	}

	baseURL := determineBaseURL(siteURL, doc)
	links := make(map[string]empty)
	for _, href := range extractManifestTags(doc) {
		absoluteURL, err := resolveURLReference(baseURL, href)
		if err == nil {
			links[absoluteURL] = empty{}
		}
	}
	return slices.Sorted(maps.Keys(links)), nil
}

func extractManifestTags(doc *goquery.Document) []string {
	var links []string
	doc.Find("link[href][rel]").Each(func(_ int, selection *goquery.Selection) {
		rel, _ := selection.Attr("rel")
		if !slices.Contains(strings.Fields(strings.ToLower(rel)), "manifest") {
			return
		}
		href, _ := selection.Attr("href")
		if href != "" {
			links = append(links, href)
		}
	})
	return links
}

func (b *Besticon) findManifestIconLinks(siteURL *url.URL, html []byte) []string {
	manifestLinks, err := findManifestLinks(siteURL, html)
	if err != nil {
		return nil
	}
	if len(manifestLinks) > maxManifestLinks {
		manifestLinks = manifestLinks[:maxManifestLinks]
	}

	var iconLinks []string
	for _, manifestLink := range manifestLinks {
		iconLinks = append(iconLinks, b.fetchManifestIconLinks(manifestLink)...)
	}
	return uniqueLinks(iconLinks)
}

func (b *Besticon) fetchManifestIconLinks(manifestURL string) []string {
	response, err := b.Get(manifestURL)
	if err != nil {
		return nil
	}
	body, err := b.GetBodyBytes(response)
	if err != nil || response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil
	}

	var manifest webManifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return nil
	}

	baseURL, err := url.Parse(manifestURL)
	if err != nil {
		return nil
	}
	if response.Request != nil && response.Request.URL != nil {
		baseURL = response.Request.URL
	}
	var links []string
	for _, icon := range manifest.Icons {
		if icon.Src == "" {
			continue
		}
		absoluteURL, err := resolveURLReference(baseURL, icon.Src)
		if err == nil {
			links = append(links, absoluteURL)
		}
	}
	return uniqueLinks(links)
}

func uniqueLinks(linkSets ...[]string) []string {
	links := make(map[string]empty)
	for _, linkSet := range linkSets {
		for _, link := range linkSet {
			links[link] = empty{}
		}
	}
	return slices.Sorted(maps.Keys(links))
}
