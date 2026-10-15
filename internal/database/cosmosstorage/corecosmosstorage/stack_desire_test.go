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

package corecosmosstorage

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armdeploymentstacks"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
)

type stackDesireTransport func(*http.Request) (*http.Response, error)

func (transport stackDesireTransport) Do(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestStackDesireTransactionGetItem(t *testing.T) {
	id, err := coreapihelpers.ToStackDesireResourceID("Sub", "RG", "Cluster", "Desire")
	require.NoError(t, err)
	desire := &coreapi.StackDesire{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: id, PartitionKey: "sub", InstanceVersion: 2},
		Spec:           coreapi.StackDesireSpec{Action: coreapi.StackDesireActionApply, Stack: &armdeploymentstacks.DeploymentStack{Properties: &armdeploymentstacks.DeploymentStackProperties{Template: map[string]any{"number": float64(42)}}}},
		Status:         coreapi.StackDesireStatus{ObservedSpecHash: "hash", Stack: &armdeploymentstacks.DeploymentStack{Properties: &armdeploymentstacks.DeploymentStackProperties{CorrelationID: ptr.To("correlation"), ProvisioningState: ptr.To(armdeploymentstacks.DeploymentStackProvisioningStateSucceeded)}}, Error: &armdeploymentstacks.ErrorDetail{Code: ptr.To("error"), Details: []*armdeploymentstacks.ErrorDetail{{Message: ptr.To("detail")}}}},
	}
	document, err := cosmosstorageutils.InternalToCosmosGeneric(desire)
	require.NoError(t, err)
	document.ResourceType = strings.ToUpper(coreapi.StackDesireResourceType.String())
	document.CosmosETag = `"transaction-etag"`
	data, err := json.Marshal(document)
	require.NoError(t, err)
	result := newCosmosDBTransactionResult()
	result.items[document.ID] = data
	item, err := result.GetItem(document.ID)
	require.NoError(t, err)
	actual, ok := item.(*coreapi.StackDesire)
	require.True(t, ok, "transaction result must decode to the same typed API as CRUD and mocks")
	require.Equal(t, desire.Spec, actual.Spec)
	require.Equal(t, desire.Status, actual.Status)
	require.Equal(t, "sub", actual.PartitionKey)
	require.EqualValues(t, 2, actual.InstanceVersion)
	require.Equal(t, document.CosmosETag, actual.CosmosETag)
	require.Equal(t, id.String(), actual.ResourceID.String())
	_, err = result.GetItem("missing")
	require.ErrorIs(t, err, ErrItemNotFound)
}

func TestStackDesireCosmosCreateReplace(t *testing.T) {
	id, err := azcorearm.ParseResourceID("/subscriptions/Sub/resourceGroups/RG/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/Cluster/stackDesires/Desire")
	require.NoError(t, err)
	target, err := azcorearm.ParseResourceID("/subscriptions/other/providers/Microsoft.Resources/deploymentStacks/stack")
	require.NoError(t, err)
	calls := 0
	credential, err := azcosmos.NewKeyCredential("a2V5")
	require.NoError(t, err)
	client, err := azcosmos.NewClientWithKey("https://cosmos.test", credential, &azcosmos.ClientOptions{ClientOptions: policy.ClientOptions{
		Transport: stackDesireTransport(func(request *http.Request) (*http.Response, error) {
			if request.Method == http.MethodGet && (request.URL.Path == "" || request.URL.Path == "/") {
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"writableLocations":[{"name":"test","databaseAccountEndpoint":"https://cosmos.test"}],"readableLocations":[{"name":"test","databaseAccountEndpoint":"https://cosmos.test"}]}`)), Request: request}, nil
			}
			calls++
			require.Equal(t, `["sub"]`, request.Header.Get("x-ms-documentdb-partitionkey"))
			body, err := io.ReadAll(request.Body)
			require.NoError(t, err)
			var document cosmosstorageutils.GenericDocument[coreapi.StackDesire]
			require.NoError(t, json.Unmarshal(body, &document))
			require.Equal(t, strings.ToLower(coreapi.StackDesireResourceType.String()), strings.ToLower(document.ResourceType))
			require.Equal(t, strings.ToLower(id.String()), strings.ToLower(document.ResourceID.String()))
			if calls == 1 {
				require.Equal(t, http.MethodPost, request.Method)
				require.Equal(t, "/dbs/test/colls/Resources/docs", request.URL.Path)
				require.Empty(t, request.Header.Get("If-Match"))
			} else {
				require.Equal(t, http.MethodPut, request.Method)
				require.Equal(t, "/dbs/test/colls/Resources/docs/"+document.ID, request.URL.Path)
				require.Equal(t, `"etag-1"`, request.Header.Get("If-Match"))
			}
			// Return the submitted Cosmos document with the new ETag.
			var response map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(body, &response))
			etag := fmt.Sprintf(`"etag-%d"`, calls)
			response["_etag"], err = json.Marshal(etag)
			require.NoError(t, err)
			body, err = json.Marshal(response)
			require.NoError(t, err)
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}, "Etag": {etag}}, Body: io.NopCloser(bytes.NewReader(body)), Request: request}, nil
		}),
	}})
	require.NoError(t, err)
	database, err := client.NewDatabase("test")
	require.NoError(t, err)
	container, err := database.NewContainer("Resources")
	require.NoError(t, err)
	db := &resourcesCosmosDBClient{resources: container}
	crud := db.StackDesires("SUB", "RG", "Cluster")
	desire := &coreapi.StackDesire{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: id, PartitionKey: "sub"},
		Spec:           coreapi.StackDesireSpec{TargetResourceID: target, Action: coreapi.StackDesireActionApply, Stack: &armdeploymentstacks.DeploymentStack{Properties: &armdeploymentstacks.DeploymentStackProperties{Template: map[string]any{"number": float64(42)}}}},
	}
	created, err := crud.Create(t.Context(), desire, nil)
	require.NoError(t, err)
	require.EqualValues(t, 1, created.InstanceVersion)
	require.Equal(t, desire.Spec.Stack, created.Spec.Stack, "the typed SDK stack must survive storage")
	require.Equal(t, "sub", created.PartitionKey)
	require.Equal(t, strings.ToLower(id.Parent.String()), strings.ToLower(created.ParentResourceID.String()))
	created.Status.Stack = &armdeploymentstacks.DeploymentStack{Properties: &armdeploymentstacks.DeploymentStackProperties{ProvisioningState: ptr.To(armdeploymentstacks.DeploymentStackProvisioningStateSucceeded)}}
	updated, err := crud.Replace(t.Context(), created, nil)
	require.NoError(t, err)
	require.EqualValues(t, 2, updated.InstanceVersion)
	require.EqualValues(t, `"etag-2"`, updated.CosmosETag)
	require.Equal(t, created.Status, updated.Status)
	require.Equal(t, 2, calls)
}
