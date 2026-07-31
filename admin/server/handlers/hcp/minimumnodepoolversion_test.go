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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/blang/semver/v4"
	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

var (
	testClusterResourceID  = api.Must(azcorearm.ParseResourceID(api.TestClusterResourceID))
	testNodePoolResourceID = api.Must(azcorearm.ParseResourceID(api.TestNodePoolResourceID))
)

// defaultSetup returns a setup func for the happy path: cluster resource ID in
// context, nodepoolName path value set, and the given JSON body on the request.
// For error cases that need to omit one of these, provide a custom setup inline.
func defaultSetup(body string) func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) (context.Context, *http.Request) {
	return func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) (context.Context, *http.Request) {
		t.Helper()
		ctx = utils.ContextWithResourceID(ctx, testClusterResourceID)
		req := httptest.NewRequest(http.MethodPost, "/minimumversion", strings.NewReader(body))
		req.SetPathValue("nodepoolName", api.TestNodePoolName)
		return ctx, req
	}
}

// seedMinVersion pre-populates a ServiceProviderNodePool with the given
// MinimumVersion so tests can verify overwrite and clear behaviour.
func seedMinVersion(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, version string) {
	t.Helper()
	existing, err := database.GetOrCreateServiceProviderNodePool(ctx, db, testNodePoolResourceID)
	require.NoError(t, err)
	v := semver.MustParse(version)
	existing.Spec.NodePoolVersion.MinimumVersion = &v
	_, err = db.ServiceProviderNodePools(testNodePoolResourceID.SubscriptionID, testNodePoolResourceID.ResourceGroupName, testNodePoolResourceID.Parent.Name, testNodePoolResourceID.Name).Replace(ctx, existing, nil)
	require.NoError(t, err)
}

// expectCloudError returns a validate func that asserts the handler returned
// an arm.CloudError with the given status code and error substring.
func expectCloudError(expectedStatusCode int, expectedError string) func(t *testing.T, err error, recorder *httptest.ResponseRecorder, db *databasetesting.MockResourcesDBClient) {
	return func(t *testing.T, err error, recorder *httptest.ResponseRecorder, db *databasetesting.MockResourcesDBClient) {
		t.Helper()
		require.Error(t, err)
		var cloudErr *arm.CloudError
		require.True(t, errors.As(err, &cloudErr), "expected CloudError but got %T: %v", err, err)
		require.Equal(t, expectedStatusCode, cloudErr.StatusCode)
		require.Contains(t, err.Error(), expectedError)
	}
}

// expectVersion returns a validate func that asserts a successful handler call
// and checks both the Cosmos document and the HTTP response body match the
// expected version. Pass nil to assert the version was cleared.
func expectVersion(wantVersion *semver.Version) func(t *testing.T, err error, recorder *httptest.ResponseRecorder, db *databasetesting.MockResourcesDBClient) {
	return func(t *testing.T, err error, recorder *httptest.ResponseRecorder, db *databasetesting.MockResourcesDBClient) {
		t.Helper()
		require.NoError(t, err)

		spnp, err := db.ServiceProviderNodePools(testNodePoolResourceID.SubscriptionID, testNodePoolResourceID.ResourceGroupName, testNodePoolResourceID.Parent.Name, testNodePoolResourceID.Name).Get(context.Background(), api.ServiceProviderNodePoolResourceName)
		require.NoError(t, err)

		var respBody minimumNodePoolVersionRequest
		require.NoError(t, json.NewDecoder(recorder.Body).Decode(&respBody))

		if wantVersion == nil {
			require.Nil(t, spnp.Spec.NodePoolVersion.MinimumVersion, "expected MinimumVersion cleared")
			require.Nil(t, respBody.MinimumVersion, "expected response minimumVersion nil")
		} else {
			require.NotNil(t, spnp.Spec.NodePoolVersion.MinimumVersion, "expected MinimumVersion to be set")
			require.True(t, spnp.Spec.NodePoolVersion.MinimumVersion.EQ(*wantVersion), "expected version %s, got %s", wantVersion, spnp.Spec.NodePoolVersion.MinimumVersion)
			require.NotNil(t, respBody.MinimumVersion, "expected response version set")
			require.Equal(t, wantVersion.String(), *respBody.MinimumVersion)
		}
	}
}

