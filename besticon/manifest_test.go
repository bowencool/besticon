package besticon

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchIconsFindsManifestIcon(t *testing.T) {
	iconData := mustReadFile("testdata/favicon.ico")
	mux := http.NewServeMux()
	mux.HandleFunc("/ui/zashboard/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ui/zashboard/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head><link rel="manifest" href="./manifest.webmanifest"></head></html>`))
	})
	mux.HandleFunc("/ui/zashboard/manifest.webmanifest", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/manifest+json")
		_, _ = w.Write([]byte(`{"icons":[{"src":"manifest-icon.ico"}]}`))
	})
	mux.HandleFunc("/ui/zashboard/manifest-icon.ico", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/x-icon")
		_, _ = w.Write(iconData)
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	b := New(
		WithHTTPClient(server.Client()),
		WithPrivateNetworkProtectionDisabled(),
	)
	icons, err := b.NewIconFinder().FetchIcons(server.URL + "/ui/zashboard/")
	if err != nil {
		t.Fatal(err)
	}
	if len(icons) != 1 {
		t.Fatalf("FetchIcons() returned %d icons, want 1: %v", len(icons), icons)
	}
	expectedURL := server.URL + "/ui/zashboard/manifest-icon.ico"
	if icons[0].URL != expectedURL {
		t.Errorf("FetchIcons()[0].URL = %q, want %q", icons[0].URL, expectedURL)
	}
}
