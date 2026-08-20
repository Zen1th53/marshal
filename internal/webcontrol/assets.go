package webcontrol

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed dist/*
var embeddedAssets embed.FS

type AssetHandler struct {
	fileSystem http.FileSystem
}

func NewAssetHandler() *AssetHandler {
	sub, err := fs.Sub(embeddedAssets, "dist")
	if err != nil {
		return &AssetHandler{fileSystem: nil}
	}
	return &AssetHandler{fileSystem: http.FS(sub)}
}

func (h *AssetHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	// CRITICAL INVARIANT: Never swallow API 404s with SPA fallback
	if strings.HasPrefix(path, "/api/") {
		writeError(w, http.StatusNotFound, "api_endpoint_not_found", "API endpoint not found", "")
		return
	}

	if h.fileSystem == nil {
		http.NotFound(w, r)
		return
	}

	// Immutable cache headers for hashed static assets
	if strings.HasPrefix(path, "/assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else if path == "/" || path == "/index.html" || !strings.Contains(path, ".") {
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	}

	// Handle root or SPA fallback
	if path == "/" || path == "/index.html" || !strings.Contains(path, ".") {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		f, err := h.fileSystem.Open("index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		stat, _ := f.Stat()
		if stat != nil {
			http.ServeContent(w, r, "index.html", stat.ModTime(), f)
			return
		}
	}

	// Try serving exact file
	f, err := h.fileSystem.Open(strings.TrimPrefix(path, "/"))
	if err == nil {
		defer f.Close()
		stat, err := f.Stat()
		if err == nil && !stat.IsDir() {
			http.FileServer(h.fileSystem).ServeHTTP(w, r)
			return
		}
	}

	// Default fallback to index.html
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fIndex, err := h.fileSystem.Open("index.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer fIndex.Close()
	stat, _ := fIndex.Stat()
	if stat != nil {
		http.ServeContent(w, r, "index.html", stat.ModTime(), fIndex)
		return
	}
	http.NotFound(w, r)
}
