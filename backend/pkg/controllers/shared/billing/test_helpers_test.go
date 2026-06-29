// Copyright 2026 Microsoft Corporation
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

package billing

import (
	"strings"
	"testing"
	"time"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

// mustParseTime parses a time string in RFC3339 format and panics on error.
// Use for test constants to make date values more readable.
func mustParseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

// Common test constants
const (
	testSubscriptionID      = "00000000-0000-0000-0000-000000000000"
	testResourceGroupName   = "test-rg"
	testClusterName         = "test-cluster"
	testTenantID            = "11111111-1111-1111-1111-111111111111"
	testAzureLocation       = "eastus"
	testClusterServiceIDStr = "/api/clusters_mgmt/v1/clusters/abc123"
)

func newTestCluster(t *testing.T, clusterUID string, state arm.ProvisioningState, createdAt *time.Time) *api.HCPOpenShiftCluster {
	t.Helper()

	var systemData *arm.SystemData
	if createdAt != nil {
		systemData = &arm.SystemData{CreatedAt: createdAt}
	}

	resourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName,
	))

	return &api.HCPOpenShiftCluster{
		CosmosMetadata: arm.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:         resourceID,
				Name:       testClusterName,
				Type:       resourceID.ResourceType.String(),
				SystemData: systemData,
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ProvisioningState: state,
			ClusterUID:        clusterUID,
			ClusterServiceID:  api.Ptr(api.Must(api.NewInternalID(testClusterServiceIDStr))),
		},
	}
}
