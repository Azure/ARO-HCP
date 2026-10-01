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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/blang/semver/v4"
	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

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

// seedHCPCluster creates a minimal HCPOpenShiftCluster in the mock DB with
// the given version ID (e.g. "4.17") and channel group (e.g. "stable").
// If channelGroup is empty, it defaults to "stable".
func seedHCPCluster(ctx context.Context, t *testing.T, dbClient corecosmosstorage.ResourcesDBClient, resourceID *azcorearm.ResourceID, versionID, channelGroup string) {
	t.Helper()
	if channelGroup == "" {
		channelGroup = coreapi.DefaultClusterVersionChannelGroup
	}
	hcp := &coreapi.Cluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{ID: resourceID},
		},
	}
	hcp.CustomerProperties.Version.ID = versionID
	hcp.CustomerProperties.Version.ChannelGroup = channelGroup
	_, err := dbClient.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).Create(ctx, hcp, nil)
	require.NoError(t, err)
}

func TestVersionPinHandler(t *testing.T) {
	rollbackHistory := func(previous string) []hsv1beta1.ControlPlaneUpdateHistory {
		return []hsv1beta1.ControlPlaneUpdateHistory{
			{Version: "4.17.10", State: configv1.PartialUpdate},
			{Version: previous, State: configv1.CompletedUpdate},
		}
	}

	tests := []struct {
		name                 string
		body                 string
		skipResourceID       bool
		seedClusterVersion   string // version ID for the seeded HCP cluster (e.g. "4.17"); empty skips seeding
		seedChannelGroup     string // channel group for the seeded cluster; empty defaults to "stable"
		existingPin          *coreapi.ServiceProviderClusterPinnedVersion
		observedHistory      []hsv1beta1.ControlPlaneUpdateHistory
		expectedStatusCode   int
		expectedError        string
		expectProviderAbsent bool
		expectedPin          *coreapi.ServiceProviderClusterPinnedVersion
		expectedResponse     string
	}{
		{
			name:               "missing resource ID",
			body:               `{"exactVersion":"4.17.2"}`,
			skipResourceID:     true,
			expectedStatusCode: http.StatusBadRequest,
			expectedError:      "invalid resource identifier in request",
		},
		{
			name:               "invalid JSON body",
			body:               `{not json`,
			expectedStatusCode: http.StatusBadRequest,
			expectedError:      "invalid JSON body",
		},
		{
			name:               "empty string exactVersion rejected",
			body:               `{"exactVersion":""}`,
			expectedStatusCode: http.StatusBadRequest,
			expectedError:      "exactVersion must not be empty",
		},
		{
			name:               "invalid semver exactVersion",
			body:               `{"exactVersion":"not-a-version"}`,
			expectedStatusCode: http.StatusBadRequest,
			expectedError:      "is not a valid semantic version",
		},
		{
			name:               "exactVersion with build metadata rejected",
			body:               `{"exactVersion":"4.17.2+build.1"}`,
			expectedStatusCode: http.StatusBadRequest,
			expectedError:      "exactVersion must not contain build metadata",
		},
		{
			name:               "zero version rejected by cluster release line",
			body:               `{"exactVersion":"0.0.0"}`,
			seedClusterVersion: "4.20",
			expectedStatusCode: http.StatusBadRequest,
			expectedError:      "must be in the cluster's release line 4.20",
		},
		{
			name:               "untilExactVersion without exactVersion",
			body:               `{"untilExactVersion":"4.17.3"}`,
			expectedStatusCode: http.StatusBadRequest,
			expectedError:      "untilExactVersion requires exactVersion to be set",
		},
		{
			name:               "empty string untilExactVersion rejected",
			body:               `{"exactVersion":"4.17.2","untilExactVersion":""}`,
			expectedStatusCode: http.StatusBadRequest,
			expectedError:      "untilExactVersion must not be empty",
		},
		{
			name:               "invalid semver untilExactVersion",
			body:               `{"exactVersion":"4.17.2","untilExactVersion":"bad"}`,
			expectedStatusCode: http.StatusBadRequest,
			expectedError:      "is not a valid semantic version",
		},
		{
			name:               "untilExactVersion with build metadata rejected",
			body:               `{"exactVersion":"4.17.2","untilExactVersion":"4.17.4+build.1"}`,
			expectedStatusCode: http.StatusBadRequest,
			expectedError:      "untilExactVersion must not contain build metadata",
		},
		{
			name:               "untilExactVersion less than exactVersion",
			body:               `{"exactVersion":"4.17.3","untilExactVersion":"4.17.2"}`,
			expectedStatusCode: http.StatusBadRequest,
			expectedError:      "must be greater than or equal to exactVersion",
		},
		{
			name:               "untilExactVersion different minor than exactVersion",
			body:               `{"exactVersion":"4.17.2","untilExactVersion":"4.18.1"}`,
			expectedStatusCode: http.StatusBadRequest,
			expectedError:      "must be in the same major.minor release line as exactVersion",
		},
		{
			name:               "untilExactVersion different major than exactVersion",
			body:               `{"exactVersion":"4.17.2","untilExactVersion":"5.17.2"}`,
			expectedStatusCode: http.StatusBadRequest,
			expectedError:      "must be in the same major.minor release line as exactVersion",
		},
		{
			name:               "cluster not found",
			body:               `{"exactVersion":"4.17.2"}`,
			expectedStatusCode: http.StatusNotFound,
			expectedError:      "not found",
		},
		{
			name:               "cross-minor pin rejected",
			body:               `{"exactVersion":"4.18.1"}`,
			seedClusterVersion: "4.17",
			expectedStatusCode: http.StatusBadRequest,
			expectedError:      "must be in the cluster's release line 4.17",
		},
		{
			name:               "cross-major pin rejected",
			body:               `{"exactVersion":"5.17.2"}`,
			seedClusterVersion: "4.17",
			expectedStatusCode: http.StatusBadRequest,
			expectedError:      "must be in the cluster's release line 4.17",
		},
		{
			name:               "nightly cluster pin with untilExactVersion rejected",
			body:               `{"exactVersion":"4.22.0-0.nightly-multi-2026-04-07-114433","untilExactVersion":"4.22.0-0.nightly-multi-2026-04-09-114433"}`,
			seedClusterVersion: "4.22",
			seedChannelGroup:   "nightly",
			observedHistory: []hsv1beta1.ControlPlaneUpdateHistory{
				{Version: "4.22.0-0.nightly-multi-2026-04-08-114433", State: configv1.PartialUpdate},
				{Version: "4.22.0-0.nightly-multi-2026-04-07-114433", State: configv1.CompletedUpdate},
			},
			expectedStatusCode: http.StatusBadRequest,
			expectedError:      "untilExactVersion is not supported for nightly clusters",
		},
		{
			name:               "nightly cluster pin without untilExactVersion",
			body:               `{"exactVersion":"4.22.0-0.nightly-multi-2026-04-07-114433"}`,
			seedClusterVersion: "4.22",
			seedChannelGroup:   "nightly",
			observedHistory: []hsv1beta1.ControlPlaneUpdateHistory{
				{Version: "4.22.0-0.nightly-multi-2026-04-08-114433", State: configv1.PartialUpdate},
				{Version: "4.22.0-0.nightly-multi-2026-04-07-114433", State: configv1.CompletedUpdate},
			},
			expectedStatusCode: http.StatusOK,
			expectedPin: &coreapi.ServiceProviderClusterPinnedVersion{
				ExactVersion: ptr.To(semver.MustParse("4.22.0-0.nightly-multi-2026-04-07-114433")),
			},
			expectedResponse: `{"exactVersion":"4.22.0-0.nightly-multi-2026-04-07-114433"}`,
		},
		{
			name:               "nightly cluster pin to newer version",
			body:               `{"exactVersion":"4.22.0-0.nightly-multi-2026-04-09-114433"}`,
			seedClusterVersion: "4.22",
			seedChannelGroup:   "nightly",
			observedHistory: []hsv1beta1.ControlPlaneUpdateHistory{
				{Version: "4.22.0-0.nightly-multi-2026-04-07-114433", State: configv1.CompletedUpdate},
			},
			expectedStatusCode: http.StatusOK,
			expectedPin: &coreapi.ServiceProviderClusterPinnedVersion{
				ExactVersion: ptr.To(semver.MustParse("4.22.0-0.nightly-multi-2026-04-09-114433")),
			},
			expectedResponse: `{"exactVersion":"4.22.0-0.nightly-multi-2026-04-09-114433"}`,
		},
		{
			name:               "set pin with exact version only",
			body:               `{"exactVersion":"4.17.2"}`,
			seedClusterVersion: "4.17",
			observedHistory:    rollbackHistory("4.17.2"),
			expectedStatusCode: http.StatusOK,
			expectedPin: &coreapi.ServiceProviderClusterPinnedVersion{
				ExactVersion: ptr.To(semver.MustParse("4.17.2")),
			},
			expectedResponse: `{"exactVersion":"4.17.2"}`,
		},
		{
			name:               "set pin with exact and until version",
			body:               `{"exactVersion":"4.17.2","untilExactVersion":"4.17.4"}`,
			seedClusterVersion: "4.17",
			observedHistory:    rollbackHistory("4.17.2"),
			expectedStatusCode: http.StatusOK,
			expectedPin: &coreapi.ServiceProviderClusterPinnedVersion{
				ExactVersion:      ptr.To(semver.MustParse("4.17.2")),
				UntilExactVersion: ptr.To(semver.MustParse("4.17.4")),
			},
			expectedResponse: `{"exactVersion":"4.17.2","untilExactVersion":"4.17.4"}`,
		},
		{
			name:               "exactVersion requires a patch component",
			body:               `{"exactVersion":"4.17"}`,
			expectedStatusCode: http.StatusBadRequest,
			expectedError:      "exactVersion \"4.17\" is not a valid semantic version",
		},
		{
			name:               "untilExactVersion requires a patch component",
			body:               `{"exactVersion":"4.17.2","untilExactVersion":"4.17"}`,
			expectedStatusCode: http.StatusBadRequest,
			expectedError:      "untilExactVersion \"4.17\" is not a valid semantic version",
		},
		{
			name:               "clear existing pin",
			body:               `{}`,
			seedClusterVersion: "4.17",
			existingPin: &coreapi.ServiceProviderClusterPinnedVersion{
				ExactVersion: ptr.To(semver.MustParse("4.17.2")),
			},
			expectedStatusCode: http.StatusOK,
			expectedPin:        &coreapi.ServiceProviderClusterPinnedVersion{},
			expectedResponse:   `{}`,
		},
		{
			name:               "clear existing nightly pin",
			body:               `{}`,
			seedClusterVersion: "4.17",
			seedChannelGroup:   "nightly",
			existingPin: &coreapi.ServiceProviderClusterPinnedVersion{
				ExactVersion: ptr.To(semver.MustParse("4.17.2")),
			},
			expectedStatusCode: http.StatusOK,
			expectedPin:        &coreapi.ServiceProviderClusterPinnedVersion{},
			expectedResponse:   `{}`,
		},
		{
			name:               "clear existing pin with explicit null fields and whitespace",
			body:               " \n{\"exactVersion\":null,\"untilExactVersion\":null}\t\n",
			seedClusterVersion: "4.17",
			existingPin: &coreapi.ServiceProviderClusterPinnedVersion{
				ExactVersion:      ptr.To(semver.MustParse("4.17.2")),
				UntilExactVersion: ptr.To(semver.MustParse("4.17.20")),
			},
			expectedStatusCode: http.StatusOK,
			expectedPin:        &coreapi.ServiceProviderClusterPinnedVersion{},
			expectedResponse:   `{}`,
		},
		{
			name:               "set pin with surrounding whitespace",
			body:               " \n{\"exactVersion\":\"4.17.2\"}\t\n",
			seedClusterVersion: "4.17",
			observedHistory:    rollbackHistory("4.17.2"),
			expectedStatusCode: http.StatusOK,
			expectedPin: &coreapi.ServiceProviderClusterPinnedVersion{
				ExactVersion: ptr.To(semver.MustParse("4.17.2")),
			},
			expectedResponse: `{"exactVersion":"4.17.2"}`,
		},
		{
			name:               "overwrite existing pin",
			body:               `{"exactVersion":"4.17.3"}`,
			seedClusterVersion: "4.17",
			existingPin: &coreapi.ServiceProviderClusterPinnedVersion{
				ExactVersion: ptr.To(semver.MustParse("4.17.2")),
			},
			observedHistory:    rollbackHistory("4.17.3"),
			expectedStatusCode: http.StatusOK,
			expectedPin: &coreapi.ServiceProviderClusterPinnedVersion{
				ExactVersion: ptr.To(semver.MustParse("4.17.3")),
			},
			expectedResponse: `{"exactVersion":"4.17.3"}`,
		},
		{
			name:                 "clear pin without provider document rejected without creating it",
			body:                 `{}`,
			seedClusterVersion:   "4.17",
			expectedStatusCode:   http.StatusConflict,
			expectedError:        "ServiceProviderCluster for HCP cluster",
			expectProviderAbsent: true,
		},
		{
			name:               "clear pin on nonexistent cluster rejected",
			body:               `{}`,
			expectedStatusCode: http.StatusNotFound,
			expectedError:      "not found",
		},
		{
			name:               "unknown field rejected",
			body:               `{"exactVerison":"4.17.2"}`,
			expectedStatusCode: http.StatusBadRequest,
			expectedError:      "invalid JSON body",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			mockResourcesDBClient := corecosmosstoragetesting.NewMockResourcesDBClient()

			resourceID, err := azcorearm.ParseResourceID(coreapitesting.TestClusterResourceID)
			require.NoError(t, err)

			if tt.seedClusterVersion != "" {
				seedHCPCluster(ctx, t, mockResourcesDBClient, resourceID, tt.seedClusterVersion, tt.seedChannelGroup)
			}

			if tt.existingPin != nil || tt.observedHistory != nil {
				existing, err := corecosmosstorage.GetOrCreateServiceProviderCluster(ctx, mockResourcesDBClient, resourceID)
				require.NoError(t, err)
				if tt.existingPin != nil {
					existing.Spec.PinnedVersion = *tt.existingPin
				}
				if tt.observedHistory != nil {
					existing.Status.ActualHostedCluster = hostedClusterVersionHistory(tt.observedHistory...)
				}
				_, err = mockResourcesDBClient.ServiceProviderClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName, resourceID.Name).Replace(ctx, existing, nil)
				require.NoError(t, err)
			}

			handler := NewHCPVersionPinHandler(mockResourcesDBClient)

			if !tt.skipResourceID {
				ctx = utils.ContextWithResourceID(ctx, resourceID)
			}

			req := httptest.NewRequest(http.MethodPost, "/controlplaneversionpin", strings.NewReader(tt.body))
			req = req.WithContext(ctx)
			recorder := httptest.NewRecorder()

			err = handler.ServeHTTP(recorder, req)

			if tt.expectedStatusCode >= 400 {
				if err == nil {
					t.Fatalf("expected error but got none")
				}
				var cloudErr *coreapi.CloudError
				if !errors.As(err, &cloudErr) {
					t.Fatalf("expected CloudError but got %T: %v", err, err)
				}
				if cloudErr.StatusCode != tt.expectedStatusCode {
					t.Errorf("expected status %d, got %d", tt.expectedStatusCode, cloudErr.StatusCode)
				}
				if tt.expectedError != "" && !strings.Contains(err.Error(), tt.expectedError) {
					t.Errorf("expected error containing %q, got %q", tt.expectedError, err.Error())
				}
				if tt.expectProviderAbsent {
					_, readErr := mockResourcesDBClient.ServiceProviderClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName, resourceID.Name).Get(ctx, coreapi.ServiceProviderClusterResourceName)
					require.True(t, cosmosstorageutils.IsNotFoundError(readErr), "rejected clear must not create a provider document")
				}
				return
			}

			if err != nil {
				t.Fatalf("expected no error but got %v", err)
			}
			require.Equal(t, tt.expectedStatusCode, recorder.Code)

			spc, err := mockResourcesDBClient.ServiceProviderClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName, resourceID.Name).Get(ctx, coreapi.ServiceProviderClusterResourceName)
			require.NoError(t, err)
			require.NotNil(t, tt.expectedPin, "successful cases must specify the expected persisted pin")
			require.Equal(t, *tt.expectedPin, spc.Spec.PinnedVersion)
			require.NotEmpty(t, tt.expectedResponse, "successful cases must specify the expected response")
			require.JSONEq(t, tt.expectedResponse, recorder.Body.String())
		})
	}
}

