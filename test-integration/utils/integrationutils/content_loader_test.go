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

package integrationutils

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
)

func TestMockCosmosLoadContent(t *testing.T) {
	for _, testCase := range []struct {
		name              string
		content           string
		expectedContainer string
	}{
		{name: "management cluster", content: `{"id":"fixture","partitionKey":"1","resourceType":"Microsoft.RedHatOpenShift/stamps/managementClusters"}`, expectedContainer: "Fleet"},
		{name: "stamp case insensitive", content: `{"id":"fixture","partitionKey":"1","resourceType":"microsoft.redhatopenshift/STAMPS"}`, expectedContainer: "Fleet"},
		{name: "service provider cluster", content: `{"id":"fixture","partitionKey":"subscription","resourceType":"Microsoft.RedHatOpenShift/hcpOpenShiftClusters/serviceProviderClusters"}`, expectedContainer: "Resources"},
		{name: "invalid JSON", content: `{`},
		{name: "missing ID", content: `{"partitionKey":"1","resourceType":"Microsoft.RedHatOpenShift/stamps/managementClusters"}`},
		{name: "missing partition", content: `{"id":"fixture","resourceType":"Microsoft.RedHatOpenShift/stamps/managementClusters"}`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			storage, err := NewMockCosmosFromTestingEnv(t.Context(), t)
			require.NoError(t, err)
			mock := storage.(*MockCosmosIntegrationTestInfo)
			err = mock.LoadContent(t.Context(), []byte(testCase.content))
			if testCase.expectedContainer == "" {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			containers := map[string]map[string]json.RawMessage{
				"Resources": mock.mockResourcesDBClient.GetAllDocuments(),
				"Fleet":     mock.mockFleetDBClient.GetAllDocuments(),
			}
			for container, documents := range containers {
				if container != testCase.expectedContainer {
					require.Empty(t, documents)
					continue
				}
				require.Len(t, documents, 1)
				require.JSONEq(t, testCase.content, string(documents["fixture"]))
			}
		})
	}
}

func TestPrepareCosmosDocument(t *testing.T) {
	for _, testCase := range []struct {
		resourceType      azcorearm.ResourceType
		expectedContainer string
	}{
		{azcorearm.SubscriptionResourceType, "Resources"},
		{coreapi.OperationStatusResourceType, "Resources"},
		{coreapi.ClusterResourceType, "Resources"},
		{coreapi.NodePoolResourceType, "Resources"},
		{coreapi.ExternalAuthResourceType, "Resources"},
		{coreapi.ServiceProviderClusterResourceType, "Resources"},
		{coreapi.ServiceProviderNodePoolResourceType, "Resources"},
		{coreapi.ServiceProviderExternalAuthResourceType, "Resources"},
		{coreapi.ClusterControllerResourceType, "Resources"},
		{coreapi.NodePoolControllerResourceType, "Resources"},
		{coreapi.ExternalAuthControllerResourceType, "Resources"},
		{coreapi.SystemAdminCredentialRequestResourceType, "Resources"},
		{coreapi.SystemAdminCredentialRevocationResourceType, "Resources"},
		{coreapi.SystemAdminCredentialRequestControllerResourceType, "Resources"},
		{coreapi.SystemAdminCredentialRevocationControllerResourceType, "Resources"},
		{coreapi.ClusterScopedManagementClusterContentResourceType, "Resources"},
		{coreapi.NodePoolScopedManagementClusterContentResourceType, "Resources"},
		{fleetapi.StampResourceType, "Fleet"},
		{fleetapi.ManagementClusterResourceType, "Fleet"},
		{fleetapi.ManagementClusterControllerResourceType, "Fleet"},
		{fleetapi.ManagementClusterSchedulingResourceType, "Fleet"},
		{fleetapi.HCPResourceRequirementsResourceType, "Fleet"},
		{fleetapi.ControlPlaneVersionRolloutResourceType, "Fleet"},
	} {
		t.Run(testCase.resourceType.String(), func(t *testing.T) {
			content := []byte(fmt.Sprintf(`{"id":"fixture","partitionKey":"partition","resourceType":%q}`, strings.ToUpper(testCase.resourceType.String())))
			document, container, err := prepareCosmosDocument(content)
			require.NoError(t, err)
			require.Equal(t, testCase.expectedContainer, container)
			require.Equal(t, "fixture", document.ID)
			require.Equal(t, "partition", document.PartitionKey)
		})
	}
	for _, resourceType := range []string{"", "unknown/type", fleetapi.StampResourceType.String() + "Extra", fleetapi.StampResourceType.String() + "/unknown", kubeapplierapi.ClusterScopedApplyDesireResourceType.String(), kubeapplierapi.NodePoolScopedReadDesireResourceType.String(), kubeapplierapi.ManagementClusterScopedApplyDesireResourceType.String()} {
		t.Run("reject/"+resourceType, func(t *testing.T) {
			content := []byte(fmt.Sprintf(`{"id":"fixture","partitionKey":"partition","resourceType":%q}`, resourceType))
			document, container, err := prepareCosmosDocument(content)
			require.Error(t, err)
			require.Nil(t, document)
			require.Empty(t, container)
		})
	}
	for _, content := range []string{`{`, `null`, `{}`, `{"resourceType":3}`, `{"resourceType":"Microsoft.Resources/subscriptions","id":"fixture"}`, `{"resourceType":"Microsoft.Resources/subscriptions","partitionKey":"subscription"}`} {
		_, _, err := prepareCosmosDocument([]byte(content))
		require.Error(t, err)
	}
}

