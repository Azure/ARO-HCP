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

package systemadmincredentialcontrollers

import (
	"fmt"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
)

const (
	testEnvIdentifier        = "test"
	testClusterServiceIDStr  = "/api/clusters_mgmt/v1/clusters/abc123"
	testClusterServiceIDOnly = "abc123"
)

var (
	testManagementClusterResourceID = api.Must(azcorearm.ParseResourceID(
		"/providers/microsoft.redhatopenshift/stamps/1/managementclusters/default"))

	testClusterServiceID = api.Must(api.NewInternalID(testClusterServiceIDStr))
)

func newTestClusterWithCSID() *api.HCPOpenShiftCluster {
	cluster := newTestCluster()
	csID := testClusterServiceID // copy
	cluster.ServiceProviderProperties.ClusterServiceID = &csID
	return cluster
}

func newTestClusterWithDeletion() *api.HCPOpenShiftCluster {
	cluster := newTestCluster()
	now := metav1.NewTime(time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC))
	cluster.ServiceProviderProperties.DeletionTimestamp = &now
	cluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &now
	return cluster
}

func newTestSPC(mcResourceID *azcorearm.ResourceID) *api.ServiceProviderCluster {
	clusterResourceIDStr := fmt.Sprintf(
		"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/%s",
		testSubscriptionID, testResourceGroupName, testClusterName,
	)
	spcResourceIDStr := fmt.Sprintf("%s/%s/%s",
		clusterResourceIDStr,
		api.ServiceProviderClusterResourceTypeName,
		api.ServiceProviderClusterResourceName,
	)
	spcResourceID := api.Must(azcorearm.ParseResourceID(spcResourceIDStr))

	return &api.ServiceProviderCluster{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID:   spcResourceID,
			PartitionKey: strings.ToLower(testSubscriptionID),
		},
		Status: api.ServiceProviderClusterStatus{
			ManagementClusterResourceID: mcResourceID,
		},
	}
}

func newTestCredential(credName string, phase api.SystemAdminCredentialPhase) *api.SystemAdminCredential {
	credResourceID := api.Must(api.ToSystemAdminCredentialResourceID(testSubscriptionID, testResourceGroupName, testClusterName, credName))
	cred := &api.SystemAdminCredential{}
	cred.SetResourceID(credResourceID)
	cred.SetPartitionKey(strings.ToLower(testSubscriptionID))
	cred.Spec = api.SystemAdminCredentialSpec{
		Username:            "system-admin",
		OperationID:         "test-op",
		ExpirationTimestamp: metav1.NewTime(time.Date(2025, 6, 16, 10, 0, 0, 0, time.UTC)),
		PublicKeyPEM:        "test-public-key",
		PrivateKeyPEM:       "test-private-key",
	}
	cred.Status = api.SystemAdminCredentialStatus{
		Phase: phase,
	}
	return cred
}

func newTestCredentialWithDesires(credName string, phase api.SystemAdminCredentialPhase, desires []api.SystemAdminCredentialDesireRef) *api.SystemAdminCredential {
	cred := newTestCredential(credName, phase)
	cred.Status.OutstandingDesires = desires
	return cred
}

func newTestClusterScopedReadDesire(desireName string) *kubeapplier.ReadDesire {
	resourceIDStr := kubeapplier.ToClusterScopedReadDesireResourceIDString(
		testSubscriptionID, testResourceGroupName, testClusterName, desireName)
	resourceID := api.Must(azcorearm.ParseResourceID(resourceIDStr))
	return &kubeapplier.ReadDesire{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(testManagementClusterResourceID.String()),
		},
		Spec: kubeapplier.ReadDesireSpec{
			ManagementCluster: testManagementClusterResourceID,
			TargetItem: kubeapplier.ResourceReference{
				Group:    "",
				Version:  "v1",
				Resource: "secrets",
			},
		},
	}
}

func newTestClusterScopedApplyDesire(desireName string) *kubeapplier.ApplyDesire {
	resourceIDStr := kubeapplier.ToClusterScopedApplyDesireResourceIDString(
		testSubscriptionID, testResourceGroupName, testClusterName, desireName)
	resourceID := api.Must(azcorearm.ParseResourceID(resourceIDStr))
	return &kubeapplier.ApplyDesire{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(testManagementClusterResourceID.String()),
		},
		Spec: kubeapplier.ApplyDesireSpec{
			ManagementCluster: testManagementClusterResourceID,
			TargetItem: kubeapplier.ResourceReference{
				Group:    "",
				Version:  "v1",
				Resource: "secrets",
			},
		},
	}
}

// hcpNamespace returns the expected HCP namespace for the test cluster.
func hcpNamespace() string {
	return fmt.Sprintf("ocm-%s-%s", testEnvIdentifier, testClusterServiceIDOnly)
}

// Ensure imports compile.
var _ arm.CosmosMetadata
