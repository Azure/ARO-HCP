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

package hcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/blang/semver/v4"
	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	configv1 "github.com/openshift/api/config/v1"
	hsv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apitesting/coreapitesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/errorutils"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func seedFleetPinCluster(ctx context.Context, t *testing.T, db corecosmosstorage.ResourcesDBClient, name, group, minor string) *coreapi.ServiceProviderCluster {
	t.Helper()
	base := metadataapi.Must(azcorearm.ParseResourceID(coreapitesting.TestClusterResourceID))
	id := metadataapi.Must(azcorearm.ParseResourceID(strings.TrimSuffix(base.String(), base.Name) + name))
	seedHCPCluster(ctx, t, db, id, minor, group)
	provider, err := corecosmosstorage.GetOrCreateServiceProviderCluster(ctx, db, id)
	require.NoError(t, err)
	provider.Spec.ControlPlaneVersion.DesiredVersion = ptr.To(semver.MustParse(minor + ".10"))
	provider.Status.ActualHostedCluster = hostedClusterVersionHistory(
		hsv1beta1.ControlPlaneUpdateHistory{Version: minor + ".10", State: configv1.CompletedUpdate},
		hsv1beta1.ControlPlaneUpdateHistory{Version: minor + ".2", State: configv1.CompletedUpdate},
		hsv1beta1.ControlPlaneUpdateHistory{Version: minor + ".0", State: configv1.CompletedUpdate},
	)
	return storeFleetProvider(ctx, t, db, provider)
}

func storeFleetProvider(ctx context.Context, t *testing.T, db corecosmosstorage.ResourcesDBClient, provider *coreapi.ServiceProviderCluster) *coreapi.ServiceProviderCluster {
	t.Helper()
	id := provider.ResourceID.Parent
	stored, err := db.ServiceProviderClusters(id.SubscriptionID, id.ResourceGroupName, id.Name).Replace(ctx, provider, nil)
	require.NoError(t, err)
	return stored
}

func getFleetProvider(ctx context.Context, t *testing.T, db corecosmosstorage.ResourcesDBClient, before *coreapi.ServiceProviderCluster) *coreapi.ServiceProviderCluster {
	t.Helper()
	id := before.ResourceID.Parent
	after, err := db.ServiceProviderClusters(id.SubscriptionID, id.ResourceGroupName, id.Name).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	return after
}

func callFleetPin(ctx context.Context, db corecosmosstorage.ResourcesDBClient, channel, body string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	mux.Handle("POST /admin/v1/versionrollouts/{channel}/controlplaneversionpin", errorutils.ReportError(NewFleetVersionPinHandler(db).ServeHTTP))
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/v1/versionrollouts/"+channel+"/controlplaneversionpin", strings.NewReader(body)).WithContext(ctx)
	mux.ServeHTTP(recorder, req)
	return recorder
}

func fleetSummary(t *testing.T, recorder *httptest.ResponseRecorder) fleetVersionPinResponse {
	t.Helper()
	var response fleetVersionPinResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Equal(t, response.MatchedCount, response.AffectedCount+response.UnchangedCount+response.FailedCount)
	require.Len(t, response.Failures, response.FailedCount)
	return response
}

