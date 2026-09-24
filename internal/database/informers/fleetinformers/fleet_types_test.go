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
	"testing"

	"github.com/stretchr/testify/require"

	"k8s.io/client-go/tools/cache"
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
