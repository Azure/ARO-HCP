package frontend

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"net/http"
	"regexp"

	uuid "github.com/google/uuid"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

// Referenced in https://learn.microsoft.com/en-us/azure/azure-resource-manager/management/resource-name-rules#microsoftresources
var rxResourceGroupName = regexp.MustCompile(`^[a-zA-Z0-9_()-][a-zA-Z0-9_().-]{0,87}[a-zA-Z0-9_()-]$`)
var rxResourceName = regexp.MustCompile(`^[a-zA-Z0-9-]{3,24}$`)

func MiddlewareValidateStatic(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {

	subId := r.PathValue(PathSegmentSubscriptionID)
	resourceGroupName := r.PathValue(PathSegmentResourceGroupName)
	resourceName := r.PathValue(PathSegmentResourceName)

	if subId != "" {
		if uuid.Validate(subId) != nil {
			arm.WriteError(w, http.StatusBadRequest, arm.CloudErrorCodeInvalidSubscriptionID, "", "The provided subscription identifier '%s' is malformed or invalid.", subId)
			return
		}
	}

	if resourceGroupName != "" {
		if !rxResourceGroupName.MatchString(resourceGroupName) {
			arm.WriteError(w, http.StatusBadRequest, arm.CloudErrorInvalidResourceGroupName, "", "Resource group '%s' is invalid.", resourceGroupName)
			return
		}
	}

	if resourceName != "" {
		if !rxResourceName.MatchString(resourceName) {
			arm.WriteError(w, http.StatusBadRequest, arm.CloudErrorInvalidResourceName, "", "The Resource '%s/%s' under resource group '%s' is invalid.", api.ResourceType, resourceName, resourceGroupName)
			return
		}
	}

	next(w, r)
}
