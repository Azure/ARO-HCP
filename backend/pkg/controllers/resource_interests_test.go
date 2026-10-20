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

package controllers_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"k8s.io/client-go/tools/cache"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	clusterdeletion "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/deletion"
	clusterproperties "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/properties"
	clusterreaddesires "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/readdesires"
	clusterstatus "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/status"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/version/rollout"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/clusterresources"
	nodepoolreaddesires "github.com/Azure/ARO-HCP/backend/pkg/controllers/nodepool/readdesires"
	nodepoolstatus "github.com/Azure/ARO-HCP/backend/pkg/controllers/nodepool/status"
	nodepoolversion "github.com/Azure/ARO-HCP/backend/pkg/controllers/nodepool/version"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/informers/kubeapplierinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
)

// Capture the handlers installed by real constructors, including the union's
// per-management-cluster handlers. No reflector, database or worker timing is
// involved in delivering events.
type interestInformer struct {
	cache.SharedIndexInformer
	handlers []cache.ResourceEventHandler
	options  []cache.HandlerOptions
}

func (i *interestInformer) AddEventHandlerWithOptions(h cache.ResourceEventHandler, options cache.HandlerOptions) (cache.ResourceEventHandlerRegistration, error) {
	i.handlers = append(i.handlers, h)
	i.options = append(i.options, options)
	return interestRegistration{}, nil
}

type interestRegistration struct {
	cache.ResourceEventHandlerRegistration
}

func (interestRegistration) HasSynced() bool { return true }

func (i *interestInformer) HasSynced() bool { return true }

type interestBackendInformers struct {
	coreinformers.BackendInformers
	sources       map[string]*interestInformer
	clusters      corelisters.ClusterLister
	nodePools     corelisters.NodePoolLister
	externalAuths corelisters.ExternalAuthLister
}

func (i *interestBackendInformers) Clusters() (cache.SharedIndexInformer, corelisters.ClusterLister) {
	return i.sources["cluster"], i.clusters
}
func (i *interestBackendInformers) NodePools() (cache.SharedIndexInformer, corelisters.NodePoolLister) {
	return i.sources["nodepool"], i.nodePools
}
func (i *interestBackendInformers) ServiceProviderClusters() (cache.SharedIndexInformer, corelisters.ServiceProviderClusterLister) {
	return i.sources["spc"], nil
}
func (i *interestBackendInformers) ServiceProviderNodePools() (cache.SharedIndexInformer, corelisters.ServiceProviderNodePoolLister) {
	return i.sources["spnp"], nil
}
func (i *interestBackendInformers) ManagementClusterContents() (cache.SharedIndexInformer, corelisters.ManagementClusterContentLister) {
	return i.sources["content"], nil
}
func (i *interestBackendInformers) Controllers() (cache.SharedIndexInformer, corelisters.ControllerLister) {
	return i.sources["controller"], nil
}
func (i *interestBackendInformers) ExternalAuths() (cache.SharedIndexInformer, corelisters.ExternalAuthLister) {
	return i.sources["externalauth"], i.externalAuths
}
func (i *interestBackendInformers) ServiceProviderExternalAuths() (cache.SharedIndexInformer, corelisters.ServiceProviderExternalAuthLister) {
	return i.sources["spc"], nil
}
func (i *interestBackendInformers) SystemAdminCredentialRequests() (cache.SharedIndexInformer, corelisters.SystemAdminCredentialRequestLister) {
	return i.sources["request"], nil
}
func (i *interestBackendInformers) SystemAdminCredentialRevocations() (cache.SharedIndexInformer, corelisters.SystemAdminCredentialRevocationLister) {
	return i.sources["revocation"], nil
}

type interestDesireInformers struct {
	kubeapplierinformers.KubeApplierInformers
	sources map[string]*interestInformer
}

func (i *interestDesireInformers) ApplyDesires() (cache.SharedIndexInformer, kubeapplierlisters.ApplyDesireLister) {
	return i.sources["apply"], nil
}
func (i *interestDesireInformers) ReadDesires() (cache.SharedIndexInformer, kubeapplierlisters.ReadDesireLister) {
	return i.sources["read"], nil
}

// The wrapper's first lister read records the key and stops reconciliation.
// This observes queue behavior without executing unrelated domain operations.
type interestClusterLister struct {
	corelisters.ClusterLister
	observed chan string
}

func (l *interestClusterLister) Get(_ context.Context, _, _, cluster string) (*coreapi.Cluster, error) {
	l.observed <- cluster
	return nil, cosmosstorageutils.NewNotFoundError()
}

type interestNodePoolLister struct {
	corelisters.NodePoolLister
	observed chan string
}

func (l *interestNodePoolLister) Get(_ context.Context, _, _, cluster, _ string) (*coreapi.NodePool, error) {
	l.observed <- cluster
	return nil, cosmosstorageutils.NewNotFoundError()
}

