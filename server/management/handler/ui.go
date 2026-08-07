package handler

import (
	"io"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gsoultan/pontus/web"
)

// encodings lists the precompressed variants Vite emits, best ratio first.
var encodings = []struct {
	token  string // Accept-Encoding token
	suffix string // on-disk suffix
}{
	{token: "br", suffix: ".br"},
	{token: "gzip", suffix: ".gz"},
}

// uiHandler serves the built dashboard from an fs.FS.
//
// It negotiates the precompressed artifacts produced at build time rather than
// compressing per request, and it sets cache headers that let hashed assets be
// cached forever while keeping the entry point and the service worker fresh —
// a cached sw.js would pin clients to a stale build permanently.
type uiHandler struct {
	fsys fs.FS
}

// NewUIHandler returns an http.Handler that serves the UI files.
// It prioritizes a local "web/dist" directory if it exists, otherwise it uses the embedded UI.
// It handles SPA routing by serving index.html for unknown paths.
func NewUIHandler() (http.Handler, error) {
	// 1. Try to proxy to Vite dev server if it's running (for best developer experience)
	// We only do this if PONTUS_DEV is set or if we can reach the dev server quickly
	if os.Getenv("PONTUS_DEV") == "true" {
		viteURL, _ := url.Parse("http://localhost:5173")
		log.Printf("UI: Development mode enabled, proxying to Vite at %s", viteURL)
		return httputil.NewSingleHostReverseProxy(viteURL), nil
	}

	// Quick check if Vite is running even if PONTUS_DEV is not set
	client := &http.Client{Timeout: 50 * time.Millisecond}
	if resp, err := client.Get("http://localhost:5173"); err == nil {
		resp.Body.Close()
		viteURL, _ := url.Parse("http://localhost:5173")
		log.Printf("UI: Vite dev server detected, proxying to %s", viteURL)
		return httputil.NewSingleHostReverseProxy(viteURL), nil
	}

	var uiFS fs.FS

	// Check if we should use local filesystem (for development)
	// We look for web/dist relative to current working directory
	localDist := filepath.Join("web", "dist")
	if info, err := os.Stat(localDist); err == nil && info.IsDir() {
		uiFS = os.DirFS(localDist)
	} else {
		// Use embedded FS
		var err error
		uiFS, err = fs.Sub(web.Dist, "dist")
		if err != nil {
			return nil, err
		}
	}

	return &uiHandler{fsys: uiFS}, nil
}

func (h *uiHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "" || !h.exists(name) {
		// Unknown path: hand the SPA its entry point and let the router decide.
		name = "index.html"
	}

	// The dashboard renders captured SQL and log lines. Refusing MIME sniffing
	// keeps a mistyped response from ever being interpreted as script.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", cacheControlFor(name))

	if ctype := mime.TypeByExtension(path.Ext(name)); ctype != "" {
		w.Header().Set("Content-Type", ctype)
	}

	if h.serveEncoded(w, r, name) {
		return
	}
	h.servePlain(w, r, name)
}

// serveEncoded writes a precompressed variant when the client accepts one.
// Reports whether it handled the request.
func (h *uiHandler) serveEncoded(w http.ResponseWriter, r *http.Request, name string) bool {
	// Range requests are answered from the identity file: byte offsets into a
	// compressed representation are not what the client asked for.
	if r.Header.Get("Range") != "" {
		return false
	}

	accept := r.Header.Get("Accept-Encoding")
	for _, enc := range encodings {
		if !acceptsEncoding(accept, enc.token) {
			continue
		}
		file, err := h.fsys.Open(name + enc.suffix)
		if err != nil {
			continue
		}
		defer file.Close()

		w.Header().Set("Content-Encoding", enc.token)
		w.Header().Add("Vary", "Accept-Encoding")

		if info, err := file.Stat(); err == nil {
			w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
		}
		if r.Method == http.MethodHead {
			return true
		}
		if _, err := io.Copy(w, file); err != nil {
			log.Printf("UI: write %s: %v", name, err)
		}
		return true
	}
	return false
}

func (h *uiHandler) servePlain(w http.ResponseWriter, r *http.Request, name string) {
	file, err := h.fsys.Open(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()

	seeker, ok := file.(io.ReadSeeker)
	if !ok {
		if r.Method != http.MethodHead {
			if _, err := io.Copy(w, file); err != nil {
				log.Printf("UI: write %s: %v", name, err)
			}
		}
		return
	}

	info, err := file.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	// Content-Type is already set; ServeContent handles ranges and conditionals.
	http.ServeContent(w, r, name, info.ModTime(), seeker)
}

func (h *uiHandler) exists(name string) bool {
	file, err := h.fsys.Open(name)
	if err != nil {
		return false
	}
	defer file.Close()
	if info, err := file.Stat(); err == nil && info.IsDir() {
		return false
	}
	return true
}

// cacheControlFor keeps content-hashed assets cacheable indefinitely while
// forcing revalidation of the files that decide which build a client runs.
func cacheControlFor(name string) string {
	switch {
	case name == "index.html", name == "sw.js", name == "registerSW.js",
		strings.HasSuffix(name, "manifest.webmanifest"):
		return "no-cache"
	case strings.HasPrefix(name, "assets/"):
		return "public, max-age=31536000, immutable"
	default:
		return "public, max-age=3600"
	}
}

// acceptsEncoding reports whether the header offers the token with a non-zero
// quality value.
func acceptsEncoding(header, token string) bool {
	for _, part := range strings.Split(header, ",") {
		fields := strings.Split(strings.TrimSpace(part), ";")
		if !strings.EqualFold(strings.TrimSpace(fields[0]), token) {
			continue
		}
		for _, param := range fields[1:] {
			param = strings.TrimSpace(param)
			if after, found := strings.CutPrefix(param, "q="); found {
				return after != "0" && after != "0.0" && after != "0.00"
			}
		}
		return true
	}
	return false
}
