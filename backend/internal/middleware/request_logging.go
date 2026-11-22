package middleware

import (
	chimw "github.com/go-chi/chi/v5/middleware"
	"net/http"
)

// RequestLogger omits query parameters because WebSocket authentication and
// other API requests may carry credentials in their URLs.
func RequestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logged := r.Clone(r.Context())
		copiedURL := *r.URL
		copiedURL.RawQuery = ""
		copiedURL.ForceQuery = false
		logged.URL = &copiedURL
		logged.RequestURI = copiedURL.RequestURI()
		chimw.Logger(http.HandlerFunc(func(w http.ResponseWriter, loggedRequest *http.Request) {
			next.ServeHTTP(w, r.WithContext(loggedRequest.Context()))
		})).ServeHTTP(w, logged)
	})
}