func TestVersionPinHandler_RejectsDeletingCluster(t *testing.T) {
	for _, tt := range []struct {
		name string
		body string
	}{
		{name: "set pin", body: `{"exactVersion":"4.17.2"}`},
		{name: "clear pin", body: `{}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			resourceID := metadataapi.Must(azcorearm.ParseResourceID(coreapitesting.TestClusterResourceID))
			ctx = utils.ContextWithResourceID(ctx, resourceID)
			db := corecosmosstoragetesting.NewMockResourcesDBClient()
			seedHCPCluster(ctx, t, db, resourceID, "4.17", "")

			clusterCRUD := db.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName)
			cluster, err := clusterCRUD.Get(ctx, resourceID.Name)
			require.NoError(t, err)
			deletionTime := metav1.Now()
			cluster.ServiceProviderProperties.DeletionTimestamp = &deletionTime
			_, err = clusterCRUD.Replace(ctx, cluster, nil)
			require.NoError(t, err)

			provider, err := corecosmosstorage.GetOrCreateServiceProviderCluster(ctx, db, resourceID)
			require.NoError(t, err)
			provider.Spec.PinnedVersion.ExactVersion = ptr.To(semver.MustParse("4.17.1"))
			provider.Status.ActualHostedCluster = hostedClusterVersionHistory(
				hsv1beta1.ControlPlaneUpdateHistory{Version: "4.17.10", State: configv1.PartialUpdate},
				hsv1beta1.ControlPlaneUpdateHistory{Version: "4.17.2", State: configv1.CompletedUpdate},
			)
			storage := db.ServiceProviderClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName, resourceID.Name)
			before, err := storage.Replace(ctx, provider, nil)
			require.NoError(t, err)

			recorder := httptest.NewRecorder()
			err = NewHCPVersionPinHandler(db).ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/controlplaneversionpin", strings.NewReader(tt.body)).WithContext(ctx))
			var cloudErr *coreapi.CloudError
			require.ErrorAs(t, err, &cloudErr)
			require.Equal(t, http.StatusConflict, cloudErr.StatusCode)
			require.ErrorContains(t, err, "is being deleted")

			after, err := storage.Get(ctx, coreapi.ServiceProviderClusterResourceName)
			require.NoError(t, err)
			require.Equal(t, before, after, "a deleting cluster's provider document must not change")
		})
	}
}

func TestVersionPinHandler_PreservesOtherFields(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	mockResourcesDBClient := corecosmosstoragetesting.NewMockResourcesDBClient()

	resourceID, err := azcorearm.ParseResourceID(coreapitesting.TestClusterResourceID)
	require.NoError(t, err)

	seedHCPCluster(ctx, t, mockResourcesDBClient, resourceID, "4.17", "")

	existing, err := corecosmosstorage.GetOrCreateServiceProviderCluster(ctx, mockResourcesDBClient, resourceID)
	require.NoError(t, err)
	mgmtResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + coreapitesting.TestSubscriptionID + "/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/mc"))
	existing.Status.ManagementClusterResourceID = mgmtResourceID
	sizeStr := "Large"
	existing.Spec.DesiredHostedClusterControlPlaneSize = &sizeStr
	existing.Status.ActualHostedCluster = hostedClusterVersionHistory(
		hsv1beta1.ControlPlaneUpdateHistory{Version: "4.17.10", State: configv1.CompletedUpdate},
		hsv1beta1.ControlPlaneUpdateHistory{Version: "4.17.2", State: configv1.CompletedUpdate},
	)
	_, err = mockResourcesDBClient.ServiceProviderClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName, resourceID.Name).Replace(ctx, existing, nil)
	require.NoError(t, err)

	handler := NewHCPVersionPinHandler(mockResourcesDBClient)
	ctx = utils.ContextWithResourceID(ctx, resourceID)

	body := bytes.NewBufferString(`{"exactVersion":"4.17.2"}`)
	req := httptest.NewRequest(http.MethodPost, "/controlplaneversionpin", body)
	req = req.WithContext(ctx)
	recorder := httptest.NewRecorder()

	require.NoError(t, handler.ServeHTTP(recorder, req))

	spc, err := mockResourcesDBClient.ServiceProviderClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName, resourceID.Name).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	require.Equal(t, existing.Status.ActualHostedCluster, spc.Status.ActualHostedCluster, "version pin must preserve observed HostedCluster state")
	if spc.Status.ManagementClusterResourceID == nil || spc.Status.ManagementClusterResourceID.String() != mgmtResourceID.String() {
		t.Errorf("expected ManagementClusterResourceID preserved, got %v", spc.Status.ManagementClusterResourceID)
	}
	if spc.Spec.DesiredHostedClusterControlPlaneSize == nil || *spc.Spec.DesiredHostedClusterControlPlaneSize != "Large" {
		t.Errorf("expected DesiredHostedClusterControlPlaneSize preserved, got %v", spc.Spec.DesiredHostedClusterControlPlaneSize)
	}
	if spc.Spec.PinnedVersion.ExactVersion == nil || spc.Spec.PinnedVersion.ExactVersion.String() != "4.17.2" {
		t.Errorf("expected PinnedVersion.ExactVersion 4.17.2, got %v", spc.Spec.PinnedVersion.ExactVersion)
	}
}

func hostedClusterVersionHistory(entries ...hsv1beta1.ControlPlaneUpdateHistory) *hsv1beta1.HostedCluster {
	return &hsv1beta1.HostedCluster{
		Status: hsv1beta1.HostedClusterStatus{
			ControlPlaneVersion: hsv1beta1.ControlPlaneVersionStatus{History: entries},
		},
	}
}

// versionPinConcurrentUpdateClient returns a stale provider snapshot while a
// second writer updates the real mock storage, exercising its ETag enforcement.
type versionPinConcurrentUpdateClient struct {
	corecosmosstorage.ResourcesDBClient
	providerCRUD *versionPinConcurrentUpdateCRUD
}

func (c *versionPinConcurrentUpdateClient) ServiceProviderClusters(_, _, _ string) cosmosstorageutils.ResourceCRUD[coreapi.ServiceProviderCluster, *coreapi.ServiceProviderCluster] {
	return c.providerCRUD
}

type versionPinConcurrentUpdateCRUD struct {
	cosmosstorageutils.ResourceCRUD[coreapi.ServiceProviderCluster, *coreapi.ServiceProviderCluster]
	concurrentUpdate *coreapi.ServiceProviderCluster
	reads            int
}

func (c *versionPinConcurrentUpdateCRUD) Get(ctx context.Context, resourceName string) (*coreapi.ServiceProviderCluster, error) {
	existing, err := c.ResourceCRUD.Get(ctx, resourceName)
	if err != nil {
		return nil, err
	}
	c.reads++
	updated := existing.DeepCopy()
	updated.Spec.PinnedVersion = coreapi.ServiceProviderClusterPinnedVersion{
		ExactVersion: ptr.To(semver.MustParse("4.17.3")), UntilExactVersion: ptr.To(semver.MustParse("4.17.30")),
	}
	updated.Spec.DesiredHostedClusterControlPlaneSize = ptr.To("Large")
	c.concurrentUpdate, err = c.Replace(ctx, updated, nil)
	if err != nil {
		return nil, err
	}
	return existing, nil
}

func TestVersionPinHandler_ETagConflict(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "set pin", body: `{"exactVersion":"4.17.2"}`},
		{name: "clear pin", body: `{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			resourceID := metadataapi.Must(azcorearm.ParseResourceID(coreapitesting.TestClusterResourceID))
			ctx = utils.ContextWithResourceID(ctx, resourceID)
			db := corecosmosstoragetesting.NewMockResourcesDBClient()
			seedHCPCluster(ctx, t, db, resourceID, "4.17", "")
			storage := db.ServiceProviderClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName, resourceID.Name)
			provider, err := corecosmosstorage.GetOrCreateServiceProviderCluster(ctx, db, resourceID)
			require.NoError(t, err)
			provider.Spec.PinnedVersion.ExactVersion = ptr.To(semver.MustParse("4.17.1"))
			provider.Status.ActualHostedCluster = hostedClusterVersionHistory(
				hsv1beta1.ControlPlaneUpdateHistory{Version: "4.17.10", State: configv1.CompletedUpdate},
				hsv1beta1.ControlPlaneUpdateHistory{Version: "4.17.2", State: configv1.CompletedUpdate},
			)
			before, err := storage.Replace(ctx, provider, nil)
			require.NoError(t, err)
			interceptor := &versionPinConcurrentUpdateCRUD{ResourceCRUD: storage}
			client := &versionPinConcurrentUpdateClient{ResourcesDBClient: db, providerCRUD: interceptor}

			recorder := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/controlplaneversionpin", strings.NewReader(tc.body)).WithContext(ctx)
			errorutils.ReportError(NewHCPVersionPinHandler(client).ServeHTTP).ServeHTTP(recorder, req)

			after, err := storage.Get(ctx, coreapi.ServiceProviderClusterResourceName)
			require.NoError(t, err)
			require.NotNil(t, interceptor.concurrentUpdate, "concurrent update must have been persisted")
			require.NotEqual(t, before.CosmosETag, after.CosmosETag, "concurrent update must change the stored ETag")
			require.Equal(t, interceptor.concurrentUpdate, after, "conflicting request must preserve the concurrent writer's entire document")
			require.Equal(t, 1, interceptor.reads, "handler must return the conflict to the caller rather than retrying against changed history")
			require.Equal(t, http.StatusConflict, recorder.Code, "ETag conflict must return HTTP 409")
			var response coreapi.CloudError
			require.NoError(t, json.NewDecoder(recorder.Body).Decode(&response))
			require.Equal(t, coreapi.CloudErrorCodeConflict, response.Code)
			require.Contains(t, response.Message, "retry")
		})
	}
}

