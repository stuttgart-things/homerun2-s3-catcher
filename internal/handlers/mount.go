package handlers

import "net/http"

// Mount serves app under basePath (e.g. "/archive"), the way sthings-portal
// forwards it: the path arrives with the prefix. /health, /healthz and
// /metrics stay at the root for the probes and Prometheus, and / leads to
// the UI. An empty basePath serves app at the root; a nil metrics handler
// is not mounted. extra mounts further UIs under their own prefixes (the
// Analysis entry, #15).
func Mount(basePath string, app, health, metrics http.Handler, extra ...Prefixed) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/health", health)
	mux.Handle("/healthz", health)
	if metrics != nil {
		mux.Handle("/metrics", metrics)
	}
	if basePath == "" {
		mux.Handle("/", app)
		return mux
	}
	for _, e := range extra {
		mux.Handle(e.Path+"/", http.StripPrefix(e.Path, e.Handler))
		mux.Handle(e.Path, http.RedirectHandler(e.Path+"/", http.StatusTemporaryRedirect))
	}
	toUI := http.RedirectHandler(basePath+"/", http.StatusTemporaryRedirect)
	mux.Handle(basePath+"/", http.StripPrefix(basePath, app))
	mux.Handle(basePath, toUI)
	mux.Handle("/{$}", toUI)
	return mux
}

// Prefixed is a handler served under its own path prefix.
type Prefixed struct {
	Path    string
	Handler http.Handler
}
