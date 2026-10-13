package httpapi

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

// Keep application processes independent while offering the backend's public origin.
func frontendProxy(origin string) http.Handler {
	target, _ := url.Parse(origin)
	proxy := &httputil.ReverseProxy{Rewrite: func(r *httputil.ProxyRequest) { r.SetURL(target); r.SetXForwarded(); r.Out.Host = r.In.Host }, ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
		writeError(w, fail(503, "frontend is unavailable; start the frontend service"))
	}}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			http.NotFound(w, r)
			return
		}
		proxy.ServeHTTP(w, r)
	})
}