func TestControllerResourceInterests(t *testing.T) {
	type constructor func(corecosmosstorage.ResourcesDBClient, coreinformers.BackendInformers, *unionkubeapplierinformers.UnionKubeApplierInformers) controllerutils.Controller
	type event struct {
		source string
		suffix string
		want   bool
	}
	cases := []struct {
		name          string
		nodePool      bool
		newController constructor
		events        []event
	}{
		{
			name: "cluster properties read only the HostedCluster mirror",
			newController: func(db corecosmosstorage.ResourcesDBClient, inf coreinformers.BackendInformers, union *unionkubeapplierinformers.UnionKubeApplierInformers) controllerutils.Controller {
				return clusterproperties.NewClusterPropertiesSyncController(db, inf, union, nil)
			},
			events: []event{
				{"read", "/readDesires/" + kubeapplierapi.ReadDesireNameReadonlyHostedCluster, true},
				{"read", "/readDesires/" + strings.ToUpper(kubeapplierapi.ReadDesireNameReadonlyHostedCluster), true},
				{"read", "/readDesires/" + kubeapplierapi.ReadDesireNameServingCA, false},
				{"read", "/readDesires/unrelated", false},
				{"apply", "/applyDesires/" + kubeapplierapi.ReadDesireNameReadonlyHostedCluster, false},
				{"spc", "/serviceProviderClusters/default", false},
				{"content", "/managementClusterContents/legacy", false},
				{"read", "/nodePools/np/readDesires/" + kubeapplierapi.ReadDesireNameReadonlyHostedCluster, false},
			},
		},
		{
			name: "service provider properties also read the serving CA and singleton",
			newController: func(db corecosmosstorage.ResourcesDBClient, inf coreinformers.BackendInformers, union *unionkubeapplierinformers.UnionKubeApplierInformers) controllerutils.Controller {
				return clusterproperties.NewServiceProviderClusterPropertiesSyncController(db, inf, union, nil)
			},
			events: []event{
				{"read", "/readDesires/" + kubeapplierapi.ReadDesireNameReadonlyHostedCluster, true},
				{"read", "/readDesires/" + kubeapplierapi.ReadDesireNameServingCA, true},
				{"read", "/readDesires/" + kubeapplierapi.ReadDesireNameReadonlyHypershiftControlPlaneComponentClusterAutoscaler, false},
				{"spc", "/serviceProviderClusters/DEFAULT", true},
				{"spc", "/serviceProviderClusters/unrelated", false},
				{"content", "/managementClusterContents/legacy", false},
			},
		},
		{
			name:     "node pool active versions read only the NodePool mirror",
			nodePool: true,
			newController: func(db corecosmosstorage.ResourcesDBClient, inf coreinformers.BackendInformers, union *unionkubeapplierinformers.UnionKubeApplierInformers) controllerutils.Controller {
				return nodepoolversion.NewNodePoolActiveVersionController(db, inf, union, nil)
			},
			events: []event{
				{"read", "/nodePools/np/readDesires/" + kubeapplierapi.ReadDesireNameReadonlyNodePool, true},
				{"read", "/nodePools/np/readDesires/" + kubeapplierapi.ReadDesireNameReadonlyHostedCluster, false},
				{"read", "/readDesires/" + kubeapplierapi.ReadDesireNameReadonlyNodePool, false},
				{"apply", "/nodePools/np/applyDesires/" + kubeapplierapi.ReadDesireNameReadonlyNodePool, false},
				{"spnp", "/nodePools/np/serviceProviderNodePools/default", true},
				{"spnp", "/nodePools/np/serviceProviderNodePools/unrelated", false},
				{"content", "/nodePools/np/managementClusterContents/legacy", false},
			},
		},
		{
			name: "cluster degradation consumes arbitrary direct desire names",
			newController: func(db corecosmosstorage.ResourcesDBClient, inf coreinformers.BackendInformers, union *unionkubeapplierinformers.UnionKubeApplierInformers) controllerutils.Controller {
				return clusterstatus.NewClusterDegradedAggregatorController(db, nil, nil, inf, union, nil)
			},
			events: []event{
				{"controller", "/hcpOpenShiftControllers/worker", true},
				{"controller", "/nodePools/np/hcpOpenShiftControllers/worker", false},
				{"controller", "/externalAuths/auth/hcpOpenShiftControllers/worker", false},
				{"read", "/readDesires/future-mirror", true},
				{"apply", "/applyDesires/runtime-backup-name", true},
				{"read", "/nodePools/np/readDesires/future-mirror", false},
				{"apply", "/nodePools/np/applyDesires/future-desire", false},
				{"spc", "/serviceProviderClusters/default", false},
				{"content", "/managementClusterContents/legacy", false},
			},
		},
		{
			name:     "node pool degradation consumes arbitrary node pool desire names",
			nodePool: true,
			newController: func(db corecosmosstorage.ResourcesDBClient, inf coreinformers.BackendInformers, union *unionkubeapplierinformers.UnionKubeApplierInformers) controllerutils.Controller {
				return nodepoolstatus.NewNodePoolDegradedAggregatorController(db, nil, nil, inf, union, nil)
			},
			events: []event{
				{"controller", "/nodePools/np/hcpOpenShiftControllers/worker", true},
				{"controller", "/hcpOpenShiftControllers/worker", false},
				{"read", "/nodePools/np/readDesires/future-mirror", true},
				{"apply", "/nodePools/np/applyDesires/future-desire", true},
				{"read", "/readDesires/future-mirror", false},
				{"apply", "/applyDesires/future-desire", false},
				{"spnp", "/nodePools/np/serviceProviderNodePools/default", false},
			},
		},
		{
			name: "cluster read desire creator observes its own mirrors",
			newController: func(db corecosmosstorage.ResourcesDBClient, inf coreinformers.BackendInformers, union *unionkubeapplierinformers.UnionKubeApplierInformers) controllerutils.Controller {
				_, readLister := union.ReadDesires()
				return clusterreaddesires.NewCreateClusterScopedReadDesiresController(nil, db, nil, nil, readLister, union, inf, "test")
			},
			events: []event{
				{"read", "/readDesires/" + kubeapplierapi.ReadDesireNameReadonlyHostedCluster, true},
				{"read", "/readDesires/" + kubeapplierapi.ReadDesireNameServingCA, true},
				{"read", "/readDesires/" + kubeapplierapi.ReadDesireNameReadonlyHypershiftControlPlaneComponentClusterAutoscaler, true},
				{"read", "/readDesires/unrelated", false},
				{"read", "/nodePools/np/readDesires/" + kubeapplierapi.ReadDesireNameReadonlyHostedCluster, false},
			},
		},
		{
			name: "node pool read desire creator observes its own mirror", nodePool: true,
			newController: func(db corecosmosstorage.ResourcesDBClient, inf coreinformers.BackendInformers, union *unionkubeapplierinformers.UnionKubeApplierInformers) controllerutils.Controller {
				_, readLister := union.ReadDesires()
				return nodepoolreaddesires.NewCreateNodePoolScopedReadDesiresController(nil, db, nil, nil, readLister, union, inf, "test")
			},
			events: []event{
				{"read", "/nodePools/np/readDesires/" + kubeapplierapi.ReadDesireNameReadonlyNodePool, true},
				{"read", "/nodePools/np/readDesires/unrelated", false},
				{"read", "/readDesires/" + kubeapplierapi.ReadDesireNameReadonlyNodePool, false},
			},
		},
		{
			name: "cluster child cleanup observes deletion gates",
			newController: func(db corecosmosstorage.ResourcesDBClient, inf coreinformers.BackendInformers, union *unionkubeapplierinformers.UnionKubeApplierInformers) controllerutils.Controller {
				return clusterdeletion.NewClusterChildResourcesCleanupController(db, nil, inf, union)
			},
			events: []event{
				{"nodepool", "/nodePools/np", true}, {"externalauth", "/externalAuths/auth", true},
				{"request", "/systemAdminCredentialRequests/cred", true}, {"revocation", "/systemAdminCredentialRevocations/revoke", true},
				{"apply", "/applyDesires/legacy", true}, {"read", "/readDesires/legacy", true},
				{"apply", "/nodePools/np/applyDesires/legacy", false},
			},
		},
		{
			name: "cluster credential marker observes request and revocation children",
			newController: func(db corecosmosstorage.ResourcesDBClient, inf coreinformers.BackendInformers, _ *unionkubeapplierinformers.UnionKubeApplierInformers) controllerutils.Controller {
				return clusterdeletion.NewClusterCredentialDeletionMarkerController(nil, db, inf)
			},
			events: []event{
				{"request", "/systemAdminCredentialRequests/cred", true},
				{"revocation", "/systemAdminCredentialRevocations/revoke", true},
				{"request", "/nodePools/np/systemAdminCredentialRequests/cred", false},
			},
		},
		{
			name: "cluster final deletion observes deletion gates",
			newController: func(db corecosmosstorage.ResourcesDBClient, inf coreinformers.BackendInformers, union *unionkubeapplierinformers.UnionKubeApplierInformers) controllerutils.Controller {
				return clusterdeletion.NewClusterDeletionController(nil, db, nil, inf, nil, union)
			},
			events: []event{
				{"nodepool", "/nodePools/np", true}, {"externalauth", "/externalAuths/auth", true},
				{"request", "/systemAdminCredentialRequests/cred", true}, {"revocation", "/systemAdminCredentialRevocations/revoke", true},
				{"apply", "/applyDesires/legacy", true}, {"apply", "/nodePools/np/applyDesires/legacy", false},
			},
		},
		{
			name: "cluster resources observes node pools",
			newController: func(db corecosmosstorage.ResourcesDBClient, inf coreinformers.BackendInformers, u *unionkubeapplierinformers.UnionKubeApplierInformers) controllerutils.Controller {
				return clusterresources.NewClusterResourcesController(db, nil, nil, inf, u, nil)
			},
			events: []event{{"nodepool", "/nodePools/np", true}},
		},
		{
			name: "minor upgrade observes node pool provider state",
			newController: func(db corecosmosstorage.ResourcesDBClient, inf coreinformers.BackendInformers, _ *unionkubeapplierinformers.UnionKubeApplierInformers) controllerutils.Controller {
				return rollout.NewMinorUpgradeNormalClusterDesiredVersionController(nil, db, inf, nil)
			},
			events: []event{{"nodepool", "/nodePools/np", true}, {"spnp", "/nodePools/np/serviceProviderNodePools/default", true}, {"spnp", "/nodePools/np/serviceProviderNodePools/other", false}, {"spnp", "/nodePools/np/nested/n/serviceProviderNodePools/default", false}},
		},
	}
	for _, tc := range cases {
		for _, mode := range []string{"add", "update", "resync", "delete", "tombstone"} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				sources := map[string]*interestInformer{}
				for _, name := range []string{"cluster", "nodepool", "spc", "spnp", "content", "read", "apply", "controller", "externalauth", "request", "revocation"} {
					sources[name] = &interestInformer{}
				}
				observed := make(chan string, len(tc.events)+1)
				backend := &interestBackendInformers{
					sources:   sources,
					clusters:  &interestClusterLister{observed: observed},
					nodePools: &interestNodePoolLister{observed: observed},
				}
				union := unionkubeapplierinformers.NewUnionKubeApplierInformers()
				mcID := metadataapi.Must(azcorearm.ParseResourceID("/providers/Microsoft.RedHatOpenShift/stamps/1/managementClusters/default"))
				require.NoError(t, union.Add(mcID, &interestDesireInformers{sources: sources}))
				controller := tc.newController(corecosmosstoragetesting.NewMockResourcesDBClient(), backend, union)
				primarySource := "cluster"
				if tc.nodePool {
					primarySource = "nodepool"
				}
				for name, source := range sources {
					if name == primarySource {
						continue
					}
					for _, options := range source.options {
						require.NotNil(t, options.ResyncPeriod)
						require.Zero(t, *options.ResyncPeriod, "supplementary %s must use cache events, not synthetic resync", name)
					}
				}

				// Queue the entire batch before starting one worker. A final accepted
				// primary-resource event proves that all preceding events were processed,
				// allowing negative assertions without sleeps or timing-dependent checks.
				events := append([]event(nil), tc.events...)
				primary := event{source: "cluster", want: true}
				if tc.nodePool {
					primary = event{source: "nodepool", suffix: "/nodePools/np", want: true}
				}
				events = append(events, primary)
				var want []string
				for i, e := range events {
					key := fmt.Sprintf("event-%d", i)
					id := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + key + e.suffix))
					old := &coreapi.CosmosMetadata{ResourceID: id}
					old.SetEtag("old")
					current := &coreapi.CosmosMetadata{ResourceID: id}
					current.SetEtag("new")
					require.NotEmpty(t, sources[e.source].handlers, "constructor must wire the event source")
					for _, handler := range sources[e.source].handlers {
						if i == len(events)-1 {
							handler.OnAdd(current, false)
							continue
						}
						switch mode {
						case "add":
							handler.OnAdd(current, false)
						case "update":
							handler.OnUpdate(old, current)
						case "resync":
							handler.OnUpdate(current, current)
						case "delete":
							handler.OnDelete(current)
						case "tombstone":
							handler.OnDelete(cache.DeletedFinalStateUnknown{Obj: current})
						}
					}
					if e.want {
						want = append(want, key)
					}
				}
				ctx, cancel := context.WithCancel(context.Background())
				done := make(chan struct{})
				go func() { defer close(done); controller.Run(ctx, 1) }()
				defer func() { cancel(); <-done }()
				var got []string
				sentinel := fmt.Sprintf("event-%d", len(events)-1)
				timer := time.NewTimer(5 * time.Second)
				defer timer.Stop()
				for {
					select {
					case key := <-observed:
						got = append(got, key)
						if key == sentinel {
							require.Equal(t, want, got, "only relevant events should reach reconciliation, in queue order")
							return
						}
					case <-timer.C:
						t.Fatalf("primary-resource event never reached reconciliation; observed %v", got)
					}
				}
			})
		}
	}
}
