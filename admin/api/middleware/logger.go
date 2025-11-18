package middleware

import (
	"net/http"
	"time"

	"github.com/go-logr/logr"
)

func WithLogger(logger logr.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		start := time.Now()
		requestLogger := logger.WithValues("path", request.URL.Path, "method", request.Method)
		requestLogger.Info("Got request.")
		next.ServeHTTP(writer, request.WithContext(logr.NewContext(request.Context(), requestLogger)))
		requestLogger = requestLogger.WithValues("duration", time.Since(start).String())
		requestLogger.Info("Completed request.")
	})
}
