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
