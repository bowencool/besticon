package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"image"
	"image/draw"
	"image/png"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mat/besticon/v3/besticon"
	"github.com/mat/besticon/v3/besticon/iconserver/assets"
	"github.com/mat/besticon/v3/lettericon"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/cors"
	xdraw "golang.org/x/image/draw"
)

type server struct {
	maxIconSize                int
	cacheDuration              time.Duration
	demoSites                  []string
	hostOnlyDomains            []string
	googleDefaultFaviconSHA256 string

	besticon *besticon.Besticon
}

func (s *server) indexHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "" || r.URL.Path == "/" {
		renderHTMLTemplate(w, 200, templateFromAsset("index.html", "index.html"), pageInfo{DemoSites: s.demoSites})
	} else {
		renderHTMLTemplate(w, 404, templateFromAsset("not_found.html", "not_found.html"), nil)
	}
}

func (s *server) iconsHandler(w http.ResponseWriter, r *http.Request) {
	url, err := s.demoURLFromRequest(r)
	if err != nil {
		renderHTMLTemplate(w, 400, templateFromAsset("icons.html", "icons.html"), pageInfo{
			URL:       r.FormValue(urlParam),
			Error:     err,
			DemoSites: s.demoSites,
		})
		return
	}
	if len(url) == 0 {
		http.Redirect(w, r, "/", 302)
		return
	}

	finder := s.newIconFinder()

	formats := r.FormValue("formats")
	if formats != "" {
		finder.FormatsAllowed = strings.Split(r.FormValue("formats"), ",")
	}

	icons, e := finder.FetchIcons(url)
	switch {
	case e != nil:
		renderHTMLTemplate(w, 404, templateFromAsset("icons.html", "icons.html"), pageInfo{URL: url, Error: e, DemoSites: s.demoSites})
	case len(icons) == 0:
		errNoIcons := errors.New("this poor site has no icons at all :-(")
		renderHTMLTemplate(w, 404, templateFromAsset("icons.html", "icons.html"), pageInfo{URL: url, Error: errNoIcons, DemoSites: s.demoSites})
	default:
		addCacheControl(w, s.cacheDuration)
		renderHTMLTemplate(w, 200, templateFromAsset("icons.html", "icons.html"), pageInfo{Icons: icons, URL: url, DemoSites: s.demoSites})
	}
}

