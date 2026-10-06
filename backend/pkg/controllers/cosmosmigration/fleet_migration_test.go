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
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/blang/semver/v4"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerconfig"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/fleetapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosclient"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosratelimit"
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

func (db migrationFleetDB) GlobalListers() fleetcosmosstorage.FleetGlobalListers {
	return migrationFleetListers{crud: db.crud}
}

type migrationFleetListers struct {
	fleetcosmosstorage.FleetGlobalListers
	crud cosmosstorageutils.ValidatingResourceCRUD[fleetapi.ControlPlaneVersionRollout, *fleetapi.ControlPlaneVersionRollout]
}

func (l migrationFleetListers) ControlPlaneVersionRollouts() cosmosstorageutils.GlobalLister[fleetapi.ControlPlaneVersionRollout] {
	return l.crud
}

type migrationRolloutCRUD struct {
	cosmosstorageutils.ValidatingResourceCRUD[fleetapi.ControlPlaneVersionRollout, *fleetapi.ControlPlaneVersionRollout]
	get     func(context.Context, string) (*fleetapi.ControlPlaneVersionRollout, error)
	list    func(context.Context, *cosmosstorageutils.DBClientListResourceDocsOptions) (cosmosstorageutils.DBClientIterator[fleetapi.ControlPlaneVersionRollout], error)
	replace func(context.Context, *fleetapi.ControlPlaneVersionRollout, *fleetapi.ControlPlaneVersionRollout, *azcosmos.ItemOptions) (*fleetapi.ControlPlaneVersionRollout, error)
}

func (c migrationRolloutCRUD) Get(ctx context.Context, name string) (*fleetapi.ControlPlaneVersionRollout, error) {
	if c.get != nil {
		return c.get(ctx, name)
	}
	return c.ValidatingResourceCRUD.Get(ctx, name)
}

