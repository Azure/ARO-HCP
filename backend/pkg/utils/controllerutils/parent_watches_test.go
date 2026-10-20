// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License 2.0.

package controllerutils

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"k8s.io/client-go/tools/cache"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/billingcosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
)

type capturedParentMapping struct {
	source       cache.SharedIndexInformer
	mapper       controllerutil.ResourceMapper
	dependencies []cache.InformerSynced
}
type mappingController struct {
	WatchingController
	watches []capturedParentMapping
}

func (c *mappingController) QueueForMappedInformer(_ time.Duration, source cache.SharedIndexInformer, mapper controllerutil.ResourceMapper, dependencies ...cache.InformerSynced) error {
	c.watches = append(c.watches, capturedParentMapping{source, mapper, dependencies})
	return nil
}

func TestParentMappingsUseScopedCaches(t *testing.T) {
	// Real informer indexers and typed listers, with no running reflectors or
	// live database. Same cluster names in different scopes catch broad scans.
	const prefix = "/subscriptions/sub1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster"
	db := corecosmosstoragetesting.NewMockResourcesDBClient()
	inf := coreinformers.NewBackendInformers(t.Context(), db.ResourcesGlobalListers(), db, billingcosmosstoragetesting.NewMockBillingDBClient().BillingGlobalListers())
	clusterInformer, _ := inf.Clusters()
	for _, id := range []string{prefix, "/subscriptions/sub1/resourceGroups/other/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster", "/subscriptions/sub2/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster"} {
		rid := metadataapi.Must(azcorearm.ParseResourceID(id))
		obj := &coreapi.Cluster{}
		obj.ResourceID = rid
		obj.ID = rid
		require.NoError(t, clusterInformer.GetIndexer().Add(obj))
	}
	for _, tc := range []struct {
		typ    azcorearm.ResourceType
		source func() cache.SharedIndexInformer
		object func(*azcorearm.ResourceID) any
		watch  func(WatchingController, coreinformers.BackendInformers, ...cache.SharedIndexInformer) error
	}{
		{coreapi.NodePoolResourceType, func() cache.SharedIndexInformer { i, _ := inf.NodePools(); return i }, func(id *azcorearm.ResourceID) any {
			o := &coreapi.NodePool{}
			o.ResourceID = id
			o.ID = id
			return o
		}, WatchClusterParentsForNodePools},
		{coreapi.ExternalAuthResourceType, func() cache.SharedIndexInformer { i, _ := inf.ExternalAuths(); return i }, func(id *azcorearm.ResourceID) any {
			o := &coreapi.ExternalAuth{}
			o.ResourceID = id
			o.ID = id
			return o
		}, WatchClusterParentsForExternalAuths},
		{coreapi.SystemAdminCredentialRequestResourceType, func() cache.SharedIndexInformer { i, _ := inf.SystemAdminCredentialRequests(); return i }, func(id *azcorearm.ResourceID) any {
			return &coreapi.SystemAdminCredentialRequest{CosmosMetadata: coreapi.CosmosMetadata{ResourceID: id}}
		}, WatchClusterParentsForCredentialRequests},
		{coreapi.SystemAdminCredentialRevocationResourceType, func() cache.SharedIndexInformer { i, _ := inf.SystemAdminCredentialRevocations(); return i }, func(id *azcorearm.ResourceID) any {
			return &coreapi.SystemAdminCredentialRevocation{CosmosMetadata: coreapi.CosmosMetadata{ResourceID: id}}
		}, WatchClusterParentsForCredentialRevocations},
	} {
		t.Run(tc.typ.String(), func(t *testing.T) {
			leaf := tc.typ.Types[len(tc.typ.Types)-1]
			childInformer := tc.source()
			var want []string
			for _, scope := range []string{prefix, "/subscriptions/sub1/resourceGroups/other/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster", "/subscriptions/sub2/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster"} {
				for _, name := range []string{"a", "b"} {
					id := metadataapi.Must(azcorearm.ParseResourceID(scope + "/" + leaf + "/" + name))
					require.NoError(t, childInformer.GetIndexer().Add(tc.object(id)))
					if scope == prefix {
						want = append(want, id.String())
					}
				}
			}
			providerInformer, _ := inf.ServiceProviderClusters()
			controller := &mappingController{}
			require.NoError(t, tc.watch(controller, inf, clusterInformer, providerInformer))
			require.Len(t, controller.watches, 2)
			for i, suffix := range []string{"", "/serviceProviderClusters/default"} {
				ids, err := controller.watches[i].mapper(t.Context(), metadataapi.Must(azcorearm.ParseResourceID(prefix+suffix)))
				require.NoError(t, err)
				var got []string
				for _, id := range ids {
					got = append(got, id.String())
				}
				require.ElementsMatch(t, want, got, "only children of the changed cluster")
				require.Len(t, controller.watches[i].dependencies, 1)
			}
		})
	}
	t.Run("subscription to clusters", func(t *testing.T) {
		c := &mappingController{}
		require.NoError(t, WatchSubscriptionsForClusters(c, inf))
		ids, err := c.watches[0].mapper(t.Context(), metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/sub1")))
		require.NoError(t, err)
		require.Len(t, ids, 2)
		for _, id := range ids {
			require.Equal(t, "sub1", id.SubscriptionID)
		}
	})
	t.Run("subscription to node pools", func(t *testing.T) {
		c := &mappingController{}
		require.NoError(t, WatchSubscriptionsForNodePools(c, inf))
		ids, err := c.watches[0].mapper(t.Context(), metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/sub1")))
		require.NoError(t, err)
		require.Len(t, ids, 4)
		for _, id := range ids {
			require.Equal(t, "sub1", id.SubscriptionID)
		}
		ids, err = c.watches[0].mapper(t.Context(), metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/missing")))
		require.NoError(t, err)
		require.Empty(t, ids)
	})
}