func (s *server) iconHandler(w http.ResponseWriter, r *http.Request) {
	url, err := s.demoURLFromRequest(r)
	if err != nil {
		writeAPIError(w, 400, err)
		return
	}
	if len(url) == 0 {
		writeAPIError(w, 400, errors.New("need url parameter"))
		return
	}

	sizeRange, err := besticon.ParseSizeRange(r.FormValue("size"), s.maxIconSize)
	if err != nil {
		writeAPIError(w, 400, errors.New("bad size parameter"))
		return
	}

	finder := s.newIconFinder()
	formats := r.FormValue("formats")
	if formats != "" {
		finder.FormatsAllowed = strings.Split(r.FormValue("formats"), ",")
	}

	finder.FetchIcons(url)

	icon := finder.IconInSizeRange(*sizeRange)
	if icon != nil {
		s.returnIcon(w, r, icon.URL)
		return
	}

	// Redirect mode cannot resize an upstream icon. In download mode, use the
	// smallest otherwise suitable raster icon above Max and shrink it to Max.
	// Icons already inside the requested range keep the existing fast path and
	// are returned byte-for-byte without decoding or re-encoding.
	if os.Getenv("SERVER_MODE") == "download" {
		oversizedIcon := smallestOversizedIcon(finder.Icons(), *sizeRange)
		if oversizedIcon != nil && s.returnResizedIcon(w, oversizedIcon, sizeRange.Max) {
			return
		}
	}

	fallbackIconURL := r.FormValue("fallback_icon_url")
	if fallbackIconURL != "" {
		s.returnIcon(w, r, fallbackIconURL)
		return
	}

	if googleIconURL, googleIconData, ok := s.fetchGoogleFavicon(url, sizeRange.Perfect); ok {
		s.returnFetchedIcon(w, r, googleIconURL, googleIconData)
		return
	}

	if getTrueFromEnv("DISABLE_LETTER_FALLBACK") {
		addCacheControl(w, s.cacheDuration)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	iconColor := finder.MainColorForIcons()
	letter := lettericon.MainLetterFromURL(url)

	fallbackColorHex := r.FormValue("fallback_icon_color")
	if iconColor == nil && fallbackColorHex != "" {
		color, err := lettericon.ColorFromHex(fallbackColorHex)
		if err == nil {
			iconColor = color
		}
	}

	// We support both PNG and SVG fallback. Only return SVG if requested.
	format := "png"
	if includesString(finder.FormatsAllowed, "svg") {
		format = "svg"
	}
	redirectPath := lettericon.IconPath(letter, fmt.Sprintf("%d", sizeRange.Perfect), iconColor, format)
	s.returnLetterIcon(w, r, redirectPath)
}

const (
	urlParam = "url"
)

func (s *server) alliconsHandler(w http.ResponseWriter, r *http.Request) {
	url, err := s.demoURLFromRequest(r)
	if err != nil {
		writeAPIError(w, 400, err)
		return
	}
	if len(url) == 0 {
		errMissingURL := errors.New("need url query parameter")
		writeAPIError(w, 400, errMissingURL)
		return
	}

	finder := s.newIconFinder()
	formats := r.FormValue("formats")
	if formats != "" {
		finder.FormatsAllowed = strings.Split(r.FormValue("formats"), ",")
	}

	icons, e := finder.FetchIcons(url)
	if e != nil {
		writeAPIError(w, 404, e)
		return
	}

	addCacheControl(w, s.cacheDuration)
	writeAPIIcons(w, url, icons)
}

func (s *server) lettericonHandler(w http.ResponseWriter, r *http.Request) {
	charParam, col, size, format := lettericon.ParseIconPath(r.URL.Path)
	if charParam == "" || col == nil || size <= 0 || format == "" {
		writeAPIError(w, 400, errors.New("wrong format for lettericons/ path, must look like lettericons/M-144-EFC25D.png or M-EFC25D.svg"))
		return
	}

	addCacheControl(w, oneYear)

	if format == "svg" {
		w.Header().Add(contentType, imageSVG)
		lettericon.RenderSVG(charParam, col, w)
	} else {
		w.Header().Add(contentType, imagePNG)
		lettericon.RenderPNG(charParam, col, size, w)
	}
}

func writeAPIError(w http.ResponseWriter, httpStatus int, e error) {
	data := struct {
		Error string `json:"error"`
	}{
		e.Error(),
	}
	renderJSONResponse(w, httpStatus, data)
}

func writeAPIIcons(w http.ResponseWriter, url string, icons []besticon.Icon) {
	// Don't return whole image data
	newIcons := []besticon.Icon{}
	for _, ico := range icons {
		newIcon := ico
		newIcon.ImageData = nil
		newIcons = append(newIcons, newIcon)
	}

	data := &struct {
		URL   string          `json:"url"`
		Icons []besticon.Icon `json:"icons"`
	}{
		url,
		newIcons,
	}
	renderJSONResponse(w, 200, data)
}

const (
	contentType     = "Content-Type"
	applicationJSON = "application/json"
	imagePNG        = "image/png"
	imageSVG        = "image/svg+xml"
)

func renderJSONResponse(w http.ResponseWriter, httpStatus int, data any) {
	w.Header().Add(contentType, applicationJSON)
	w.WriteHeader(httpStatus)
	enc := json.NewEncoder(w)
	enc.Encode(data)
}

type pageInfo struct {
	URL       string
	Icons     []besticon.Icon
	Error     error
	DemoSites []string
}

func (pi pageInfo) Host() string {
	u := pi.URL
	url, _ := url.Parse(u)
	if url != nil && url.Host != "" {
		return url.Host
	}
	return pi.URL
}

func (pi pageInfo) Best() string {
	if len(pi.Icons) > 0 {
		best := pi.Icons[0]
		return best.URL
	}
	return ""
}

func (pi pageInfo) ExampleSites() []string {
	return pi.DemoSites
}

func (pi pageInfo) DefaultURL() string {
	if len(pi.DemoSites) > 0 {
		return pi.DemoSites[0]
	}

	return "github.com"
}

func (pi pageInfo) FormURL() string {
	if strings.TrimSpace(pi.URL) != "" {
		return pi.URL
	}

	return pi.DefaultURL()
}

func renderHTMLTemplate(w http.ResponseWriter, httpStatus int, templ *template.Template, data any) {
	w.Header().Add(contentType, "text/html; charset=utf-8")
	w.WriteHeader(httpStatus)

	err := templ.Execute(w, data)
	if err != nil {
		err = fmt.Errorf("server: could not generate output: %s", err)
		logger.Print(err)
		w.Write([]byte(err.Error()))
	}
}

func startServer(port string, address string) {
	var opts []besticon.Option

	cacheSize := os.Getenv("CACHE_SIZE_MB")
	if cacheSize == "" {
		opts = append(opts, besticon.WithCache(32))
	} else {
		n, _ := strconv.Atoi(cacheSize)
		opts = append(opts, besticon.WithCache(int64(n)))
	}

	cacheDuration, err := time.ParseDuration(getenvOrFallback("HTTP_MAX_AGE_DURATION", "720h"))
	if err != nil {
		panic(err)
	}

	maxIconSize, err := strconv.Atoi(getenvOrFallback("MAX_ICON_SIZE", "500"))
	if err != nil {
		panic(err)
	}

	userAgent := getenvOrFallback("HTTP_USER_AGENT", "Mozilla/5.0 (iPhone; CPU iPhone OS 10_0 like Mac OS X) AppleWebKit/602.1.38 (KHTML, like Gecko) Version/10.0 Mobile/14A5297c Safari/602.1")
	var httpClient *http.Client
	if getTrueFromEnv("DISABLE_PRIVATE_NETWORK_PROTECTION") {
		logger.Print("WARNING: private network protection is disabled; untrusted callers can make requests to internal services")
		httpClient = besticon.NewUnsafeHTTPClient(userAgent)
		opts = append(opts, besticon.WithPrivateNetworkProtectionDisabled())
	} else {
		httpClient = besticon.NewDefaultHTTPClient()
		httpClient.Transport = besticon.NewDefaultHTTPTransport(userAgent)
	}

	opts = append(opts, besticon.WithHTTPClient(httpClient))

	s := &server{
		maxIconSize:     maxIconSize,
		cacheDuration:   cacheDuration,
		demoSites:       normalizedHostListFromEnv("DEMO_SITES"),
		hostOnlyDomains: strings.Split(os.Getenv("HOST_ONLY_DOMAINS"), ","),

		besticon: besticon.New(opts...),
	}

	registerHandler("/icon", s.iconHandler)
	registerHandler("/allicons.json", s.alliconsHandler)
	registerHandler("/lettericons/", s.lettericonHandler)
	registerHandler("/up", s.upHandler)

	disableBrowsePages := getTrueFromEnv("DISABLE_BROWSE_PAGES")

	if !disableBrowsePages {
		registerHandler("/", s.indexHandler)
		registerHandler("/icons", s.iconsHandler)

		serveAsset("/pico.min.css", "pico.min.css", oneYear)
		serveAsset("/site.css", "site.css", oneMonth)

		serveAsset("/icon.svg", "icon.svg", oneYear)
		serveAsset("/favicon.ico", "favicon.ico", oneYear)
		serveAsset("/apple-touch-icon.png", "apple-touch-icon.png", oneYear)
	}

	metricsPath := getenvOrFallback("METRICS_PATH", "/metrics")

	if metricsPath != "disable" {
		if !strings.HasPrefix(metricsPath, "/") {
			logger.Fatalf("METRICS_PATH must start with a slash")
		}

		http.Handle(metricsPath, promhttp.Handler())
	}

	addr := address + ":" + port
	logger.Print("Starting server on ", addr, "...")
	err = http.ListenAndServe(addr, httpHandler())
	if err != nil {
		logger.Fatalf("cannot start server: %s\n", err)
	}
}

func httpHandler() http.Handler {
	corsEnabled := getTrueFromEnv("CORS_ENABLED")
	if corsEnabled {
		logger.Print("Enabling CORS middleware")
		return corsHandler(newLoggingMux())
	} else {
		return newLoggingMux()
	}
}

func corsHandler(mux http.HandlerFunc) http.Handler {
	corsOpts := cors.Options{
		AllowedOrigins:   stringSliceFromEnv("CORS_ALLOWED_ORIGINS"),
		AllowedMethods:   stringSliceFromEnv("CORS_ALLOWED_METHODS"),
		AllowedHeaders:   stringSliceFromEnv("CORS_ALLOWED_HEADERS"),
		AllowCredentials: getTrueFromEnv("CORS_ALLOW_CREDENTIALS"),
		Debug:            getTrueFromEnv("CORS_DEBUG"),
	}
	return cors.New(corsOpts).Handler(mux)
}

const (
	cacheControl = "Cache-Control"
	oneMonth     = 30 * 24 * time.Hour
	oneYear      = 365 * 24 * time.Hour
)

func (s *server) returnIcon(w http.ResponseWriter, r *http.Request, iconURL string) {
	if os.Getenv("SERVER_MODE") == "download" {
		s.downloadAndReturn(w, r, iconURL)
	} else {
		s.redirectWithCacheControl(w, r, iconURL)
	}
}

func (s *server) returnFetchedIcon(w http.ResponseWriter, r *http.Request, iconURL string, data []byte) {
	if os.Getenv("SERVER_MODE") != "download" {
		s.redirectWithCacheControl(w, r, iconURL)
		return
	}

	addCacheControl(w, s.cacheDuration)
	w.Header().Set(contentType, http.DetectContentType(data))
	_, _ = w.Write(data)
}

const (
	maxResizeSourceDimension = 8192
	maxResizeSourcePixels    = 16 * 1024 * 1024
)

func smallestOversizedIcon(icons []besticon.Icon, sizeRange besticon.SizeRange) *besticon.Icon {
	var selected *besticon.Icon
	var selectedPixels int64

	for i := range icons {
		icon := &icons[i]
		if icon.Format == "svg" || icon.Width <= 0 || icon.Height <= 0 {
			continue
		}
		if icon.Width <= sizeRange.Max && icon.Height <= sizeRange.Max {
			continue
		}
		if !safeResizeSource(icon.Width, icon.Height) {
			continue
		}

		width, height := scaledDimensions(icon.Width, icon.Height, sizeRange.Max)
		if width < sizeRange.Min || height < sizeRange.Min {
			continue
		}

		pixels := int64(icon.Width) * int64(icon.Height)
		if selected == nil || pixels < selectedPixels || (pixels == selectedPixels && icon.Bytes < selected.Bytes) {
			selected = icon
			selectedPixels = pixels
		}
	}

	return selected
}

func (s *server) returnResizedIcon(w http.ResponseWriter, icon *besticon.Icon, maxSize int) bool {
	data, resized, err := resizeIconData(icon, maxSize)
	if err != nil || !resized {
		return false
	}

	addCacheControl(w, s.cacheDuration)
	w.Header().Set(contentType, imagePNG)
	_, _ = w.Write(data)
	return true
}

func resizeIconData(icon *besticon.Icon, maxSize int) ([]byte, bool, error) {
	if icon.Width <= maxSize && icon.Height <= maxSize {
		return icon.ImageData, false, nil
	}
	if icon.Width <= 0 || icon.Height <= 0 || maxSize <= 0 {
		return nil, false, errors.New("invalid icon dimensions")
	}
	if !safeResizeSource(icon.Width, icon.Height) {
		return nil, false, errors.New("icon is too large to resize safely")
	}

	source, _, err := image.Decode(bytes.NewReader(icon.ImageData))
	if err != nil {
		return nil, false, err
	}

	width, height := scaledDimensions(icon.Width, icon.Height, maxSize)
	destination := image.NewNRGBA(image.Rect(0, 0, width, height))
	xdraw.CatmullRom.Scale(destination, destination.Bounds(), source, source.Bounds(), draw.Src, nil)

	var output bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.DefaultCompression}
	if err := encoder.Encode(&output, destination); err != nil {
		return nil, false, err
	}

	return output.Bytes(), true, nil
}

