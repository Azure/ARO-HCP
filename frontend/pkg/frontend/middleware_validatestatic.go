package frontend

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"net/http"
	"regexp"
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/google/uuid"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

// Referenced in https://learn.microsoft.com/en-us/azure/azure-resource-manager/management/resource-name-rules#microsoftresources
var rxHCPOpenShiftClusterResourceName = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9-]{2,53}$`)
var rxNodePoolResourceName = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9-]{2,14}$`)

var resourceTypeSubscription = "Microsoft.Resources/subscriptions"

func MiddlewareValidateStatic(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	// To conform with "OAPI012: Resource IDs must not be case sensitive"
	// we need to use the original, non-lowercased resource ID components
	// in response messages.
	//TODO: Inspect the error instead of ignoring it
	originalPath, _ := OriginalPathFromContext(r.Context())
	resource, _ := azcorearm.ParseResourceID(originalPath)

	if resource != nil {
		if resource.SubscriptionID != "" {
			if uuid.Validate(resource.SubscriptionID) != nil {
				arm.WriteError(w, http.StatusBadRequest,
					arm.CloudErrorCodeInvalidSubscriptionID,
					resource.String(),
					"The provided subscription identifier '%s' is malformed or invalid.",
					resource.SubscriptionID)
				return
			}
		}

		// Skip static validation for subscription resources
		if !strings.EqualFold(resource.ResourceType.String(), resourceTypeSubscription) {
			switch strings.ToLower(resource.ResourceType.Type) {
			case strings.ToLower(api.ClusterResourceTypeName):
				if !rxHCPOpenShiftClusterResourceName.MatchString(resource.Name) {
					arm.WriteError(w, http.StatusBadRequest,
						arm.CloudErrorInvalidResourceName,
						resource.String(),
						"The Resource '%s/%s' under resource group '%s' does not conform to the naming restriction.",
						resource.ResourceType, resource.Name,
						resource.ResourceGroupName)
					return
				}
			case strings.ToLower(api.ClusterResourceTypeName + "/" + api.NodePoolResourceTypeName):
				if !rxNodePoolResourceName.MatchString(resource.Name) {
					arm.WriteError(w, http.StatusBadRequest,
						arm.CloudErrorInvalidResourceName,
						resource.String(),
						"The Resource '%s/%s' under resource group '%s' does not conform to the naming restriction.",
						resource.ResourceType, resource.Name,
						resource.ResourceGroupName)
					return
				}
			}
		}
	}

	next(w, r)
}
