// Package ui serves the embedded Next.js static export (built by `make ui`
// into the dist/ directory — see agent_docs/ui_patterns.md).
package ui

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

// dist holds the Next.js static export. The directory is git-ignored except
// for a .gitkeep; `go build` always succeeds, and Handler falls back to a
// notice page when the UI has not been built.
//
//go:embed all:dist
var dist embed.FS

const notBuilt = `<!doctype html><meta charset="utf-8"><title>Avuru Obs</title>
<body style="font-family:system-ui;background:#09090b;color:#fafafa;display:grid;place-items:center;height:100vh;margin:0">
<div><h1>Avuru Obs hub is running</h1><p>The UI has not been embedded in this build. Run <code>make ui hub</code>.</p></div>`

// Handler serves the SPA: real files when present, index.html for client-side
// routes, and a build notice when the export is absent.
func Handler() http.Handler {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic("ui: embedded dist missing: " + err.Error())
	}
	index, indexErr := fs.ReadFile(sub, "index.html")
	files := http.FileServerFS(sub)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if indexErr != nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(notBuilt))
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}
		// Next static export emits "<route>.html" PLUS a "<route>/" dir of
		// RSC payloads — extensionless routes must resolve to the .html file,
		// never the directory (FileServer would 301 to "<route>/").
		if !strings.Contains(path, ".") {
			if page, err := fs.ReadFile(sub, path+".html"); err == nil {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				_, _ = w.Write(page)
				return
			}
		}
		if info, err := fs.Stat(sub, path); err == nil && !info.IsDir() {
			files.ServeHTTP(w, r)
			return
		}
		// Client-side route: let the SPA router handle it.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(index)
	})
}