func safeResizeSource(width, height int) bool {
	return width > 0 && height > 0 &&
		width <= maxResizeSourceDimension && height <= maxResizeSourceDimension &&
		int64(width)*int64(height) <= maxResizeSourcePixels
}

func scaledDimensions(width, height, maxSize int) (int, int) {
	if width >= height {
		return maxSize, max(1, height*maxSize/width)
	}
	return max(1, width*maxSize/height), maxSize
}

func (s *server) returnLetterIcon(w http.ResponseWriter, r *http.Request, iconPath string) {
	if os.Getenv("SERVER_MODE") != "download" {
		s.redirectWithCacheControl(w, r, iconPath)
		return
	}

	letterRequest := r.Clone(r.Context())
	letterRequest.URL.Path = iconPath
	letterRequest.URL.RawQuery = ""
	s.lettericonHandler(w, letterRequest)
}

func (s *server) downloadAndReturn(w http.ResponseWriter, r *http.Request, iconURL string) {
	response, err := s.besticon.Get(iconURL)
	if err != nil {
		s.redirectWithCacheControl(w, r, iconURL)
		return
	}

	b, err := s.besticon.GetBodyBytes(response)
	if err != nil {
		s.redirectWithCacheControl(w, r, iconURL)
		return
	}

	addCacheControl(w, s.cacheDuration)
	w.Write(b)
}

