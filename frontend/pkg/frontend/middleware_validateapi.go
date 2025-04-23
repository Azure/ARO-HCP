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
	"net/http"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

func MiddlewareValidateAPIVersion(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	ctx := r.Context()
	logger := LoggerFromContext(ctx)

	apiVersion := r.URL.Query().Get(APIVersionKey)
	if apiVersion == "" {
		arm.WriteError(
			w, http.StatusBadRequest,
			arm.CloudErrorCodeInvalidParameter, "",
			"The request is missing required parameter '%s'.",
			APIVersionKey)
	} else if version, ok := api.Lookup(apiVersion); !ok {
		arm.WriteError(
			w, http.StatusBadRequest,
			arm.CloudErrorCodeInvalidResourceType, "",
			"The resource type '%s' could not be found API version '%s'.",
			api.ClusterResourceType,
			apiVersion)
	} else {
		logger = logger.With("api_version", apiVersion)
		ctx = ContextWithLogger(ctx, logger)
		ctx = ContextWithVersion(ctx, version)
		r = r.WithContext(ctx)

		span := trace.SpanFromContext(ctx)
		span.SetAttributes(attribute.String("aro.api_version", apiVersion))

		next(w, r)
	}
}
