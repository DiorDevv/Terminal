package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/gin-gonic/gin"
)

func router(t *testing.T, fsys fstest.MapFS) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/health", func(c *gin.Context) { c.JSON(200, gin.H{"status": "ok"}) })
	r.NoRoute(NewHandler(fsys))
	return r
}

var site = fstest.MapFS{
	"index.html":               {Data: []byte("<!doctype html><div id=root></div>")},
	"favicon.svg":              {Data: []byte("<svg/>")},
	"assets/index-abc123.js":   {Data: []byte("console.log(1)")},
	"assets/index-abc123.css":  {Data: []byte("body{}")},
	"assets/inter-latin.woff2": {Data: []byte("font")},
	".gitkeep":                 {Data: []byte("")},
}

func get(r *gin.Engine, method, path string, hdr ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	req.Host = "panel.example.com"
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestServesTheSinglePageApp(t *testing.T) {
	r := router(t, site)
	for _, p := range []string{"/", "/stats", "/users", "/access/rules", "/some/deep/client/route"} {
		w := get(r, "GET", p)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `id=root`) {
			t.Errorf("%s: %d %q", p, w.Code, w.Body.String())
		}
		if !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
			t.Errorf("%s: content type %q", p, w.Header().Get("Content-Type"))
		}
		if w.Header().Get("Cache-Control") != "no-cache" {
			t.Errorf("%s: the page must be revalidated every visit, got %q", p, w.Header().Get("Cache-Control"))
		}
	}
}

func TestAssetsAreTypedAndCachedForever(t *testing.T) {
	r := router(t, site)
	cases := map[string]string{
		"/assets/index-abc123.js":   "javascript",
		"/assets/index-abc123.css":  "text/css",
		"/assets/inter-latin.woff2": "font",
		"/favicon.svg":              "image/svg",
	}
	for p, wantType := range cases {
		w := get(r, "GET", p)
		if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Type"), wantType) {
			t.Errorf("%s: %d type=%q", p, w.Code, w.Header().Get("Content-Type"))
		}
	}
	if cc := get(r, "GET", "/assets/index-abc123.js").Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") || !strings.Contains(cc, "max-age=31536000") {
		t.Errorf("hashed assets are cached for a year: %q", cc)
	}
	if cc := get(r, "GET", "/favicon.svg").Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("files without a hash must be revalidated: %q", cc)
	}
}

func TestConditionalRequestsAnswer304(t *testing.T) {
	r := router(t, site)
	first := get(r, "GET", "/assets/index-abc123.js")
	etag := first.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag")
	}
	if w := get(r, "GET", "/assets/index-abc123.js", "If-None-Match", etag); w.Code != http.StatusNotModified || w.Body.Len() != 0 {
		t.Errorf("an unchanged file must answer 304 without a body: %d", w.Code)
	}
	if w := get(r, "GET", "/assets/index-abc123.js", "If-None-Match", `"stale"`); w.Code != 200 {
		t.Errorf("a different ETag means send the file: %d", w.Code)
	}
}

func TestAMissingFileIsA404NotTheHTMLPage(t *testing.T) {
	r := router(t, site)
	for _, p := range []string{"/assets/missing.js", "/nope.png", "/robots.txt", "/.env", "/.gitkeep"} {
		w := get(r, "GET", p)
		if w.Code != 404 || strings.Contains(w.Body.String(), "id=root") {
			t.Errorf("%s: %d %q (a missing file must not be answered with the app page)", p, w.Code, w.Body.String())
		}
	}
}

func TestUnknownAPIPathsAreJSONErrors(t *testing.T) {
	r := router(t, site)
	for _, p := range []string{"/api/nothing", "/api/squid/missing", "/api", "/api/"} {
		w := get(r, "GET", p)
		if w.Code != 404 || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") || strings.Contains(w.Body.String(), "<") {
			t.Errorf("%s: %d %s %q", p, w.Code, w.Header().Get("Content-Type"), w.Body.String())
		}
	}
	if w := get(r, "GET", "/api/health"); w.Code != 200 {
		t.Errorf("real API routes are untouched: %d", w.Code)
	}
}

func TestOnlyGetAndHeadServeFiles(t *testing.T) {
	r := router(t, site)
	if w := get(r, "POST", "/stats"); w.Code != 404 {
		t.Errorf("POST /stats: %d", w.Code)
	}
	if w := get(r, "HEAD", "/stats"); w.Code != 200 || w.Body.Len() != 0 {
		t.Errorf("HEAD /stats: %d, body %d bytes", w.Code, w.Body.Len())
	}
}

func TestPathTraversalCannotEscapeTheSite(t *testing.T) {
	r := router(t, site)
	for _, p := range []string{"/../../etc/passwd", "/..%2f..%2fetc%2fpasswd", "/assets/../../secret", "/%2e%2e/%2e%2e/etc/passwd", "//etc/passwd"} {
		w := get(r, "GET", p)
		body := w.Body.String()
		if strings.Contains(body, "root:") || (w.Code == 200 && !strings.Contains(body, "id=root")) {
			t.Errorf("%s leaked something: %d %q", p, w.Code, body)
		}
	}
}

func TestThePageCarriesAContentSecurityPolicy(t *testing.T) {
	r := router(t, site)
	csp := get(r, "GET", "/stats").Header().Get("Content-Security-Policy")
	for _, want := range []string{
		"default-src 'none'", "script-src 'self'", "frame-ancestors 'none'", "base-uri 'none'",
		"connect-src 'self' ws://panel.example.com wss://panel.example.com",
	} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP lacks %q: %s", want, csp)
		}
	}
	if strings.Contains(csp, "unsafe-eval") || strings.Contains(csp, "script-src 'self' 'unsafe-inline'") {
		t.Errorf("scripts must not be allowed inline or through eval: %s", csp)
	}
	// A hostile Host header must not be able to inject into the policy.
	req := httptest.NewRequest("GET", "/stats", nil)
	req.Host = "evil.example; script-src *"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if got := w.Header().Get("Content-Security-Policy"); strings.Contains(got, "evil.example") || strings.Contains(got, "script-src *") {
		t.Errorf("the Host header leaked into the policy: %s", got)
	}
}

func TestAnUnbuiltFrontendExplainsItself(t *testing.T) {
	r := router(t, fstest.MapFS{".gitkeep": {}})
	w := get(r, "GET", "/")
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "scripts/build.sh") {
		t.Errorf("%d %q", w.Code, w.Body.String())
	}
	if w := get(r, "GET", "/api/nothing"); w.Code != 404 {
		t.Errorf("the API keeps working without a frontend: %d", w.Code)
	}
}

func TestTheEmbeddedHandlerBuilds(t *testing.T) {
	// Handler() reads the real embedded directory; in a source checkout that is
	// only the placeholder, so it must still construct and answer.
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.NoRoute(Handler())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != 200 && w.Code != http.StatusServiceUnavailable {
		t.Errorf("the embedded handler answered %d", w.Code)
	}
}
