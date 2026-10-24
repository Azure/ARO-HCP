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

package operations

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	operationtesting "github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils/operationtesting"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/kubeapplierlistertesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// erroringReadDesireLister fails the HostedCluster probe. The embedded interface
// is left nil: GetForCluster is the only method buildDeletionTimeoutMessage reaches.
type erroringReadDesireLister struct {
	kubeapplierlisters.ReadDesireLister
	err error
}

func (l *erroringReadDesireLister) GetForCluster(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, name string,
) (*kubeapplierapi.ReadDesire, error) {
	return nil, l.err
}

// erroringUntypedCRUDDBClient fails the descendant-resources probe while leaving
// every other read served by the embedded mock client.
type erroringUntypedCRUDDBClient struct {
	corecosmosstorage.ResourcesDBClient
	err error
}

func (c *erroringUntypedCRUDDBClient) UntypedCRUD(parentResourceID azcorearm.ResourceID) (cosmosstorageutils.UntypedResourceCRUD, error) {
	return nil, c.err
}

func TestFormatDeletionTimeoutMessage(t *testing.T) {
	testCases := []struct {
		name   string
		detail string
		errs   []error
		want   string
	}{
		{
			name: "no detail and no errors returns the bare deadline message",
			want: "cluster deletion did not complete before the deadline",
		},
		{
			name:   "detail is appended after the deadline message",
			detail: "[hostedCluster] HostedCluster still exists",
			want:   "cluster deletion did not complete before the deadline; [hostedCluster] HostedCluster still exists",
		},
		{
			name: "probe errors are surfaced when there is no detail",
			errs: []error{errors.New("failed to get ClusterService status: boom"), errors.New("failed to list descendant resources: kaboom")},
			want: "cluster deletion did not complete before the deadline; diagnostic errors: failed to get ClusterService status: boom; failed to list descendant resources: kaboom",
		},
		{
			name:   "detail wins over probe errors",
			detail: "[descendantResources] remaining resources: 1 nodePools",
			errs:   []error{errors.New("failed to get cached HostedCluster: boom")},
			want:   "cluster deletion did not complete before the deadline; [descendantResources] remaining resources: 1 nodePools",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, formatDeletionTimeoutMessage(tc.detail, tc.errs))
		})
	}
}

func TestBuildDeletionTimeoutMessage(t *testing.T) {
	fixture := operationtesting.NewClusterTestFixture()
	probeErr := errors.New("cosmos unavailable")

	// clusterServiceDeletionStatus cannot return an error, so the realistic
	// no-detail case is one where ClusterService is already gone (making the two
	// ClusterService probes report Succeeded with an empty message) and the
	// remaining probes fail.
	clusterWithClusterServiceGone := func() *coreapi.HCPOpenShiftCluster {
		cluster := fixture.NewCluster(nil)
		cluster.ServiceProviderProperties.ClusterServiceID = nil
		return cluster
	}

	clusterStillDeletingInClusterService := func() *coreapi.HCPOpenShiftCluster {
		cluster := fixture.NewCluster(nil)
		cluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &metav1.Time{Time: operationtesting.MustParseTime("2025-01-20T10:00:00Z")}
		return cluster
	}

	testCases := []struct {
		name              string
		cluster           *coreapi.HCPOpenShiftCluster
		setupCSMock       func(ctrl *gomock.Controller, fixture *operationtesting.ClusterTestFixture) ocm.ClusterServiceClientSpec
		failDescendants   bool
		failHostedCluster bool
		wantContains      []string
		wantNotContains   []string
	}{
		{
			name:              "probe failures are reported when no probe produced a usable message",
			cluster:           clusterWithClusterServiceGone(),
			failDescendants:   true,
			failHostedCluster: true,
			wantContains: []string{
				"cluster deletion did not complete before the deadline; diagnostic errors: ",
				"failed to create untyped CRUD: cosmos unavailable",
				"failed to get cached HostedCluster: ",
			},
		},
		{
			name:    "stuck-resource detail wins and probe errors stay log-only",
			cluster: clusterStillDeletingInClusterService(),
			setupCSMock: func(ctrl *gomock.Controller, fixture *operationtesting.ClusterTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				mockCSClient.EXPECT().
					GetClusterStatus(gomock.Any(), fixture.ClusterInternalID).
					Return(nil, errors.New("cluster service unavailable"))
				return mockCSClient
			},
			failHostedCluster: true,
			wantContains: []string{
				"cluster deletion did not complete before the deadline; ",
				"[clusterServiceDeletion] ClusterService cluster ",
			},
			wantNotContains: []string{"diagnostic errors: "},
		},
		{
			name:            "no probe failures and no detail keeps the bare deadline message",
			cluster:         clusterWithClusterServiceGone(),
			wantContains:    []string{"cluster deletion did not complete before the deadline"},
			wantNotContains: []string{"diagnostic errors: ", ";"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockResourcesDBClient, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{tc.cluster})
			require.NoError(t, err)

			var resourcesDBClient corecosmosstorage.ResourcesDBClient = mockResourcesDBClient
			if tc.failDescendants {
				resourcesDBClient = &erroringUntypedCRUDDBClient{ResourcesDBClient: mockResourcesDBClient, err: probeErr}
			}

			var readDesireLister kubeapplierlisters.ReadDesireLister = &kubeapplierlistertesting.SliceReadDesireLister{}
			if tc.failHostedCluster {
				readDesireLister = &erroringReadDesireLister{err: probeErr}
			}

			var mockCSClient ocm.ClusterServiceClientSpec
			if tc.setupCSMock != nil {
				mockCSClient = tc.setupCSMock(ctrl, fixture)
			}

			controller := &operationClusterDelete{
				resourcesDBClient:    resourcesDBClient,
				readDesireLister:     readDesireLister,
				clusterServiceClient: mockCSClient,
			}

			message := controller.buildDeletionTimeoutMessage(ctx, nil, tc.cluster)

			assert.True(t, strings.HasPrefix(message, deletionDeadlinePrefix),
				"message must start with the deadline prefix, got %q", message)
			for _, want := range tc.wantContains {
				assert.Contains(t, message, want)
			}
			for _, notWant := range tc.wantNotContains {
				assert.NotContains(t, message, notWant)
			}
		})
	}
}
