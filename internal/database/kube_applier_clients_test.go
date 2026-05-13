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

package database

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/fleet"
)

// fakeManagementClusterLister is the minimal in-memory ManagementClusterLister
// the registry tests need. listers.ManagementClusterLister can't be used here
// without an import cycle.
type fakeManagementClusterLister struct {
	mu    sync.Mutex
	mcs   []*fleet.ManagementCluster
	calls int // counts List() invocations so tests can assert the lister was hit
	err   error
}

func (f *fakeManagementClusterLister) List(ctx context.Context) ([]*fleet.ManagementCluster, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	out := make([]*fleet.ManagementCluster, len(f.mcs))
	copy(out, f.mcs)
	return out, nil
}

func newFakeMC(t *testing.T, stampIdentifier, containerName, consumerName string) *fleet.ManagementCluster {
	t.Helper()
	rid := api.Must(fleet.ToManagementClusterResourceID(stampIdentifier))
	return &fleet.ManagementCluster{
		CosmosMetadata: api.CosmosMetadata{ResourceID: rid},
		ResourceID:     rid,
		Status: fleet.ManagementClusterStatus{
			KubeApplierCosmosContainerName: containerName,
			MaestroConsumerName:            consumerName,
		},
	}
}

// TestKubeApplierDBClients_ForReturnsNilForUnknownMC documents the contract: an
// unknown resourceID yields nil so callers can decide how to handle the gap.
func TestKubeApplierDBClients_ForReturnsNilForUnknownMC(t *testing.T) {
	clients := NewKubeApplierDBClients(nil, &fakeManagementClusterLister{})
	rid := mustParseResourceIDForKubeApplierTest(t, "/providers/microsoft.redhatopenshift/stamps/1/managementclusters/default")
	assert.Nil(t, clients.For(rid), "unknown management cluster should return nil")
}

// TestKubeApplierDBClients_ManagementClusterResourceIDs verifies the lister
// drives ManagementClusterResourceIDs() — adding an MC to the lister makes it
// visible without rebuilding the registry. The orphan controller relies on
// this iteration to walk every reachable MC's container.
func TestKubeApplierDBClients_ManagementClusterResourceIDs(t *testing.T) {
	lister := &fakeManagementClusterLister{}
	lister.mcs = []*fleet.ManagementCluster{
		newFakeMC(t, "1", "container-a", "mc-a"),
		newFakeMC(t, "2", "container-b", "mc-b"),
	}
	clients := NewKubeApplierDBClients(nil, lister)

	rids := clients.ManagementClusterResourceIDs()
	require.Len(t, rids, 2)

	got := map[string]bool{}
	for _, rid := range rids {
		got[strings.ToLower(rid.String())] = true
	}
	assert.True(t, got["/providers/microsoft.redhatopenshift/stamps/1/managementclusters/default"], "mc-a resourceID round-trips")
	assert.True(t, got["/providers/microsoft.redhatopenshift/stamps/2/managementclusters/default"], "mc-b resourceID round-trips")
}

// TestKubeApplierDBClients_ForUsesLister_ReturnsNilWhenMissing covers the
// "iterate the entire list" branch: an MC not in the lister produces nil even
// when other MCs are present. It also verifies the lister is consulted (calls
// counter goes up).
func TestKubeApplierDBClients_ForUsesLister_ReturnsNilWhenMissing(t *testing.T) {
	lister := &fakeManagementClusterLister{
		mcs: []*fleet.ManagementCluster{newFakeMC(t, "present", "container-a", "mc-a")},
	}
	clients := NewKubeApplierDBClients(nil, lister)

	missing := mustParseResourceIDForKubeApplierTest(t, "/providers/microsoft.redhatopenshift/stamps/missing/managementclusters/default")
	assert.Nil(t, clients.For(missing))
	assert.GreaterOrEqual(t, lister.calls, 1, "For() must consult the lister")
}

// TestKubeApplierDBClients_ForIsThreadSafe stress-tests the lazy cache under
// concurrent callers. The contract: For(rid) must not race the cache or the
// lister, and unknown-rid lookups consistently return nil. Run under -race to
// verify the mutex actually protects the maps.
func TestKubeApplierDBClients_ForIsThreadSafe_UnknownReturnsNilUnderRace(t *testing.T) {
	clients := NewKubeApplierDBClients(nil, &fakeManagementClusterLister{})
	rid := mustParseResourceIDForKubeApplierTest(t, "/providers/microsoft.redhatopenshift/stamps/1/managementclusters/default")

	const goroutines = 50
	wg := sync.WaitGroup{}
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			assert.Nil(t, clients.For(rid))
			_ = clients.ManagementClusterResourceIDs()
		}()
	}
	wg.Wait()
}

func mustParseResourceIDForKubeApplierTest(t *testing.T, s string) *azcorearm.ResourceID {
	t.Helper()
	rid, err := azcorearm.ParseResourceID(s)
	require.NoError(t, err)
	return rid
}