func TestFleetVersionPinHandler_SelectionAndRetry(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	db := corecosmosstoragetesting.NewMockResourcesDBClient()
	selected := []*coreapi.ServiceProviderCluster{seedFleetPinCluster(ctx, t, db, "desired", "stable", "4.22")}
	fallback := seedFleetPinCluster(ctx, t, db, "fallback", "stable", "4.22")
	fallback.Spec.ControlPlaneVersion.DesiredVersion = nil
	fallback.Status.ControlPlaneVersion.ActiveVersions = []coreapi.ServiceProviderClusterActiveVersion{
		{Version: ptr.To(semver.MustParse("4.23.1")), State: configv1.PartialUpdate},
		{Version: ptr.To(semver.MustParse("4.22.10")), State: configv1.CompletedUpdate},
	}
	selected = append(selected, storeFleetProvider(ctx, t, db, fallback))
	// Global enumeration must also include another subscription/partition.
	otherID := metadataapi.Must(azcorearm.ParseResourceID(strings.Replace(selected[0].ResourceID.Parent.String(), selected[0].ResourceID.SubscriptionID, "22222222-2222-2222-2222-222222222222", 1)))
	seedHCPCluster(ctx, t, db, otherID, "4.22", "stable")
	other, err := corecosmosstorage.GetOrCreateServiceProviderCluster(ctx, db, otherID)
	require.NoError(t, err)
	other.Spec.ControlPlaneVersion.DesiredVersion = selected[0].Spec.ControlPlaneVersion.DesiredVersion
	other.Status.ActualHostedCluster = selected[0].Status.ActualHostedCluster.DeepCopy()
	selected = append(selected, storeFleetProvider(ctx, t, db, other))

	excluded := []*coreapi.ServiceProviderCluster{
		seedFleetPinCluster(ctx, t, db, "fast", "fast", "4.22"),
		seedFleetPinCluster(ctx, t, db, "candidate", "candidate", "4.22"),
		seedFleetPinCluster(ctx, t, db, "nightly", "nightly", "4.22"),
		seedFleetPinCluster(ctx, t, db, "other-minor", "stable", "4.23"),
		seedFleetPinCluster(ctx, t, db, "other-major", "stable", "5.22"),
	}
	for _, name := range []string{"deleting", "orphan", "missing-group", "unknown-minor", "partial-only", "nil-completed", "minor-upgrade"} {
		provider := seedFleetPinCluster(ctx, t, db, name, "stable", "4.22")
		id := provider.ResourceID.Parent
		clusterCRUD := db.HCPClusters(id.SubscriptionID, id.ResourceGroupName)
		switch name {
		case "deleting", "missing-group":
			cluster, err := clusterCRUD.Get(ctx, id.Name)
			require.NoError(t, err)
			if name == "deleting" {
				cluster.ServiceProviderProperties.DeletionTimestamp = ptr.To(metav1.Now())
			} else {
				cluster.CustomerProperties.Version.ChannelGroup = ""
			}
			_, err = clusterCRUD.Replace(ctx, cluster, nil)
			require.NoError(t, err)
		case "orphan":
			require.NoError(t, clusterCRUD.Delete(ctx, id.Name))
		case "unknown-minor", "partial-only", "nil-completed":
			provider.Spec.ControlPlaneVersion.DesiredVersion = nil
			switch name {
			case "partial-only":
				provider.Status.ControlPlaneVersion.ActiveVersions = []coreapi.ServiceProviderClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.22.10")), State: configv1.PartialUpdate}}
			case "nil-completed":
				provider.Status.ControlPlaneVersion.ActiveVersions = []coreapi.ServiceProviderClusterActiveVersion{{State: configv1.CompletedUpdate}}
			}
			provider = storeFleetProvider(ctx, t, db, provider)
		case "minor-upgrade":
			provider.Spec.ControlPlaneVersion.DesiredVersion = ptr.To(semver.MustParse("4.23.1"))
			provider.Status.ControlPlaneVersion.ActiveVersions = []coreapi.ServiceProviderClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.22.10")), State: configv1.CompletedUpdate}}
			provider = storeFleetProvider(ctx, t, db, provider)
		}
		excluded = append(excluded, provider)
	}
	recorder := callFleetPin(ctx, db, "stable-4.22", `{"exactVersion":"4.22.2","untilExactVersion":"4.22.20"}`)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	summary := fleetSummary(t, recorder)
	require.Equal(t, "stable-4.22", summary.Channel)
	require.Equal(t, len(selected), summary.MatchedCount)
	require.Equal(t, len(selected), summary.AffectedCount)
	for i, before := range selected {
		after := getFleetProvider(ctx, t, db, before)
		expected := before.DeepCopy()
		expected.Spec.PinnedVersion = coreapi.ServiceProviderClusterPinnedVersion{ExactVersion: ptr.To(semver.MustParse("4.22.2")), UntilExactVersion: ptr.To(semver.MustParse("4.22.20"))}
		expected.CosmosMetadata = after.CosmosMetadata
		require.Equal(t, expected, after, "only the pin and Cosmos metadata may change")
		// A retry after rollback completes must be a no-op, even without history.
		after.Status.ActualHostedCluster = nil
		selected[i] = storeFleetProvider(ctx, t, db, after)
	}
	for _, before := range excluded {
		require.Equal(t, before, getFleetProvider(ctx, t, db, before))
	}
	recorder = callFleetPin(ctx, db, "stable-4.22", `{"exactVersion":"4.22.2","untilExactVersion":"4.22.20"}`)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	summary = fleetSummary(t, recorder)
	require.Equal(t, len(selected), summary.UnchangedCount)
	require.Zero(t, summary.AffectedCount)
	for _, before := range selected {
		require.Equal(t, before, getFleetProvider(ctx, t, db, before), "no-op must not change the ETag")
	}
	recorder = callFleetPin(ctx, db, "stable-4.22", `{"exactVersion":null}`)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Equal(t, len(selected), fleetSummary(t, recorder).AffectedCount)
	for _, before := range selected {
		require.Equal(t, coreapi.ServiceProviderClusterPinnedVersion{}, getFleetProvider(ctx, t, db, before).Spec.PinnedVersion)
	}
	recorder = callFleetPin(ctx, db, "stable-4.22", `{}`)
	require.Equal(t, len(selected), fleetSummary(t, recorder).UnchangedCount)
}