func (s *server) redirectWithCacheControl(w http.ResponseWriter, r *http.Request, redirectURL string) {
	addCacheControl(w, s.cacheDuration)
	http.Redirect(w, r, redirectURL, 302)
}

func addCacheControl(w http.ResponseWriter, maxAge time.Duration) {
	w.Header().Add(cacheControl, fmt.Sprintf("max-age=%d", int(maxAge.Seconds())))
}

func serveAsset(path string, assetPath string, maxAge time.Duration) {
	registerHandler(path, func(w http.ResponseWriter, r *http.Request) {
		f, err := assetFS().Open(assetPath)
		if err != nil {
			panic(err)
		}
		defer f.Close()

		stat, err := f.Stat()
		if err != nil {
			panic(err)
		}

		data, err := io.ReadAll(f)
		if err != nil {
			panic(err)
		}

		addCacheControl(w, maxAge)

		http.ServeContent(w, r, stat.Name(), stat.ModTime(), bytes.NewReader(data))
	})
}

func registerHandler(path string, f http.HandlerFunc) {
	http.Handle(path, newPrometheusHandler(path, f))
}

// /up is a simple health check endpoint (used by kamal deploy)
func (s *server) upHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

func main() {
	fmt.Printf("iconserver %s (%s) (%s) - https://github.com/mat/besticon\n", besticon.VersionString, besticon.BuildDate, runtime.Version())
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	address := os.Getenv("ADDRESS")
	if address == "" {
		address = "0.0.0.0"
	}
	startServer(port, address)
}

