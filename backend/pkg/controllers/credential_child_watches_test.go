// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License 2.0.

package controllers_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"k8s.io/client-go/tools/cache"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	requestcreation "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/credentialrequest/creation"
	requestdeletion "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/credentialrequest/deletion"
	revocationcreation "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/credentialrevocation/creation"
	revocationdeletion "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/credentialrevocation/deletion"
	externalauthstatus "github.com/Azure/ARO-HCP/backend/pkg/controllers/externalauth/status"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	unioninformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
)

type childObservedDB struct {
	corecosmosstorage.ResourcesDBClient
	observed chan string
}

func (d childObservedDB) HCPClusters(sub, rg string) corecosmosstorage.HCPClusterCRUD {
	return childObservedCluster{HCPClusterCRUD: d.ResourcesDBClient.HCPClusters(sub, rg), observed: d.observed}
}

type childObservedCluster struct {
	corecosmosstorage.HCPClusterCRUD
	observed chan string
}

func (c childObservedCluster) SystemAdminCredentialRequests(cluster string) corecosmosstorage.SystemAdminCredentialRequestsCRUD {
	return childObservedRequests{SystemAdminCredentialRequestsCRUD: c.HCPClusterCRUD.SystemAdminCredentialRequests(cluster), observed: c.observed}
}
func (c childObservedCluster) SystemAdminCredentialRevocations(cluster string) corecosmosstorage.SystemAdminCredentialRevocationsCRUD {
	return childObservedRevocations{SystemAdminCredentialRevocationsCRUD: c.HCPClusterCRUD.SystemAdminCredentialRevocations(cluster), observed: c.observed}
}

type childObservedRequests struct {
	corecosmosstorage.SystemAdminCredentialRequestsCRUD
	observed chan string
}

func (c childObservedRequests) Get(_ context.Context, name string) (*coreapi.SystemAdminCredentialRequest, error) {
	c.observed <- name
	return nil, cosmosstorageutils.NewNotFoundError()
}

type childObservedRevocations struct {
	corecosmosstorage.SystemAdminCredentialRevocationsCRUD
	observed chan string
}

func (c childObservedRevocations) Get(_ context.Context, name string) (*coreapi.SystemAdminCredentialRevocation, error) {
	c.observed <- name
	return nil, cosmosstorageutils.NewNotFoundError()
}

type childObservedExternalAuth struct {
	corelisters.ExternalAuthLister
	observed chan string
}

type childExistingCluster struct{ corelisters.ClusterLister }

func (childExistingCluster) Get(context.Context, string, string, string) (*coreapi.Cluster, error) {
	return &coreapi.Cluster{}, nil
}

func (c childObservedExternalAuth) Get(_ context.Context, _, _, _, name string) (*coreapi.ExternalAuth, error) {
	c.observed <- name
	return nil, cosmosstorageutils.NewNotFoundError()
}

