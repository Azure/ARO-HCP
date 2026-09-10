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
)

func TestEnsureReadDesire_NilClient(t *testing.T) {
	clients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
	syncer := &ensureReadDesireSyncer{kubeApplierDBClients: clients}

	err := syncer.SyncOnce(context.Background(), testKey())
	require.NoError(t, err)
}

func TestEnsureReadDesire_CreatesReadDesireOnFirstCall(t *testing.T) {
	mockClient := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
	clients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
	clients.Register(testManagementClusterResourceID(), mockClient)

	syncer := &ensureReadDesireSyncer{kubeApplierDBClients: clients}

	err := syncer.SyncOnce(context.Background(), testKey())
	require.NoError(t, err)

	crud, err := mockClient.ReadDesiresForManagementCluster(testStampIdentifier)
	require.NoError(t, err)

	existing, err := crud.Get(context.Background(), ReadDesireName)
	require.NoError(t, err)
	assert.Equal(t, SharedIngressTarget, existing.Spec.TargetItem)
}

func TestEnsureReadDesire_UpdatesStaleSpec(t *testing.T) {
	mockClient := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
	clients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
	clients.Register(testManagementClusterResourceID(), mockClient)

	staleTarget := kubeapplierapi.ResourceReference{
		Group:    "old.group",
		Version:  "v1",
		Resource: "oldresources",
		Name:     "old",
	}
	desireIDString := kubeapplierapi.ToManagementClusterScopedReadDesireResourceIDString(testStampIdentifier, ReadDesireName)
	stale := controllerutil.BuildReadDesire(desireIDString, testManagementClusterResourceID(), staleTarget)

	crud, err := mockClient.ReadDesiresForManagementCluster(testStampIdentifier)
	require.NoError(t, err)
	_, err = crud.Create(context.Background(), stale, nil)
	require.NoError(t, err)

	syncer := &ensureReadDesireSyncer{kubeApplierDBClients: clients}

	err = syncer.SyncOnce(context.Background(), testKey())
	require.NoError(t, err)

	updated, err := crud.Get(context.Background(), ReadDesireName)
	require.NoError(t, err)
	assert.Equal(t, SharedIngressTarget, updated.Spec.TargetItem)
}

func TestEnsureReadDesire_ConflictOnCreateIsSwallowed(t *testing.T) {
	clients := &conflictOnCreateDBClients{}
	syncer := &ensureReadDesireSyncer{kubeApplierDBClients: clients}

	err := syncer.SyncOnce(context.Background(), testKey())
	require.NoError(t, err)
}

func TestEnsureReadDesire_PreconditionFailedOnReplaceIsSwallowed(t *testing.T) {
	clients := &preconditionOnReplaceDBClients{}
	syncer := &ensureReadDesireSyncer{kubeApplierDBClients: clients}

	// The existing ReadDesire is stale (so a Replace is attempted) and the
	// Replace loses the optimistic-concurrency check → 412 Precondition Failed,
	// which must be swallowed as a no-op.
	err := syncer.SyncOnce(context.Background(), testKey())
	require.NoError(t, err)
}

// --- Test doubles for conflict-on-create scenario ---

// conflictOnCreateDBClients implements KubeApplierDBClients, returning a
// client whose ReadDesiresForManagementCluster CRUD returns NotFound on Get
// and Conflict on Create — simulating a race where another controller wins
// the create.
type conflictOnCreateDBClients struct{}

func (c *conflictOnCreateDBClients) For(_ context.Context, _ *azcorearm.ResourceID) kubeappliercosmosstorage.KubeApplierDBClient {
	return &conflictOnCreateDBClient{}
}

type conflictOnCreateDBClient struct {
	kubeappliercosmosstorage.KubeApplierDBClient // embedded nil — only ReadDesiresForManagementCluster is called
}

func (c *conflictOnCreateDBClient) ReadDesiresForManagementCluster(_ string) (cosmosstorageutils.ResourceCRUD[kubeapplierapi.ReadDesire, *kubeapplierapi.ReadDesire], error) {
	return &notFoundThenConflictCRUD{}, nil
}

type notFoundThenConflictCRUD struct {
	cosmosstorageutils.ResourceCRUD[kubeapplierapi.ReadDesire, *kubeapplierapi.ReadDesire] // embedded nil — only Get and Create are called
}

func (c *notFoundThenConflictCRUD) Get(_ context.Context, _ string) (*kubeapplierapi.ReadDesire, error) {
	return nil, cosmosstorageutils.NewNotFoundError()
}

func (c *notFoundThenConflictCRUD) Create(_ context.Context, _ *kubeapplierapi.ReadDesire, _ *azcosmos.ItemOptions) (*kubeapplierapi.ReadDesire, error) {
	return nil, &azcore.ResponseError{StatusCode: http.StatusConflict}
}

// --- Test doubles for precondition-failed-on-replace scenario ---

// preconditionOnReplaceDBClients implements KubeApplierDBClients, returning a
// client whose ReadDesiresForManagementCluster CRUD returns a stale ReadDesire
// on Get (so the controller attempts a Replace) and PreconditionFailed on
// Replace — simulating a lost optimistic-concurrency race.
type preconditionOnReplaceDBClients struct{}

func (c *preconditionOnReplaceDBClients) For(_ context.Context, _ *azcorearm.ResourceID) kubeappliercosmosstorage.KubeApplierDBClient {
	return &preconditionOnReplaceDBClient{}
}

type preconditionOnReplaceDBClient struct {
	kubeappliercosmosstorage.KubeApplierDBClient // embedded nil — only ReadDesiresForManagementCluster is called
}

func (c *preconditionOnReplaceDBClient) ReadDesiresForManagementCluster(_ string) (cosmosstorageutils.ResourceCRUD[kubeapplierapi.ReadDesire, *kubeapplierapi.ReadDesire], error) {
	return &staleThenPreconditionReplaceCRUD{}, nil
}

type staleThenPreconditionReplaceCRUD struct {
	cosmosstorageutils.ResourceCRUD[kubeapplierapi.ReadDesire, *kubeapplierapi.ReadDesire] // embedded nil — only Get and Replace are called
}

func (c *staleThenPreconditionReplaceCRUD) Get(_ context.Context, _ string) (*kubeapplierapi.ReadDesire, error) {
	// Return a stale ReadDesire (different target) so ReadDesireNeedsWork is
	// true and the controller proceeds to Replace.
	staleTarget := kubeapplierapi.ResourceReference{
		Group:    "old.group",
		Version:  "v1",
		Resource: "oldresources",
		Name:     "old",
	}
	desireIDString := kubeapplierapi.ToManagementClusterScopedReadDesireResourceIDString(testStampIdentifier, ReadDesireName)
	return controllerutil.BuildReadDesire(desireIDString, testManagementClusterResourceID(), staleTarget), nil
}

func (c *staleThenPreconditionReplaceCRUD) Replace(_ context.Context, _ *kubeapplierapi.ReadDesire, _ *azcosmos.ItemOptions) (*kubeapplierapi.ReadDesire, error) {
	return nil, &azcore.ResponseError{StatusCode: http.StatusPreconditionFailed}
}