func TestMinimumNodePoolVersionHandler(t *testing.T) {
	// Each test case defines two callbacks:
	//   setup    — configures the context, request, and DB state; use defaultSetup
	//              for the happy path, or inline a func to omit context/path values.
	//   validate — asserts the handler result; use expectCloudError for error cases
	//              or expectVersion for success cases (nil = version cleared).
	tests := []struct {
		name     string
		setup    func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) (context.Context, *http.Request)
		validate func(t *testing.T, err error, recorder *httptest.ResponseRecorder, db *databasetesting.MockResourcesDBClient)
	}{
		{
			name: "missing resource ID",
			setup: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) (context.Context, *http.Request) {
				t.Helper()
				req := httptest.NewRequest(http.MethodPost, "/minimumversion", strings.NewReader(`{"minimumVersion":"4.17.0"}`))
				req.SetPathValue("nodepoolName", api.TestNodePoolName)
				return ctx, req
			},
			validate: expectCloudError(http.StatusBadRequest, "invalid resource identifier in request"),
		},
		{
			name: "missing nodepoolName path value",
			setup: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) (context.Context, *http.Request) {
				t.Helper()
				ctx = utils.ContextWithResourceID(ctx, testClusterResourceID)
				req := httptest.NewRequest(http.MethodPost, "/minimumversion", strings.NewReader(`{"minimumVersion":"4.17.0"}`))
				return ctx, req
			},
			validate: expectCloudError(http.StatusBadRequest, "nodepoolName parameter is required"),
		},
		{
			name:     "invalid JSON body",
			setup:    defaultSetup(`{not json`),
			validate: expectCloudError(http.StatusBadRequest, "invalid JSON body"),
		},
		{
			name:     "empty string minimumVersion rejected",
			setup:    defaultSetup(`{"minimumVersion":""}`),
			validate: expectCloudError(http.StatusBadRequest, "minimumVersion must not be empty"),
		},
		{
			name:     "invalid semver value",
			setup:    defaultSetup(`{"minimumVersion":"not-semver"}`),
			validate: expectCloudError(http.StatusBadRequest, "invalid semver"),
		},
		{
			name:     "valid semver sets MinimumVersion",
			setup:    defaultSetup(`{"minimumVersion":"4.17.0"}`),
			validate: expectVersion(semverPtr("4.17.0")),
		},
		{
			name: "overwrites existing MinimumVersion",
			setup: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) (context.Context, *http.Request) {
				t.Helper()
				seedMinVersion(t, ctx, db, "4.17.0")
				return defaultSetup(`{"minimumVersion":"4.18.0"}`)(t, ctx, db)
			},
			validate: expectVersion(semverPtr("4.18.0")),
		},
		{
			name: "omitted minimumVersion clears previously-set value",
			setup: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) (context.Context, *http.Request) {
				t.Helper()
				seedMinVersion(t, ctx, db, "4.17.0")
				return defaultSetup(`{}`)(t, ctx, db)
			},
			validate: expectVersion(nil),
		},
		{
			name: "explicit null clears previously-set value",
			setup: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) (context.Context, *http.Request) {
				t.Helper()
				seedMinVersion(t, ctx, db, "4.17.0")
				return defaultSetup(`{"minimumVersion":null}`)(t, ctx, db)
			},
			validate: expectVersion(nil),
		},
		{
			name:     "omitted minimumVersion on fresh SPNP is a no-op success",
			setup:    defaultSetup(`{}`),
			validate: expectVersion(nil),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			db := databasetesting.NewMockResourcesDBClient()
			handler := NewHCPMinimumNodePoolVersionHandler(db)

			ctx, req := tt.setup(t, ctx, db)
			recorder := httptest.NewRecorder()
			err := handler.ServeHTTP(recorder, req.WithContext(ctx))
			tt.validate(t, err, recorder, db)
		})
	}
}

func TestMinimumNodePoolVersionHandler_PreservesOtherFields(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	db := databasetesting.NewMockResourcesDBClient()

	existing, err := database.GetOrCreateServiceProviderNodePool(ctx, db, testNodePoolResourceID)
	require.NoError(t, err)
	desiredVersion := semver.MustParse("4.16.0")
	existing.Spec.NodePoolVersion.DesiredVersion = &desiredVersion
	_, err = db.ServiceProviderNodePools(testNodePoolResourceID.SubscriptionID, testNodePoolResourceID.ResourceGroupName, testNodePoolResourceID.Parent.Name, testNodePoolResourceID.Name).Replace(ctx, existing, nil)
	require.NoError(t, err)

	handler := NewHCPMinimumNodePoolVersionHandler(db)
	ctx = utils.ContextWithResourceID(ctx, testClusterResourceID)

	req := httptest.NewRequest(http.MethodPost, "/minimumversion", strings.NewReader(`{"minimumVersion":"4.17.0"}`))
	req = req.WithContext(ctx)
	req.SetPathValue("nodepoolName", api.TestNodePoolName)
	recorder := httptest.NewRecorder()

	require.NoError(t, handler.ServeHTTP(recorder, req))

	spnp, err := db.ServiceProviderNodePools(testNodePoolResourceID.SubscriptionID, testNodePoolResourceID.ResourceGroupName, testNodePoolResourceID.Parent.Name, testNodePoolResourceID.Name).Get(ctx, api.ServiceProviderNodePoolResourceName)
	require.NoError(t, err)
	require.NotNil(t, spnp.Spec.NodePoolVersion.DesiredVersion)
	require.True(t, spnp.Spec.NodePoolVersion.DesiredVersion.EQ(desiredVersion), "expected DesiredVersion preserved as %s, got %s", desiredVersion, spnp.Spec.NodePoolVersion.DesiredVersion)
	require.NotNil(t, spnp.Spec.NodePoolVersion.MinimumVersion)
	require.True(t, spnp.Spec.NodePoolVersion.MinimumVersion.EQ(semver.MustParse("4.17.0")), "expected MinimumVersion 4.17.0, got %v", spnp.Spec.NodePoolVersion.MinimumVersion)
}

func semverPtr(s string) *semver.Version {
	v := semver.MustParse(s)
	return &v
}
