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

package frontend

import (
	"fmt"
	"net/http"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	semconv "go.opentelemetry.io/otel/semconv/v1.27.0"
	"go.opentelemetry.io/otel/trace"
)

// This middleware only applies to endpoints whose path form a valid Azure
// resource ID. It should follow the MiddlewareLowercase function.
func MiddlewareResourceID(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	ctx := r.Context()
	logger := LoggerFromContext(ctx)

	originalPath, _ := OriginalPathFromContext(ctx)
	if originalPath == "" {
		// MiddlewareLowercase has not run; fall back to the request path.
		logger.Warn("Middleware dependency error: MiddlewareResourceID ran before MiddlewareLowercase")
		originalPath = r.URL.Path
	}

	resourceID, err := azcorearm.ParseResourceID(originalPath)
	if err == nil {
		span := trace.SpanFromContext(ctx)
		span.SetAttributes(semconv.CloudResourceID(resourceID.String()))

		ctx = ContextWithResourceID(ctx, resourceID)
		r = r.WithContext(ctx)
	} else {
		logger.Warn(fmt.Sprintf("Failed to parse '%s' as resource ID: %v", originalPath, err))
	}

	next(w, r)
}
