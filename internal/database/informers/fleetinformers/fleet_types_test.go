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

package fleetinformers

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr/funcr"
	"github.com/stretchr/testify/require"

	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/fleetcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/fleetcosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type informerWithSyncState struct {
	cache.SharedIndexInformer
	synced bool
}

func (i *informerWithSyncState) HasSynced() bool {
	return i.synced
}

func newFleetInformersWithSyncState(synced bool) *fleetInformers {
	informer := &informerWithSyncState{synced: synced}
	return &fleetInformers{
		stampInformer:                       informer,
		managementClusterInformer:           informer,
		managementClusterSchedulingInformer: informer,
		controlPlaneVersionRolloutInformer:  informer,
		hcpResourceRequirementsInformer:     informer,
	}
}

func TestFleetInformersHasSynced(t *testing.T) {
	unsyncedInformer := &informerWithSyncState{}
	testCases := []struct {
		name        string
		setUnsynced func(*fleetInformers)
	}{
		{"Stamps", func(informers *fleetInformers) { informers.stampInformer = unsyncedInformer }},
		{"ManagementClusters", func(informers *fleetInformers) { informers.managementClusterInformer = unsyncedInformer }},
		{"ManagementClusterSchedulings", func(informers *fleetInformers) {
			informers.managementClusterSchedulingInformer = unsyncedInformer
		}},
		{"ControlPlaneVersionRollouts", func(informers *fleetInformers) {
			informers.controlPlaneVersionRolloutInformer = unsyncedInformer
		}},
		{"HCPResourceRequirements", func(informers *fleetInformers) {
			informers.hcpResourceRequirementsInformer = unsyncedInformer
		}},
	}

	require.True(t, newFleetInformersWithSyncState(true).HasSynced())

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			informers := newFleetInformersWithSyncState(true)
			testCase.setUnsynced(informers)

			require.False(t, informers.HasSynced())
		})
	}
}

func TestFleetInformersHCPResourceRequirements(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	var captureMutex sync.Mutex
	var logLines []string
	logger := funcr.NewJSON(func(line string) {
		captureMutex.Lock()
		defer captureMutex.Unlock()
		logLines = append(logLines, line)
	}, funcr.Options{})
	ctx = utils.ContextWithLogger(ctx, logger)
	client := fleetcosmosstoragetesting.NewMockFleetDBClient()
	requirements, err := fleetcosmosstorage.GetOrCreateHCPResourceRequirements(ctx, client, fleetapi.HCPResourceRequirementsResourceName)
	require.NoError(t, err)
	informers := NewFleetInformers(ctx, client.GlobalListers(), client)
	informer, lister := informers.HCPResourceRequirements()
	require.NotNil(t, informer)
	require.NotNil(t, lister)

	_, err = lister.Get(ctx)
	require.Error(t, err)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		informers.RunWithContext(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Error("fleet informers did not stop after cancellation")
		}
	})
	require.True(t, cache.WaitForCacheSync(ctx.Done(), informers.HasSynced))

	actual, err := lister.Get(ctx)
	require.NoError(t, err)
	require.Equal(t, requirements, actual)
	listed, err := lister.List(ctx)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Equal(t, actual, listed[0])

	captureMutex.Lock()
	capturedLines := append([]string(nil), logLines...)
	captureMutex.Unlock()
	var snapshots []map[string]any
	for _, line := range capturedLines {
		var entry map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &entry))
		if entry["snapshotType"] == "cosmos" {
			snapshots = append(snapshots, entry)
		}
	}
	require.Len(t, snapshots, 1, "the initial list must emit the singleton snapshot")
	snapshot := snapshots[0]
	require.Equal(t, "cosmos", snapshot["snapshotType"])
	require.Equal(t, "dumping resourceID "+requirements.ResourceID.String(), snapshot["msg"])
	require.Equal(t, requirements.ResourceID.String(), snapshot["currentResourceID"])
	metadata, ok := snapshot["objectMetadata"].(map[string]any)
	require.True(t, ok, "snapshot must contain structured objectMetadata")
	require.Equal(t, "fleet", metadata["cosmosContainer"])
	require.Equal(t, strings.ToLower(fleetapi.HCPResourceRequirementsResourceType.String()), metadata["resourceType"])
	require.Equal(t, requirements.ResourceID.String(), metadata["resourceID"])
	require.Equal(t, fleetapi.HCPResourceRequirementsResourceName, metadata["resourceName"])
	require.NotEmpty(t, snapshot["content"])
}
