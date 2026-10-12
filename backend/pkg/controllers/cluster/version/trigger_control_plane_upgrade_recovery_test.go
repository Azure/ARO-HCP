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

package version

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blang/semver/v4"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/informers/kubeapplierinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/kubeapplierlistertesting"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

// Capture the production constructor's subscriptions while using real indexer
// listers and the real union, workqueue and controller-status wrapper.
// Handlers are registered before workers start; cached objects are immutable.
type upgradeEventInformer struct {
	cache.SharedIndexInformer
	indexer  cache.Indexer
	handlers []cache.ResourceEventHandler
	resyncs  []time.Duration
}

func newUpgradeEventInformer() *upgradeEventInformer {
	return &upgradeEventInformer{indexer: cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})}
}

func (i *upgradeEventInformer) AddEventHandlerWithOptions(handler cache.ResourceEventHandler, opts cache.HandlerOptions) (cache.ResourceEventHandlerRegistration, error) {
	i.handlers = append(i.handlers, handler)
	i.resyncs = append(i.resyncs, *opts.ResyncPeriod)
	return nil, nil
}

func (i *upgradeEventInformer) notify(obj any) {
	for _, handler := range i.handlers {
		handler.OnAdd(obj, false)
	}
}

type upgradeBackendInformers struct {
	coreinformers.BackendInformers
	clusters, providers, contents *upgradeEventInformer
}

func (i *upgradeBackendInformers) Clusters() (cache.SharedIndexInformer, corelisters.ClusterLister) {
	return i.clusters, corelisters.NewClusterLister(i.clusters.indexer)
}
func (i *upgradeBackendInformers) ServiceProviderClusters() (cache.SharedIndexInformer, corelisters.ServiceProviderClusterLister) {
	return i.providers, corelisters.NewServiceProviderClusterLister(i.providers.indexer)
}
func (i *upgradeBackendInformers) ManagementClusterContents() (cache.SharedIndexInformer, corelisters.ManagementClusterContentLister) {
	return i.contents, nil
}

type upgradeKubeApplierInformers struct {
	kubeapplierinformers.KubeApplierInformers
	reads, applies *upgradeEventInformer
}

func (i *upgradeKubeApplierInformers) ReadDesires() (cache.SharedIndexInformer, kubeapplierlisters.ReadDesireLister) {
	return i.reads, kubeapplierlisters.NewReadDesireLister(i.reads.indexer)
}
func (i *upgradeKubeApplierInformers) ApplyDesires() (cache.SharedIndexInformer, kubeapplierlisters.ApplyDesireLister) {
	return i.applies, &kubeapplierlistertesting.SliceApplyDesireLister{}
}

