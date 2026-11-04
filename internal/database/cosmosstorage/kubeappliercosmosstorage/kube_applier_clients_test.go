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

package kubeappliercosmosstorage

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/fleetapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosclient"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosratelimit"
)

// fakeManagementClusterLister is the minimal in-memory ManagementClusterLister
// the registry tests need. listers.ManagementClusterLister can't be used here
// without an import cycle.
type fakeManagementClusterLister struct {
	mu    sync.Mutex
	mcs   []*fleetapi.ManagementCluster
	calls int // counts List() invocations so tests can assert the lister was hit
	err   error
}

func (f *fakeManagementClusterLister) List(ctx context.Context) ([]*fleetapi.ManagementCluster, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	out := make([]*fleetapi.ManagementCluster, len(f.mcs))
	copy(out, f.mcs)
	return out, nil
}

func newFakeMC(t *testing.T, stampIdentifier, containerName string) *fleetapi.ManagementCluster {
	t.Helper()
	rid := metadataapi.Must(fleetapihelpers.ToManagementClusterResourceID(stampIdentifier))
	return &fleetapi.ManagementCluster{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: rid},
		Status: fleetapi.ManagementClusterStatus{
			KubeApplierCosmosContainerName: containerName,
		},
	}
}

// TestKubeApplierDBClients_ForReturnsNilForUnknownMC documents the contract: an
// unknown resourceID yields nil so callers can decide how to handle the gap.
func TestKubeApplierDBClients_ForReturnsNilForUnknownMC(t *testing.T) {
	clients := NewKubeApplierDBClients("https://cosmos.test", "test", cosmosclient.Options{}, func(string) (*cosmosratelimit.TokenBucket, error) {
		return cosmosratelimit.NewUnlimitedTokenBucket("test"), nil
	}, &fakeManagementClusterLister{})
	rid := mustParseResourceIDForKubeApplierTest(t, "/providers/microsoft.redhatopenshift/stamps/1/managementclusters/default")
	client, err := clients.For(context.Background(), rid)
	assert.NoError(t, err)
	assert.Nil(t, client)
}

// TestKubeApplierDBClients_ForUsesLister_ReturnsNilWhenMissing covers the
// "iterate the entire list" branch: an MC not in the lister produces nil even
// when other MCs are present. It also verifies the lister is consulted (calls
// counter goes up).
func TestKubeApplierDBClients_ForUsesLister_ReturnsNilWhenMissing(t *testing.T) {
	lister := &fakeManagementClusterLister{
		mcs: []*fleetapi.ManagementCluster{newFakeMC(t, "present", "container-a")},
	}
	clients := NewKubeApplierDBClients("https://cosmos.test", "test", cosmosclient.Options{}, func(string) (*cosmosratelimit.TokenBucket, error) {
		return cosmosratelimit.NewUnlimitedTokenBucket("test"), nil
	}, lister)

	missing := mustParseResourceIDForKubeApplierTest(t, "/providers/microsoft.redhatopenshift/stamps/missing/managementclusters/default")
	client, err := clients.For(context.Background(), missing)
	assert.NoError(t, err)
	assert.Nil(t, client)
	assert.GreaterOrEqual(t, lister.calls, 1, "For() must consult the lister")
}

