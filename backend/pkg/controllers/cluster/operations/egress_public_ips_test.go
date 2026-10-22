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

package operations

import (
	"context"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	operationbase "github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils/operationtesting"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
)

func confirmedEgressPublicIPs(t *testing.T, cluster *coreapi.Cluster) map[string]coreapi.AzureResourceObservation {
	t.Helper()
	expected, err := coreapihelpers.EgressPublicIPResourceIDs(cluster)
	require.NoError(t, err)
	observations := map[string]coreapi.AzureResourceObservation{}
	for _, resourceID := range expected {
		observations[resourceID.String()] = coreapi.AzureResourceFound
	}
	return observations
}

func TestEgressPublicIPsOperationStatus(t *testing.T) {
	for _, name := range []string{"all", "zero", "partial", "unrelated", "unknown", "read failure", "UDR", "default", "deleting", "missing cluster ID", "missing group", "uncached"} {
		t.Run(name, func(t *testing.T) {
			fixture := operationtesting.NewClusterTestFixture()
			cluster := fixture.NewCluster(nil)
			cluster.CustomerProperties.Platform.ManagedResourceGroup = "managed-rg"
			cluster.CustomerProperties.Platform.OutboundType = metadataapi.OutboundTypeLoadBalancer
			observations := confirmedEgressPublicIPs(t, cluster)
			expected, err := coreapihelpers.EgressPublicIPResourceIDs(cluster)
			require.NoError(t, err)
			wantState := coreapi.ProvisioningStateProvisioning
			wantMessage := "Unable to verify"
			switch name {
			case "all", "default", "UDR", "deleting":
				wantState, wantMessage = coreapi.ProvisioningStateSucceeded, ""
			case "zero":
				for _, resourceID := range expected {
					observations[resourceID.String()] = coreapi.AzureResourceNotFound
				}
				wantMessage = "Check that Azure Policy permits"
			case "partial":
				observations[expected[0].String()] = coreapi.AzureResourceNotFound
				wantMessage = "Check that Azure Policy permits"
			case "unrelated":
				observations = map[string]coreapi.AzureResourceObservation{"unrelated": coreapi.AzureResourceFound}
			case "unknown":
				observations = nil
			case "read failure":
				observations[expected[0].String()] = coreapi.AzureResourceNotFound
				observations[expected[8].String()] = coreapi.AzureResourceUnknown
			case "missing cluster ID":
				cluster.ServiceProviderProperties.ClusterServiceID = nil
			case "missing group":
				cluster.CustomerProperties.Platform.ManagedResourceGroup = ""
			}
			if name == "UDR" {
				cluster.CustomerProperties.Platform.OutboundType = metadataapi.OutboundType("UserDefinedRouting")
			}
			if name == "default" {
				cluster.CustomerProperties.Platform.OutboundType = ""
			}
			if name == "deleting" {
				cluster.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{}
			}
			serviceProvider := &coreapi.ServiceProviderCluster{CosmosMetadata: coreapi.CosmosMetadata{ResourceID: metadataapi.Must(azcorearm.ParseResourceID(fixture.ClusterResourceID.String() + "/" + coreapi.ServiceProviderClusterResourceTypeName + "/" + coreapi.ServiceProviderClusterResourceName))}}
			serviceProvider.Status.AzureResources.EgressPublicIPs = observations
			lister := &corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: []*coreapi.ServiceProviderCluster{serviceProvider}}
			if name == "uncached" {
				lister.ServiceProviderClusters = nil
			}
			controller := &operationClusterCreate{serviceProviderClusterLister: lister}
			state := controller.egressPublicIPsOperationStatus(context.Background(), cluster)
			require.Equal(t, wantState, state.ProvisioningState)
			require.Contains(t, state.Message, wantMessage)
			if wantMessage == "Unable to verify" {
				require.NotContains(t, state.Message, "Azure Policy")
			}
			if state.ProvisioningState == coreapi.ProvisioningStateProvisioning {
				terminal := operationbase.NewOperationState(coreapi.ProvisioningStateFailed, "stronger failure").WithCloudErrorCode(coreapi.CloudErrorCodeInvalidParameter)
				states := []*operationbase.OperationState{state.WithSource("egressPublicIPs"), terminal}
				slices.SortStableFunc(states, operationbase.CompareOperationState)
				worst, err := operationbase.PickWorstOperationState(states)
				require.NoError(t, err)
				require.Equal(t, coreapi.CloudErrorCodeInvalidParameter, worst.CloudErrorCode)
				require.NotContains(t, worst.Message, "public IP")
			}
		})
	}
}
