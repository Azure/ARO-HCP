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
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	// This will invoke the init() function in each
	// API version package so it can register itself.
	_ "github.com/Azure/ARO-HCP/internal/api/v20240610preview"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/v20240610preview/generated"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/mocks"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

var dummyClusterHREF = ocm.GenerateClusterHREF(api.TestClusterName)
var dummyNodePoolHREF = ocm.GenerateNodePoolHREF(dummyClusterHREF, api.TestNodePoolName)

var dummyVMSize = "Big"
var dummyVersionID = "4.18.0"

func TestCreateNodePool(t *testing.T) {
	clusterResourceID, _ := azcorearm.ParseResourceID(api.TestClusterResourceID)
	clusterDoc := database.NewResourceDocument(clusterResourceID)
	clusterDoc.InternalID, _ = api.NewInternalID(dummyClusterHREF)

	nodePoolResourceID, _ := azcorearm.ParseResourceID(api.TestNodePoolResourceID)
	nodePoolDoc := database.NewResourceDocument(nodePoolResourceID)
	nodePoolDoc.InternalID, _ = api.NewInternalID(dummyNodePoolHREF)

	arm.SetAzureLocation(api.TestLocation)
	location := arm.GetAzureLocation()

	requestBody := generated.NodePool{
		Location: &location,
		Properties: &generated.NodePoolProperties{
			Version: &generated.NodePoolVersionProfile{
				ID:           &dummyVersionID,
				ChannelGroup: api.Ptr("stable"),
			},
			Platform: &generated.NodePoolPlatformProfile{
				VMSize: &dummyVMSize,
			},
		},
	}

	expectedBuilder := arohcpv1alpha1.NewNodePool().
		ID(strings.ToLower(api.TestNodePoolName)).
		AvailabilityZone("").
		AzureNodePool(arohcpv1alpha1.NewAzureNodePool().
			ResourceName(strings.ToLower(api.TestNodePoolName)).
			VMSize(dummyVMSize).
			EncryptionAtHost(
				arohcpv1alpha1.NewAzureNodePoolEncryptionAtHost().
					State("disabled"),
			).
			OsDisk(arohcpv1alpha1.NewAzureNodePoolOsDisk().
				SizeGibibytes(64).
				StorageAccountType("Premium_LRS")),
		).
		Subnet("").
		Version(arohcpv1alpha1.NewVersion().
			ID(ocm.NewOpenShiftVersionXYZ(dummyVersionID, "stable")).
			ChannelGroup("stable"),
		).
		Replicas(0).
		AutoRepair(true)

	tests := []struct {
		name               string
		urlPath            string
		subscription       *arm.Subscription
		systemData         *arm.SystemData
		subDoc             *arm.Subscription
		clusterDoc         *database.ResourceDocument
		nodePoolDoc        *database.ResourceDocument
		expectedBuilder    *arohcpv1alpha1.NodePoolBuilder
		expectedStatusCode int
	}{
		{
			name:    "PUT Node Pool - Create a new Node Pool",
			urlPath: api.TestNodePoolResourceID + "?api-version=2024-06-10-preview",
			subDoc: &arm.Subscription{
				State:            arm.SubscriptionStateRegistered,
				RegistrationDate: api.Ptr(time.Now().String()),
				Properties:       nil,
			},
			clusterDoc:         clusterDoc,
			nodePoolDoc:        nodePoolDoc,
			expectedBuilder:    expectedBuilder,
			expectedStatusCode: http.StatusCreated,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockDBClient := mocks.NewMockDBClient(ctrl)
			mockDBTransaction := mocks.NewMockDBTransaction(ctrl)
			mockDBTransactionResult := mocks.NewMockDBTransactionResult(ctrl)
			mockCSClient := mocks.NewMockClusterServiceClientSpec(ctrl)
			pk := database.NewPartitionKey(api.TestSubscriptionID)
			reg := prometheus.NewRegistry()

			f := NewFrontend(
				api.NewTestLogger(),
				nil,
				nil,
				reg,
				mockDBClient,
				mockCSClient,
				newNoopAuditClient(t),
			)

			requestHeader := make(http.Header)
			requestHeader.Add(arm.HeaderNameHomeTenantID, api.TestTenantID)

			body, _ := json.Marshal(requestBody)

			subs := map[string]*arm.Subscription{api.TestSubscriptionID: test.subDoc}
			ts := newHTTPServer(f, ctrl, mockDBClient, subs)

			// CreateOrUpdateNodePool
			mockCSClient.EXPECT().
				GetCluster(gomock.Any(), clusterDoc.InternalID).
				Return(arohcpv1alpha1.NewCluster().
					API(arohcpv1alpha1.NewClusterAPI().
						Listening(arohcpv1alpha1.ListeningMethodExternal)).
					Azure(arohcpv1alpha1.NewAzure().
						NodesOutboundConnectivity(arohcpv1alpha1.NewAzureNodesOutboundConnectivity().
							OutboundType("load_balancer"))).
					ImageRegistry(arohcpv1alpha1.NewClusterImageRegistry().
						State("enabled")).
					Version(arohcpv1alpha1.NewVersion().
						ChannelGroup("stable")).
					Autoscaler(arohcpv1alpha1.NewClusterAutoscaler().
						PodPriorityThreshold(-10).
						MaxNodeProvisionTime("15m").
						MaxPodGracePeriod(600)).
					Build())
			// CreateOrUpdateNodePool
			mockCSClient.EXPECT().
				PostNodePool(gomock.Any(), clusterDoc.InternalID, expectedBuilder).
				DoAndReturn(
					func(ctx context.Context, clusterInternalID ocm.InternalID, builder *arohcpv1alpha1.NodePoolBuilder) (*arohcpv1alpha1.NodePool, error) {
						return builder.HREF(dummyNodePoolHREF).Build()
					},
				)

			// MiddlewareLockSubscription
			mockDBClient.EXPECT().
				GetLockClient()
			// MiddlewareValidateSubscriptionState
			mockDBClient.EXPECT().
				GetSubscriptionDoc(gomock.Any(), api.TestSubscriptionID).
				Return(test.subDoc, nil).
				Times(1)
			// CreateOrUpdateNodePool
			mockDBClient.EXPECT().
				GetResourceDoc(gomock.Any(), equalResourceID(test.nodePoolDoc.ResourceID)).
				Return("", nil, &azcore.ResponseError{StatusCode: http.StatusNotFound})
			// CheckForProvisioningStateConflict and CreateOrUpdateNodePool
			mockDBClient.EXPECT().
				GetResourceDoc(gomock.Any(), equalResourceID(test.clusterDoc.ResourceID)). // defined in frontend_test.go
				Return("itemID", test.clusterDoc, nil).
				Times(3)
			// CreateOrUpdateNodePool
			mockDBClient.EXPECT().
				NewTransaction(pk).
				Return(mockDBTransaction)
			// CreateOrUpdateNodePool
			operationID := uuid.New().String()
			mockDBTransaction.EXPECT().
				CreateOperationDoc(gomock.Any(), nil).
				Return(operationID)

			// ExposeOperation
			mockDBTransaction.EXPECT().
				PatchOperationDoc(operationID, gomock.Any(), nil)
			// ExposeOperation
			mockDBTransaction.EXPECT().
				OnSuccess(gomock.Any())

			// CreateOrUpdateNodePool
			nodePoolItemID := uuid.New().String()
			mockDBTransaction.EXPECT().
				CreateResourceDoc(test.nodePoolDoc, gomock.Any() /*functions aren't self-comparable*/, nil).
				Return(nodePoolItemID)
			// CreateOrUpdateNodePool
			mockDBTransaction.EXPECT().
				PatchResourceDoc(nodePoolItemID, gomock.Any(), nil)
			// CreateOrUpdateNodePool
			mockDBTransaction.EXPECT().
				Execute(gomock.Any(), &azcosmos.TransactionalBatchOptions{
					EnableContentResponseOnWrite: true}).
				Return(mockDBTransactionResult, nil)
			// CreateOrUpdateNodePool
			mockDBTransactionResult.EXPECT().
				GetResourceDoc(nodePoolItemID).
				Return(test.nodePoolDoc, nil)

			req, err := http.NewRequest(http.MethodPut, ts.URL+test.urlPath, bytes.NewReader(body))
			require.NoError(t, err)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set(arm.HeaderNameARMResourceSystemData, "{}")

			rs, err := ts.Client().Do(req)
			t.Log(rs)
			require.NoError(t, err)

			if !assert.Equal(t, test.expectedStatusCode, rs.StatusCode) {
				defer rs.Body.Close()
				body, err := io.ReadAll(rs.Body)
				require.NoError(t, err)

				t.Log(string(body))
			}

			lintMetrics(t, reg)
			assertHTTPMetrics(t, reg, test.subDoc)
		})
	}
}

