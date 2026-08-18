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

package controllerutils

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr/funcr"
	"github.com/stretchr/testify/require"

	"k8s.io/client-go/tools/cache"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type capturingNotifier struct {
	addFunc    func(any)
	updateFunc func(any, any)
}

func (n *capturingNotifier) AddEventHandlerWithOptions(handler cache.ResourceEventHandler, opts cache.HandlerOptions) (cache.ResourceEventHandlerRegistration, error) {
	f := handler.(cache.ResourceEventHandlerFuncs)
	n.addFunc = f.AddFunc
	n.updateFunc = f.UpdateFunc
	return nil, nil
}

type mockClusterSyncer struct {
	syncOnceFunc func(ctx context.Context, key HCPClusterKey) error
	cooldown     controllerutil.CooldownChecker
}

func newFakeClusterLister(subscriptionID, resourceGroup, clusterName string) corelisters.ClusterLister {
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + subscriptionID +
			"/resourceGroups/" + resourceGroup +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + clusterName,
	))
	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	err := indexer.Add(&coreapi.HCPOpenShiftCluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID: resourceID,
		},
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.NewResource(resourceID),
		},
	})
	if err != nil {
		panic(err)
	}
	return corelisters.NewClusterLister(indexer)
}

func (m *mockClusterSyncer) SyncOnce(ctx context.Context, key HCPClusterKey) error {
	if m.syncOnceFunc != nil {
		return m.syncOnceFunc(ctx, key)
	}
	return nil
}

func (m *mockClusterSyncer) CooldownChecker() controllerutil.CooldownChecker {
	if m.cooldown != nil {
		return m.cooldown
	}
	return controllerutil.NewTimeBasedCooldownChecker(time.Minute)
}

func TestClusterWatchingControllerSyncHasLoggerContextValues(t *testing.T) {
	subscriptionID := "00000000-0000-0000-0000-000000000000"
	resourceGroup := "test-rg"
	clusterName := "test-cluster"

	capturedCtxCh := make(chan context.Context, 1)
	mockSyncer := &mockClusterSyncer{
		syncOnceFunc: func(ctx context.Context, key HCPClusterKey) error {
			select {
			case capturedCtxCh <- ctx:
			default:
			}
			return nil
		},
	}

	mockResourcesDBClient := corecosmosstoragetesting.NewMockResourcesDBClient()

	inner := &clusterWatchingController{
		name:              "test-controller",
		resourcesDBClient: mockResourcesDBClient,
		syncer:            mockSyncer,
		clusterLister:     newFakeClusterLister(subscriptionID, resourceGroup, clusterName),
	}
	gwc := newGenericWatchingController("test-controller", coreapi.ClusterResourceType, inner)

	notifier := &capturingNotifier{}
	require.NoError(t, gwc.QueueForInformers(time.Minute, notifier))

	clusterResourceID := metadataapi.Must(coreapi.ToClusterResourceID(subscriptionID, resourceGroup, clusterName))
	notifier.addFunc(&coreapi.CosmosMetadata{ResourceID: clusterResourceID})

	var logOutput strings.Builder
	logger := funcr.New(func(prefix, args string) {
		logOutput.WriteString(prefix)
		logOutput.WriteString(args)
	}, funcr.Options{})

	ctx, cancel := context.WithCancel(utils.ContextWithLogger(context.Background(), logger))

	done := make(chan struct{})
	go func() {
		defer close(done)
		gwc.Run(ctx, 1)
	}()

	var capturedCtx context.Context
	select {
	case capturedCtx = <-capturedCtxCh:
	case <-time.After(5 * time.Second):
		t.Fatal("syncer should have been called")
	}

	cancel()
	<-done

	capturedLogger := utils.LoggerFromContext(capturedCtx)
	capturedLogger.Info("test")

	output := logOutput.String()
	require.Contains(t, output, ` "subscription_id"="00000000-0000-0000-0000-000000000000" `, "logger should contain subscription_id")
	require.Contains(t, output, ` "resource_group"="test-rg" `, "logger should contain resource_group")
	require.Contains(t, output, ` "resource_name"="test-cluster" `, "logger should contain cluster name")
	require.Contains(t, output, `"hcp_cluster_name"="/subscriptions/00000000-0000-0000-0000-000000000000/resourcegroups/test-rg/providers/microsoft.redhatopenshift/hcpopenshiftclusters/test-cluster"`)

}