func templateFromAsset(assetPath, templateName string) *template.Template {
	if !getTrueFromEnv("SERVE_ASSETS_FROM_DISK") {
		return cachedTemplate(assetPath, templateName)
	}

	data, err := fs.ReadFile(assetFS(), assetPath)
	if err != nil {
		panic(err)
	}
	return template.Must(template.New(templateName).Funcs(funcMap).Parse(string(data)))
}

var funcMap = template.FuncMap{
	"CurrentYear": currentYear,
	"ImgWidth":    imgWidth,
}

var templateCache sync.Map

func imgWidth(i *besticon.Icon) int {
	return i.Width / 2.0
}

func currentYear() int {
	return time.Now().Year()
}

func (s *server) demoURLFromRequest(r *http.Request) (string, error) {
	requestedURL := strings.TrimSpace(r.FormValue(urlParam))
	if requestedURL == "" {
		return "", nil
	}

	if len(s.demoSites) == 0 {
		return requestedURL, nil
	}

	host, err := normalizeRequestedHost(requestedURL)
	if err != nil {
		return "", errors.New("need a valid demo site URL or hostname")
	}

	if slices.Contains(s.demoSites, host) {
		return requestedURL, nil
	}

	return "", fmt.Errorf("this demo only supports these sites: %s", strings.Join(s.demoSites, ", "))
}

