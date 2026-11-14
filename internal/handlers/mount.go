package handlers

import "net/http"

// Mount serves app under basePath (e.g. "/archive"), the way sthings-portal
// forwards it: the path arrives with the prefix. /health, /healthz and
// /metrics stay at the root for the probes and Prometheus, and / leads to
// the UI. An empty basePath serves app at the root; a nil metrics handler
// is not mounted.
func Mount(basePath string, app, health, metrics http.Handler) http.Handler {
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
	toUI := http.RedirectHandler(basePath+"/", http.StatusTemporaryRedirect)
	mux.Handle(basePath+"/", http.StripPrefix(basePath, app))
	mux.Handle(basePath, toUI)
	mux.Handle("/{$}", toUI)
	return mux
}
