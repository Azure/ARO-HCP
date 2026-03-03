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

	"github.com/Azure/ARO-HCP/internal/utils"
)

// MiddlewareLogger attaches a request-specific logger to the request context.
func MiddlewareLogger(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	logger := utils.LoggerFromContext(r.Context())
	start := time.Now()
	requestLogger := logger.WithValues(
		utils.LogValues{}.
			AddPath(r.URL.Path).
			AddMethod(r.Method)...)
	requestLogger.Info("Got request.")

	requestCtx := utils.ContextWithLogger(r.Context(), requestLogger)
	next(w, r.WithContext(requestCtx))

	requestLogger.Info("Completed request.", "duration", time.Since(start).String())
}
