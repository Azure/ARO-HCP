package frontend

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestMiddlewareResourceID(t *testing.T) {
	tests := []struct {
		name          string
		path          string
		resourceTypes []string
	}{
		{
			name: "subscription resource",
			path: "/SUBSCRIPTIONS/00000000-0000-0000-0000-000000000000",
			resourceTypes: []string{
				"Microsoft.Resources/subscriptions",
				"Microsoft.Resources/tenants",
			},
		},
		{
			name: "cluster resource",
			path: "/SUBSCRIPTIONS/00000000-0000-0000-0000-000000000000/RESOURCEGROUPS/MyResourceGroup/PROVIDERS/MICROSOFT.REDHATOPENSHIFT/HCPOPENSHIFTCLUSTERS/myCluster",
			resourceTypes: []string{
				"MICROSOFT.REDHATOPENSHIFT/HCPOPENSHIFTCLUSTERS",
				"Microsoft.Resources/resourceGroups",
				"Microsoft.Resources/subscriptions",
				"Microsoft.Resources/tenants",
			},
		},
		{
			// Parser treats the action name as a subtype
			name: "cluster resource with action",
			path: "/SUBSCRIPTIONS/00000000-0000-0000-0000-000000000000/RESOURCEGROUPS/MyResourceGroup/PROVIDERS/MICROSOFT.REDHATOPENSHIFT/HCPOPENSHIFTCLUSTERS/myCluster/myAction",
			resourceTypes: []string{
				"MICROSOFT.REDHATOPENSHIFT/HCPOPENSHIFTCLUSTERS/myAction",
				"MICROSOFT.REDHATOPENSHIFT/HCPOPENSHIFTCLUSTERS",
				"Microsoft.Resources/resourceGroups",
				"Microsoft.Resources/subscriptions",
				"Microsoft.Resources/tenants",
			},
		},
		{
			name: "node pool resource",
			path: "/SUBSCRIPTIONS/00000000-0000-0000-0000-000000000000/RESOURCEGROUPS/MyResourceGroup/PROVIDERS/MICROSOFT.REDHATOPENSHIFT/HCPOPENSHIFTCLUSTERS/myCluster/NODEPOOLS/myNodePool",
			resourceTypes: []string{
				"MICROSOFT.REDHATOPENSHIFT/HCPOPENSHIFTCLUSTERS/NODEPOOLS",
				"MICROSOFT.REDHATOPENSHIFT/HCPOPENSHIFTCLUSTERS",
				"Microsoft.Resources/resourceGroups",
				"Microsoft.Resources/subscriptions",
				"Microsoft.Resources/tenants",
			},
		},
		{
			name: "preflight deployment",
			path: "/SUBSCRIPTIONS/00000000-0000-0000-0000-000000000000/RESOURCEGROUPS/MyResourceGroup/PROVIDERS/MICROSOFT.REDHATOPENSHIFT/DEPLOYMENTS/preflight",
			resourceTypes: []string{
				"MICROSOFT.REDHATOPENSHIFT/DEPLOYMENTS",
				"Microsoft.Resources/resourceGroups",
				"Microsoft.Resources/subscriptions",
				"Microsoft.Resources/tenants",
			},
		},
		{
			name: "operation status",
			path: "/SUBSCRIPTIONS/00000000-0000-0000-0000-000000000000/PROVIDERS/MICROSOFT.REDHATOPENSHIFT/LOCATIONS/eastus/HCPOPERATIONSSTATUS/11111111-1111-1111-1111-111111111111",
			resourceTypes: []string{
				"MICROSOFT.REDHATOPENSHIFT/LOCATIONS/HCPOPERATIONSSTATUS",
				"MICROSOFT.REDHATOPENSHIFT/LOCATIONS",
				"Microsoft.Resources/subscriptions",
				"Microsoft.Resources/tenants",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writer := httptest.NewRecorder()

			// Convert path to simulate MiddlewareLowercase
			url := "http://example.com" + strings.ToLower(tt.path)

			request := httptest.NewRequest("GET", url, nil)
			request = request.WithContext(ContextWithLogger(request.Context(), slog.Default()))
			request = request.WithContext(ContextWithOriginalPath(request.Context(), tt.path))

			next := func(w http.ResponseWriter, r *http.Request) {
				request = r // capture modified request
				w.WriteHeader(http.StatusOK)
			}

			MiddlewareResourceID(writer, request, next)

			resourceID, err := ResourceIDFromContext(request.Context())
			if err != nil {
				t.Error(err)
			}

			resourceTypes := []string{}
			for resourceID != nil {
				resourceTypes = append(resourceTypes, resourceID.ResourceType.String())
				resourceID = resourceID.GetParent()
			}

			if !reflect.DeepEqual(resourceTypes, tt.resourceTypes) {
				t.Error(cmp.Diff(resourceTypes, tt.resourceTypes))
			}
		})
	}
}