func TestFleetVersionPinHandler_PrevalidatesEntireSelection(t *testing.T) {
	for _, scenario := range []string{"older installed target", "missing control-plane history", "customer minor mismatch"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			db := corecosmosstoragetesting.NewMockResourcesDBClient()
			first := seedFleetPinCluster(ctx, t, db, "a-valid", "stable", "4.22")
			invalid := seedFleetPinCluster(ctx, t, db, "z-invalid", "stable", "4.22")
			switch scenario {
			case "older installed target":
				invalid.Status.ActualHostedCluster = hostedClusterVersionHistory(
					hsv1beta1.ControlPlaneUpdateHistory{Version: "4.22.10", State: configv1.CompletedUpdate},
					hsv1beta1.ControlPlaneUpdateHistory{Version: "4.22.5", State: configv1.CompletedUpdate},
					hsv1beta1.ControlPlaneUpdateHistory{Version: "4.22.2", State: configv1.CompletedUpdate},
				)
			case "missing control-plane history":
				invalid.Status.ActualHostedCluster = nil
				invalid.Status.ControlPlaneVersion.ActiveVersions = []coreapi.ServiceProviderClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.22.2")), State: configv1.CompletedUpdate}}
			case "customer minor mismatch":
				id := invalid.ResourceID.Parent
				crud := db.HCPClusters(id.SubscriptionID, id.ResourceGroupName)
				cluster, err := crud.Get(ctx, id.Name)
				require.NoError(t, err)
				cluster.CustomerProperties.Version.ID = "4.23"
				_, err = crud.Replace(ctx, cluster, nil)
				require.NoError(t, err)
			}
			invalid = storeFleetProvider(ctx, t, db, invalid)
			anotherInvalid := seedFleetPinCluster(ctx, t, db, "z-missing", "stable", "4.22")
			anotherInvalid.Status.ActualHostedCluster = nil
			anotherInvalid = storeFleetProvider(ctx, t, db, anotherInvalid)
			recorder := callFleetPin(ctx, db, "stable-4.22", `{"exactVersion":"4.22.2"}`)
			require.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
			var response coreapi.CloudError
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
			require.Len(t, response.Details, 2)
			require.Equal(t, invalid.ResourceID.Parent.String(), response.Details[0].Target)
			require.Equal(t, anotherInvalid.ResourceID.Parent.String(), response.Details[1].Target)
			for _, before := range []*coreapi.ServiceProviderCluster{first, invalid, anotherInvalid} {
				require.Equal(t, before, getFleetProvider(ctx, t, db, before), "validation failure must make no writes")
			}
		})
	}
}

