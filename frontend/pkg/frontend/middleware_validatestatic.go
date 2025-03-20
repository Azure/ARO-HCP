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

// Resource name patterns as per the ARO-HCP API specification (see hcpCluster-models.tsp).
// The names, when lowercased, must be valid RFC 1035 labels for Cluster Service to accept.
var rxHCPOpenShiftClusterResourceName = regexp.MustCompile(`^[a-zA-Z][-a-zA-Z0-9]{1,52}[a-zA-Z0-9]$`)
var rxNodePoolResourceName = regexp.MustCompile(`^[a-zA-Z][-a-zA-Z0-9]{1,13}[a-z-A-Z0-9]$`)

// MiddlewareValidateStatic ensures that the URL path parses to a valid resource ID.
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

		switch strings.ToLower(resource.ResourceType.Type) {
		case strings.ToLower(api.ClusterResourceType.Type):
			if !rxHCPOpenShiftClusterResourceName.MatchString(resource.Name) {
				arm.WriteError(w, http.StatusBadRequest,
					arm.CloudErrorCodeInvalidResourceName,
					resource.String(),
					"The Resource '%s/%s' under resource group '%s' does not conform to the naming restriction.",
					resource.ResourceType, resource.Name,
					resource.ResourceGroupName)
				return
			}
		case strings.ToLower(api.NodePoolResourceType.Type):
			// The collection GET endpoint for nested resources
			// parses into a ResourceID with an empty Name field.
			if resource.Name != "" && !rxNodePoolResourceName.MatchString(resource.Name) {
				arm.WriteError(w, http.StatusBadRequest,
					arm.CloudErrorCodeInvalidResourceName,
					resource.String(),
					"The Resource '%s/%s' under resource group '%s' does not conform to the naming restriction.",
					resource.ResourceType, resource.Name,
					resource.ResourceGroupName)
				return
			}
		}
	}

	next(w, r)
}
