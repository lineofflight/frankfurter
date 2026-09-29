package api

import (
	"bytes"
	"embed"
	"io/fs"
	"net/http"
	"path"
	"time"
)

// public is a copy of lib/public (go:embed cannot reach outside the module). Refresh it with go generate
// ./internal/api; TestPublicMatchesRepository fails while the copies differ.
//
//go:generate sh -c "rm -rf public && cp -R ../../../lib/public public"
//go:embed public
var public embed.FS

// staticFiles maps each served path to its file under public, as the Roda static plugin lists them.
var staticFiles = map[string]string{
	"/favicon.ico":     "favicon.ico",
	"/robots.txt":      "robots.txt",
	"/v1/openapi.json": "v1/openapi.json",
	"/v2/openapi.json": "v2/openapi.json",
}

var staticTypes = map[string]string{
	".ico":  "image/vnd.microsoft.icon",
	".txt":  "text/plain",
	".json": "application/json",
}

// static serves one embedded file like Rack::Files behind Rack::Static's header rule: a day of public caching, GET and
// HEAD (with conditional and range requests), an empty OPTIONS answer, and 405 for anything else.
func static(file string) http.Handler {
	data, err := fs.ReadFile(public, path.Join("public", file))
	if err != nil {
		panic(err) // embedded at build time
	}
	contentType := staticTypes[path.Ext(file)]
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			w.Header().Set("Cache-Control", cacheOneDay)
			w.Header().Set("Content-Type", contentType)
			http.ServeContent(w, r, file, time.Time{}, bytes.NewReader(data))
		case http.MethodOptions:
			w.Header().Set("Allow", "GET, HEAD, OPTIONS")
			w.Header().Set("Content-Length", "0")
			w.WriteHeader(http.StatusOK)
		default:
			w.Header().Set("Allow", "GET, HEAD, OPTIONS")
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusMethodNotAllowed)
			w.Write([]byte("Method Not Allowed\n"))
		}
	})
}
