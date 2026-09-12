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

package sharedingress

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/kubeappliercosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/kubeappliercosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/kubeapplierlistertesting"
)

func staleReadDesire() *kubeapplierapi.ReadDesire {
	staleTarget := kubeapplierapi.ResourceReference{
		Group:    "old.group",
		Version:  "v1",
		Resource: "oldresources",
		Name:     "old",
	}
	desireIDString := kubeapplierapi.ToManagementClusterScopedReadDesireResourceIDString(testStampIdentifier, ReadDesireName)
	return controllerutil.BuildReadDesire(desireIDString, testManagementClusterResourceID(), staleTarget)
}

func TestEnsureReadDesire_NilClient(t *testing.T) {
	clients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
	syncer := &ensureReadDesireSyncer{
		kubeApplierDBClients: clients,
		readDesireLister:     &kubeapplierlistertesting.SliceReadDesireLister{},
	}

	err := syncer.SyncOnce(context.Background(), testKey())
	require.NoError(t, err)
}

func TestEnsureReadDesire_CreatesReadDesireOnFirstCall(t *testing.T) {
	mockClient := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
	clients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
	clients.Register(testManagementClusterResourceID(), mockClient)

	// The lister is empty (not found) so the controller proceeds to Create.
	syncer := &ensureReadDesireSyncer{
		kubeApplierDBClients: clients,
		readDesireLister:     &kubeapplierlistertesting.SliceReadDesireLister{},
	}

	err := syncer.SyncOnce(context.Background(), testKey())
	require.NoError(t, err)

	crud, err := mockClient.ReadDesiresForManagementCluster(testStampIdentifier)
	require.NoError(t, err)

	existing, err := crud.Get(context.Background(), ReadDesireName)
	require.NoError(t, err)
	assert.Equal(t, SharedIngressTarget, existing.Spec.TargetItem)
}

func TestEnsureReadDesire_UpdatesStaleSpec(t *testing.T) {
	ctx := context.Background()
	mockClient := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
	clients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
	clients.Register(testManagementClusterResourceID(), mockClient)

	crud, err := mockClient.ReadDesiresForManagementCluster(testStampIdentifier)
	require.NoError(t, err)
	_, err = crud.Create(ctx, staleReadDesire(), nil)
	require.NoError(t, err)

	// Seed the lister with the stored object (carrying the current etag) so the
	// controller observes drift via the lister and Replaces via the crud.
	stored, err := crud.Get(ctx, ReadDesireName)
	require.NoError(t, err)
	lister := &kubeapplierlistertesting.SliceReadDesireLister{Desires: []*kubeapplierapi.ReadDesire{stored}}

	syncer := &ensureReadDesireSyncer{
		kubeApplierDBClients: clients,
		readDesireLister:     lister,
	}

	err = syncer.SyncOnce(ctx, testKey())
	require.NoError(t, err)

	updated, err := crud.Get(ctx, ReadDesireName)
	require.NoError(t, err)
	assert.Equal(t, SharedIngressTarget, updated.Spec.TargetItem)
}

func TestEnsureReadDesire_ConflictOnCreateIsSwallowed(t *testing.T) {
	// Lister is empty (not found) so the controller attempts a Create, which
	// loses the race and returns 409 Conflict — must be swallowed.
	syncer := &ensureReadDesireSyncer{
		kubeApplierDBClients: &conflictOnCreateDBClients{},
		readDesireLister:     &kubeapplierlistertesting.SliceReadDesireLister{},
	}

	err := syncer.SyncOnce(context.Background(), testKey())
	require.NoError(t, err)
}

func TestEnsureReadDesire_PreconditionFailedOnReplaceIsSwallowed(t *testing.T) {
	// Lister returns a stale ReadDesire so the controller attempts a Replace,
	// which loses the optimistic-concurrency check and returns 412 Precondition
	// Failed — must be swallowed.
	lister := &kubeapplierlistertesting.SliceReadDesireLister{Desires: []*kubeapplierapi.ReadDesire{staleReadDesire()}}
	syncer := &ensureReadDesireSyncer{
		kubeApplierDBClients: &preconditionOnReplaceDBClients{},
		readDesireLister:     lister,
	}

	err := syncer.SyncOnce(context.Background(), testKey())
	require.NoError(t, err)
}

// --- Test double: kube-applier crud whose Create returns 409 Conflict ---

type conflictOnCreateDBClients struct{}

func (c *conflictOnCreateDBClients) For(_ context.Context, _ *azcorearm.ResourceID) kubeappliercosmosstorage.KubeApplierDBClient {
	return &conflictOnCreateDBClient{}
}

type conflictOnCreateDBClient struct {
	kubeappliercosmosstorage.KubeApplierDBClient // embedded nil — only ReadDesiresForManagementCluster is called
}

func (c *conflictOnCreateDBClient) ReadDesiresForManagementCluster(_ string) (cosmosstorageutils.ResourceCRUD[kubeapplierapi.ReadDesire, *kubeapplierapi.ReadDesire], error) {
	return &conflictOnCreateCRUD{}, nil
}

type conflictOnCreateCRUD struct {
	cosmosstorageutils.ResourceCRUD[kubeapplierapi.ReadDesire, *kubeapplierapi.ReadDesire] // embedded nil — only Create is called
}

func (c *conflictOnCreateCRUD) Create(_ context.Context, _ *kubeapplierapi.ReadDesire, _ *azcosmos.ItemOptions) (*kubeapplierapi.ReadDesire, error) {
	return nil, &azcore.ResponseError{StatusCode: http.StatusConflict}
}

// --- Test double: kube-applier crud whose Replace returns 412 Precondition Failed ---

type preconditionOnReplaceDBClients struct{}

func (c *preconditionOnReplaceDBClients) For(_ context.Context, _ *azcorearm.ResourceID) kubeappliercosmosstorage.KubeApplierDBClient {
	return &preconditionOnReplaceDBClient{}
}

type preconditionOnReplaceDBClient struct {
	kubeappliercosmosstorage.KubeApplierDBClient // embedded nil — only ReadDesiresForManagementCluster is called
}

func (c *preconditionOnReplaceDBClient) ReadDesiresForManagementCluster(_ string) (cosmosstorageutils.ResourceCRUD[kubeapplierapi.ReadDesire, *kubeapplierapi.ReadDesire], error) {
	return &preconditionOnReplaceCRUD{}, nil
}

type preconditionOnReplaceCRUD struct {
	cosmosstorageutils.ResourceCRUD[kubeapplierapi.ReadDesire, *kubeapplierapi.ReadDesire] // embedded nil — only Replace is called
}

func (c *preconditionOnReplaceCRUD) Replace(_ context.Context, _ *kubeapplierapi.ReadDesire, _ *azcosmos.ItemOptions) (*kubeapplierapi.ReadDesire, error) {
	return nil, &azcore.ResponseError{StatusCode: http.StatusPreconditionFailed}
}