func TestVersionPinHandler_RejectsNonObjectAndTrailingJSON(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "top-level null", body: `null`},
		{name: "top-level array", body: `[]`},
		{name: "top-level string", body: `"clear"`},
		{name: "top-level number", body: `42`},
		{name: "top-level boolean", body: `true`},
		{name: "second object after clear", body: `{}{"unexpected":true}`},
		{name: "second object after set", body: `{"exactVersion":"4.17.2"}{"unexpected":true}`},
		{name: "invalid trailing data", body: `{} trailing`},
		{name: "trailing null", body: `{} null`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			resourceID := metadataapi.Must(azcorearm.ParseResourceID(coreapitesting.TestClusterResourceID))
			ctx = utils.ContextWithResourceID(ctx, resourceID)
			db := corecosmosstoragetesting.NewMockResourcesDBClient()
			seedHCPCluster(ctx, t, db, resourceID, "4.17", "")
			storage := db.ServiceProviderClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName, resourceID.Name)
			provider, err := corecosmosstorage.GetOrCreateServiceProviderCluster(ctx, db, resourceID)
			require.NoError(t, err)
			provider.Spec.PinnedVersion = coreapi.ServiceProviderClusterPinnedVersion{
				ExactVersion:      ptr.To(semver.MustParse("4.17.1")),
				UntilExactVersion: ptr.To(semver.MustParse("4.17.20")),
			}
			provider.Status.ActualHostedCluster = hostedClusterVersionHistory(
				hsv1beta1.ControlPlaneUpdateHistory{Version: "4.17.10", State: configv1.CompletedUpdate},
				hsv1beta1.ControlPlaneUpdateHistory{Version: "4.17.2", State: configv1.CompletedUpdate},
			)
			before, err := storage.Replace(ctx, provider, nil)
			require.NoError(t, err)

			recorder := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/controlplaneversionpin", strings.NewReader(tt.body)).WithContext(ctx)
			errorutils.ReportError(NewHCPVersionPinHandler(db).ServeHTTP).ServeHTTP(recorder, req)

			after, err := storage.Get(ctx, coreapi.ServiceProviderClusterResourceName)
			require.NoError(t, err)
			pinJSON, err := json.Marshal(after.Spec.PinnedVersion)
			require.NoError(t, err)
			t.Logf("body=%s: HTTP %d, persisted pin=%s", tt.body, recorder.Code, pinJSON)
			if recorder.Code != http.StatusBadRequest {
				t.Errorf("malformed version-pin request must return HTTP 400, got %d", recorder.Code)
			}
			if !reflect.DeepEqual(before, after) {
				t.Error("malformed version-pin request modified the provider document")
			}
		})
	}
}