func TestFleetVersionPinHandler_RequestValidationAndEmptySelection(t *testing.T) {
	for _, tc := range []struct {
		channel, body string
		status        int
	}{
		{"stable-4.22", `{}`, http.StatusOK},
		{"stable-4.22", `{"exactVersion":"4.22.2"}`, http.StatusOK},
		{"unknown-4.22", `{}`, http.StatusBadRequest},
		{"stable-4.22.1", `{}`, http.StatusBadRequest},
		{"stable-04.22", `{}`, http.StatusBadRequest},
		{"stable", `{}`, http.StatusBadRequest},
		{"stable-4.22", `null`, http.StatusBadRequest},
		{"stable-4.22", `[]`, http.StatusBadRequest},
		{"stable-4.22", `{} {}`, http.StatusBadRequest},
		{"stable-4.22", `{"unexpected":true}`, http.StatusBadRequest},
		{"stable-4.22", `{"exactVersion":"bad"}`, http.StatusBadRequest},
		{"stable-4.22", `{"exactVersion":""}`, http.StatusBadRequest},
		{"stable-4.22", `{"exactVersion":"0.0.0"}`, http.StatusBadRequest},
		{"stable-4.22", `{"untilExactVersion":"4.22.20"}`, http.StatusBadRequest},
		{"stable-4.22", `{"exactVersion":"4.22.2","untilExactVersion":"4.22.1"}`, http.StatusBadRequest},
		{"stable-4.22", `{"exactVersion":"4.22.2","untilExactVersion":"4.23.1"}`, http.StatusBadRequest},
		{"stable-4.22", `{"exactVersion":"4.23.2"}`, http.StatusBadRequest},
		{"stable-4.22", `{"exactVersion":"5.22.2"}`, http.StatusBadRequest},
		{"nightly-4.22", `{"exactVersion":"4.22.2"}`, http.StatusBadRequest},
		{"nightly-4.22", `{"exactVersion":"4.22.2","untilExactVersion":"4.22.20"}`, http.StatusBadRequest},
		{"nightly-4.22", `{}`, http.StatusOK},
	} {
		t.Run(tc.channel+"/"+tc.body, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			db := corecosmosstoragetesting.NewMockResourcesDBClient()
			recorder := callFleetPin(ctx, db, tc.channel, tc.body)
			require.Equal(t, tc.status, recorder.Code, recorder.Body.String())
			if tc.status == http.StatusOK {
				require.Zero(t, fleetSummary(t, recorder).MatchedCount)
			}
		})
	}
	for _, group := range []string{"fast", "candidate"} {
		t.Run(group, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			db := corecosmosstoragetesting.NewMockResourcesDBClient()
			provider := seedFleetPinCluster(ctx, t, db, group, group, "4.22")
			recorder := callFleetPin(ctx, db, group+"-4.22", `{"exactVersion":"4.22.2"}`)
			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
			require.Equal(t, 1, fleetSummary(t, recorder).AffectedCount)
			require.Equal(t, "4.22.2", getFleetProvider(ctx, t, db, provider).Spec.PinnedVersion.ExactVersion.String())
		})
	}
	t.Run("nightly pin rejected but existing pin can be cleared", func(t *testing.T) {
		ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
		db := corecosmosstoragetesting.NewMockResourcesDBClient()
		provider := seedFleetPinCluster(ctx, t, db, "nightly", "nightly", "4.22")
		provider.Spec.PinnedVersion.ExactVersion = ptr.To(semver.MustParse("4.22.2"))
		provider = storeFleetProvider(ctx, t, db, provider)

		recorder := callFleetPin(ctx, db, "nightly-4.22", `{"exactVersion":"4.22.3"}`)
		require.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
		require.Equal(t, provider, getFleetProvider(ctx, t, db, provider))

		recorder = callFleetPin(ctx, db, "nightly-4.22", `{}`)
		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
		require.Equal(t, 1, fleetSummary(t, recorder).AffectedCount)
		require.Empty(t, getFleetProvider(ctx, t, db, provider).Spec.PinnedVersion)
	})
}

