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

package coreinformers

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

func newBackendInformersWithSyncState(synced bool) *backendInformers {
	informer := &informerWithSyncState{synced: synced}
	return &backendInformers{
		subscriptionInformer:                    informer,
		activeOperationInformer:                 informer,
		allOperationInformer:                    informer,
		clusterInformer:                         informer,
		nodePoolInformer:                        informer,
		externalAuthInformer:                    informer,
		serviceProviderClusterInformer:          informer,
		serviceProviderNodePoolInformer:         informer,
		controllerInformer:                      informer,
		managementClusterContentInformer:        informer,
		systemAdminCredentialRequestInformer:    informer,
		systemAdminCredentialRevocationInformer: informer,
		billingInformer:                         informer,
	}
}

func TestBackendInformersHasSynced(t *testing.T) {
	unsyncedInformer := &informerWithSyncState{}
	testCases := []struct {
		name        string
		setUnsynced func(*backendInformers)
	}{
		{"Subscriptions", func(informers *backendInformers) { informers.subscriptionInformer = unsyncedInformer }},
		{"ActiveOperations", func(informers *backendInformers) { informers.activeOperationInformer = unsyncedInformer }},
		{"AllOperations", func(informers *backendInformers) { informers.allOperationInformer = unsyncedInformer }},
		{"Clusters", func(informers *backendInformers) { informers.clusterInformer = unsyncedInformer }},
		{"NodePools", func(informers *backendInformers) { informers.nodePoolInformer = unsyncedInformer }},
		{"ExternalAuths", func(informers *backendInformers) { informers.externalAuthInformer = unsyncedInformer }},
		{"ServiceProviderClusters", func(informers *backendInformers) { informers.serviceProviderClusterInformer = unsyncedInformer }},
		{"ServiceProviderNodePools", func(informers *backendInformers) { informers.serviceProviderNodePoolInformer = unsyncedInformer }},
		{"Controllers", func(informers *backendInformers) { informers.controllerInformer = unsyncedInformer }},
		{"ManagementClusterContents", func(informers *backendInformers) { informers.managementClusterContentInformer = unsyncedInformer }},
		{"SystemAdminCredentialRequests", func(informers *backendInformers) { informers.systemAdminCredentialRequestInformer = unsyncedInformer }},
		{"SystemAdminCredentialRevocations", func(informers *backendInformers) {
			informers.systemAdminCredentialRevocationInformer = unsyncedInformer
		}},
		{"BillingDocs", func(informers *backendInformers) { informers.billingInformer = unsyncedInformer }},
	}

	require.True(t, newBackendInformersWithSyncState(true).HasSynced())

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			informers := newBackendInformersWithSyncState(true)
			testCase.setUnsynced(informers)

			require.False(t, informers.HasSynced())
		})
	}
}