func TestVersionPinHandler_ValidatesTargetAgainstHistory(t *testing.T) {
	completed := func(version string) hsv1beta1.ControlPlaneUpdateHistory {
		return hsv1beta1.ControlPlaneUpdateHistory{Version: version, State: configv1.CompletedUpdate}
	}
	partial := func(version string) hsv1beta1.ControlPlaneUpdateHistory {
		return hsv1beta1.ControlPlaneUpdateHistory{Version: version, State: configv1.PartialUpdate}
	}
	tests := []struct {
		name               string
		target             string
		seedClusterVersion string
		seedChannelGroup   string
		desiredVersion     string
		hostedCluster      *hsv1beta1.HostedCluster
		missingSPC         bool
		expectedStatusCode int
		expectedError      string
	}{
		{
			name:          "rollback after completed upgrade",
			target:        "4.17.2",
			hostedCluster: hostedClusterVersionHistory(completed("4.17.10"), completed("4.17.2"), completed("4.17.0")),
		},
		{
			name:          "rollback during partial upgrade",
			target:        "4.17.2",
			hostedCluster: hostedClusterVersionHistory(partial("4.17.10"), completed("4.17.2")),
		},
		{
			name:               "rollback between nightlies months apart",
			target:             "4.21.0-0.nightly-2026-10-01-125400",
			seedClusterVersion: "4.21",
			seedChannelGroup:   "nightly",
			hostedCluster: hostedClusterVersionHistory(
				completed("4.21.0-0.nightly-2026-12-03-180000"),
				completed("4.21.0-0.nightly-2026-10-01-125400"),
			),
		},
		{
			name:          "arbitrary older z-stream rejected",
			target:        "4.17.5",
			hostedCluster: hostedClusterVersionHistory(completed("4.17.10"), completed("4.17.2")),
			expectedError: "must be the immediately previous successfully installed control-plane version",
		},
		{
			name:          "older installed version rejected",
			target:        "4.17.0",
			hostedCluster: hostedClusterVersionHistory(completed("4.17.10"), completed("4.17.2"), completed("4.17.0")),
			expectedError: "must be the immediately previous successfully installed control-plane version",
		},
		{
			name:          "current completed version can be held",
			target:        "4.17.10",
			hostedCluster: hostedClusterVersionHistory(completed("4.17.10"), completed("4.17.2")),
		},
		{
			name:          "current version can be held without a previous entry",
			target:        "4.17.10",
			hostedCluster: hostedClusterVersionHistory(completed("4.17.10")),
		},
		{
			name:          "forward version can be pinned",
			target:        "4.17.11",
			hostedCluster: hostedClusterVersionHistory(completed("4.17.10"), completed("4.17.2")),
		},
		{
			name:          "current partial version can be held",
			target:        "4.17.10",
			hostedCluster: hostedClusterVersionHistory(partial("4.17.10"), completed("4.17.2")),
		},
		{
			name:           "pin ahead of observed history during upgrade",
			target:         "4.17.5",
			desiredVersion: "4.17.10",
			hostedCluster:  hostedClusterVersionHistory(completed("4.17.2")),
		},
		{
			name:          "partial versions are not installed rollback targets",
			target:        "4.17.5",
			hostedCluster: hostedClusterVersionHistory(partial("4.17.10"), partial("4.17.5"), completed("4.17.2")),
			expectedError: "must be the immediately previous successfully installed control-plane version",
		},
		{
			name:          "last completed version after multiple partial attempts",
			target:        "4.17.2",
			hostedCluster: hostedClusterVersionHistory(partial("4.17.10"), partial("4.17.5"), completed("4.17.2")),
		},
		{
			name:          "duplicate latest versions skipped",
			target:        "4.17.2",
			hostedCluster: hostedClusterVersionHistory(completed("4.17.10"), completed("4.17.10"), completed("4.17.2")),
		},
		{
			name:          "single version history rejected",
			target:        "4.17.2",
			hostedCluster: hostedClusterVersionHistory(completed("4.17.10")),
			expectedError: "no previous successfully installed control-plane version",
		},
		{
			name:          "no completed previous version rejected",
			target:        "4.17.2",
			hostedCluster: hostedClusterVersionHistory(partial("4.17.10"), partial("4.17.2")),
			expectedError: "no previous successfully installed control-plane version",
		},
		{
			name:          "empty history permits pin",
			target:        "4.17.2",
			hostedCluster: hostedClusterVersionHistory(),
		},
		{
			name:   "missing HostedCluster permits pin",
			target: "4.17.2",
		},
		{
			name:               "missing provider document rejected without creating it",
			target:             "4.17.2",
			missingSPC:         true,
			expectedStatusCode: http.StatusConflict,
			expectedError:      "ServiceProviderCluster for HCP cluster",
		},
		{
			name:          "malformed latest version rejected",
			target:        "4.17.2",
			hostedCluster: hostedClusterVersionHistory(partial("invalid"), completed("4.17.2")),
			expectedError: "latest control-plane history version",
		},
		{
			name:          "latest version with build metadata cannot validate a hold",
			target:        "4.17.10",
			hostedCluster: hostedClusterVersionHistory(completed("4.17.10+build.1"), completed("4.17.2")),
			expectedError: "latest control-plane history version \"4.17.10+build.1\" contains build metadata",
		},
		{
			name:          "malformed previous version must not fall back to older entry",
			target:        "4.17.2",
			hostedCluster: hostedClusterVersionHistory(completed("4.17.10"), completed("invalid"), completed("4.17.2")),
			expectedError: "previous control-plane history version",
		},
		{
			name:          "previous completed version with build metadata cannot validate a rollback",
			target:        "4.17.2",
			hostedCluster: hostedClusterVersionHistory(completed("4.17.10"), completed("4.17.2+build.1")),
			expectedError: "previous control-plane history version \"4.17.2+build.1\" contains build metadata",
		},
		{
			name:          "duplicate latest completed version with build metadata cannot be skipped",
			target:        "4.17.2",
			hostedCluster: hostedClusterVersionHistory(completed("4.17.10"), completed("4.17.10+build.1"), completed("4.17.2")),
			expectedError: "previous control-plane history version \"4.17.10+build.1\" contains build metadata",
		},
		{
			name:          "previous minor rejected even when present in history",
			target:        "4.17.2",
			hostedCluster: hostedClusterVersionHistory(completed("4.17.10"), completed("4.16.2")),
			expectedError: "must be the immediately previous successfully installed control-plane version",
		},
		{
			name:          "completed rollback does not prevent a forward pin",
			target:        "4.17.10",
			hostedCluster: hostedClusterVersionHistory(completed("4.17.2"), completed("4.17.10"), completed("4.17.0")),
		},
		{
			name:   "guest version history does not classify control-plane pin",
			target: "4.17.2",
			hostedCluster: &hsv1beta1.HostedCluster{Status: hsv1beta1.HostedClusterStatus{
				Version: &hsv1beta1.ClusterVersionStatus{History: []configv1.UpdateHistory{
					{Version: "4.17.10", State: configv1.CompletedUpdate},
				}},
			}},
		},
		{
			name: "clear pin without observed history",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			ctx = utils.ContextWithResourceID(ctx, metadataapi.Must(azcorearm.ParseResourceID(coreapitesting.TestClusterResourceID)))
			resourceID, err := utils.ResourceIDFromContext(ctx)
			require.NoError(t, err)
			db := corecosmosstoragetesting.NewMockResourcesDBClient()
			seedClusterVersion := tt.seedClusterVersion
			if seedClusterVersion == "" {
				seedClusterVersion = "4.17"
			}
			seedHCPCluster(ctx, t, db, resourceID, seedClusterVersion, tt.seedChannelGroup)
			storage := db.ServiceProviderClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName, resourceID.Name)
			var before *coreapi.ServiceProviderCluster
			if !tt.missingSPC {
				provider, err := corecosmosstorage.GetOrCreateServiceProviderCluster(ctx, db, resourceID)
				require.NoError(t, err)
				provider.Status.ActualHostedCluster = tt.hostedCluster
				if tt.desiredVersion != "" {
					provider.Spec.ControlPlaneVersion.DesiredVersion = ptr.To(semver.MustParse(tt.desiredVersion))
				}
				// This deliberately disagrees with the HostedCluster history: the
				// distilled active versions must never authorize a rollback target.
				provider.Status.ControlPlaneVersion.ActiveVersions = []coreapi.ServiceProviderClusterActiveVersion{
					{Version: ptr.To(semver.MustParse("4.17.10")), State: configv1.CompletedUpdate},
					{Version: ptr.To(semver.MustParse("4.17.5")), State: configv1.CompletedUpdate},
				}
				provider.Spec.PinnedVersion = coreapi.ServiceProviderClusterPinnedVersion{
					ExactVersion: ptr.To(semver.MustParse("4.17.1")), UntilExactVersion: ptr.To(semver.MustParse("4.17.20")),
				}
				before, err = storage.Replace(ctx, provider, nil)
				require.NoError(t, err)
			}
			body := `{}`
			if tt.target != "" {
				body = fmt.Sprintf(`{"exactVersion":%q}`, tt.target)
			}
			recorder := httptest.NewRecorder()
			err = NewHCPVersionPinHandler(db).ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/controlplaneversionpin", strings.NewReader(body)).WithContext(ctx))
			if tt.expectedError != "" {
				var cloudErr *coreapi.CloudError
				require.ErrorAs(t, err, &cloudErr)
				expectedStatusCode := tt.expectedStatusCode
				if expectedStatusCode == 0 {
					expectedStatusCode = http.StatusBadRequest
				}
				require.Equal(t, expectedStatusCode, cloudErr.StatusCode)
				require.ErrorContains(t, err, tt.expectedError)
				after, readErr := storage.Get(ctx, coreapi.ServiceProviderClusterResourceName)
				if tt.missingSPC {
					require.True(t, cosmosstorageutils.IsNotFoundError(readErr), "rejected pin must not create a provider document")
				} else {
					require.NoError(t, readErr)
					require.Equal(t, before, after, "rejected pin must not modify the provider document")
				}
				return
			}
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, recorder.Code)
			after, err := storage.Get(ctx, coreapi.ServiceProviderClusterResourceName)
			require.NoError(t, err)
			require.Equal(t, before.Status, after.Status, "pin changes must preserve observed state")
			if tt.target == "" {
				require.Empty(t, after.Spec.PinnedVersion)
			} else {
				require.NotNil(t, after.Spec.PinnedVersion.ExactVersion)
				require.Equal(t, tt.target, after.Spec.PinnedVersion.ExactVersion.String())
				require.Nil(t, after.Spec.PinnedVersion.UntilExactVersion)
			}
		})
	}
}
