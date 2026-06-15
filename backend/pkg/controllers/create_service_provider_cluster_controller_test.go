// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package controllers

import (
	"context"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listertesting"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	createSPCSubscriptionID    = "00000000-0000-0000-0000-000000000000"
	createSPCResourceGroupName = "test-rg"
	createSPCClusterName       = "test-cluster"
)

func newCreateSPCClusterKey() controllerutils.HCPClusterKey {
	return controllerutils.HCPClusterKey{
		SubscriptionID:    createSPCSubscriptionID,
		ResourceGroupName: createSPCResourceGroupName,
		HCPClusterName:    createSPCClusterName,
	}
}

func newCreateSPCClusterResourceID(t *testing.T) *azcorearm.ResourceID {
	t.Helper()
	return api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + createSPCSubscriptionID +
			"/resourceGroups/" + createSPCResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + createSPCClusterName))
}

func newCreateSPCCluster(t *testing.T, opts ...func(*api.HCPOpenShiftCluster)) *api.HCPOpenShiftCluster {
	t.Helper()
	clusterResourceID := newCreateSPCClusterResourceID(t)
	cluster := &api.HCPOpenShiftCluster{
		CosmosMetadata: arm.CosmosMetadata{ResourceID: clusterResourceID},
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   clusterResourceID,
				Name: createSPCClusterName,
				Type: api.ClusterResourceType.String(),
			},
			Location: "eastus",
		},
	}
	for _, opt := range opts {
		opt(cluster)
	}
	return cluster
}

func newCreateSPCServiceProviderCluster(t *testing.T) *api.ServiceProviderCluster {
	t.Helper()
	clusterResourceID := newCreateSPCClusterResourceID(t)
	spcResourceID := api.Must(azcorearm.ParseResourceID(
		clusterResourceID.String() + "/" + api.ServiceProviderClusterResourceTypeName + "/" + api.ServiceProviderClusterResourceName))
	return &api.ServiceProviderCluster{
		CosmosMetadata: arm.CosmosMetadata{ResourceID: spcResourceID},
	}
}

type alwaysAllowCooldownChecker struct{}

func (a *alwaysAllowCooldownChecker) CanSync(_ context.Context, _ any) bool { return true }

var _ controllerutil.CooldownChecker = (*alwaysAllowCooldownChecker)(nil)

func TestCreateServiceProviderClusterSyncer_SyncOnce(t *testing.T) {
	testKey := newCreateSPCClusterKey()

	tests := []struct {
		name         string
		clusters     []*api.HCPOpenShiftCluster
		spcForLister []*api.ServiceProviderCluster
		seedSPCInDB  bool
		wantSPCInDB  bool
		expectError  bool
	}{
		{
			name:        "cluster missing from cache is a no-op",
			clusters:    nil,
			wantSPCInDB: false,
			expectError: false,
		},
		{
			name: "cluster with deletion timestamp is a no-op",
			clusters: []*api.HCPOpenShiftCluster{
				newCreateSPCCluster(t, func(c *api.HCPOpenShiftCluster) {
					c.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: time.Now()}
				}),
			},
			wantSPCInDB: false,
		},
		{
			name: "service provider cluster already in cache is a no-op",
			clusters: []*api.HCPOpenShiftCluster{
				newCreateSPCCluster(t),
			},
			spcForLister: []*api.ServiceProviderCluster{
				newCreateSPCServiceProviderCluster(t),
			},
			wantSPCInDB: false,
		},
		{
			name: "creates service provider cluster when missing from cache and DB",
			clusters: []*api.HCPOpenShiftCluster{
				newCreateSPCCluster(t),
			},
			wantSPCInDB: true,
		},
		{
			name: "create conflict when document already exists in DB is treated as success",
			clusters: []*api.HCPOpenShiftCluster{
				newCreateSPCCluster(t),
			},
			seedSPCInDB: true,
			wantSPCInDB: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
			mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
			if tt.seedSPCInDB {
				_, err := mockResourcesDBClient.ServiceProviderClusters(
					createSPCSubscriptionID, createSPCResourceGroupName, createSPCClusterName,
				).Create(ctx, newCreateSPCServiceProviderCluster(t), nil)
				require.NoError(t, err)
			}

			syncer := &createServiceProviderClusterSyncer{
				cooldownChecker: &alwaysAllowCooldownChecker{},
				clusterLister: &listertesting.SliceClusterLister{
					Clusters: tt.clusters,
				},
				serviceProviderClusterLister: &listertesting.SliceServiceProviderClusterLister{
					ServiceProviderClusters: tt.spcForLister,
				},
				resourcesDBClient: mockResourcesDBClient,
			}

			err := syncer.SyncOnce(ctx, testKey)
			if tt.expectError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			_, getErr := mockResourcesDBClient.ServiceProviderClusters(
				createSPCSubscriptionID, createSPCResourceGroupName, createSPCClusterName,
			).Get(ctx, api.ServiceProviderClusterResourceName)
			if tt.wantSPCInDB {
				require.NoError(t, getErr)
			} else {
				assert.True(t, database.IsNotFoundError(getErr))
			}
		})
	}
}