// TestKubeApplierDBClients_ForIsThreadSafe stress-tests the lazy cache under
// concurrent callers. The contract: For(rid) must not race the cache or the
// lister, and unknown-rid lookups consistently return nil. Run under -race to
// verify the mutex actually protects the maps.
func TestKubeApplierDBClients_ForIsThreadSafe_UnknownReturnsNilUnderRace(t *testing.T) {
	clients := NewKubeApplierDBClients("https://cosmos.test", "test", cosmosclient.Options{}, func(string) (*cosmosratelimit.TokenBucket, error) {
		return cosmosratelimit.NewUnlimitedTokenBucket("test"), nil
	}, &fakeManagementClusterLister{})
	rid := mustParseResourceIDForKubeApplierTest(t, "/providers/microsoft.redhatopenshift/stamps/1/managementclusters/default")

	const goroutines = 50
	wg := sync.WaitGroup{}
	wg.Add(goroutines)
	ctx := context.Background()
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			client, err := clients.For(ctx, rid)
			assert.NoError(t, err)
			assert.Nil(t, client)
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

func TestKubeApplierClientFailuresAreNotAbsenceOrCached(t *testing.T) {
	for _, failure := range []string{"lister", "container", "bucket", "constructor", "nil bucket"} {
		t.Run(failure, func(t *testing.T) {
			mc := newFakeMC(t, "1", "container")
			lister := &fakeManagementClusterLister{mcs: []*fleetapi.ManagementCluster{mc}}
			injected := errors.New("injected failure")
			credential, err := azcosmos.NewKeyCredential("ZmFrZQ==")
			require.NoError(t, err)
			failed := true
			clients := NewKubeApplierDBClients("https://cosmos.test", "test", cosmosclient.Options{KeyCredential: &credential}, func(string) (*cosmosratelimit.TokenBucket, error) {
				if failed && failure == "bucket" {
					return nil, injected
				}
				if failed && failure == "nil bucket" {
					return nil, nil
				}
				return cosmosratelimit.NewUnlimitedTokenBucket("test"), nil
			}, lister).(*kubeApplierDBClients)
			switch failure {
			case "lister":
				lister.err = injected
			case "container":
				mc.Status.KubeApplierCosmosContainerName = ""
			case "constructor":
				clients.url = "%"
			}
			client, err := clients.For(t.Context(), mc.ResourceID)
			require.Error(t, err)
			require.ErrorContains(t, err, mc.ResourceID.String())
			if failure == "lister" || failure == "bucket" {
				require.ErrorIs(t, err, injected)
			}
			require.Nil(t, client)
			require.Empty(t, clients.clients)
			failed = false
			lister.err = nil
			mc.Status.KubeApplierCosmosContainerName = "container"
			clients.url = "https://cosmos.test"
			client, err = clients.For(t.Context(), mc.ResourceID)
			require.NoError(t, err)
			require.NotNil(t, client)
			lister.err = injected
			cached, err := clients.For(t.Context(), mc.ResourceID)
			require.NoError(t, err)
			require.Same(t, client, cached)
		})
	}
}

func TestKubeApplierClientLookupCompatibility(t *testing.T) {
	managementCluster := newFakeMC(t, "first", "first-container")
	duplicate := newFakeMC(t, "first", "second-container")
	uppercaseID := mustParseResourceIDForKubeApplierTest(t, strings.ToUpper(managementCluster.ResourceID.String()))
	type testCase struct {
		name          string
		resourceID    *azcorearm.ResourceID
		clusters      []*fleetapi.ManagementCluster
		wantContainer string
		wantListCalls int
	}
	testCases := []testCase{
		{name: "nil ID", clusters: []*fleetapi.ManagementCluster{managementCluster}},
		{name: "case insensitive", resourceID: uppercaseID, clusters: []*fleetapi.ManagementCluster{managementCluster}, wantContainer: "first-container", wantListCalls: 1},
		{name: "first matching entry", resourceID: managementCluster.ResourceID, clusters: []*fleetapi.ManagementCluster{managementCluster, duplicate}, wantContainer: "first-container", wantListCalls: 1},
		{name: "skip nil resource ID", resourceID: managementCluster.ResourceID, clusters: []*fleetapi.ManagementCluster{{}, managementCluster}, wantContainer: "first-container", wantListCalls: 1},
		{name: "missing", resourceID: managementCluster.ResourceID, wantListCalls: 1},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			credential, err := azcosmos.NewKeyCredential("ZmFrZQ==")
			require.NoError(t, err)
			lister := &fakeManagementClusterLister{mcs: testCase.clusters}
			var containerNames []string
			clients := NewKubeApplierDBClients("https://cosmos.test", "test", cosmosclient.Options{KeyCredential: &credential}, func(containerName string) (*cosmosratelimit.TokenBucket, error) {
				containerNames = append(containerNames, containerName)
				return cosmosratelimit.NewUnlimitedTokenBucket("test"), nil
			}, lister)
			client, err := clients.For(t.Context(), testCase.resourceID)
			require.NoError(t, err)
			require.Equal(t, testCase.wantListCalls, lister.calls)
			if testCase.wantContainer == "" {
				require.Nil(t, client)
				require.Empty(t, containerNames)
			} else {
				require.NotNil(t, client)
				require.Equal(t, []string{testCase.wantContainer}, containerNames)
			}
		})
	}
}

func TestKubeApplierClientCacheCompatibility(t *testing.T) {
	type testCase struct {
		name      string
		listError error
		remove    bool
		container string
	}
	testCases := []testCase{
		{name: "lister failure", listError: errors.New("list failed")},
		{name: "removed management cluster", remove: true},
		{name: "changed container", container: "replacement"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			managementCluster := newFakeMC(t, "first", "original")
			lister := &fakeManagementClusterLister{mcs: []*fleetapi.ManagementCluster{managementCluster}}
			credential, err := azcosmos.NewKeyCredential("ZmFrZQ==")
			require.NoError(t, err)
			budgetCalls := 0
			clients := NewKubeApplierDBClients("https://cosmos.test", "test", cosmosclient.Options{KeyCredential: &credential}, func(string) (*cosmosratelimit.TokenBucket, error) {
				budgetCalls++
				return cosmosratelimit.NewUnlimitedTokenBucket("test"), nil
			}, lister).(*kubeApplierDBClients)
			client, err := clients.For(t.Context(), managementCluster.ResourceID)
			require.NoError(t, err)
			require.NotNil(t, client)
			lister.err = testCase.listError
			if testCase.remove {
				lister.mcs = nil
			}
			if testCase.container != "" {
				managementCluster.Status.KubeApplierCosmosContainerName = testCase.container
			}
			clients.url = "%"
			uppercaseID := mustParseResourceIDForKubeApplierTest(t, strings.ToUpper(managementCluster.ResourceID.String()))
			cached, err := clients.For(t.Context(), uppercaseID)
			require.NoError(t, err)
			require.Same(t, client, cached)
			require.Equal(t, 1, lister.calls)
			require.Equal(t, 1, budgetCalls)
		})
	}
}