func TestLoadCosmosContentMissingClient(t *testing.T) {
	for _, resourceType := range []azcorearm.ResourceType{coreapi.ClusterResourceType, fleetapi.ManagementClusterResourceType} {
		content := []byte(fmt.Sprintf(`{"id":"fixture","partitionKey":"partition","resourceType":%q}`, resourceType.String()))
		require.ErrorContains(t, LoadCosmosContent(t.Context(), nil, nil, content), "container client is missing")
	}
}

func TestContainerListingAndSaveErrors(t *testing.T) {
	for _, container := range []string{"Resources", "Fleet"} {
		t.Run(container, func(t *testing.T) {
			storage, err := NewMockCosmosFromTestingEnv(t.Context(), t)
			require.NoError(t, err)
			mock := storage.(*MockCosmosIntegrationTestInfo)
			if container == "Resources" {
				mock.mockResourcesDBClient.StoreDocument("invalid", json.RawMessage(`{`))
			} else {
				mock.mockFleetDBClient.StoreDocument("invalid", json.RawMessage(`{`))
			}
			documents, err := mock.ListAllDocuments(t.Context())
			require.ErrorContains(t, err, container)
			require.Nil(t, documents)
			_, err = mock.ListDocumentsByContainer(t.Context())
			require.ErrorContains(t, err, container)
		})
	}

	storage, err := NewMockCosmosFromTestingEnv(t.Context(), t)
	require.NoError(t, err)
	mock := storage.(*MockCosmosIntegrationTestInfo)
	require.NoError(t, mock.LoadContent(t.Context(), []byte(`{"id":"subscription","partitionKey":"subscription","resourceType":"Microsoft.Resources/subscriptions","properties":{}}`)))
	containers, err := mock.ListDocumentsByContainer(t.Context())
	require.NoError(t, err)
	directory := t.TempDir()
	require.NoError(t, saveAllDatabaseContent(t.Context(), containers, directory))
	resourcePath := filepath.Join(directory, "cosmos-content", "Resources", "subscriptions", "subscription_subscription.json")
	require.FileExists(t, resourcePath, "existing Resources artifact layout must not change")
	require.NoError(t, os.Remove(resourcePath))
	require.NoError(t, os.Mkdir(resourcePath, 0755))
	require.ErrorContains(t, saveAllDatabaseContent(t.Context(), containers, directory), "failed to write document")
}
