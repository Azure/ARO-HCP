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

package cosmosmigration

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/blang/semver/v4"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerconfig"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/fleetapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/fleetcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/fleetcosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/informers/fleetinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/fleetlisters"
)

func seedRollout(t *testing.T, db *fleetcosmosstoragetesting.MockFleetDBClient, historical bool) *fleetapi.ControlPlaneVersionRollout {
	t.Helper()
	id, err := fleetapihelpers.ToControlPlaneVersionRolloutResourceID("stable-4.21")
	require.NoError(t, err)
	best := semver.MustParse("4.21.3")
	now := metav1.NewTime(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	rollout, err := db.ControlPlaneVersionRollouts().Create(t.Context(), &fleetapi.ControlPlaneVersionRollout{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: id, PartitionKey: strings.ToLower(coreapi.ProviderNamespace)},
		Spec: fleetapi.ControlPlaneVersionRolloutSpec{
			Version: coreapi.VersionProfile{ID: "4.21", ChannelGroup: "stable"}, BestExactVersion: &best,
		},
		Status: fleetapi.ControlPlaneVersionRolloutStatus{
			LastAssignmentTime: &now,
			Conditions: []metav1.Condition{{
				Type: "Progressing", Status: metav1.ConditionTrue, Reason: "Canary", Message: "waiting", LastTransitionTime: now,
			}},
			ClusterCountByDesiredExactVersion:            map[string]int64{"4.21.3": 4},
			MismatchedClusterCountByDesiredExactVersion:  map[string]int64{"4.21.3": 3},
			FailedClusterCountByDesiredExactVersion:      map[string]int64{"4.21.3": 1},
			ClusterCountByAchievedExactVersion:           map[string]int64{"4.21.3": 1},
			SuccessfulClusterCountByAchievedExactVersion: map[string]int64{"4.21.3": 1},
		},
	}, nil)
	require.NoError(t, err)
	if historical {
		data, ok := db.GetDocument(rollout.GetCosmosUID())
		require.True(t, ok)
		var doc map[string]any
		require.NoError(t, json.Unmarshal(data, &doc))
		delete(doc["properties"].(map[string]any)["spec"].(map[string]any), "version")
		data, err = json.Marshal(doc)
		require.NoError(t, err)
		db.StoreDocument(rollout.GetCosmosUID(), data)
	}
	return rollout
}

func TestMigrateFleetPersistsVersionAndPreservesRollout(t *testing.T) {
	for _, historical := range []bool{true, false} {
		t.Run(map[bool]string{true: "historical", false: "structured"}[historical], func(t *testing.T) {
			db := fleetcosmosstoragetesting.NewMockFleetDBClient()
			before := seedRollout(t, db, historical)
			// No subscriptions or cluster references are needed to migrate Fleet.
			MigrateAllRolloutVersionsOrDie(t.Context(), db)
			data, ok := db.GetDocument(before.GetCosmosUID())
			require.True(t, ok)
			var stored cosmosstorageutils.GenericDocument[fleetapi.ControlPlaneVersionRollout]
			require.NoError(t, json.Unmarshal(data, &stored))
			require.Equal(t, before.Spec, stored.Content.Spec, "inspect raw storage, not normalized Get")
			require.Equal(t, before.Status, stored.Content.Status)
			after, err := db.ControlPlaneVersionRollouts().Get(t.Context(), before.ResourceID.Name)
			require.NoError(t, err)
			require.Equal(t, before.ResourceID, after.ResourceID)
			require.Equal(t, before.GetCosmosUID(), after.GetCosmosUID())
			require.Equal(t, before.PartitionKey, after.PartitionKey)
			require.NotEqual(t, before.GetEtag(), after.GetEtag(), "structured rollouts must also be rewritten")
		})
	}
}

type migrationFleetDB struct {
	fleetcosmosstorage.FleetDBClient
	crud cosmosstorageutils.ValidatingResourceCRUD[fleetapi.ControlPlaneVersionRollout, *fleetapi.ControlPlaneVersionRollout]
}

func (db migrationFleetDB) ControlPlaneVersionRollouts() cosmosstorageutils.ValidatingResourceCRUD[fleetapi.ControlPlaneVersionRollout, *fleetapi.ControlPlaneVersionRollout] {
	return db.crud
}

