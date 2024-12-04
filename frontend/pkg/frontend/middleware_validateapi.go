package frontend

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"net/http"

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
		next(w, r)
	}
}
