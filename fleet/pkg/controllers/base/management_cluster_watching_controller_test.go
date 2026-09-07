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

package base

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/fleetcosmosstoragetesting"
)

// fakeManagementClusterSyncer is a ManagementClusterSyncer whose SyncOnce
// returns a configured error.
type fakeManagementClusterSyncer struct {
	err error
}

func (f *fakeManagementClusterSyncer) SyncOnce(_ context.Context, _ ManagementClusterKey) error {
	return f.err
}

func (f *fakeManagementClusterSyncer) CooldownChecker() controllerutils.CooldownChecker {
	return nil
}

func TestManagementClusterKeyGetResourceID(t *testing.T) {
	key := ManagementClusterKey{StampIdentifier: "s1"}
	rid := key.GetResourceID()
	require.NotNil(t, rid, "expected non-nil resource ID")
	assert.Equal(t, "/providers/microsoft.redhatopenshift/stamps/s1/managementclusters/default", rid.String())
}

func TestManagementClusterKeyInitialController(t *testing.T) {
	key := ManagementClusterKey{StampIdentifier: "S1"}

	controller := key.InitialController("NodePoolController")

	require.NotNil(t, controller)
	assert.Equal(t, "s1", controller.PartitionKey, "partition key must be lowercased")
	assert.Equal(t, key.GetResourceID(), controller.ExternalID)
	assert.Equal(t,
		"/providers/microsoft.redhatopenshift/stamps/s1/managementclusters/default/controllers/NodePoolController",
		controller.ResourceID.String())
	assert.Empty(t, controller.Status.Conditions)
}

func TestManagementClusterWatchingControllerMakeKey(t *testing.T) {
	adapter := &managementClusterWatchingController{}

	t.Run("management cluster resource ID", func(t *testing.T) {
		rid, err := azcorearm.ParseResourceID("/providers/microsoft.redhatopenshift/stamps/s1/managementclusters/default")
		require.NoError(t, err)

		key := adapter.MakeKey(rid)
		assert.Equal(t, "s1", key.StampIdentifier)
	})

	t.Run("resource ID with no parent panics", func(t *testing.T) {
		assert.Panics(t, func() { adapter.MakeKey(azcorearm.RootResourceID) })
	})
}

func TestManagementClusterWatchingControllerSyncOnce(t *testing.T) {
	const stampID = "s1"
	const controllerName = "NodePoolController"
	key := ManagementClusterKey{StampIdentifier: stampID}

	t.Run("successful sync writes Degraded=False and returns nil", func(t *testing.T) {
		mockDB := fleetcosmosstoragetesting.NewMockFleetDBClient()
		c := &managementClusterWatchingController{
			name:          controllerName,
			fleetDBClient: mockDB,
			syncer:        &fakeManagementClusterSyncer{},
		}

		err := c.SyncOnce(testContext(), key)
		require.NoError(t, err, "SyncOnce")

		stored, err := mockDB.Stamps().ManagementClusters(stampID).Controllers().Get(testContext(), controllerName)
		require.NoError(t, err, "Get controller document")
		cond := apimeta.FindStatusCondition(stored.Status.Conditions, "Degraded")
		require.NotNil(t, cond, "expected Degraded condition")
		assert.Equal(t, metav1.ConditionFalse, cond.Status, "Degraded status")
	})

	t.Run("sync error is returned and written as Degraded=True", func(t *testing.T) {
		mockDB := fleetcosmosstoragetesting.NewMockFleetDBClient()
		syncErr := errors.New("sync boom")
		c := &managementClusterWatchingController{
			name:          controllerName,
			fleetDBClient: mockDB,
			syncer:        &fakeManagementClusterSyncer{err: syncErr},
		}

		err := c.SyncOnce(testContext(), key)
		assert.ErrorIs(t, err, syncErr, "SyncOnce must return the syncer error")

		stored, err := mockDB.Stamps().ManagementClusters(stampID).Controllers().Get(testContext(), controllerName)
		require.NoError(t, err, "Get controller document")
		cond := apimeta.FindStatusCondition(stored.Status.Conditions, "Degraded")
		require.NotNil(t, cond, "expected Degraded condition")
		assert.Equal(t, metav1.ConditionTrue, cond.Status, "Degraded status")
		assert.Contains(t, cond.Message, "sync boom", "Degraded message carries the sync error")
	})
}
