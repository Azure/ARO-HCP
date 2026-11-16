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

package namespacedeletion

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"k8s.io/client-go/tools/cache"

	backendcontrollerutils "github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/api/fleetapi"
	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/apihelpers/fleetapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/kubeappliercosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
)

func TestCleanupApplyDesiresHaveManagementClusterPartitionKey(t *testing.T) {
	ctx := context.Background()
	key := backendcontrollerutils.NamespaceDeletionKey{StampIdentifier: "stamp", Name: "ocm-arohcpprod-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	managementCluster, err := fleetapihelpers.ToManagementClusterResourceID(key.StampIdentifier)
	require.NoError(t, err)
	client := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
	clients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
	clients.Register(managementCluster, client)
	indexer := cache.NewIndexer(func(interface{}) (string, error) { return "", nil }, cache.Indexers{})
	syncer := &namespaceDeletionSyncer{
		kubeApplierDBClients: clients,
		applyDesireLister:    kubeapplierlisters.NewApplyDesireLister(indexer),
		config:               Config{MinimumDwell: time.Hour},
	}
	request := &fleetapi.NamespaceDeletion{
		Spec: fleetapi.NamespaceDeletionSpec{
			HostedClusterNamespaceName: key.Name,
			HostedClusterName:          "hcp1",
		},
		Status: fleetapi.NamespaceDeletionStatus{
			NamespaceUID: "namespace-uid", HostedClusterUID: "hosted-cluster-uid",
			ControlPlaneNamespaceUID: "control-plane-uid",
		},
	}
	relationship := &fleetapihelpers.NamespaceRelationship{HostedClusterNamespace: key.Name, ControlPlaneNamespace: key.Name + "-hcp1"}
	require.NoError(t, syncer.ensureUnblockHostedClusterDesire(ctx, key, request, hostedClusterObservation{uid: request.Status.HostedClusterUID}))
	require.NoError(t, syncer.ensureFinalizeDesire(ctx, key, request, relationship, namespaceObservation{}))
	crud, err := client.ApplyDesiresForManagementCluster(key.StampIdentifier)
	require.NoError(t, err)
	for _, name := range []string{UnblockHostedClusterDesireNameFor(key.Name), FinalizeDesireNameFor(key.Name)} {
		desire, err := crud.Get(ctx, name)
		require.NoError(t, err)
		require.Equal(t, strings.ToLower(managementCluster.String()), desire.PartitionKey)
	}
}
