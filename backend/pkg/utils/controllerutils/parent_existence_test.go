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
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/fleetcosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/fleetlisters"
)

type parentGuardSyncer[K any] struct {
	called bool
	err    error
}

func (s *parentGuardSyncer[K]) SyncOnce(context.Context, K) error {
	s.called = true
	return s.err
}

func (s *parentGuardSyncer[K]) CooldownChecker() controllerutil.CooldownChecker { return nil }

type parentReadErrorIndexer struct {
	cache.Indexer
	err error
}

func (i parentReadErrorIndexer) GetByKey(string) (any, bool, error) {
	return nil, false, i.err
}

func TestWatchingControllersParentExistence(t *testing.T) {
	readErr := errors.New("parent cache read failed")
	syncErr := errors.New("sync failed")
	for _, kind := range []string{"credential request", "credential revocation", "management cluster"} {
		t.Run(kind, func(t *testing.T) {
			for _, tc := range []struct {
				name    string
				present bool
				readErr error
				syncErr error
			}{
				{name: "missing parent"},
				{name: "parent read error", readErr: readErr},
				{name: "present parent", present: true},
				{name: "present parent with sync error", present: true, syncErr: syncErr},
			} {
				t.Run(tc.name, func(t *testing.T) {
					ctx := t.Context()
					const controllerName = "test-controller"
					resourcesDB := corecosmosstoragetesting.NewMockResourcesDBClient()
					fleetDB := fleetcosmosstoragetesting.NewMockFleetDBClient()
					var parent any
					var parentID string
					indexer := cache.NewIndexer(func(any) (string, error) { return strings.ToLower(parentID), nil }, cache.Indexers{})
					if tc.readErr != nil {
						indexer = parentReadErrorIndexer{Indexer: indexer, err: tc.readErr}
					}
					var syncOnce func() error
					var called *bool
					var controllers cosmosstorageutils.ResourceCRUD[coreapi.Controller, *coreapi.Controller]
					switch kind {
					case "credential request":
						key := SystemAdminCredentialRequestKey{SubscriptionID: "sub", ResourceGroupName: "rg", HCPClusterName: "cluster", CredentialName: "credential"}
						parent, parentID = &coreapi.SystemAdminCredentialRequest{}, key.GetResourceID().String()
						syncer := &parentGuardSyncer[SystemAdminCredentialRequestKey]{err: tc.syncErr}
						c := &systemAdminCredentialRequestWatchingController{
							name: controllerName, syncer: syncer, resourcesDBClient: resourcesDB,
							credentialRequestLister: corelisters.NewSystemAdminCredentialRequestLister(indexer),
						}
						syncOnce, called = func() error { return c.SyncOnce(ctx, key) }, &syncer.called
						controllers = resourcesDB.HCPClusters(key.SubscriptionID, key.ResourceGroupName).SystemAdminCredentialRequests(key.HCPClusterName).Controllers(key.CredentialName)
					case "credential revocation":
						key := SystemAdminCredentialRevocationKey{SubscriptionID: "sub", ResourceGroupName: "rg", HCPClusterName: "cluster", RevocationName: "revocation"}
						parent, parentID = &coreapi.SystemAdminCredentialRevocation{}, key.GetResourceID().String()
						syncer := &parentGuardSyncer[SystemAdminCredentialRevocationKey]{err: tc.syncErr}
						c := &systemAdminCredentialRevocationWatchingController{
							name: controllerName, syncer: syncer, resourcesDBClient: resourcesDB,
							credentialRevocationLister: corelisters.NewSystemAdminCredentialRevocationLister(indexer),
						}
						syncOnce, called = func() error { return c.SyncOnce(ctx, key) }, &syncer.called
						controllers = resourcesDB.HCPClusters(key.SubscriptionID, key.ResourceGroupName).SystemAdminCredentialRevocations(key.HCPClusterName).Controllers(key.RevocationName)
					case "management cluster":
						key := ManagementClusterKey{StampIdentifier: "stamp"}
						parent, parentID = &fleetapi.ManagementCluster{}, key.GetResourceID().String()
						syncer := &parentGuardSyncer[ManagementClusterKey]{err: tc.syncErr}
						c := &managementClusterWatchingController{
							name: controllerName, syncer: syncer, fleetDBClient: fleetDB,
							managementClusterLister: fleetlisters.NewManagementClusterLister(indexer),
						}
						syncOnce, called = func() error { return c.SyncOnce(ctx, key) }, &syncer.called
						controllers = fleetDB.Stamps().ManagementClusters(key.StampIdentifier).Controllers()
					}
					if tc.present {
						require.NoError(t, indexer.Add(parent))
					}

					err := syncOnce()
					switch {
					case tc.readErr != nil:
						require.ErrorIs(t, err, tc.readErr)
					case tc.syncErr != nil:
						require.ErrorIs(t, err, tc.syncErr)
					default:
						require.NoError(t, err)
					}
					require.Equal(t, tc.present, *called, "sync only runs with a known parent")
					if !tc.present {
						require.Empty(t, resourcesDB.GetAllDocuments(), "must not create orphaned status")
						require.Empty(t, fleetDB.GetAllDocuments(), "must not create orphaned status")
						return
					}
					stored, err := controllers.Get(ctx, controllerName)
					require.NoError(t, err)
					condition := apimeta.FindStatusCondition(stored.Status.Conditions, "Degraded")
					require.NotNil(t, condition)
					if tc.syncErr != nil {
						require.Equal(t, metav1.ConditionTrue, condition.Status)
						require.Contains(t, condition.Message, tc.syncErr.Error())
					} else {
						require.Equal(t, metav1.ConditionFalse, condition.Status)
					}
				})
			}
		})
	}
}
