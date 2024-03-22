package main

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"context"
	"net/http"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

func MiddlewareValidateAPIVersion(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
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
			"The resource type '%s' could not be found in "+
				"the namespace '%s' for API version '%s'.",
			api.ResourceType,
			api.ProviderNamespace,
			apiVersion)
	} else {
		r = r.WithContext(context.WithValue(r.Context(), ContextKeyVersion, version))
		next(w, r)
	}
}
