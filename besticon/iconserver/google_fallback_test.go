package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"image/color"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mat/besticon/v3/besticon"
	"github.com/mat/besticon/v3/lettericon"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestGoogleFaviconURL(t *testing.T) {
	got, ok := googleFaviconURL("https://ChatGPT.com/some/path", 64)
	if !ok {
		t.Fatal("expected public domain to use Google fallback")
	}
	assertStringEquals(t, "https://www.google.com/s2/favicons?domain=chatgpt.com&sz=64", got)

	got, ok = googleFaviconURL("chatgpt.com", 0)
	if !ok {
		t.Fatal("expected schemeless public domain to use Google fallback")
	}
	assertStringEquals(t, "https://www.google.com/s2/favicons?domain=chatgpt.com&sz=16", got)
}

func TestGoogleFaviconURLSkipsNonPublicTargets(t *testing.T) {
	tests := []string{
		"http://127.0.0.1:8080/admin",
		"http://10.7.21.1:4965/ui/",
		"http://localhost:3000/",
		"http://printer.local/",
		"http://service.internal/",
		"http://router.home.arpa/",
	}

	for _, target := range tests {
		t.Run(target, func(t *testing.T) {
			if _, ok := googleFaviconURL(target, 64); ok {
				t.Fatalf("expected %q to skip Google fallback", target)
			}
		})
	}
}

func TestPinnedGoogleDefaultHashIsValid(t *testing.T) {
	digest, err := hex.DecodeString(googleDefaultFaviconSHA256)
	if err != nil {
		t.Fatalf("decode pinned hash: %v", err)
	}
	if len(digest) != sha256.Size {
		t.Fatalf("expected %d-byte hash, got %d", sha256.Size, len(digest))
	}
}

func TestGetIconUsesGoogleFallbackInDownloadMode(t *testing.T) {
	t.Setenv("GOOGLE_FAVICON_FALLBACK", "true")
	t.Setenv("SERVER_MODE", "download")

	googleIcon := renderTestIcon(t, "G", 64)
	s := newGoogleFallbackTestServer(googleIcon)
	req := httptest.NewRequest(http.MethodGet, "/icon?url=https%3A%2F%2Fchatgpt.com%2F&size=0..64..64", nil)
	w := httptest.NewRecorder()

	s.iconHandler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	assertStringEquals(t, imagePNG, w.Header().Get(contentType))
	assertStringEquals(t, "", w.Header().Get("Location"))
	if !bytes.Equal(googleIcon, w.Body.Bytes()) {
		wantDigest := sha256.Sum256(googleIcon)
		gotDigest := sha256.Sum256(w.Body.Bytes())
		t.Fatalf("expected Google favicon bytes in response: want len=%d sha256=%x, got len=%d sha256=%x", len(googleIcon), wantDigest, w.Body.Len(), gotDigest)
	}
}

func TestGetIconRejectsGoogleDefaultAndReturnsLetterInDownloadMode(t *testing.T) {
	t.Setenv("GOOGLE_FAVICON_FALLBACK", "true")
	t.Setenv("SERVER_MODE", "download")

	googleDefault := renderTestIcon(t, "?", 16)
	s := newGoogleFallbackTestServer(googleDefault)
	digest := sha256.Sum256(googleDefault)
	s.googleDefaultFaviconSHA256 = hex.EncodeToString(digest[:])
	req := httptest.NewRequest(http.MethodGet, "/icon?url=https%3A%2F%2Fchatgpt.com%2F&size=0..64..64", nil)
	w := httptest.NewRecorder()

	s.iconHandler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	assertStringEquals(t, imagePNG, w.Header().Get(contentType))
	assertStringEquals(t, "", w.Header().Get("Location"))
	if bytes.Equal(googleDefault, w.Body.Bytes()) {
		t.Fatal("expected a generated letter icon instead of Google's default globe")
	}
}

func newGoogleFallbackTestServer(googleIcon []byte) *server {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		status := http.StatusForbidden
		body := []byte("forbidden")
		headers := http.Header{"Content-Type": []string{"text/html"}}
		if req.URL.Hostname() == "www.google.com" {
			status = http.StatusOK
			body = googleIcon
			headers.Set("Content-Type", imagePNG)
		}
		return &http.Response{
			StatusCode: status,
			Header:     headers,
			Body:       io.NopCloser(bytes.NewReader(body)),
			Request:    req,
		}, nil
	})}

	return &server{
		maxIconSize:   500,
		cacheDuration: 720 * time.Hour,
		besticon: besticon.New(
			besticon.WithHTTPClient(client),
			besticon.WithLogger(besticon.NewDefaultLogger(io.Discard)),
			besticon.WithPrivateNetworkProtectionDisabled(),
		),
	}
}

func renderTestIcon(t *testing.T, letter string, size int) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := lettericon.RenderPNG(letter, color.RGBA{R: 10, G: 20, B: 30, A: 255}, size, &out); err != nil {
		t.Fatalf("render test icon: %v", err)
	}
	return out.Bytes()
}
