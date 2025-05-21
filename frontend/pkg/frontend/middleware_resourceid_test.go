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
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"
	semconv "go.opentelemetry.io/otel/semconv/v1.27.0"
)

func TestMiddlewareResourceID(t *testing.T) {
	tests := []struct {
		name          string
		path          string
		resourceTypes []string
		expectedErr   bool
	}{
		{
			name: "subscription resource",
			path: "/SUBSCRIPTIONS/00000000-0000-0000-0000-000000000000",
			resourceTypes: []string{
				azcorearm.SubscriptionResourceType.String(),
				azcorearm.TenantResourceType.String(),
			},
		},
		{
			name: "cluster resource",
			path: "/SUBSCRIPTIONS/00000000-0000-0000-0000-000000000000/RESOURCEGROUPS/MyResourceGroup/PROVIDERS/MICROSOFT.REDHATOPENSHIFT/HCPOPENSHIFTCLUSTERS/myCluster",
			resourceTypes: []string{
				"MICROSOFT.REDHATOPENSHIFT/HCPOPENSHIFTCLUSTERS",
				azcorearm.ResourceGroupResourceType.String(),
				azcorearm.SubscriptionResourceType.String(),
				azcorearm.TenantResourceType.String(),
			},
		},
		{
			// Parser treats the action name as a subtype
			name: "cluster resource with action",
			path: "/SUBSCRIPTIONS/00000000-0000-0000-0000-000000000000/RESOURCEGROUPS/MyResourceGroup/PROVIDERS/MICROSOFT.REDHATOPENSHIFT/HCPOPENSHIFTCLUSTERS/myCluster/myAction",
			resourceTypes: []string{
				"MICROSOFT.REDHATOPENSHIFT/HCPOPENSHIFTCLUSTERS/myAction",
				"MICROSOFT.REDHATOPENSHIFT/HCPOPENSHIFTCLUSTERS",
				azcorearm.ResourceGroupResourceType.String(),
				azcorearm.SubscriptionResourceType.String(),
				azcorearm.TenantResourceType.String(),
			},
		},
		{
			name: "node pool resource",
			path: "/SUBSCRIPTIONS/00000000-0000-0000-0000-000000000000/RESOURCEGROUPS/MyResourceGroup/PROVIDERS/MICROSOFT.REDHATOPENSHIFT/HCPOPENSHIFTCLUSTERS/myCluster/NODEPOOLS/myNodePool",
			resourceTypes: []string{
				"MICROSOFT.REDHATOPENSHIFT/HCPOPENSHIFTCLUSTERS/NODEPOOLS",
				"MICROSOFT.REDHATOPENSHIFT/HCPOPENSHIFTCLUSTERS",
				azcorearm.ResourceGroupResourceType.String(),
				azcorearm.SubscriptionResourceType.String(),
				azcorearm.TenantResourceType.String(),
			},
		},
		{
			name: "node pool collection",
			path: "/SUBSCRIPTIONS/00000000-0000-0000-0000-000000000000/RESOURCEGROUPS/MyResourceGroup/PROVIDERS/MICROSOFT.REDHATOPENSHIFT/HCPOPENSHIFTCLUSTERS/myCluster/NODEPOOLS",
			resourceTypes: []string{
				"MICROSOFT.REDHATOPENSHIFT/HCPOPENSHIFTCLUSTERS/NODEPOOLS",
				"MICROSOFT.REDHATOPENSHIFT/HCPOPENSHIFTCLUSTERS",
				azcorearm.ResourceGroupResourceType.String(),
				azcorearm.SubscriptionResourceType.String(),
				azcorearm.TenantResourceType.String(),
			},
		},
		{
			name: "preflight deployment",
			path: "/SUBSCRIPTIONS/00000000-0000-0000-0000-000000000000/RESOURCEGROUPS/MyResourceGroup/PROVIDERS/MICROSOFT.REDHATOPENSHIFT/DEPLOYMENTS/MyDeployment/preflight",
			resourceTypes: []string{
				"MICROSOFT.REDHATOPENSHIFT/DEPLOYMENTS/preflight",
				"MICROSOFT.REDHATOPENSHIFT/DEPLOYMENTS",
				azcorearm.ResourceGroupResourceType.String(),
				azcorearm.SubscriptionResourceType.String(),
				azcorearm.TenantResourceType.String(),
			},
		},
		{
			name: "operation statuses",
			path: "/SUBSCRIPTIONS/00000000-0000-0000-0000-000000000000/PROVIDERS/MICROSOFT.REDHATOPENSHIFT/LOCATIONS/eastus/HCPOPERATIONSTATUSES/11111111-1111-1111-1111-111111111111",
			resourceTypes: []string{
				"MICROSOFT.REDHATOPENSHIFT/LOCATIONS/HCPOPERATIONSTATUSES",
				"MICROSOFT.REDHATOPENSHIFT/LOCATIONS",
				azcorearm.SubscriptionResourceType.String(),
				azcorearm.TenantResourceType.String(),
			},
		},
		{
			name:          "invalid path",
			path:          "/healthz",
			resourceTypes: []string{},
			expectedErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writer := httptest.NewRecorder()

			// Convert path to simulate MiddlewareLowercase
			url := "http://example.com" + strings.ToLower(tt.path)

			ctx := context.Background()
			ctx = ContextWithLogger(ctx, slog.Default())
			ctx = ContextWithOriginalPath(ctx, tt.path)

			ctx, sr := initSpanRecorder(ctx)

			request := httptest.NewRequestWithContext(ctx, "GET", url, nil)

			next := func(w http.ResponseWriter, r *http.Request) {
				request = r // capture modified request
				w.WriteHeader(http.StatusOK)
			}

			MiddlewareResourceID(writer, request, next)

			resourceID, err := ResourceIDFromContext(request.Context())
			if tt.expectedErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			resourceTypes := []string{}
			for resourceID != nil {
				resourceTypes = append(resourceTypes, resourceID.ResourceType.String())
				resourceID = resourceID.Parent
			}

			if !reflect.DeepEqual(resourceTypes, tt.resourceTypes) {
				t.Error(cmp.Diff(resourceTypes, tt.resourceTypes))
			}

			// Check that the "cloud.resource_id" attribute has been added and isn't empty.
			ss := sr.collect()
			require.Len(t, ss, 1)
			require.Len(t, ss[0].Attributes(), 1)
			require.Equal(t, semconv.CloudResourceIDKey, ss[0].Attributes()[0].Key)
			require.NotEmpty(t, ss[0].Attributes()[0].Value.AsString())
		})
	}
}
