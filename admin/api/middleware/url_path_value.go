package middleware

import (
	"context"
	"net/http"
)

const (
	URLPathValue = contextKey("url_path_value")
)

// WithURLPathValue adds the current URL's path to the context.
func WithURLPathValue(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(
			r.Context(),
			URLPathValue,
			r.URL.Path,
		)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
