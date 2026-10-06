package static

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"
)

type Handler struct {
	root *os.Root
}

func NewHandler(directory string) (*Handler, error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, fmt.Errorf("open static-dir: %w", err)
	}
	index, err := root.Stat("index.html")
	if err != nil || !index.Mode().IsRegular() {
		root.Close()
		return nil, fmt.Errorf("static-dir must contain a built frontend index.html: %w", errors.Join(err, fs.ErrInvalid))
	}
	return &Handler{root: root}, nil
}

func (h *Handler) Close() error { return h.root.Close() }

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
	w.Header().Set("Cache-Control", "no-cache")
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Path == "/healthz" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte("ok\n"))
		}
		return
	}
	name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if name == "" {
		name = "index.html"
	}
	if !fs.ValidPath(name) || strings.ContainsAny(name, "\\%\x00") || hiddenPath(name) {
		http.NotFound(w, r)
		return
	}
	file, info, err := h.openFile(name)
	if err != nil && errors.Is(err, fs.ErrNotExist) && !assetPath(name) {
		file, info, err = h.openFile("index.html")
	}
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	if strings.HasPrefix(name, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	http.ServeContent(w, r, info.Name(), info.ModTime(), file)
}

func (h *Handler) openFile(name string) (*os.File, fs.FileInfo, error) {
	file, err := h.root.Open(name)
	if err != nil {
		return nil, nil, err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		file.Close()
		return nil, nil, errors.Join(err, fs.ErrNotExist)
	}
	return file, info, nil
}

func assetPath(name string) bool {
	return name == "assets" || strings.HasPrefix(name, "assets/") || path.Ext(name) != ""
}

func hiddenPath(name string) bool {
	for _, part := range strings.Split(name, "/") {
		if strings.HasPrefix(part, ".") {
			return true
		}
	}
	return false
}
