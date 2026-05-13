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

	resourcesapi "github.com/Azure/ARO-HCP/internal/apis/resources"
	armresourcesapi "github.com/Azure/ARO-HCP/internal/apis/resources/arm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type middlewareValidatedAPIVersion struct {
	apiRegistry resourcesapi.APIRegistry
}

func newMiddlewareValidatedAPIVersion(apiRegistry resourcesapi.APIRegistry) *middlewareValidatedAPIVersion {
	return &middlewareValidatedAPIVersion{
		apiRegistry: apiRegistry,
	}
}

func (h *middlewareValidatedAPIVersion) handleRequest(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	ctx := r.Context()
	logger := utils.LoggerFromContext(ctx)

	apiVersion := r.URL.Query().Get(APIVersionKey)
	if apiVersion == "" {
		armresourcesapi.WriteError(
			w, http.StatusBadRequest,
			armresourcesapi.CloudErrorCodeInvalidParameter, "",
			"The request is missing required parameter '%s'.",
			APIVersionKey)
	} else if version, ok := h.apiRegistry.Lookup(apiVersion); !ok {
		armresourcesapi.WriteError(
			w, http.StatusBadRequest,
			armresourcesapi.CloudErrorCodeInvalidResourceType, "",
			"The resource type '%s' could not be found API version '%s'.",
			resourcesapi.ClusterResourceType,
			apiVersion)
	} else {
		logger = logger.WithValues(utils.LogValues{}.AddAPIVersion(apiVersion)...)
		ctx = utils.ContextWithLogger(ctx, logger)
		ctx = ContextWithVersion(ctx, version)
		r = r.WithContext(ctx)

		span := trace.SpanFromContext(ctx)
		span.SetAttributes(attribute.String("aro.api_version", apiVersion))

		next(w, r)
	}
}
