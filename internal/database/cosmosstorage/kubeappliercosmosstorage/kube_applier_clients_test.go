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
	"fmt"
	"sync"
	"testing"
	"time"

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

func newFakeMC(t *testing.T, stampIdentifier, containerName, consumerName string) *fleetapi.ManagementCluster {
	t.Helper()
	rid := metadataapi.Must(fleetapihelpers.ToManagementClusterResourceID(stampIdentifier))
	return &fleetapi.ManagementCluster{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: rid},
		Status: fleetapi.ManagementClusterStatus{
			KubeApplierCosmosContainerName: containerName,
			MaestroConsumerName:            consumerName,
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
	assert.Nil(t, client)
	assert.ErrorIs(t, err, ErrManagementClusterNotFound)
}

// TestKubeApplierDBClients_ForUsesLister_ReturnsNilWhenMissing covers the
// "iterate the entire list" branch: an MC not in the lister produces nil even
// when other MCs are present. It also verifies the lister is consulted (calls
// counter goes up).
func TestKubeApplierDBClients_ForUsesLister_ReturnsNilWhenMissing(t *testing.T) {
	lister := &fakeManagementClusterLister{
		mcs: []*fleetapi.ManagementCluster{newFakeMC(t, "present", "container-a", "mc-a")},
	}
	clients := NewKubeApplierDBClients("https://cosmos.test", "test", cosmosclient.Options{}, func(string) (*cosmosratelimit.TokenBucket, error) {
		return cosmosratelimit.NewUnlimitedTokenBucket("test"), nil
	}, lister)

	missing := mustParseResourceIDForKubeApplierTest(t, "/providers/microsoft.redhatopenshift/stamps/missing/managementclusters/default")
	client, err := clients.For(context.Background(), missing)
	assert.Nil(t, client)
	assert.ErrorIs(t, err, ErrManagementClusterNotFound)
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
			assert.Nil(t, client)
			assert.ErrorIs(t, err, ErrManagementClusterNotFound)
		}()
	}
	wg.Wait()
}

func TestKubeApplierDBClients_DiscoveryDoesNotHoldCacheLock(t *testing.T) {
	first := newFakeMC(t, "first", "slow", "first")
	second := newFakeMC(t, "second", "fast", "second")
	started, release := make(chan struct{}), make(chan struct{})
	clients := NewKubeApplierDBClients("https://cosmos.test", "test", cosmosclient.Options{}, func(container string) (*cosmosratelimit.TokenBucket, error) {
		if container == "slow" {
			close(started)
			<-release
		}
		return nil, fmt.Errorf("discovery unavailable")
	}, &fakeManagementClusterLister{mcs: []*fleetapi.ManagementCluster{first, second}})
	done := make(chan error, 1)
	go func() { _, err := clients.For(t.Context(), first.ResourceID); done <- err }()
	<-started
	fast := make(chan error, 1)
	go func() { _, err := clients.For(t.Context(), second.ResourceID); fast <- err }()
	select {
	case err := <-fast:
		require.Error(t, err)
		require.NotErrorIs(t, err, ErrManagementClusterNotFound)
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("discovery blocked another management cluster")
	}
	close(release)
	require.Error(t, <-done)
	require.Empty(t, clients.(*kubeApplierDBClients).clients)
}

func mustParseResourceIDForKubeApplierTest(t *testing.T, s string) *azcorearm.ResourceID {
	t.Helper()
	rid, err := azcorearm.ParseResourceID(s)
	require.NoError(t, err)
	return rid
}

func TestKubeApplierDBClientsOperationalErrors(t *testing.T) {
	for _, failure := range []string{"lister", "cancelled", "container", "budget", "initialization", "nil ID"} {
		t.Run(failure, func(t *testing.T) {
			mc := newFakeMC(t, "present", "container", "consumer")
			lister := &fakeManagementClusterLister{mcs: []*fleetapi.ManagementCluster{mc}}
			failureErr := fmt.Errorf("temporary failure")
			endpoint := "https://cosmos.test"
			switch failure {
			case "lister":
				lister.err = failureErr
			case "cancelled":
				lister.err = context.Canceled
				failureErr = context.Canceled
			case "container":
				mc.Status.KubeApplierCosmosContainerName = ""
			case "initialization":
				endpoint = ":invalid"
			}
			credential, err := azcosmos.NewKeyCredential("ZmFrZQ==")
			require.NoError(t, err)
			clients := NewKubeApplierDBClients(endpoint, "test", cosmosclient.Options{KeyCredential: &credential}, func(string) (*cosmosratelimit.TokenBucket, error) {
				if failure == "budget" {
					return nil, failureErr
				}
				return cosmosratelimit.NewUnlimitedTokenBucket("test"), nil
			}, lister)
			id := mc.ResourceID
			if failure == "nil ID" {
				id = nil
			}
			client, err := clients.For(t.Context(), id)
			require.Nil(t, client)
			require.Error(t, err)
			require.NotErrorIs(t, err, ErrManagementClusterNotFound)
			if failure == "lister" || failure == "cancelled" || failure == "budget" {
				require.ErrorIs(t, err, failureErr)
			}
			require.Empty(t, clients.(*kubeApplierDBClients).clients)
		})
	}
}