// TODO: Fix the update logic for this test.

// func TestUpdateNodePool(t *testing.T) {
// 	clusterResourceID, _ := arm.ParseResourceID(api.TestClusterResourceID)
// 	clusterDoc := database.NewResourceDocument(clusterResourceID)
// 	clusterDoc.InternalID, _ = ocm.NewInternalID(dummyClusterHREF)

// 	nodePoolResourceID, _ := arm.ParseResourceID(api.TestNodePoolResourceID)
// 	nodePoolDoc := database.NewResourceDocument(nodePoolResourceID)
// 	nodePoolDoc.InternalID, _ = ocm.NewInternalID(dummyNodePoolHREF)

// 	var dummyReplicas int32 = 2
// 	requestBody := generated.NodePool{
// 		Location: &dummyLocation,
// 		Properties: &generated.NodePoolProperties{
// 			Spec: &generated.NodePoolSpec{
// 				Replicas: &dummyReplicas,
// 				Version: &generated.VersionProfile{
// 					ID:           &dummyVersionID,
//					ChannelGroup: api.Ptr("stable"),
// 				},
// 			},
// 		},
// 	}

// 	tests := []struct {
// 		name               string
// 		urlPath            string
// 		subscription       *arm.Subscription
// 		systemData         *arm.SystemData
// 		subDoc             *arm.Subscription
// 		clusterDoc         *database.ResourceDocument
// 		nodePoolDoc        *database.ResourceDocument
// 		expectedStatusCode int
// 	}{
// 		{
// 			name:    "PUT Node Pool - Update an existing Node Pool",
// 			urlPath: api.TestNodePoolResourceID + "?api-version=2024-06-10-preview",
// 			subDoc: &arm.Subscription{
// 				State:            arm.SubscriptionStateRegistered,
// 				RegistrationDate: api.Ptr(time.Now().String()),
// 				Properties:       nil,
// 			},
// 			clusterDoc:         clusterDoc,
// 			nodePoolDoc:        nodePoolDoc,
// 			systemData:         &arm.SystemData{},
// 			expectedStatusCode: http.StatusAccepted,
// 		},
// 	}
// 	mockCSClient := ocm.NewMockClusterServiceClient()