// The interceptor changes the real stored ETag just before Replace, after global
// enumeration has returned a snapshot. This exercises the mock DB's actual CAS.
type fleetPinWriteClient struct {
	corecosmosstorage.ResourcesDBClient
	beforeWrite func(context.Context, *coreapi.ServiceProviderCluster, cosmosstorageutils.ResourceCRUD[coreapi.ServiceProviderCluster, *coreapi.ServiceProviderCluster]) error
}

func (c *fleetPinWriteClient) ServiceProviderClusters(subscriptionID, resourceGroup, clusterName string) cosmosstorageutils.ResourceCRUD[coreapi.ServiceProviderCluster, *coreapi.ServiceProviderCluster] {
	return &fleetPinWriteCRUD{ResourceCRUD: c.ResourcesDBClient.ServiceProviderClusters(subscriptionID, resourceGroup, clusterName), beforeWrite: c.beforeWrite}
}

type fleetPinWriteCRUD struct {
	cosmosstorageutils.ResourceCRUD[coreapi.ServiceProviderCluster, *coreapi.ServiceProviderCluster]
	beforeWrite func(context.Context, *coreapi.ServiceProviderCluster, cosmosstorageutils.ResourceCRUD[coreapi.ServiceProviderCluster, *coreapi.ServiceProviderCluster]) error
}

func (c *fleetPinWriteCRUD) Replace(ctx context.Context, provider *coreapi.ServiceProviderCluster, options *azcosmos.ItemOptions) (*coreapi.ServiceProviderCluster, error) {
	if err := c.beforeWrite(ctx, provider, c.ResourceCRUD); err != nil {
		return nil, err
	}
	return c.ResourceCRUD.Replace(ctx, provider, options)
}

func TestFleetVersionPinHandler_PartialWrites(t *testing.T) {
	for _, clear := range []bool{false, true} {
		for _, internalFailure := range []bool{false, true} {
			t.Run(fmt.Sprintf("clear=%t/internalFailure=%t", clear, internalFailure), func(t *testing.T) {
				ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
				db := corecosmosstoragetesting.NewMockResourcesDBClient()
				var before []*coreapi.ServiceProviderCluster
				for _, name := range []string{"a-first", "b-conflict", "c-error", "d-last"} {
					provider := seedFleetPinCluster(ctx, t, db, name, "stable", "4.22")
					if clear {
						provider.Spec.PinnedVersion.ExactVersion = ptr.To(semver.MustParse("4.22.1"))
						provider = storeFleetProvider(ctx, t, db, provider)
					}
					before = append(before, provider)
				}
				var concurrent *coreapi.ServiceProviderCluster
				writes := 0
				client := &fleetPinWriteClient{ResourcesDBClient: db, beforeWrite: func(ctx context.Context, provider *coreapi.ServiceProviderCluster, crud cosmosstorageutils.ResourceCRUD[coreapi.ServiceProviderCluster, *coreapi.ServiceProviderCluster]) error {
					writes++
					switch provider.ResourceID.Parent.Name {
					case "b-conflict":
						current, err := crud.Get(ctx, coreapi.ServiceProviderClusterResourceName)
						require.NoError(t, err)
						current.Spec.DesiredHostedClusterControlPlaneSize = ptr.To("Large")
						current.Spec.PinnedVersion.ExactVersion = ptr.To(semver.MustParse("4.22.3"))
						concurrent, err = crud.Replace(ctx, current, nil)
						require.NoError(t, err)
					case "c-error":
						if internalFailure {
							return errors.New("private database diagnostics")
						}
					}
					return nil
				}}
				body := `{"exactVersion":"4.22.2"}`
				expectedStatus, affected, failed := http.StatusConflict, 3, 1
				if clear {
					body = `{}`
				}
				if internalFailure {
					expectedStatus, affected, failed = http.StatusInternalServerError, 2, 2
				}
				recorder := callFleetPin(ctx, client, "stable-4.22", body)
				require.Equal(t, expectedStatus, recorder.Code, recorder.Body.String())
				summary := fleetSummary(t, recorder)
				require.Equal(t, 4, writes, "continue after failures, without retrying conflicting snapshots")
				require.Equal(t, affected, summary.AffectedCount)
				require.Equal(t, failed, summary.FailedCount)
				require.Equal(t, before[1].ResourceID.Parent.String(), summary.Failures[0].ResourceID)
				require.Equal(t, coreapi.CloudErrorCodeConflict, summary.Failures[0].Code)
				require.NotEqual(t, before[1].CosmosETag, concurrent.CosmosETag)
				require.Equal(t, concurrent, getFleetProvider(ctx, t, db, before[1]))
				require.NotContains(t, recorder.Body.String(), "private database diagnostics")
				if internalFailure {
					require.Equal(t, coreapi.CloudErrorCodeInternalServerError, summary.Failures[1].Code)
					require.Equal(t, before[2], getFleetProvider(ctx, t, db, before[2]))
				}
				for _, i := range []int{0, 3} {
					after := getFleetProvider(ctx, t, db, before[i])
					if clear {
						require.Nil(t, after.Spec.PinnedVersion.ExactVersion)
					} else {
						require.Equal(t, "4.22.2", after.Spec.PinnedVersion.ExactVersion.String())
					}
				}
			})
		}
	}
}