func (c migrationRolloutCRUD) List(ctx context.Context, opts *cosmosstorageutils.DBClientListResourceDocsOptions) (cosmosstorageutils.DBClientIterator[fleetapi.ControlPlaneVersionRollout], error) {
	if c.list != nil {
		return c.list(ctx, opts)
	}
	return c.ValidatingResourceCRUD.List(ctx, opts)
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

type migrationTransport func(*http.Request) (*http.Response, error)

func (f migrationTransport) Do(req *http.Request) (*http.Response, error) { return f(req) }

func TestMigrateFleetProductionGetSkipsTombstone(t *testing.T) {
	db := fleetcosmosstoragetesting.NewMockFleetDBClient()
	before := seedRollout(t, db, false)
	require.NoError(t, db.ControlPlaneVersionRollouts().Delete(t.Context(), before.ResourceID.Name))
	tombstone, exists := db.GetDocument(before.GetCosmosUID())
	require.True(t, exists)
	require.True(t, cosmosstorageutils.IsSoftDeleted(tombstone))
	credential, err := azcosmos.NewKeyCredential("ZmFrZQ==")
	require.NoError(t, err)
	reads := 0
	options := cosmosclient.Options{KeyCredential: &credential, ClientOptions: azcore.ClientOptions{
		Retry: policy.RetryOptions{MaxRetries: -1},
		Transport: migrationTransport(func(req *http.Request) (*http.Response, error) {
			body := `{"id":"test"}`
			if req.URL.Path != "" && req.URL.Path != "/" {
				reads++
				require.Equal(t, http.MethodGet, req.Method, "migration must not replace a tombstone")
				require.Equal(t, "/dbs/test/colls/Fleet/docs/"+before.GetCosmosUID(), req.URL.Path)
				require.Equal(t, `["microsoft.redhatopenshift"]`, req.Header.Get("x-ms-documentdb-partitionkey"))
				body = string(tombstone)
			}
			return &http.Response{StatusCode: http.StatusOK, Request: req, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
		}),
	}}
	bucket, err := cosmosratelimit.NewTokenBucket("migration-test", 1000, 1000)
	require.NoError(t, err)
	production, err := fleetcosmosstorage.NewFleetDBClient("https://cosmos.test", "test", options, bucket)
	require.NoError(t, err)
	_, err = production.ControlPlaneVersionRollouts().Get(t.Context(), before.ResourceID.Name)
	require.True(t, cosmosstorageutils.IsNotFoundError(err), "production Get must hide soft-deleted documents")
	c := &cosmosRolloutVersionMigrationController{fleetDBClient: production}
	require.NoError(t, c.SyncOnce(t.Context(), controllerutils.ControlPlaneVersionRolloutKey{YStreamChannel: before.ResourceID.Name}))
	require.Equal(t, 2, reads, "migration must Get the queued document and skip its tombstone")
}

type failingRolloutIterator struct {
	cosmosstorageutils.DBClientIterator[fleetapi.ControlPlaneVersionRollout]
}

func (i failingRolloutIterator) GetError() error { return errors.New("page failure") }

func TestMigrateRolloutReportsFailuresAndRemainsRetryable(t *testing.T) {
	for _, failure := range []string{"get", "conflict", "precondition", "replace", "cancel"} {
		t.Run(failure, func(t *testing.T) {
			db := fleetcosmosstoragetesting.NewMockFleetDBClient()
			seedRollout(t, db, false)
			attempts := 0
			reads := 0
			fail := true
			c := &cosmosRolloutVersionMigrationController{fleetDBClient: migrationFleetDB{crud: migrationRolloutCRUD{
				ValidatingResourceCRUD: db.ControlPlaneVersionRollouts(),
				get: func(ctx context.Context, name string) (*fleetapi.ControlPlaneVersionRollout, error) {
					reads++
					if fail && failure == "get" {
						return nil, errors.New("get failure")
					}
					return db.ControlPlaneVersionRollouts().Get(ctx, name)
				},
				replace: func(ctx context.Context, next, old *fleetapi.ControlPlaneVersionRollout, opts *azcosmos.ItemOptions) (*fleetapi.ControlPlaneVersionRollout, error) {
					attempts++
					if fail {
						switch failure {
						case "conflict":
							return nil, newConflictError()
						case "precondition":
							return nil, &azcore.ResponseError{StatusCode: http.StatusPreconditionFailed}
						case "replace":
							return nil, errors.New("replace failure")
						}
					}
					return db.ControlPlaneVersionRollouts().Replace(ctx, next, old, opts)
				},
			}}}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if failure == "cancel" {
				cancel()
			}
			key := controllerutils.ControlPlaneVersionRolloutKey{YStreamChannel: "stable-4.21"}
			require.Error(t, c.SyncOnce(ctx, key))
			_, completed := c.completedRollouts.Load(key.YStreamChannel)
			require.False(t, completed)
			switch failure {
			case "conflict", "precondition":
				require.Equal(t, 3, attempts, "conflict retries are bounded")
				require.Equal(t, attempts, reads, "each retry must re-read")
			case "replace":
				require.Equal(t, 1, attempts, "non-conflict errors are returned to the queue")
			default:
				require.Zero(t, attempts)
			}
			fail = false
			require.NoError(t, c.SyncOnce(t.Context(), key))
			_, completed = c.completedRollouts.Load(key.YStreamChannel)
			require.True(t, completed)
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

func TestMigrateAllRolloutVersionsReportsListFailures(t *testing.T) {
	for _, failure := range []string{"list", "page"} {
		t.Run(failure, func(t *testing.T) {
			db := fleetcosmosstoragetesting.NewMockFleetDBClient()
			fleet := migrationFleetDB{crud: migrationRolloutCRUD{
				list: func(ctx context.Context, opts *cosmosstorageutils.DBClientListResourceDocsOptions) (cosmosstorageutils.DBClientIterator[fleetapi.ControlPlaneVersionRollout], error) {
					if failure == "list" {
						return nil, errors.New("list failure")
					}
					iterator, err := db.GlobalListers().ControlPlaneVersionRollouts().List(ctx, opts)
					return failingRolloutIterator{iterator}, err
				},
			}}
			require.Panics(t, func() { MigrateAllRolloutVersionsOrDie(t.Context(), fleet) })
		})
	}
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
