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

package creation

import (
	"context"
	"errors"
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	creatorTestSubscriptionID   = "00000000-0000-0000-0000-000000000000"
	creatorTestResourceGroup    = "test-rg"
	creatorTestClusterName      = "test-cluster"
	creatorTestExternalAuthName = "test-externalauth"
)

func newCreatorTestExternalAuthKey() controllerutils.HCPExternalAuthKey {
	return controllerutils.HCPExternalAuthKey{
		SubscriptionID:      creatorTestSubscriptionID,
		ResourceGroupName:   creatorTestResourceGroup,
		HCPClusterName:      creatorTestClusterName,
		HCPExternalAuthName: creatorTestExternalAuthName,
	}
}

func newCreatorTestExternalAuth(t *testing.T) *coreapi.HCPOpenShiftClusterExternalAuth {
	t.Helper()
	resourceID := metadataapi.Must(coreapi.ToExternalAuthResourceID(creatorTestSubscriptionID, creatorTestResourceGroup, creatorTestClusterName, creatorTestExternalAuthName))
	return &coreapi.HCPOpenShiftClusterExternalAuth{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: resourceID},
		ProxyResource: coreapi.ProxyResource{
			Resource: coreapi.Resource{
				ID:   resourceID,
				Name: creatorTestExternalAuthName,
				Type: coreapi.ExternalAuthResourceType.String(),
			},
		},
	}
}

type boomExternalAuthLister struct {
	corelisters.ExternalAuthLister
	err error
}

func (b *boomExternalAuthLister) Get(_ context.Context, _, _, _, _ string) (*coreapi.HCPOpenShiftClusterExternalAuth, error) {
	return nil, b.err
}

type boomServiceProviderExternalAuthLister struct {
	corelisters.ServiceProviderExternalAuthLister
	err error
}

func (b *boomServiceProviderExternalAuthLister) Get(_ context.Context, _, _, _, _ string) (*coreapi.ServiceProviderExternalAuth, error) {
	return nil, b.err
}

