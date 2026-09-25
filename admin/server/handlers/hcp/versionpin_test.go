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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/blang/semver/v4"
	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/require"

	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apitesting/coreapitesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
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
	tests := []struct {
		name               string
		body               string
		skipResourceID     bool
		seedClusterVersion string // version ID for the seeded HCP cluster (e.g. "4.17"); empty skips seeding
		seedChannelGroup   string // channel group for the seeded cluster; empty defaults to "stable"
		existingPin        *coreapi.ServiceProviderClusterPinnedVersion
		expectedStatusCode int
		expectedError      string
		expectPinCleared   bool
		expectedExact      string
		expectedUntil      string
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
			name:               "zero version rejected",
			body:               `{"exactVersion":"0.0.0"}`,
			expectedStatusCode: http.StatusBadRequest,
			expectedError:      "exactVersion must be a non-zero version",
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
			name:               "nightly cluster with untilExactVersion rejected",
			body:               `{"exactVersion":"4.17.2","untilExactVersion":"4.17.4"}`,
			seedClusterVersion: "4.17",
			seedChannelGroup:   "nightly",
			expectedStatusCode: http.StatusBadRequest,
			expectedError:      "untilExactVersion is not supported for nightly clusters",
		},
		{
			name:               "nightly cluster pin without untilExactVersion allowed",
			body:               `{"exactVersion":"4.17.2"}`,
			seedClusterVersion: "4.17",
			seedChannelGroup:   "nightly",
			expectedStatusCode: http.StatusOK,
			expectedExact:      "4.17.2",
		},
		{
			name:               "set pin with exact version only",
			body:               `{"exactVersion":"4.17.2"}`,
			seedClusterVersion: "4.17",
			expectedStatusCode: http.StatusOK,
			expectedExact:      "4.17.2",
		},
		{
			name:               "set pin with exact and until version",
			body:               `{"exactVersion":"4.17.2","untilExactVersion":"4.17.4"}`,
			seedClusterVersion: "4.17",
			expectedStatusCode: http.StatusOK,
			expectedExact:      "4.17.2",
			expectedUntil:      "4.17.4",
		},
		{
			name:               "tolerant semver parsing (no patch)",
			body:               `{"exactVersion":"4.17"}`,
			seedClusterVersion: "4.17",
			expectedStatusCode: http.StatusOK,
			expectedExact:      "4.17.0",
		},
		{
			name:               "clear existing pin",
			body:               `{}`,
			seedClusterVersion: "4.17",
			existingPin: &coreapi.ServiceProviderClusterPinnedVersion{
				ExactVersion: ptr.To(semver.MustParse("4.17.2")),
			},
			expectedStatusCode: http.StatusOK,
			expectPinCleared:   true,
		},
		{
			name:               "overwrite existing pin",
			body:               `{"exactVersion":"4.17.3"}`,
			seedClusterVersion: "4.17",
			existingPin: &coreapi.ServiceProviderClusterPinnedVersion{
				ExactVersion: ptr.To(semver.MustParse("4.17.2")),
			},
			expectedStatusCode: http.StatusOK,
			expectedExact:      "4.17.3",
		},
		{
			name:               "clear pin on fresh SPC is a no-op success",
			body:               `{}`,
			seedClusterVersion: "4.17",
			expectedStatusCode: http.StatusOK,
			expectPinCleared:   true,
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

			if tt.existingPin != nil {
				existing, err := corecosmosstorage.GetOrCreateServiceProviderCluster(ctx, mockResourcesDBClient, resourceID)
				require.NoError(t, err)
				existing.Spec.PinnedVersion = *tt.existingPin
				_, err = mockResourcesDBClient.ServiceProviderClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName, resourceID.Name).Replace(ctx, existing, nil)
				require.NoError(t, err)
			}

			handler := NewHCPVersionPinHandler(mockResourcesDBClient)

			if !tt.skipResourceID {
				ctx = utils.ContextWithResourceID(ctx, resourceID)
			}

			req := httptest.NewRequest(http.MethodPost, "/versionpin", strings.NewReader(tt.body))
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
				return
			}

			if err != nil {
				t.Fatalf("expected no error but got %v", err)
			}

			spc, err := mockResourcesDBClient.ServiceProviderClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName, resourceID.Name).Get(ctx, coreapi.ServiceProviderClusterResourceName)
			require.NoError(t, err)

			var respBody versionPinResponse
			require.NoError(t, json.NewDecoder(recorder.Body).Decode(&respBody))

			if tt.expectPinCleared {
				if spc.Spec.PinnedVersion.ExactVersion != nil {
					t.Errorf("expected PinnedVersion.ExactVersion cleared, got %s", spc.Spec.PinnedVersion.ExactVersion)
				}
				if respBody.ExactVersion != nil {
					t.Errorf("expected response exactVersion nil, got %q", *respBody.ExactVersion)
				}
				return
			}

			if spc.Spec.PinnedVersion.ExactVersion == nil {
				t.Fatalf("expected PinnedVersion.ExactVersion to be set, got nil")
			}
			if spc.Spec.PinnedVersion.ExactVersion.String() != tt.expectedExact {
				t.Errorf("expected exactVersion %q, got %q", tt.expectedExact, spc.Spec.PinnedVersion.ExactVersion.String())
			}
			if respBody.ExactVersion == nil || *respBody.ExactVersion != tt.expectedExact {
				t.Errorf("expected response exactVersion %q, got %v", tt.expectedExact, respBody.ExactVersion)
			}

			if tt.expectedUntil != "" {
				if spc.Spec.PinnedVersion.UntilExactVersion == nil {
					t.Fatalf("expected PinnedVersion.UntilExactVersion to be set, got nil")
				}
				if spc.Spec.PinnedVersion.UntilExactVersion.String() != tt.expectedUntil {
					t.Errorf("expected untilExactVersion %q, got %q", tt.expectedUntil, spc.Spec.PinnedVersion.UntilExactVersion.String())
				}
				if respBody.UntilExactVersion == nil || *respBody.UntilExactVersion != tt.expectedUntil {
					t.Errorf("expected response untilExactVersion %q, got %v", tt.expectedUntil, respBody.UntilExactVersion)
				}
			} else if spc.Spec.PinnedVersion.UntilExactVersion != nil {
				t.Errorf("expected PinnedVersion.UntilExactVersion nil, got %s", spc.Spec.PinnedVersion.UntilExactVersion)
			}
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
	_, err = mockResourcesDBClient.ServiceProviderClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName, resourceID.Name).Replace(ctx, existing, nil)
	require.NoError(t, err)

	handler := NewHCPVersionPinHandler(mockResourcesDBClient)
	ctx = utils.ContextWithResourceID(ctx, resourceID)

	body := bytes.NewBufferString(`{"exactVersion":"4.17.2"}`)
	req := httptest.NewRequest(http.MethodPost, "/versionpin", body)
	req = req.WithContext(ctx)
	recorder := httptest.NewRecorder()

	require.NoError(t, handler.ServeHTTP(recorder, req))

	spc, err := mockResourcesDBClient.ServiceProviderClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName, resourceID.Name).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
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