// 	for _, test := range tests {
// 		t.Run(test.name, func(t *testing.T) {
// 			f := &Frontend{
// 				dbClient:             database.NewCache(),
// 				logger:               api.NewTestLogger(),
// 				metrics:              NewPrometheusEmitter(),
// 				clusterServiceClient: &mockCSClient,
// 			}
// 			hcpCluster := api.NewDefaultHCPOpenShiftCluster()
// 			hcpCluster.Name = dummyCluster
// 			csCluster, _ := f.BuildCSCluster(clusterResourceID, api.TestTenantID, hcpCluster, false)

// 			hcpNodePool := api.NewDefaultHCPOpenShiftClusterNodePool()
// 			hcpNodePool.Name = dummyNodePool
// 			csNodePool, _ := f.BuildCSNodePool(context.TODO(), hcpNodePool, false)

// 			if test.subDoc != nil {
// 				err := f.dbClient.CreateSubscriptionDoc(context.TODO(), api.TestSubscriptionID, test.subDoc)
// 				if err != nil {
// 					t.Fatal(err)
// 				}
// 			}

// 			if test.clusterDoc != nil {
// 				err := f.dbClient.CreateResourceDoc(context.TODO(), test.clusterDoc)
// 				if err != nil {
// 					t.Fatal(err)
// 				}
// 				_, err = f.clusterServiceClient.PostCluster(context.TODO(), csCluster)
// 				if err != nil {
// 					t.Fatal(err)
// 				}
// 			}

// 			if test.nodePoolDoc != nil {
// 				err := f.dbClient.CreateResourceDoc(context.TODO(), test.nodePoolDoc)
// 				if err != nil {
// 					t.Fatal(err)
// 				}
// 				_, err = f.clusterServiceClient.PostNodePool(context.TODO(), clusterDoc.InternalID, csNodePool)
// 				if err != nil {
// 					t.Fatal(err)
// 				}
// 			}
// 			body, _ := json.Marshal(requestBody)

// 			ts := httptest.NewServer(f.routes())
// 			ts.Config.BaseContext = func(net.Listener) context.Context {
// 				ctx := context.Background()
// 				ctx = ContextWithLogger(ctx, f.logger)
// 				ctx = ContextWithDBClient(ctx, f.dbClient)
// 				ctx = ContextWithSystemData(ctx, test.systemData)

// 				return ctx
// 			}

// 			req, err := http.NewRequest(http.MethodPatch, ts.URL+test.urlPath, bytes.NewReader(body))
// 			if err != nil {
// 				t.Fatal(err)
// 			}
// 			req.Header.Set("Content-Type", "application/json")

// 			rs, err := ts.Client().Do(req)
// 			t.Log(rs)
// 			if err != nil {
// 				t.Log(err)
// 				t.Fatal(err)
// 			}

// 			if rs.StatusCode != test.expectedStatusCode {
// 				t.Errorf("expected status code %d, got %d", test.expectedStatusCode, rs.StatusCode)
// 			}
// 		})
// 	}
// }