// Inject failures at List or after Items has yielded records, simulating a
// later Cosmos page failing. Neither case may start any write.
type fleetPinErrorLister[T any] struct {
	cosmosstorageutils.GlobalLister[T]
	listFailure bool
}

func (l fleetPinErrorLister[T]) List(ctx context.Context, options *cosmosstorageutils.DBClientListResourceDocsOptions) (cosmosstorageutils.DBClientIterator[T], error) {
	if l.listFailure {
		return nil, errors.New("list failed")
	}
	iterator, err := l.GlobalLister.List(ctx, options)
	if err != nil {
		return nil, err
	}
	return fleetPinErrorIterator[T]{DBClientIterator: iterator}, nil
}

type fleetPinErrorIterator[T any] struct {
	cosmosstorageutils.DBClientIterator[T]
}

func (i fleetPinErrorIterator[T]) GetError() error { return errors.New("later page failed") }

type fleetPinErrorGlobals struct {
	corecosmosstorage.ResourcesGlobalListers
	clusterList bool
	listFailure bool
}

func (g fleetPinErrorGlobals) Clusters() cosmosstorageutils.GlobalLister[coreapi.Cluster] {
	base := g.ResourcesGlobalListers.Clusters()
	if g.clusterList {
		return fleetPinErrorLister[coreapi.Cluster]{GlobalLister: base, listFailure: g.listFailure}
	}
	return base
}
func (g fleetPinErrorGlobals) ServiceProviderClusters() cosmosstorageutils.GlobalLister[coreapi.ServiceProviderCluster] {
	base := g.ResourcesGlobalListers.ServiceProviderClusters()
	if !g.clusterList {
		return fleetPinErrorLister[coreapi.ServiceProviderCluster]{GlobalLister: base, listFailure: g.listFailure}
	}
	return base
}

func TestFleetVersionPinHandler_ListingFailureMakesNoWrites(t *testing.T) {
	for _, clusterList := range []bool{false, true} {
		for _, listFailure := range []bool{false, true} {
			t.Run(fmt.Sprintf("clusterList=%t/listFailure=%t", clusterList, listFailure), func(t *testing.T) {
				ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
				db := corecosmosstoragetesting.NewMockResourcesDBClient()
				before := seedFleetPinCluster(ctx, t, db, "cluster", "stable", "4.22")
				db.SetResourcesGlobalListers(fleetPinErrorGlobals{ResourcesGlobalListers: db.ResourcesGlobalListers(), clusterList: clusterList, listFailure: listFailure})
				recorder := callFleetPin(ctx, db, "stable-4.22", `{"exactVersion":"4.22.2"}`)
				require.Equal(t, http.StatusInternalServerError, recorder.Code, recorder.Body.String())
				require.Equal(t, before, getFleetProvider(ctx, t, db, before))
			})
		}
	}
}
