// Package web serves the built frontend from inside the Go binary, so the whole
// panel installs as one file: no nginx, no Node, no second port.
//
// The frontend is built by scripts/build.sh into internal/web/dist before
// `go build`. A source checkout that has not been built still compiles (the
// directory holds only a placeholder) and shows a page saying what to do.
package web

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"net/http"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

//go:embed all:dist
var embedded embed.FS

// hostRe limits what may be echoed into the Content-Security-Policy header.
var hostRe = regexp.MustCompile(`^[A-Za-z0-9.\-:\[\]]{1,255}$`)

// Handler serves the frontend embedded in the binary.
func Handler() gin.HandlerFunc {
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		panic(err) // the directory is embedded at build time; cannot fail
	}
	return NewHandler(sub)
}

type file struct {
	data []byte
	etag string
}

// NewHandler serves static files from fsys, falling back to index.html for
// client-side routes (/stats, /users, ...). It is meant for gin's NoRoute.
func NewHandler(fsys fs.FS) gin.HandlerFunc {
	files := map[string]file{}
	_ = fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasPrefix(path.Base(p), ".") {
			return nil
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return nil
		}
		sum := sha256.Sum256(data)
		files[p] = file{data: data, etag: `"` + hex.EncodeToString(sum[:8]) + `"`}
		return nil
	})
	index, haveIndex := files["index.html"]

	return func(c *gin.Context) {
		p := c.Request.URL.Path

		// An unknown API path must be a JSON error, never the HTML page.
		if p == "/api" || strings.HasPrefix(p, "/api/") {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
			c.String(http.StatusNotFound, "not found")
			return
		}

		if !haveIndex {
			c.Header("Content-Type", "text/html; charset=utf-8")
			c.Header("Cache-Control", "no-store")
			c.String(http.StatusServiceUnavailable, notBuilt)
			return
		}

		name := strings.TrimPrefix(path.Clean("/"+p), "/")
		if f, ok := files[name]; ok && name != "" {
			serve(c, name, f, cacheFor(name))
			return
		}
		// A path that names a file (has an extension) but does not exist is a
		// real 404; serving the HTML page for a missing script would only hide
		// the mistake behind a confusing syntax error.
		if path.Ext(name) != "" {
			c.String(http.StatusNotFound, "not found")
			return
		}
		// Every other path is a route of the single-page app.
		serve(c, "index.html", index, "no-cache")
	}
}

// cacheFor: Vite gives files under /assets/ a content hash in their name, so
// they may be cached for good; everything else (index.html, the icon) is
// revalidated on each visit so a new release shows up at once.
func cacheFor(name string) string {
	if strings.HasPrefix(name, "assets/") {
		return "public, max-age=31536000, immutable"
	}
	return "no-cache"
}

func serve(c *gin.Context, name string, f file, cache string) {
	h := c.Writer.Header()
	h.Set("ETag", f.etag)
	h.Set("Cache-Control", cache)
	if name == "index.html" {
		h.Set("Content-Security-Policy", csp(c.Request.Host))
	}
	if inm := c.GetHeader("If-None-Match"); inm != "" && inm == f.etag {
		c.Status(http.StatusNotModified)
		return
	}
	// ServeContent sets the Content-Type from the file name and handles HEAD
	// and byte ranges; a zero modtime keeps it from adding Last-Modified.
	http.ServeContent(c.Writer, c.Request, name, time.Time{}, bytes.NewReader(f.data))
}

// csp is the Content-Security-Policy of the page: only files from this origin
// run or load, nothing may frame the panel, and the live-log WebSocket may only
// go back to this host (a bare 'self' does not cover ws:// in every browser).
func csp(host string) string {
	connect := "'self'"
	if hostRe.MatchString(host) {
		connect += " ws://" + host + " wss://" + host
	}
	return strings.Join([]string{
		"default-src 'none'",
		"script-src 'self'",
		"style-src 'self' 'unsafe-inline'",
		"img-src 'self' data:",
		"font-src 'self'",
		"connect-src " + connect,
		"base-uri 'none'",
		"form-action 'self'",
		"frame-ancestors 'none'",
	}, "; ")
}

const notBuilt = `<!doctype html><meta charset="utf-8"><title>Squid Admin</title>
<body style="font-family:system-ui;max-width:40rem;margin:4rem auto;padding:0 1rem">
<h1>Interfeys yig'ilmagan</h1>
<p>Bu binar interfeys fayllarisiz qurilgan. <code>scripts/build.sh</code> ni ishga tushiring
(frontend'ni quradi va binarga joylaydi) yoki ishlab chiqish uchun <code>npm run dev</code> ni ishlating.</p>
<p>The frontend was not built into this binary: run <code>scripts/build.sh</code>.</p>
</body>`