func TestTriggerControlPlaneUpgradeControllerRecovery(t *testing.T) {
	for _, restart := range []bool{false, true} {
		name := "rate limited recovery without another notification"
		if restart {
			name = "restart with terminal ARM operation and mirror notification"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			client := ocm.NewMockClusterServiceClientSpec(gomock.NewController(t))
			syncer, cluster, spc := newUpgradeTestSyncer(t, client)
			// The controller must operate both without an ARM operation and with
			// a terminal one. Neither case changes the desired-state gate.
			if restart {
				cluster.SystemData = &coreapi.SystemData{CreatedAt: ptr.To(syncer.clock.Now())}
				cluster.ServiceProviderProperties.ActiveOperationID = "finished-create"
				opID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/providers/Microsoft.RedHatOpenShift/locations/eastus/hcpOperationStatuses/finished-create"))
				syncer.activeOperationLister = &corelistertesting.SliceActiveOperationLister{Operations: []*coreapi.Operation{{OperationID: opID, Status: coreapi.ProvisioningStateSucceeded, Request: cosmosstorageutils.OperationRequestCreate}}}
			}
			informers := &upgradeBackendInformers{clusters: newUpgradeEventInformer(), providers: newUpgradeEventInformer(), contents: newUpgradeEventInformer()}
			require.NoError(t, informers.clusters.indexer.Add(cluster))
			require.NoError(t, informers.providers.indexer.Add(spc))
			ka := &upgradeKubeApplierInformers{reads: newUpgradeEventInformer(), applies: newUpgradeEventInformer()}
			blocked := upgradeReadDesires(t, false)[0]
			eligible := upgradeReadDesires(t, true)[0]
			require.NoError(t, ka.reads.indexer.Add(blocked))
			union := unionkubeapplierinformers.NewUnionKubeApplierInformers()
			require.NoError(t, union.Add(blocked.Spec.ManagementCluster, ka))
			_, readLister := union.ReadDesires()
			// The selected backend mirror keeps its MC and HostedCluster target
			// alongside delivered conditions; a separate CS mirror is not read.
			observed, err := readLister.GetForCluster(ctx, testSubscriptionID, testResourceGroupName, testClusterName, blocked.ResourceID.Name)
			require.NoError(t, err)
			require.Equal(t, blocked.Spec, observed.Spec)
			require.Equal(t, blocked.Status.KubeContent.Raw, observed.Status.KubeContent.Raw)
			csMirror := eligible.DeepCopy()
			csMirror.ResourceID = metadataapi.Must(azcorearm.ParseResourceID(strings.Replace(csMirror.ResourceID.String(), "readonlyhypershifthostedcluster", "csreadonlyhypershifthostedcluster", 1)))
			require.NoError(t, ka.reads.indexer.Add(csMirror))

			db := corecosmosstoragetesting.NewMockResourcesDBClient()
			newController := func() controllerutils.Controller {
				_, clusters := informers.Clusters()
				_, providers := informers.ServiceProviderClusters()
				return NewTriggerControlPlaneUpgradeController(syncer.clock, db, clusters, client, syncer.activeOperationLister, providers, informers, union, readLister)
			}
			var latest atomic.Value
			latest.Store("")
			var lists, reads, posts atomic.Int64
			csID := *cluster.ServiceProviderProperties.ClusterServiceID
			client.EXPECT().ListControlPlaneUpgradePolicies(csID, "creation_timestamp desc").DoAndReturn(func(metadataapi.InternalID, string) ocm.ControlPlaneUpgradePolicyListIterator {
				lists.Add(1)
				var policies []*arohcpv1alpha1.ControlPlaneUpgradePolicy
				if target := latest.Load().(string); target != "" {
					policies = append(policies, metadataapi.Must(arohcpv1alpha1.NewControlPlaneUpgradePolicy().Version(target).Build()))
				}
				return ocm.NewSimpleControlPlaneUpgradePolicyListIterator(policies, nil)
			}).AnyTimes()
			client.EXPECT().GetCluster(gomock.Any(), csID).DoAndReturn(func(context.Context, metadataapi.InternalID) (*arohcpv1alpha1.Cluster, error) {
				reads.Add(1)
				return upgradeSourceCluster("4.19.1"), nil
			}).AnyTimes()
			client.EXPECT().PostControlPlaneUpgradePolicy(gomock.Any(), csID, gomock.Any()).DoAndReturn(func(_ context.Context, _ metadataapi.InternalID, builder *arohcpv1alpha1.ControlPlaneUpgradePolicyBuilder) (*arohcpv1alpha1.ControlPlaneUpgradePolicy, error) {
				policy, err := builder.Build()
				if err != nil {
					return nil, err
				}
				latest.Store(policy.Version())
				posts.Add(1)
				return policy, nil
			}).AnyTimes()
			controller := newController()
			key := upgradeTestKey()
			controllerCRUD := db.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Controllers(key.HCPClusterName)
			degraded := func(status metav1.ConditionStatus) bool {
				state, err := controllerCRUD.Get(ctx, strings.ToLower(TriggerControlPlaneUpgradeControllerName))
				return err == nil && meta.IsStatusConditionPresentAndEqual(state.Status.Conditions, "Degraded", status)
			}
			require.ErrorContains(t, controller.SyncOnce(ctx, key), "OperatorBlocked")
			require.True(t, degraded(metav1.ConditionTrue))
			require.Zero(t, posts.Load())
			require.Empty(t, spc.Status.Validations, "upgrade blocks must not enter operation-failing creation validations")
			if restart {
				// Run with a cancelled context to shut down the original queue
				// before constructing the replacement controller.
				stoppedCtx, stop := context.WithCancel(ctx)
				stop()
				controller.Run(stoppedCtx, 1)
				controller = newController()
			}
			for _, informer := range []*upgradeEventInformer{informers.clusters, informers.providers, ka.reads} {
				require.NotEmpty(t, informer.handlers)
				for _, resync := range informer.resyncs {
					require.Equal(t, time.Minute, resync)
				}
			}
			done := make(chan struct{})
			go func() { defer close(done); controller.Run(ctx, 1) }()
			defer func() { cancel(); <-done }()
			if restart {
				require.NoError(t, ka.reads.indexer.Update(eligible))
				ka.reads.notify(eligible)
			} else {
				informers.clusters.notify(cluster)
				// A second worker attempt must come from the error requeue; no
				// new informer event is sent before or after changing the cache.
				require.Eventually(t, func() bool { return reads.Load() >= 3 }, 5*time.Second, 5*time.Millisecond)
				require.NoError(t, ka.reads.indexer.Update(eligible))
			}
			require.Eventually(t, func() bool { return posts.Load() == 1 && degraded(metav1.ConditionFalse) }, 5*time.Second, 5*time.Millisecond)
			require.Equal(t, "4.20.2", latest.Load())
			require.Empty(t, spc.Status.Validations)

			// A later observation change must not reassess a submitted target.
			require.NoError(t, ka.reads.indexer.Update(blocked))
			previousLists := lists.Load()
			ka.reads.notify(blocked)
			require.Eventually(t, func() bool { return lists.Load() > previousLists }, 5*time.Second, 5*time.Millisecond)
			require.EqualValues(t, 1, posts.Load())

			// Provider desired-state changes must enqueue this same controller.
			// The new target is blocked until a new mirror observation arrives.
			retargeted := spc.DeepCopy()
			retargeted.Spec.ControlPlaneVersion.DesiredVersion = ptr.To(semver.MustParse("4.21.1"))
			require.NoError(t, informers.providers.indexer.Update(retargeted))
			informers.providers.notify(retargeted)
			require.Eventually(t, func() bool { return degraded(metav1.ConditionTrue) }, 5*time.Second, 5*time.Millisecond)
			require.EqualValues(t, 1, posts.Load())
			require.NoError(t, ka.reads.indexer.Update(eligible))
			ka.reads.notify(eligible)
			require.Eventually(t, func() bool { return posts.Load() == 2 && degraded(metav1.ConditionFalse) }, 5*time.Second, 5*time.Millisecond)
			require.Equal(t, "4.21.1", latest.Load())
		})
	}
}
