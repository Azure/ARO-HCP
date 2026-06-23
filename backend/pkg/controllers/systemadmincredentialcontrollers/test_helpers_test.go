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

package systemadmincredentialcontrollers

import (
	"context"
	"testing"

	"github.com/go-logr/logr/testr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"k8s.io/apimachinery/pkg/runtime"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// Common test constants
const (
	testSubscriptionID    = "00000000-0000-0000-0000-000000000000"
	testResourceGroupName = "test-rg"
	testClusterName       = "test-cluster"
	testOperationName     = "aaaabbbb-cccc-dddd-eeee-ffffffffffff"
	testCredentialName    = "abcdef0123456789"
	testHCPClusterNSEnvID = "arohcptest"
	testCSClusterID       = "12345"
	testMCResourceIDStr   = "/subscriptions/11111111-1111-1111-1111-111111111111/resourceGroups/mc-rg/providers/Microsoft.ContainerService/managedClusters/mc-cluster"
)

func testCtx(t *testing.T) context.Context {
	return utils.ContextWithLogger(context.Background(), testr.New(t))
}

func testClusterKey() controllerutils.HCPClusterKey {
	return controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	}
}

func testCredentialKey() controllerutils.HCPSystemAdminCredentialKey {
	return controllerutils.HCPSystemAdminCredentialKey{
		SubscriptionID:               testSubscriptionID,
		ResourceGroupName:            testResourceGroupName,
		HCPClusterName:               testClusterName,
		HCPSystemAdminCredentialName: testCredentialName,
	}
}

func testOperationKey() controllerutils.OperationKey {
	clusterRID := api.Must(api.ToClusterResourceID(testSubscriptionID, testResourceGroupName, testClusterName))
	return controllerutils.OperationKey{
		SubscriptionID:   testSubscriptionID,
		OperationName:    testOperationName,
		ParentResourceID: clusterRID.String(),
	}
}

func testMCResourceID() *azcorearm.ResourceID {
	return api.Must(azcorearm.ParseResourceID(testMCResourceIDStr))
}

func testClusterRID() *azcorearm.ResourceID {
	return api.Must(api.ToClusterResourceID(testSubscriptionID, testResourceGroupName, testClusterName))
}

func testCSInternalID() api.InternalID {
	return api.Must(api.NewInternalID("/api/clusters_mgmt/v1/clusters/" + testCSClusterID))
}

func testCluster() *api.HCPOpenShiftCluster {
	csID := testCSInternalID()
	return &api.HCPOpenShiftCluster{
		CosmosMetadata: arm.CosmosMetadata{
			ResourceID: testClusterRID(),
		},
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   testClusterRID(),
				Name: testClusterName,
				Type: testClusterRID().ResourceType.String(),
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ClusterServiceID: &csID,
		},
	}
}

func testSPC(mcRID *azcorearm.ResourceID) *api.ServiceProviderCluster {
	spcRIDStr := api.ToServiceProviderClusterResourceIDString(testSubscriptionID, testResourceGroupName, testClusterName)
	spcRID := api.Must(azcorearm.ParseResourceID(spcRIDStr))
	return &api.ServiceProviderCluster{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID: spcRID,
		},
		Status: api.ServiceProviderClusterStatus{
			ManagementClusterResourceID: mcRID,
		},
	}
}

func testCredential(phase api.SystemAdminCredentialPhase) *api.SystemAdminCredential {
	credRID := api.Must(api.ToSystemAdminCredentialResourceID(testSubscriptionID, testResourceGroupName, testClusterName, testCredentialName))
	return &api.SystemAdminCredential{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID: credRID,
		},
		Spec: api.SystemAdminCredentialSpec{
			Username:      defaultUsername,
			OperationID:   testOperationName,
			PublicKeyPEM:  "test-public-key-pem",
			PrivateKeyPEM: "test-private-key-pem",
		},
		Status: api.SystemAdminCredentialStatus{
			Phase: phase,
		},
	}
}

func testOperation(request database.OperationRequest, status arm.ProvisioningState) *api.Operation {
	operationRID := api.Must(azcorearm.ParseResourceID(api.ToOperationResourceIDString(testSubscriptionID, testOperationName)))
	clusterRID := testClusterRID()
	return &api.Operation{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID: operationRID,
		},
		Status:      status,
		Request:     request,
		ExternalID:  clusterRID,
		OperationID: operationRID,
	}
}

// createCredentialInDB adds a SystemAdminCredential directly to the mock DB.
func createCredentialInDB(ctx context.Context, t *testing.T, db *api.SystemAdminCredential, resourcesDBClient database.ResourcesDBClient) {
	t.Helper()
	rid := db.GetResourceID()
	credentialsCRUD := resourcesDBClient.HCPClusters(rid.SubscriptionID, rid.ResourceGroupName).SystemAdminCredentials(rid.Parent.Name)
	if _, err := credentialsCRUD.Create(ctx, db, nil); err != nil {
		t.Fatalf("failed to create credential in mock DB: %v", err)
	}
}

// getCredentialFromDB retrieves a SystemAdminCredential from the mock DB.
func getCredentialFromDB(ctx context.Context, t *testing.T, resourcesDBClient database.ResourcesDBClient, credName string) *api.SystemAdminCredential {
	t.Helper()
	credentialsCRUD := resourcesDBClient.HCPClusters(testSubscriptionID, testResourceGroupName).SystemAdminCredentials(testClusterName)
	cred, err := credentialsCRUD.Get(ctx, credName)
	if err != nil {
		t.Fatalf("failed to get credential from mock DB: %v", err)
	}
	return cred
}

// testReadDesireWithCSR creates a ReadDesire with a CSR in the KubeContent.
func testReadDesireWithKubeContent(clusterRID *azcorearm.ResourceID, desireName string, kubeContent []byte) *kubeapplier.ReadDesire {
	desireRIDStr := kubeapplier.ToClusterScopedReadDesireResourceIDString(
		clusterRID.SubscriptionID, clusterRID.ResourceGroupName, clusterRID.Name, desireName)
	desireRID := api.Must(azcorearm.ParseResourceID(desireRIDStr))
	rd := &kubeapplier.ReadDesire{
		CosmosMetadata: api.CosmosMetadata{ResourceID: desireRID},
		Spec: kubeapplier.ReadDesireSpec{
			ManagementCluster: testMCResourceID(),
		},
	}
	_ = kubeapplier.ReadDesireResourceTypeName // ensure import is used
	if kubeContent != nil {
		rd.Status.KubeContent = &runtime.RawExtension{Raw: kubeContent}
	}
	return rd
}
