package site

import (
    "net/http"
    "path/filepath"
    "strings" // Re-add this
)

func (s *Server) handleStatic(w http.ResponseWriter, req *http.Request) error {
    path := req.URL.Path
    if path == "/" {
        path = "/index.html"
    }

    // Force the correct MIME type for .js files
    if strings.HasSuffix(path, ".js") {
        w.Header().Set("Content-Type", "application/javascript")
    }
    
    // Disable caching to force the browser to re-request the file
    w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")

    http.ServeFile(w, req, filepath.Join(s.contentDir, path))
    return nil
}