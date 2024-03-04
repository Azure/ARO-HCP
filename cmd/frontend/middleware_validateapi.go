package main

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"net/http"

	"github.com/Azure/ARO-HCP/pkg/api"
	"github.com/Azure/ARO-HCP/pkg/api/arm"
)

func MiddlewareValidateAPIVersion(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	apiVersion := r.URL.Query().Get(APIVersionKey)
	if apiVersion == "" {
		arm.WriteError(
			w, http.StatusBadRequest,
			arm.CloudErrorCodeInvalidParameter, "",
			"The request is missing required parameter '%s'.",
			APIVersionKey)
	} else if _, ok := api.APIs[apiVersion]; !ok {
		arm.WriteError(
			w, http.StatusBadRequest,
			arm.CloudErrorCodeInvalidResourceType, "",
			"The resource type '%s' could not be found in "+
				"the namespace '%s' for API version '%s'.",
			r.PathValue("resourceType"),
			"Microsoft.RedHatOpenShift",
			apiVersion)
	} else {
		next(w, r)
	}
}
