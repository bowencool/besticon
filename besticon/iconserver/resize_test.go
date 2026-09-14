package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/mat/besticon/v3/besticon"
)

func TestIconHandlerResizesOversizedIconInsteadOfFallingBack(t *testing.T) {
	t.Setenv("SERVER_MODE", "download")

	iconData := testPNG(t, 1024, 1024)
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, `<link rel="icon" type="image/png" href="/pwa/icon.png">`)
		case "/pwa/icon.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(iconData)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(site.Close)

	s := newTestServer()
	s.besticon = besticon.New(
		besticon.WithPrivateNetworkProtectionDisabled(),
		besticon.WithLogger(besticon.NewDefaultLogger(io.Discard)),
	)
	req := httptest.NewRequest(http.MethodGet, "/icon?url="+url.QueryEscape(site.URL)+"&size=0..64..500", nil)
	w := httptest.NewRecorder()

	s.iconHandler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	if contentType := w.Header().Get("Content-Type"); contentType != "image/png" {
		t.Fatalf("expected image/png, got %q", contentType)
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(w.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if format != "png" || config.Width != 500 || config.Height != 500 {
		t.Fatalf("expected 500x500 png, got %dx%d %s", config.Width, config.Height, format)
	}
}

func TestResizeIconDataLeavesIconsWithinMaxUnchanged(t *testing.T) {
	data := testPNG(t, 32, 24)
	icon := &besticon.Icon{Width: 32, Height: 24, Format: "png", ImageData: data}

	actual, resized, err := resizeIconData(icon, 64)
	if err != nil {
		t.Fatal(err)
	}
	if resized {
		t.Fatal("icon within Max was unexpectedly resized")
	}
	if !bytes.Equal(actual, data) {
		t.Fatal("icon within Max was unexpectedly re-encoded")
	}
}

func TestResizeIconDataShrinksOversizedIconToMax(t *testing.T) {
	data := testPNG(t, 200, 100)
	icon := &besticon.Icon{Width: 200, Height: 100, Format: "png", ImageData: data}

	actual, resized, err := resizeIconData(icon, 64)
	if err != nil {
		t.Fatal(err)
	}
	if !resized {
		t.Fatal("oversized icon was not resized")
	}

	config, format, err := image.DecodeConfig(bytes.NewReader(actual))
	if err != nil {
		t.Fatal(err)
	}
	if format != "png" {
		t.Fatalf("expected png output, got %q", format)
	}
	if config.Width != 64 || config.Height != 32 {
		t.Fatalf("expected 64x32 output, got %dx%d", config.Width, config.Height)
	}
}

func TestSmallestOversizedIcon(t *testing.T) {
	icons := []besticon.Icon{
		{URL: "normal", Width: 64, Height: 64, Format: "png", Bytes: 100},
		{URL: "large", Width: 2048, Height: 2048, Format: "png", Bytes: 200},
		{URL: "unsafe", Width: maxResizeSourceDimension + 1, Height: 1, Format: "png", Bytes: 1},
		{URL: "smallest-large", Width: 1024, Height: 1024, Format: "png", Bytes: 300},
		{URL: "vector", Width: 9999, Height: 9999, Format: "svg", Bytes: 50},
	}

	actual := smallestOversizedIcon(icons, besticon.SizeRange{Min: 0, Perfect: 64, Max: 500})
	if actual == nil {
		t.Fatal("expected an oversized icon")
	}
	if actual.URL != "smallest-large" {
		t.Fatalf("expected smallest oversized icon, got %q", actual.URL)
	}
}

func TestResizeIconDataRejectsUnsafeSourceDimensions(t *testing.T) {
	icon := &besticon.Icon{
		Width:     maxResizeSourceDimension + 1,
		Height:    1,
		Format:    "png",
		ImageData: []byte("not decoded because dimensions are rejected first"),
	}

	if _, resized, err := resizeIconData(icon, 64); err == nil || resized {
		t.Fatal("unsafe source dimensions were not rejected")
	}
}

func testPNG(t *testing.T, width, height int) []byte {
	t.Helper()

	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 42, G: 99, B: 201, A: 255})
		}
	}

	var output bytes.Buffer
	if err := png.Encode(&output, img); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}
