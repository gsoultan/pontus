package handler

import (
	"io/fs"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gsoultan/pontus/web"
)

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

	fileServer := http.FileServer(http.FS(uiFS))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}

		// Check if file exists in the filesystem
		f, err := uiFS.Open(path)
		if err != nil {
			// If file doesn't exist, it might be an SPA route.
			// Serve index.html instead.
			r.URL.Path = "/"
		} else {
			f.Close()
		}

		fileServer.ServeHTTP(w, r)
	}), nil
}
