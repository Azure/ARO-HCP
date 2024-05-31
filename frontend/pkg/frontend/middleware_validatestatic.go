package frontend

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"net/http"
	"regexp"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	uuid "github.com/google/uuid"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

// Referenced in https://learn.microsoft.com/en-us/azure/azure-resource-manager/management/resource-name-rules#microsoftresources
var rxResourceGroupName = regexp.MustCompile(`^[a-zA-Z0-9_()-][a-zA-Z0-9_().-]{0,87}[a-zA-Z0-9_()-]$`)
var rxResourceName = regexp.MustCompile(`^[a-zA-Z0-9-]{3,24}$`)

func MiddlewareValidateStatic(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	// To conform with "OAPI012: Resource IDs must not be case sensitive"
	// we need to use the original, non-lowercased resource ID components
	// in response messages.
	originalPath, _ := OriginalPathFromContext(r.Context())
	resourceID, _ := azcorearm.ParseResourceID(originalPath)

	if resourceID != nil {
		if resourceID.SubscriptionID != "" {
			if uuid.Validate(resourceID.SubscriptionID) != nil {
				arm.WriteError(w, http.StatusBadRequest,
					arm.CloudErrorCodeInvalidSubscriptionID,
					resourceID.String(),
					"The provided subscription identifier '%s' is malformed or invalid.",
					resourceID.SubscriptionID)
				return
			}
		}

		if resourceID.ResourceGroupName != "" {
			if !rxResourceGroupName.MatchString(resourceID.ResourceGroupName) {
				arm.WriteError(w, http.StatusBadRequest,
					arm.CloudErrorInvalidResourceGroupName,
					resourceID.String(),
					"Resource group '%s' is invalid.",
					resourceID.ResourceGroupName)
				return
			}
		}

		if resourceID.Name != "" {
			if !rxResourceName.MatchString(resourceID.Name) {
				arm.WriteError(w, http.StatusBadRequest,
					arm.CloudErrorInvalidResourceName,
					resourceID.String(),
					"The Resource '%s/%s' under resource group '%s' is invalid.",
					resourceID.ResourceType, resourceID.Name,
					resourceID.ResourceGroupName)
				return
			}
		}
	}

	next(w, r)
}