func (s *server) newIconFinder() *besticon.IconFinder {
	finder := s.besticon.NewIconFinder()
	if len(s.hostOnlyDomains) > 0 {
		finder.HostOnlyDomains = s.hostOnlyDomains
	}

	return finder
}

func getTrueFromEnv(s string) bool {
	return getenvOrFallback(s, "") == "true"
}

func stringSliceFromEnv(key string) []string {
	value := os.Getenv(key)
	if value == "" {
		return nil
	}
	return strings.Split(value, ",")
}

func normalizedHostListFromEnv(key string) []string {
	var hosts []string
	seen := map[string]struct{}{}

	for _, value := range stringSliceFromEnv(key) {
		host, err := normalizeRequestedHost(value)
		if err != nil {
			continue
		}
		if _, ok := seen[host]; ok {
			continue
		}
		seen[host] = struct{}{}
		hosts = append(hosts, host)
	}

	return hosts
}

func getenvOrFallback(key string, fallbackValue string) string {
	value := os.Getenv(key)
	if len(strings.TrimSpace(value)) != 0 {
		return value
	}
	return fallbackValue
}

func normalizeRequestedHost(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", errors.New("empty host")
	}

	if !strings.Contains(value, "://") {
		value = "http://" + value
	}

	parsed, err := url.Parse(value)
	if err != nil {
		return "", err
	}

	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if host == "" {
		return "", errors.New("missing host")
	}

	return host, nil
}

func cachedTemplate(assetPath, templateName string) *template.Template {
	cacheKey := assetPath + ":" + templateName
	if cached, ok := templateCache.Load(cacheKey); ok {
		return cached.(*template.Template)
	}

	data, err := fs.ReadFile(assetFS(), assetPath)
	if err != nil {
		panic(err)
	}

	templ := template.Must(template.New(templateName).Funcs(funcMap).Parse(string(data)))
	actual, _ := templateCache.LoadOrStore(cacheKey, templ)
	return actual.(*template.Template)
}

func assetFS() fs.FS {
	if getTrueFromEnv("SERVE_ASSETS_FROM_DISK") {
		return os.DirFS(assetDir())
	}
	return assets.Assets
}

func assetDir() string {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		panic("could not resolve asset directory")
	}
	return filepath.Join(filepath.Dir(thisFile), "assets")
}

func includesString(arr []string, str string) bool {
	return slices.Contains(arr, str)
}