type migrationRolloutCRUD struct {
	cosmosstorageutils.ValidatingResourceCRUD[fleetapi.ControlPlaneVersionRollout, *fleetapi.ControlPlaneVersionRollout]
	get     func(context.Context, string) (*fleetapi.ControlPlaneVersionRollout, error)
	replace func(context.Context, *fleetapi.ControlPlaneVersionRollout, *fleetapi.ControlPlaneVersionRollout, *azcosmos.ItemOptions) (*fleetapi.ControlPlaneVersionRollout, error)
}

func (c migrationRolloutCRUD) Get(ctx context.Context, name string) (*fleetapi.ControlPlaneVersionRollout, error) {
	if c.get != nil {
		return c.get(ctx, name)
	}
	return c.ValidatingResourceCRUD.Get(ctx, name)
}

func (c migrationRolloutCRUD) Replace(ctx context.Context, next, old *fleetapi.ControlPlaneVersionRollout, opts *azcosmos.ItemOptions) (*fleetapi.ControlPlaneVersionRollout, error) {
	return c.replace(ctx, next, old, opts)
}

func TestMigrateFleetRetriesConcurrentWrite(t *testing.T) {
	db := fleetcosmosstoragetesting.NewMockFleetDBClient()
	seedRollout(t, db, true)
	crud := db.ControlPlaneVersionRollouts()
	attempts := 0
	var concurrent *fleetapi.ControlPlaneVersionRollout
	c := &cosmosRolloutVersionMigrationController{fleetDBClient: migrationFleetDB{crud: migrationRolloutCRUD{
		ValidatingResourceCRUD: crud,
		replace: func(ctx context.Context, next, old *fleetapi.ControlPlaneVersionRollout, opts *azcosmos.ItemOptions) (*fleetapi.ControlPlaneVersionRollout, error) {
			attempts++
			require.NotSame(t, old, next)
			require.Equal(t, old, next)
			require.Nil(t, opts)
			if attempts == 1 {
				changed := old.DeepCopy()
				best := semver.MustParse("4.21.4")
				changed.Spec.BestExactVersion = &best
				changed.Status.ClusterCountByDesiredExactVersion["4.21.4"] = 2
				var err error
				concurrent, err = crud.Replace(ctx, changed, old, nil)
				require.NoError(t, err)
			}
			return crud.Replace(ctx, next, old, opts)
		},
	}}}
	require.NoError(t, c.SyncOnce(t.Context(), controllerutils.ControlPlaneVersionRolloutKey{YStreamChannel: "stable-4.21"}))
	require.Equal(t, 2, attempts)
	after, err := crud.Get(t.Context(), "stable-4.21")
	require.NoError(t, err)
	require.Equal(t, concurrent.Spec, after.Spec)
	require.Equal(t, concurrent.Status, after.Status)
}

func TestMigrateFleetHandlesDeletion(t *testing.T) {
	for _, stage := range []string{"get", "replace", "tombstone"} {
		t.Run(stage, func(t *testing.T) {
			db := fleetcosmosstoragetesting.NewMockFleetDBClient()
			before := seedRollout(t, db, false)
			crud := db.ControlPlaneVersionRollouts()
			var tombstone json.RawMessage
			c := &cosmosRolloutVersionMigrationController{fleetDBClient: migrationFleetDB{crud: migrationRolloutCRUD{
				ValidatingResourceCRUD: crud,
				get: func(ctx context.Context, name string) (*fleetapi.ControlPlaneVersionRollout, error) {
					switch stage {
					case "get":
						db.DeleteDocument(before.GetCosmosUID())
					case "tombstone":
						require.NoError(t, crud.Delete(ctx, before.ResourceID.Name))
						var exists bool
						tombstone, exists = db.GetDocument(before.GetCosmosUID())
						require.True(t, exists)
						require.True(t, cosmosstorageutils.IsSoftDeleted(tombstone))
						_, err := crud.Get(ctx, before.ResourceID.Name)
						require.True(t, cosmosstorageutils.IsNotFoundError(err), "soft-deleted rollouts must read as NotFound")
					}
					return crud.Get(ctx, name)
				},
				replace: func(ctx context.Context, next, old *fleetapi.ControlPlaneVersionRollout, opts *azcosmos.ItemOptions) (*fleetapi.ControlPlaneVersionRollout, error) {
					require.Equal(t, "replace", stage)
					db.DeleteDocument(old.GetCosmosUID())
					return crud.Replace(ctx, next, old, opts)
				},
			}}}
			require.NoError(t, c.SyncOnce(t.Context(), controllerutils.ControlPlaneVersionRolloutKey{YStreamChannel: before.ResourceID.Name}))
			_, completed := c.completedRollouts.Load(before.ResourceID.Name)
			require.False(t, completed, "NotFound is terminal but not a successful rewrite")
			data, exists := db.GetDocument(before.GetCosmosUID())
			if stage == "tombstone" {
				require.Equal(t, tombstone, data, "migration must leave the tombstone and its TTL untouched")
			} else {
				require.False(t, exists, "migration must not recreate deleted rollouts")
			}
		})
	}
}

