package main

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/net/publicsuffix"
)

// SHA-256 of the 16x16 default globe returned by Google's S2 favicon service
// for targets such as 127.0.0.1, localhost, and nonexistent domains.
const googleDefaultFaviconSHA256 = "59bfe9bc385ad69f50793ce4a53397316d7a875a7148a63c16df9b674c6cda64"

func (s *server) fetchGoogleFavicon(rawURL string, size int) (string, []byte, bool) {
	if !getTrueFromEnv("GOOGLE_FAVICON_FALLBACK") {
		return "", nil, false
	}

	iconURL, ok := googleFaviconURL(rawURL, size)
	if !ok {
		return "", nil, false
	}

	response, err := s.besticon.Get(iconURL)
	if err != nil {
		return "", nil, false
	}
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		return "", nil, false
	}

	data, err := s.besticon.GetBodyBytes(response)
	if err != nil || len(data) == 0 {
		return "", nil, false
	}
	if http.DetectContentType(data) != imagePNG || s.isGoogleDefaultFavicon(data) {
		return "", nil, false
	}

	return iconURL, data, true
}

func googleFaviconURL(rawURL string, size int) (string, bool) {
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		rawURL = "http://" + rawURL
	}

	target, err := url.Parse(rawURL)
	if err != nil {
		return "", false
	}
	host := strings.ToLower(strings.TrimSuffix(target.Hostname(), "."))
	if !isPublicDomainName(host) {
		return "", false
	}

	if size < 16 {
		size = 16
	}
	query := url.Values{}
	query.Set("domain", host)
	query.Set("sz", strconv.Itoa(size))
	return "https://www.google.com/s2/favicons?" + query.Encode(), true
}

func isPublicDomainName(host string) bool {
	if host == "" || net.ParseIP(host) != nil {
		return false
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") || strings.HasSuffix(host, ".home.arpa") {
		return false
	}
	_, err := publicsuffix.EffectiveTLDPlusOne(host)
	return err == nil
}

func (s *server) isGoogleDefaultFavicon(data []byte) bool {
	expected := s.googleDefaultFaviconSHA256
	if expected == "" {
		expected = googleDefaultFaviconSHA256
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]) == expected
}