func TestCreateServiceProviderExternalAuthSyncer_SyncOnce(t *testing.T) {
	externalAuthResourceID := metadataapi.Must(coreapi.ToExternalAuthResourceID(creatorTestSubscriptionID, creatorTestResourceGroup, creatorTestClusterName, creatorTestExternalAuthName))
	listerBoom := errors.New("lister exploded")

	tests := []struct {
		name             string
		buildSyncer      func(t *testing.T, mockDB *corecosmosstoragetesting.MockResourcesDBClient) *createServiceProviderExternalAuthSyncer
		seedDB           func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient)
		wantErrSubstring string
		wantCreated      bool
	}{
		{
			name: "external auth missing from lister returns nil and does not write",
			buildSyncer: func(t *testing.T, mockDB *corecosmosstoragetesting.MockResourcesDBClient) *createServiceProviderExternalAuthSyncer {
				return &createServiceProviderExternalAuthSyncer{
					resourcesDBClient:                 mockDB,
					externalAuthLister:                &corelistertesting.SliceExternalAuthLister{},
					serviceProviderExternalAuthLister: &corelistertesting.SliceServiceProviderExternalAuthLister{},
				}
			},
			wantCreated: false,
		},
		{
			name: "external auth lister error is propagated",
			buildSyncer: func(t *testing.T, mockDB *corecosmosstoragetesting.MockResourcesDBClient) *createServiceProviderExternalAuthSyncer {
				return &createServiceProviderExternalAuthSyncer{
					resourcesDBClient:                 mockDB,
					externalAuthLister:                &boomExternalAuthLister{err: listerBoom},
					serviceProviderExternalAuthLister: &corelistertesting.SliceServiceProviderExternalAuthLister{},
				}
			},
			wantErrSubstring: "failed to get ExternalAuth from lister",
			wantCreated:      false,
		},
		{
			name: "ServiceProviderExternalAuth already in lister is a no-op",
			buildSyncer: func(t *testing.T, mockDB *corecosmosstoragetesting.MockResourcesDBClient) *createServiceProviderExternalAuthSyncer {
				speaResourceID := metadataapi.Must(azcorearm.ParseResourceID(externalAuthResourceID.String() + "/" + coreapi.ServiceProviderExternalAuthResourceTypeName + "/" + coreapi.ServiceProviderExternalAuthResourceName))
				return &createServiceProviderExternalAuthSyncer{
					resourcesDBClient: mockDB,
					externalAuthLister: &corelistertesting.SliceExternalAuthLister{
						ExternalAuths: []*coreapi.HCPOpenShiftClusterExternalAuth{newCreatorTestExternalAuth(t)},
					},
					serviceProviderExternalAuthLister: &corelistertesting.SliceServiceProviderExternalAuthLister{
						ServiceProviderExternalAuths: []*coreapi.ServiceProviderExternalAuth{{
							CosmosMetadata: coreapi.CosmosMetadata{ResourceID: speaResourceID},
						}},
					},
				}
			},
			wantCreated: false,
		},
		{
			name: "ServiceProviderExternalAuth lister error other than NotFound is propagated",
			buildSyncer: func(t *testing.T, mockDB *corecosmosstoragetesting.MockResourcesDBClient) *createServiceProviderExternalAuthSyncer {
				return &createServiceProviderExternalAuthSyncer{
					resourcesDBClient: mockDB,
					externalAuthLister: &corelistertesting.SliceExternalAuthLister{
						ExternalAuths: []*coreapi.HCPOpenShiftClusterExternalAuth{newCreatorTestExternalAuth(t)},
					},
					serviceProviderExternalAuthLister: &boomServiceProviderExternalAuthLister{err: listerBoom},
				}
			},
			wantErrSubstring: "failed to get ServiceProviderExternalAuth from lister",
			wantCreated:      false,
		},
		{
			name: "external auth marked for deletion is a no-op (do not recreate child during cleanup)",
			buildSyncer: func(t *testing.T, mockDB *corecosmosstoragetesting.MockResourcesDBClient) *createServiceProviderExternalAuthSyncer {
				deletingEA := newCreatorTestExternalAuth(t)
				deletingEA.ServiceProviderProperties.DeletionTimestamp = ptr.To(metav1.Now())
				return &createServiceProviderExternalAuthSyncer{
					resourcesDBClient: mockDB,
					externalAuthLister: &corelistertesting.SliceExternalAuthLister{
						ExternalAuths: []*coreapi.HCPOpenShiftClusterExternalAuth{deletingEA},
					},
					serviceProviderExternalAuthLister: &corelistertesting.SliceServiceProviderExternalAuthLister{},
				}
			},
			wantCreated: false,
		},
		{
			name: "missing ServiceProviderExternalAuth is created",
			buildSyncer: func(t *testing.T, mockDB *corecosmosstoragetesting.MockResourcesDBClient) *createServiceProviderExternalAuthSyncer {
				return &createServiceProviderExternalAuthSyncer{
					resourcesDBClient: mockDB,
					externalAuthLister: &corelistertesting.SliceExternalAuthLister{
						ExternalAuths: []*coreapi.HCPOpenShiftClusterExternalAuth{newCreatorTestExternalAuth(t)},
					},
					serviceProviderExternalAuthLister: &corelistertesting.SliceServiceProviderExternalAuthLister{},
				}
			},
			wantCreated: true,
		},
		{
			name: "create is idempotent when ServiceProviderExternalAuth already exists in cosmos",
			buildSyncer: func(t *testing.T, mockDB *corecosmosstoragetesting.MockResourcesDBClient) *createServiceProviderExternalAuthSyncer {
				return &createServiceProviderExternalAuthSyncer{
					resourcesDBClient: mockDB,
					externalAuthLister: &corelistertesting.SliceExternalAuthLister{
						ExternalAuths: []*coreapi.HCPOpenShiftClusterExternalAuth{newCreatorTestExternalAuth(t)},
					},
					serviceProviderExternalAuthLister: &corelistertesting.SliceServiceProviderExternalAuthLister{},
				}
			},
			seedDB: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) {
				_, err := corecosmosstorage.GetOrCreateServiceProviderExternalAuth(ctx, mockDB, externalAuthResourceID)
				require.NoError(t, err)
			},
			wantCreated: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()

			if tc.seedDB != nil {
				tc.seedDB(t, ctx, mockDB)
			}

			syncer := tc.buildSyncer(t, mockDB)

			err := syncer.SyncOnce(ctx, newCreatorTestExternalAuthKey())
			if tc.wantErrSubstring != "" {
				require.Error(t, err)
				assert.ErrorContains(t, err, tc.wantErrSubstring)
			} else {
				require.NoError(t, err)
			}

			_, getErr := mockDB.ServiceProviderExternalAuths(creatorTestSubscriptionID, creatorTestResourceGroup, creatorTestClusterName, creatorTestExternalAuthName).Get(ctx, coreapi.ServiceProviderExternalAuthResourceName)
			if tc.wantCreated {
				assert.NoError(t, getErr, "expected ServiceProviderExternalAuth to exist in cosmos")
			} else {
				assert.True(t, cosmosstorageutils.IsNotFoundError(getErr), "expected ServiceProviderExternalAuth to be absent, got err=%v", getErr)
			}
		})
	}
}