func TestMigrateRolloutCompletionIsIndependentAndSurvivesUpdates(t *testing.T) {
	db := fleetcosmosstoragetesting.NewMockFleetDBClient()
	before := seedRollout(t, db, true)
	crud := db.ControlPlaneVersionRollouts()
	other := before.DeepCopy()
	id, err := fleetapihelpers.ToControlPlaneVersionRolloutResourceID("candidate-4.21")
	require.NoError(t, err)
	other.CosmosMetadata = coreapi.CosmosMetadata{ResourceID: id, PartitionKey: before.PartitionKey}
	other.Spec.Version.ChannelGroup = "candidate"
	_, err = crud.Create(t.Context(), other, nil)
	require.NoError(t, err)
	calls := map[string]int{}
	failOther := true
	c := &cosmosRolloutVersionMigrationController{fleetDBClient: migrationFleetDB{crud: migrationRolloutCRUD{
		ValidatingResourceCRUD: crud,
		replace: func(ctx context.Context, next, old *fleetapi.ControlPlaneVersionRollout, opts *azcosmos.ItemOptions) (*fleetapi.ControlPlaneVersionRollout, error) {
			calls[old.ResourceID.Name]++
			if failOther && old.ResourceID.Name == other.ResourceID.Name {
				return nil, errors.New("unavailable")
			}
			return crud.Replace(ctx, next, old, opts)
		},
	}}}
	key := controllerutils.ControlPlaneVersionRolloutKey{YStreamChannel: before.ResourceID.Name}
	otherKey := controllerutils.ControlPlaneVersionRolloutKey{YStreamChannel: other.ResourceID.Name}
	require.Error(t, c.SyncOnce(t.Context(), otherKey))
	require.NoError(t, c.SyncOnce(t.Context(), key))
	failOther = false
	require.NoError(t, c.SyncOnce(t.Context(), otherKey))
	require.NoError(t, c.SyncOnce(t.Context(), key))
	require.Equal(t, map[string]int{"stable-4.21": 1, "candidate-4.21": 2}, calls)

	// A late legacy writer does not invalidate this process's completion, but
	// reads still normalize the missing profile without another persisted backfill.
	data, exists := db.GetDocument(before.GetCosmosUID())
	require.True(t, exists)
	var doc cosmosstorageutils.GenericDocument[fleetapi.ControlPlaneVersionRollout]
	require.NoError(t, json.Unmarshal(data, &doc))
	doc.Content.Spec.Version = coreapi.VersionProfile{}
	data, err = json.Marshal(doc)
	require.NoError(t, err)
	db.StoreDocument(before.GetCosmosUID(), data)
	after, err := crud.Get(t.Context(), before.ResourceID.Name)
	require.NoError(t, err)
	require.Equal(t, before.Spec, after.Spec)
	require.Equal(t, before.Status, after.Status)
	require.NoError(t, c.SyncOnce(t.Context(), key))
	stored, exists := db.GetDocument(before.GetCosmosUID())
	require.True(t, exists)
	require.Equal(t, data, stored)
	require.Equal(t, 1, calls[key.YStreamChannel])
}

type migrationInformers struct {
	fleetinformers.FleetInformers
	rollouts cache.SharedIndexInformer
}

func (i migrationInformers) ControlPlaneVersionRollouts() (cache.SharedIndexInformer, fleetlisters.ControlPlaneVersionRolloutLister) {
	return i.rollouts, nil
}

func TestRolloutMigrationOnlyWatchesRollouts(t *testing.T) {
	// No subscriptions informer or other Fleet informer is available.
	informers := migrationInformers{rollouts: cache.NewSharedIndexInformer(&cache.ListWatch{}, &fleetapi.ControlPlaneVersionRollout{}, time.Hour, nil)}
	registration := registerCosmosRolloutVersionMigrationController()
	require.Equal(t, 5, registration.Workers)
	c, err := registration.Instantiate(controllerconfig.ControllerContext{FleetInformers: informers})
	require.NoError(t, err, "registration and cache gating must not access a subscriptions informer")
	require.NotNil(t, c)
}
