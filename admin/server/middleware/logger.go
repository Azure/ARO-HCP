// Copyright 2025 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"time"
)

// WithLogger creates a middleware that attaches a logger to the request context.
func WithLogger(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		start := time.Now()
		requestLogger := logger.With("path", request.URL.Path, "method", request.Method)
		requestLogger.Info("Got request.")

		ctx := context.WithValue(request.Context(), contextKeyLogger, requestLogger)
		next.ServeHTTP(writer, request.WithContext(ctx))

		requestLogger.Info("Completed request.", "duration", time.Since(start).String())
	})
}

func LoggerFromContext(ctx context.Context) *slog.Logger {
	if logger, ok := ctx.Value(contextKeyLogger).(*slog.Logger); ok {
		return logger
	}
	return slog.Default()
}
