package health

import "net/http"

// Handler returns the HTTP handler used for liveness and readiness probes.
func Handler() http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
	})
}