func TestCredentialAndExternalAuthChildWatches(t *testing.T) {
	type constructor func(corecosmosstorage.ResourcesDBClient, coreinformers.BackendInformers, *unioninformers.UnionKubeApplierInformers, chan string) controllerutils.Controller
	for _, tc := range []struct {
		name, owner, source, acceptedName string
		create                            constructor
		wildcard                          bool
	}{
		{"request desires", "systemAdminCredentialRequests", "apply", kubeapplierapi.SystemAdminCredentialCSRDesireName, func(db corecosmosstorage.ResourcesDBClient, inf coreinformers.BackendInformers, u *unioninformers.UnionKubeApplierInformers, _ chan string) controllerutils.Controller {
			return requestcreation.NewDesiresCreatorController(db, nil, inf, u)
		}, false},
		{"request cleanup", "systemAdminCredentialRequests", "apply", "legacy", func(db corecosmosstorage.ResourcesDBClient, inf coreinformers.BackendInformers, u *unioninformers.UnionKubeApplierInformers, _ chan string) controllerutils.Controller {
			return requestdeletion.NewPostIssuanceCleanupController(db, nil, inf, u)
		}, true},
		{"request deletion", "systemAdminCredentialRequests", "apply", "legacy", func(db corecosmosstorage.ResourcesDBClient, inf coreinformers.BackendInformers, u *unioninformers.UnionKubeApplierInformers, _ chan string) controllerutils.Controller {
			return requestdeletion.NewClusterDeletionCleanupController(db, nil, inf, u)
		}, true},
		{"revocation desires", "systemAdminCredentialRevocations", "apply", kubeapplierapi.SystemAdminCredentialRevocationDesireName, func(db corecosmosstorage.ResourcesDBClient, inf coreinformers.BackendInformers, u *unioninformers.UnionKubeApplierInformers, _ chan string) controllerutils.Controller {
			return revocationcreation.NewRevocationDesiresController(db, nil, inf, u, nil, nil)
		}, false},
		{"revocation deletion", "systemAdminCredentialRevocations", "apply", "legacy", func(db corecosmosstorage.ResourcesDBClient, inf coreinformers.BackendInformers, u *unioninformers.UnionKubeApplierInformers, _ chan string) controllerutils.Controller {
			return revocationdeletion.NewRevocationDeletionController(db, nil, inf, u)
		}, true},
		{"external auth status", "externalAuths", "controller", "worker", func(db corecosmosstorage.ResourcesDBClient, inf coreinformers.BackendInformers, _ *unioninformers.UnionKubeApplierInformers, observed chan string) controllerutils.Controller {
			return externalauthstatus.NewExternalAuthDegradedAggregatorController(db, childObservedExternalAuth{observed: observed}, nil, inf, nil)
		}, true},
	} {
		for _, mode := range []string{"add", "update", "delete", "tombstone"} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				sources := map[string]*interestInformer{}
				for _, name := range []string{"cluster", "nodepool", "spc", "spnp", "content", "read", "apply", "controller", "externalauth", "request", "revocation"} {
					sources[name] = &interestInformer{}
				}
				observed := make(chan string, 10)
				backend := &interestBackendInformers{sources: sources, clusters: childExistingCluster{}, externalAuths: childObservedExternalAuth{observed: observed}}
				union := unioninformers.NewUnionKubeApplierInformers()
				mc := metadataapi.Must(azcorearm.ParseResourceID("/providers/Microsoft.RedHatOpenShift/stamps/s1/managementClusters/default"))
				require.NoError(t, union.Add(mc, &interestDesireInformers{sources: sources}))
				controller := tc.create(childObservedDB{ResourcesDBClient: corecosmosstoragetesting.NewMockResourcesDBClient(), observed: observed}, backend, union, observed)
				handlers := sources[tc.source].handlers
				require.NotEmpty(t, handlers)
				leaf := "applyDesires"
				if tc.source == "controller" {
					leaf = "hcpOpenShiftControllers"
				}
				prefix := "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/"
				events := []struct {
					path string
					want bool
				}{
					{tc.owner + "/event-0/" + leaf + "/" + tc.acceptedName, true},
					{tc.owner + "/event-1/" + leaf + "/unrelated", tc.wildcard},
					{"nodePools/event-2/" + leaf + "/" + tc.acceptedName, false},
					{leaf + "/" + tc.acceptedName, false},
					{tc.owner + "/event-4/" + leaf + "/" + tc.acceptedName, true},
				}
				var want []string
				for i, event := range events {
					id := metadataapi.Must(azcorearm.ParseResourceID(prefix + event.path))
					obj := &coreapi.CosmosMetadata{ResourceID: id}
					obj.SetEtag("new")
					old := obj.DeepCopy()
					old.SetEtag("old")
					for _, h := range handlers {
						switch mode {
						case "add":
							h.OnAdd(obj, false)
						case "update":
							h.OnUpdate(old, obj)
						case "delete":
							h.OnDelete(obj)
						case "tombstone":
							h.OnDelete(cache.DeletedFinalStateUnknown{Obj: obj})
						}
					}
					if event.want {
						want = append(want, fmt.Sprintf("event-%d", i))
					}
				}
				ctx, cancel := context.WithCancel(t.Context())
				done := make(chan struct{})
				go func() { defer close(done); controller.Run(ctx, 1) }()
				defer func() { cancel(); <-done }()
				var got []string
				timeout := time.NewTimer(5 * time.Second)
				defer timeout.Stop()
				for {
					select {
					case key := <-observed:
						got = append(got, key)
						if key == "event-4" {
							require.Equal(t, want, got, "only direct children with accepted types/names should enqueue their owner")
							return
						}
					case <-timeout.C:
						t.Fatalf("sentinel never reconciled; got %v", got)
					}
				}
			})
		}
	}
}
