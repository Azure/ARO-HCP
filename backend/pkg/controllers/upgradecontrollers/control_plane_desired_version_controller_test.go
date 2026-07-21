// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package upgradecontrollers

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/blang/semver/v4"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/backend/pkg/listertesting"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/cincinnati"
	"github.com/Azure/ARO-HCP/internal/cincinnati/testserver"
	"github.com/Azure/ARO-HCP/internal/database"
	dblisters "github.com/Azure/ARO-HCP/internal/database/listers"
	internallistertesting "github.com/Azure/ARO-HCP/internal/database/listertesting"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// testServerCincinnatiClient wraps a Cincinnati client and redirects all
// GetUpdates calls to the test server URI, ignoring the production URI that
// desiredControlPlaneZVersion constructs internally via GetCincinnatiURI.
type testServerCincinnatiClient struct {
	inner cincinnati.Client
	uri   *url.URL
}

func (c *testServerCincinnatiClient) GetUpdates(ctx context.Context, _ *url.URL, desiredArch, currentArch, channel string, version semver.Version) (configv1.Release, []configv1.Release, []configv1.ConditionalUpdate, error) {
	u := *c.uri // clone because the CVO client mutates it
	return c.inner.GetUpdates(ctx, &u, desiredArch, currentArch, channel, version)
}

// hostedClusterFromActiveVersions constructs a HostedCluster whose
// Status.ControlPlaneVersion.History matches the given active versions.
// Returns nil when activeVersions is empty (initial install).
func hostedClusterFromActiveVersions(activeVersions []api.HCPClusterActiveVersion) *v1beta1.HostedCluster {
	if len(activeVersions) == 0 {
		return nil
	}
	hc := &v1beta1.HostedCluster{}
	for _, av := range activeVersions {
		hc.Status.ControlPlaneVersion.History = append(hc.Status.ControlPlaneVersion.History,
			v1beta1.ControlPlaneUpdateHistory{
				Version: av.Version.String(),
				State:   av.State,
			})
	}
	return hc
}

// newTestServerClient creates a Cincinnati client backed by the given test
// server, suitable for passing to desiredControlPlaneZVersion.
func newTestServerClient(server *testserver.Server) *testServerCincinnatiClient {
	return &testServerCincinnatiClient{inner: server.NewClient(), uri: server.URI()}
}

func TestDesiredControlPlaneZVersion_ZStreamManagedUpgrade(t *testing.T) {
	tests := []struct {
		name                  string
		activeVersions        []api.HCPClusterActiveVersion
		customerDesiredMinor  string
		channelGroup          string
		channels              map[string]*testserver.Graph
		expectedVersion       *semver.Version
		expectedError         bool
		expectedErrorContains string
	}{
		{
			name:                 "Z-stream upgrade - finds latest gateway",
			activeVersions:       []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.19.15")), State: configv1.CompletedUpdate}},
			customerDesiredMinor: "4.19",
			channelGroup:         "stable",
			channels: map[string]*testserver.Graph{
				"stable-4.19": testserver.NewGraph().
					Edges("4.19.0", "4.19.15", "4.19.18", "4.19.22").
					Edges("4.19.15", "4.19.18", "4.19.22"),
				"stable-4.20": testserver.NewGraph().
					Versions("4.19.22", "4.20.0", "4.20.5").
					Edges("4.19.22", "4.20.5").
					Edges("4.20.0", "4.20.5"),
			},
			expectedVersion: ptr.To(semver.MustParse("4.19.22")),
			expectedError:   false,
		},
		{
			name:                 "Z-stream upgrade - already at latest",
			activeVersions:       []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.19.22")), State: configv1.CompletedUpdate}},
			customerDesiredMinor: "4.19",
			channelGroup:         "stable",
			channels: map[string]*testserver.Graph{
				"stable-4.19": testserver.NewGraph().Versions("4.19.22"),
			},
			expectedVersion: nil,
			expectedError:   false,
		},
		{
			name:                 "Z-stream upgrade - no candidate is a gateway, next minor reachable from other versions",
			activeVersions:       []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.19.15")), State: configv1.CompletedUpdate}},
			customerDesiredMinor: "4.19",
			channelGroup:         "stable",
			channels: map[string]*testserver.Graph{
				// 4.19.20 is the only candidate reachable from 4.19.15.
				// 4.19.22 IS a gateway to 4.20 but is NOT reachable from 4.19.15.
				"stable-4.19": testserver.NewGraph().
					Edges("4.19.0", "4.19.15", "4.19.20", "4.19.22").
					Edges("4.19.15", "4.19.20"),
				"stable-4.20": testserver.NewGraph().
					Versions("4.19.22", "4.20.0", "4.20.5").
					Edges("4.19.22", "4.20.5").
					Edges("4.20.0", "4.20.5"),
			},
			expectedVersion:       nil,
			expectedError:         true,
			expectedErrorContains: "no upgrade path",
		},
		{
			name:                 "Z-stream upgrade - multiple active versions, only common candidates considered",
			customerDesiredMinor: "4.19",
			channelGroup:         "stable",
			activeVersions: []api.HCPClusterActiveVersion{
				{Version: ptr.To(semver.MustParse("4.19.12")), State: configv1.CompletedUpdate}, // Most recent
				{Version: ptr.To(semver.MustParse("4.19.10")), State: configv1.CompletedUpdate}, // Older active version
			},
			channels: map[string]*testserver.Graph{
				// 4.19.22 is reachable from 4.19.12 but NOT from 4.19.10.
				// 4.19.15 and 4.19.18 are reachable from both.
				"stable-4.19": testserver.NewGraph().
					Edges("4.19.0", "4.19.10", "4.19.12", "4.19.15", "4.19.18", "4.19.22").
					Edges("4.19.12", "4.19.15", "4.19.18", "4.19.22").
					Edges("4.19.10", "4.19.15", "4.19.18"),
				"stable-4.20": testserver.NewGraph().
					Versions("4.19.18", "4.20.0", "4.20.5").
					Edges("4.19.18", "4.20.5").
					Edges("4.20.0", "4.20.5"),
			},
			expectedVersion: ptr.To(semver.MustParse("4.19.18")), // Latest common candidate that's a gateway
			expectedError:   false,
		},
		{
			name:                 "Z-stream upgrade - next minor does not exist, returns latest",
			activeVersions:       []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.19.10")), State: configv1.CompletedUpdate}},
			customerDesiredMinor: "4.19",
			channelGroup:         "stable",
			channels: map[string]*testserver.Graph{
				// No stable-4.20 channel at all.
				"stable-4.19": testserver.NewGraph().
					Edges("4.19.10", "4.19.18"),
			},
			expectedVersion: ptr.To(semver.MustParse("4.19.18")), // Safe to upgrade - no existing path to break
			expectedError:   false,
		},
		{
			name:                 "Z-stream upgrade - Cincinnati channel not found error",
			activeVersions:       []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.19.15")), State: configv1.CompletedUpdate}},
			customerDesiredMinor: "4.19",
			channelGroup:         "stable",
			// Empty channels: the stable-4.19 channel does not exist on the test server.
			channels:              map[string]*testserver.Graph{},
			expectedVersion:       nil,
			expectedError:         true,
			expectedErrorContains: "stable-4.19",
		},
		{
			name:                 "Z-stream upgrade - no desired minor version specified",
			activeVersions:       []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.19.15")), State: configv1.CompletedUpdate}},
			customerDesiredMinor: "",
			channelGroup:         "stable",
			channels:             map[string]*testserver.Graph{},
			expectedVersion:      nil,
			expectedError:        false,
		},
		{
			name:                 "Z-stream upgrade - no channel group specified",
			activeVersions:       []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.19.15")), State: configv1.CompletedUpdate}},
			customerDesiredMinor: "4.19",
			channelGroup:         "",
			channels:             map[string]*testserver.Graph{},
			expectedVersion:      nil,
			expectedError:        false,
		},
		{
			name:                 "Z-stream upgrade - candidate channel, customer desired full version (4.20.15) normalized to same minor",
			activeVersions:       []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.20.10")), State: configv1.CompletedUpdate}},
			customerDesiredMinor: "4.20.15",
			channelGroup:         "candidate",
			channels: map[string]*testserver.Graph{
				"candidate-4.20": testserver.NewGraph().
					Edges("4.20.0", "4.20.10", "4.20.12", "4.20.15").
					Edges("4.20.10", "4.20.12", "4.20.15"),
				"candidate-4.21": testserver.NewGraph().
					Versions("4.20.15", "4.21.0").
					Edges("4.20.15", "4.21.0").
					Edges("4.21.0"),
			},
			expectedVersion: ptr.To(semver.MustParse("4.20.15")),
			expectedError:   false,
		},
		{
			name:                 "Z-stream upgrade - nightly channel, customer desired full version (4.19.0-0.nightly-multi-...) normalized to same minor",
			activeVersions:       []api.HCPClusterActiveVersion{{Version: ptr.To(api.Must(semver.ParseTolerant("4.19.0-0.nightly-multi-2026-01-10-204154"))), State: configv1.CompletedUpdate}},
			customerDesiredMinor: "4.19.0-0.nightly-multi-2026-01-12-061259",
			channelGroup:         "nightly",
			channels: map[string]*testserver.Graph{
				// No nightly-4.20 channel — next minor does not exist.
				"nightly-4.19": testserver.NewGraph().
					Edges("4.19.0-0.nightly-multi-2026-01-10-204154", "4.19.0-0.nightly-multi-2026-01-12-061259"),
			},
			expectedVersion: ptr.To(api.Must(semver.ParseTolerant("4.19.0-0.nightly-multi-2026-01-12-061259"))),
			expectedError:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := testserver.NewServer(t, tt.channels)
			cincinnatiClient := newTestServerClient(server)

			now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
			syncer := &controlPlaneDesiredVersionSyncer{
				clock:             clocktesting.NewFakePassiveClock(now),
				resourcesDBClient: databasetesting.NewMockResourcesDBClient(),
			}

			hostedCluster := hostedClusterFromActiveVersions(tt.activeVersions)
			ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
			result, err := syncer.desiredControlPlaneZVersion(ctx, cincinnatiClient, api.Must(api.ToClusterResourceID("6b690bec-0c16-4ecb-8f67-781caf40bba7", "test-rg", "test-cluster")), tt.customerDesiredMinor, tt.channelGroup, tt.activeVersions, false, hostedCluster)

			assertVersionResult(t, result, err, tt.expectedVersion, tt.expectedError, tt.expectedErrorContains)
		})
	}
}

func TestDesiredControlPlaneZVersion_NextYStreamUpgrade(t *testing.T) {
	tests := []struct {
		name                  string
		activeVersions        []api.HCPClusterActiveVersion
		customerDesiredMinor  string
		channelGroup          string
		channels              map[string]*testserver.Graph
		cosmosResources       []any
		expectedVersion       *semver.Version
		expectedError         bool
		expectedErrorContains string
	}{
		{
			name:                 "Y-stream upgrade - direct path available returns latest version with gateway to next minor",
			activeVersions:       []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.19.22")), State: configv1.CompletedUpdate}},
			customerDesiredMinor: "4.20",
			channelGroup:         "stable",
			channels: map[string]*testserver.Graph{
				"stable-4.20": testserver.NewGraph().
					Versions("4.19.22", "4.20.0", "4.20.10", "4.20.15").
					Edges("4.19.22", "4.20.10", "4.20.15").
					Edges("4.20.0", "4.20.10", "4.20.15"),
				"stable-4.21": testserver.NewGraph().
					Versions("4.20.10", "4.21.0").
					Edges("4.20.10", "4.21.0").
					Edges("4.21.0"),
				// 4.20.15 is NOT in stable-4.21 — not a gateway to 4.21.
				// 4.20.10 IS a gateway.
			},
			expectedVersion: ptr.To(semver.MustParse("4.20.10")),
			expectedError:   false,
		},
		{
			name:                 "Y-stream upgrade - succeeds with node pool within skew versus desired minor",
			activeVersions:       []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.19.22")), State: configv1.CompletedUpdate}},
			customerDesiredMinor: "4.20",
			channelGroup:         "stable",
			channels: map[string]*testserver.Graph{
				"stable-4.20": testserver.NewGraph().
					Versions("4.19.22", "4.20.0", "4.20.10", "4.20.15").
					Edges("4.19.22", "4.20.10", "4.20.15").
					Edges("4.20.0", "4.20.10", "4.20.15"),
				"stable-4.21": testserver.NewGraph().
					Versions("4.20.10", "4.21.0").
					Edges("4.20.10", "4.21.0").
					Edges("4.21.0"),
			},
			cosmosResources: testCosmosClusterWithWorkersNodePoolAtVersion("4.18.0"),
			expectedVersion: ptr.To(semver.MustParse("4.20.10")),
			expectedError:   false,
		},
		{
			name:                 "Y-stream upgrade - no direct path, falls back to Z-stream",
			activeVersions:       []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.19.15")), State: configv1.CompletedUpdate}},
			customerDesiredMinor: "4.20",
			channelGroup:         "stable",
			channels: map[string]*testserver.Graph{
				"stable-4.19": testserver.NewGraph().
					Edges("4.19.0", "4.19.15", "4.19.18", "4.19.22").
					Edges("4.19.15", "4.19.18", "4.19.22"),
				// 4.19.15 is in stable-4.20 as a node but has no edges to 4.20
				// versions — no direct path. 4.19.22 IS a gateway to 4.20.
				"stable-4.20": testserver.NewGraph().
					Versions("4.19.15", "4.19.22", "4.20.0", "4.20.5").
					Edges("4.19.22", "4.20.5").
					Edges("4.20.0", "4.20.5"),
			},
			// SelectControlPlaneVersion for desiredMinor 4.20 returns nil (no candidates
			// from 4.19.15 in 4.20), so desiredControlPlaneZVersion falls back to
			// z-stream in the current minor (4.19) and picks the latest gateway: 4.19.22.
			expectedVersion: ptr.To(semver.MustParse("4.19.22")),
			expectedError:   false,
		},
		{
			name:                 "Y-stream upgrade - multiple active versions, only common candidates considered",
			customerDesiredMinor: "4.20",
			channelGroup:         "stable",
			activeVersions: []api.HCPClusterActiveVersion{
				{Version: ptr.To(semver.MustParse("4.19.18")), State: configv1.CompletedUpdate}, // Most recent
				{Version: ptr.To(semver.MustParse("4.19.15")), State: configv1.CompletedUpdate}, // Older active version
			},
			channels: map[string]*testserver.Graph{
				// 4.20.15 is reachable from 4.19.18 but NOT from 4.19.15.
				// 4.20.8 and 4.20.12 are reachable from both.
				"stable-4.20": testserver.NewGraph().
					Versions("4.19.18", "4.19.15", "4.20.0", "4.20.8", "4.20.12", "4.20.15").
					Edges("4.19.18", "4.20.8", "4.20.12", "4.20.15").
					Edges("4.19.15", "4.20.8", "4.20.12").
					Edges("4.20.0", "4.20.8", "4.20.12", "4.20.15"),
				"stable-4.21": testserver.NewGraph().
					Versions("4.20.12", "4.21.0", "4.21.3").
					Edges("4.20.12", "4.21.3").
					Edges("4.21.0", "4.21.3"),
			},
			expectedVersion: ptr.To(semver.MustParse("4.20.12")), // Latest common candidate with gateway to 4.21
			expectedError:   false,
		},
		{
			name:                 "Y-stream upgrade - no gateway found but returns latest anyway",
			activeVersions:       []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.19.15")), State: configv1.CompletedUpdate}},
			customerDesiredMinor: "4.20",
			channelGroup:         "stable",
			channels: map[string]*testserver.Graph{
				// No stable-4.21 channel — next minor does not exist.
				"stable-4.20": testserver.NewGraph().
					Versions("4.19.15", "4.20.0", "4.20.12").
					Edges("4.19.15", "4.20.12").
					Edges("4.20.0", "4.20.12"),
			},
			expectedVersion: ptr.To(semver.MustParse("4.20.12")), // Returns latest even without gateway - user wants to be on 4.20
			expectedError:   false,
		},
		{
			name:                 "Y-stream upgrade - next minor exists but no gateway, returns latest for security",
			activeVersions:       []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.19.60")), State: configv1.CompletedUpdate}},
			customerDesiredMinor: "4.20",
			channelGroup:         "candidate",
			cosmosResources:      testCosmosClusterWithWorkersNodePoolAtVersion("4.19.60"),
			channels: map[string]*testserver.Graph{
				"candidate-4.20": testserver.NewGraph().
					Versions("4.19.60", "4.20.0", "4.20.50").
					Edges("4.19.60", "4.20.50").
					Edges("4.20.0", "4.20.50"),
				// candidate-4.21 exists but has only intra-minor edges.
				// No 4.20.x version is a gateway to 4.21.
				"candidate-4.21": testserver.NewGraph().
					Edges("4.21.0", "4.21.5"),
			},
			expectedVersion: ptr.To(semver.MustParse("4.20.50")), // Prioritize security over y-stream path
			expectedError:   false,
		},
		{
			name:                 "Y-stream upgrade - Cincinnati channel not found error",
			activeVersions:       []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.19.22")), State: configv1.CompletedUpdate}},
			customerDesiredMinor: "4.20",
			channelGroup:         "stable",
			// Empty channels: stable-4.20 does not exist.
			channels:              map[string]*testserver.Graph{},
			expectedVersion:       nil,
			expectedError:         true,
			expectedErrorContains: "stable-4.20",
		},
		{
			name:                 "Y-stream upgrade - no path in target minor and already at latest in current minor",
			activeVersions:       []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.20.22")), State: configv1.CompletedUpdate}},
			customerDesiredMinor: "4.21",
			channelGroup:         "candidate",
			cosmosResources:      testCosmosClusterWithWorkersNodePoolAtVersion("4.20.22"),
			channels: map[string]*testserver.Graph{
				// candidate-4.21: 4.20.22 is a node but has no edges to 4.21 versions.
				"candidate-4.21": testserver.NewGraph().
					Versions("4.20.22").
					Edges("4.21.0", "4.21.5"),
				// candidate-4.20: 4.20.22 has no newer z-stream candidate.
				"candidate-4.20": testserver.NewGraph().
					Versions("4.20.22"),
			},
			// No candidates in either the target minor or fallback z-stream.
			// The function returns nil without error — the controller will
			// re-evaluate on the next sync cycle.
			expectedVersion: nil,
			expectedError:   false,
		},
		{
			name:                 "Y-stream upgrade - candidates exist but no gateway to next minor, returns latest for security",
			activeVersions:       []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.20.22")), State: configv1.CompletedUpdate}},
			customerDesiredMinor: "4.21",
			channelGroup:         "candidate",
			cosmosResources:      testCosmosClusterWithWorkersNodePoolAtVersion("4.20.22"),
			channels: map[string]*testserver.Graph{
				"candidate-4.21": testserver.NewGraph().
					Versions("4.20.22", "4.21.0", "4.21.15").
					Edges("4.20.22", "4.21.15").
					Edges("4.21.0", "4.21.15"),
				// candidate-4.22 exists but has only intra-minor edges.
				// No 4.21.x version is a gateway to 4.22.
				"candidate-4.22": testserver.NewGraph().
					Edges("4.22.0", "4.22.5"),
			},
			expectedVersion: ptr.To(semver.MustParse("4.21.15")), // Prioritize security over y-stream path
			expectedError:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := testserver.NewServer(t, tt.channels)
			cincinnatiClient := newTestServerClient(server)

			now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
			ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
			mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, tt.cosmosResources)
			require.NoError(t, err)
			syncer := &controlPlaneDesiredVersionSyncer{
				clock:             clocktesting.NewFakePassiveClock(now),
				resourcesDBClient: mockResourcesDBClient,
			}

			hostedCluster := hostedClusterFromActiveVersions(tt.activeVersions)
			result, err := syncer.desiredControlPlaneZVersion(ctx, cincinnatiClient, api.Must(api.ToClusterResourceID("6b690bec-0c16-4ecb-8f67-781caf40bba7", "test-rg", "test-cluster")), tt.customerDesiredMinor, tt.channelGroup, tt.activeVersions, false, hostedCluster)

			assertVersionResult(t, result, err, tt.expectedVersion, tt.expectedError, tt.expectedErrorContains)
		})
	}
}

// testCosmosClusterWithWorkersNodePoolAtVersion returns a cluster and workers node pool for the subscription, resource group,
// and cluster name shared by desiredControlPlaneZVersion tests. nodePoolVersionId is properties.version.id on the pool.
func testCosmosClusterWithWorkersNodePoolAtVersion(nodePoolVersionId string) []any {
	clusterResourceId, cluster := testCosmosClusterResource()
	return []any{
		cluster,
		testCosmosNodePool(clusterResourceId, "workers", nodePoolVersionId, false),
	}
}

func testCosmosClusterResource() (*azcorearm.ResourceID, *api.HCPOpenShiftCluster) {
	clusterResourceId := api.Must(api.ToClusterResourceID("6b690bec-0c16-4ecb-8f67-781caf40bba7", "test-rg", "test-cluster"))
	cluster := &api.HCPOpenShiftCluster{
		CosmosMetadata: arm.CosmosMetadata{
			ResourceID:   clusterResourceId,
			PartitionKey: strings.ToLower(clusterResourceId.SubscriptionID),
		},
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   clusterResourceId,
				Name: clusterResourceId.Name,
				Type: clusterResourceId.ResourceType.String(),
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ClusterServiceID: api.Ptr(api.Must(api.NewInternalID("/api/clusters_mgmt/v1/clusters/test-cluster"))),
		},
	}
	return clusterResourceId, cluster
}

func testCosmosNodePool(clusterResourceId *azcorearm.ResourceID, name, nodePoolVersionId string, deleting bool) *api.HCPOpenShiftClusterNodePool {
	nodePoolResourceId := api.Must(azcorearm.ParseResourceID(clusterResourceId.String() + "/nodePools/" + name))
	nodePool := &api.HCPOpenShiftClusterNodePool{
		CosmosMetadata: arm.CosmosMetadata{
			ResourceID:   nodePoolResourceId,
			PartitionKey: strings.ToLower(nodePoolResourceId.SubscriptionID),
		},
		TrackedResource: arm.NewTrackedResource(nodePoolResourceId, "eastus"),
		Properties: api.HCPOpenShiftClusterNodePoolProperties{
			Version: api.NodePoolVersionProfile{ID: nodePoolVersionId},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterNodePoolServiceProviderProperties{
			ClusterServiceID: api.Ptr(api.Must(api.NewInternalID("/api/clusters_mgmt/v1/clusters/test-cluster/node_pools/" + name))),
		},
	}
	if deleting {
		nodePool.ServiceProviderProperties.DeletionTimestamp = ptr.To(metav1.Now())
	}
	return nodePool
}

// testCosmosClusterWithActiveAndDeletingNodePools returns a cluster with one active node pool and one
// node pool marked for deletion. Skew validation should consider only the active pool.
func testCosmosClusterWithActiveAndDeletingNodePools(activeNodePoolName, activeVersion, deletingNodePoolName, deletingVersion string) []any {
	clusterResourceId, cluster := testCosmosClusterResource()
	return []any{
		cluster,
		testCosmosNodePool(clusterResourceId, activeNodePoolName, activeVersion, false),
		testCosmosNodePool(clusterResourceId, deletingNodePoolName, deletingVersion, true),
	}
}

func TestDesiredControlPlaneZVersion_Validations(t *testing.T) {
	tests := []struct {
		name                        string
		activeVersions              []api.HCPClusterActiveVersion
		customerDesiredMinor        string
		channelGroup                string
		channels                    map[string]*testserver.Graph
		cosmosResources             []any
		experimentalReleaseFeatures bool
		expectedVersion             *semver.Version
		expectedError               bool
		expectedErrorContains       string
	}{
		{
			name:                  "Validation - downgrade not allowed (4.20 -> 4.19)",
			activeVersions:        []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.20.15")), State: configv1.CompletedUpdate}},
			customerDesiredMinor:  "4.19",
			channelGroup:          "stable",
			channels:              map[string]*testserver.Graph{},
			expectedVersion:       nil,
			expectedError:         true,
			expectedErrorContains: "only upgrades to the next minor version are allowed, no downgrades",
		},
		{
			name:                  "Validation - OpenShift 5.x requires AFEC (4.20 -> 5.0)",
			activeVersions:        []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.20.15")), State: configv1.CompletedUpdate}},
			customerDesiredMinor:  "5.0",
			channelGroup:          "stable",
			channels:              map[string]*testserver.Graph{},
			expectedVersion:       nil,
			expectedError:         true,
			expectedErrorContains: "OpenShift v5 and above is not supported",
		},
		{
			name:                        "Validation - unsupported cross-major (4.20 -> 5.0, not a supported 4 to 5 landing) when AFEC registered",
			activeVersions:              []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.20.15")), State: configv1.CompletedUpdate}},
			customerDesiredMinor:        "5.0",
			channelGroup:                "stable",
			channels:                    map[string]*testserver.Graph{},
			experimentalReleaseFeatures: true,
			expectedVersion:             nil,
			expectedError:               true,
			expectedErrorContains:       "cross-major upgrade from 4.20 is only allowed to",
		},
		{
			name:                  "Validation - skip minor version not allowed (4.19 -> 4.21)",
			activeVersions:        []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.19.22")), State: configv1.CompletedUpdate}},
			customerDesiredMinor:  "4.21",
			channelGroup:          "stable",
			channels:              map[string]*testserver.Graph{},
			expectedVersion:       nil,
			expectedError:         true,
			expectedErrorContains: "only upgrade to the next minor is allowed",
		},
		{
			name:                  "Validation - major version downgrade not allowed (5.1 -> 4.20)",
			activeVersions:        []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("5.1.5")), State: configv1.CompletedUpdate}},
			customerDesiredMinor:  "4.20",
			channelGroup:          "stable",
			channels:              map[string]*testserver.Graph{},
			expectedVersion:       nil,
			expectedError:         true,
			expectedErrorContains: "only upgrades to the next minor version are allowed, no downgrades",
		},
		{
			name:                        "Validation - node pool minor skew blocks supported cross-major desired minor",
			activeVersions:              []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.22.0")), State: configv1.CompletedUpdate}},
			customerDesiredMinor:        "5.0",
			channelGroup:                "stable",
			channels:                    map[string]*testserver.Graph{},
			cosmosResources:             testCosmosClusterWithWorkersNodePoolAtVersion("4.20.0"),
			experimentalReleaseFeatures: true,
			expectedVersion:             nil,
			expectedError:               true,
			expectedErrorContains:       "incompatible with node pool",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := testserver.NewServer(t, tt.channels)
			cincinnatiClient := newTestServerClient(server)

			now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
			ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
			mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, tt.cosmosResources)
			require.NoError(t, err)
			syncer := &controlPlaneDesiredVersionSyncer{
				clock:             clocktesting.NewFakePassiveClock(now),
				resourcesDBClient: mockResourcesDBClient,
			}

			hostedCluster := hostedClusterFromActiveVersions(tt.activeVersions)
			result, err := syncer.desiredControlPlaneZVersion(ctx, cincinnatiClient, api.Must(api.ToClusterResourceID("6b690bec-0c16-4ecb-8f67-781caf40bba7", "test-rg", "test-cluster")), tt.customerDesiredMinor, tt.channelGroup, tt.activeVersions, tt.experimentalReleaseFeatures, hostedCluster)

			assertVersionResult(t, result, err, tt.expectedVersion, tt.expectedError, tt.expectedErrorContains)
		})
	}
}

func TestDesiredControlPlaneZVersion_CrossMajorUpgrade(t *testing.T) {
	tests := []struct {
		name                        string
		activeVersions              []api.HCPClusterActiveVersion
		customerDesiredMinor        string
		channelGroup                string
		channels                    map[string]*testserver.Graph
		cosmosResources             []any
		experimentalReleaseFeatures bool
		expectedVersion             *semver.Version
		expectedError               bool
		expectedErrorContains       string
	}{
		{
			name:                        "Cross-major allowed -- 4.22 to 5.0 with experimental release features and compatible node pools",
			activeVersions:              []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.22.0")), State: configv1.CompletedUpdate}},
			customerDesiredMinor:        "5.0",
			channelGroup:                "stable",
			cosmosResources:             testCosmosClusterWithWorkersNodePoolAtVersion("4.22.0"),
			experimentalReleaseFeatures: true,
			channels: map[string]*testserver.Graph{
				"stable-5.0": testserver.NewGraph().
					Versions("4.22.0", "5.0.0", "5.0.10", "5.0.15").
					Edges("4.22.0", "5.0.10", "5.0.15").
					Edges("5.0.0", "5.0.10", "5.0.15"),
				"stable-5.1": testserver.NewGraph().
					Versions("5.0.10", "5.1.0").
					Edges("5.0.10", "5.1.0").
					Edges("5.1.0"),
				// 5.0.15 is NOT in stable-5.1 — not a gateway. 5.0.10 IS a gateway.
			},
			expectedVersion: ptr.To(semver.MustParse("5.0.10")),
			expectedError:   false,
		},
		{
			name:                        "Cross-major allowed when incompatible node pool is being deleted",
			activeVersions:              []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.22.0")), State: configv1.CompletedUpdate}},
			customerDesiredMinor:        "5.0",
			channelGroup:                "stable",
			cosmosResources:             testCosmosClusterWithActiveAndDeletingNodePools("infra", "4.22.0", "workers", "4.20.0"),
			experimentalReleaseFeatures: true,
			channels: map[string]*testserver.Graph{
				"stable-5.0": testserver.NewGraph().
					Versions("4.22.0", "5.0.0", "5.0.10", "5.0.15").
					Edges("4.22.0", "5.0.10", "5.0.15").
					Edges("5.0.0", "5.0.10", "5.0.15"),
				"stable-5.1": testserver.NewGraph().
					Versions("5.0.10", "5.1.0").
					Edges("5.0.10", "5.1.0").
					Edges("5.1.0"),
			},
			expectedVersion: ptr.To(semver.MustParse("5.0.10")),
			expectedError:   false,
		},
		{
			name:                        "Cross-major not allowed -- 4.22 to 5.0 without experimental release features",
			activeVersions:              []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.22.0")), State: configv1.CompletedUpdate}},
			customerDesiredMinor:        "5.0",
			channelGroup:                "stable",
			channels:                    map[string]*testserver.Graph{},
			experimentalReleaseFeatures: false,
			expectedVersion:             nil,
			expectedError:               true,
			expectedErrorContains:       "OpenShift v5 and above is not supported",
		},
		{
			name:                        "Cross-major not allowed -- 4.21 to 5.0 is not a supported landing even with experimental release features",
			activeVersions:              []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.21.10")), State: configv1.CompletedUpdate}},
			customerDesiredMinor:        "5.0",
			channelGroup:                "stable",
			cosmosResources:             testCosmosClusterWithWorkersNodePoolAtVersion("4.21.0"),
			channels:                    map[string]*testserver.Graph{},
			experimentalReleaseFeatures: true,
			expectedVersion:             nil,
			expectedError:               true,
			expectedErrorContains:       "cross-major upgrade from 4.21 is only allowed to",
		},
		{
			name:                        "Cross-major not allowed -- 4.22 to 5.1 skips the supported 4.22 to 5.0 path",
			activeVersions:              []api.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.22.0")), State: configv1.CompletedUpdate}},
			customerDesiredMinor:        "5.1",
			channelGroup:                "stable",
			cosmosResources:             testCosmosClusterWithWorkersNodePoolAtVersion("4.22.0"),
			channels:                    map[string]*testserver.Graph{},
			experimentalReleaseFeatures: true,
			expectedVersion:             nil,
			expectedError:               true,
			expectedErrorContains:       "cross-major upgrade from 4.22 is only allowed to",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := testserver.NewServer(t, tt.channels)
			cincinnatiClient := newTestServerClient(server)

			now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
			ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
			mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, tt.cosmosResources)
			require.NoError(t, err)
			syncer := &controlPlaneDesiredVersionSyncer{
				clock:             clocktesting.NewFakePassiveClock(now),
				resourcesDBClient: mockResourcesDBClient,
			}

			hostedCluster := hostedClusterFromActiveVersions(tt.activeVersions)
			result, err := syncer.desiredControlPlaneZVersion(ctx, cincinnatiClient, api.Must(api.ToClusterResourceID("6b690bec-0c16-4ecb-8f67-781caf40bba7", "test-rg", "test-cluster")), tt.customerDesiredMinor, tt.channelGroup, tt.activeVersions, tt.experimentalReleaseFeatures, hostedCluster)

			assertVersionResult(t, result, err, tt.expectedVersion, tt.expectedError, tt.expectedErrorContains)
		})
	}
}

func TestDesiredControlPlaneZVersion_InitialVersionSelection(t *testing.T) {
	tests := []struct {
		name                  string
		customerDesiredMinor  string
		channelGroup          string
		channels              map[string]*testserver.Graph
		expectedVersion       *semver.Version
		expectedError         bool
		expectedErrorContains string
	}{
		{
			name:                 "Initial version - prefers gateway over absolute latest",
			customerDesiredMinor: "4.19",
			channelGroup:         "stable",
			channels: map[string]*testserver.Graph{
				"stable-4.19": testserver.NewGraph().
					Edges("4.19.0", "4.19.15", "4.19.22").
					Edges("4.19.15", "4.19.22"),
				"stable-4.20": testserver.NewGraph().
					Versions("4.19.15", "4.20.0", "4.20.5").
					Edges("4.19.15", "4.20.5").
					Edges("4.20.0", "4.20.5"),
				// 4.19.22 is NOT in stable-4.20 — not a gateway.
				// 4.19.15 IS a gateway.
			},
			expectedVersion: ptr.To(semver.MustParse("4.19.15")), // Prefers gateway version over absolute latest
			expectedError:   false,
		},
		{
			name:                 "Initial version - no updates available, falls back to seedVersion",
			customerDesiredMinor: "4.19",
			channelGroup:         "stable",
			channels: map[string]*testserver.Graph{
				"stable-4.19": testserver.NewGraph().
					Versions("4.19.0"),
			},
			expectedVersion: ptr.To(semver.MustParse("4.19.0")), // Falls back to seedVersion
			expectedError:   false,
		},
		{
			name:                 "Initial version - next minor doesn't exist yet, returns latest",
			customerDesiredMinor: "4.19",
			channelGroup:         "stable",
			channels: map[string]*testserver.Graph{
				// No stable-4.20 channel.
				"stable-4.19": testserver.NewGraph().
					Edges("4.19.0", "4.19.15", "4.19.22"),
			},
			expectedVersion: ptr.To(semver.MustParse("4.19.22")), // Returns latest - no next minor to preserve path to
			expectedError:   false,
		},
		{
			name:                 "Initial version - Cincinnati channel not found error",
			customerDesiredMinor: "4.19",
			channelGroup:         "stable",
			// Empty channels: stable-4.19 does not exist.
			channels:              map[string]*testserver.Graph{},
			expectedVersion:       nil,
			expectedError:         true,
			expectedErrorContains: "stable-4.19",
		},
		{
			name:                 "Initial version - Cincinnati version not found",
			customerDesiredMinor: "4.19",
			channelGroup:         "stable",
			channels: map[string]*testserver.Graph{
				// Channel exists but 4.19.0 is NOT in it.
				"stable-4.19": testserver.NewGraph().
					Edges("4.19.5", "4.19.10"),
			},
			expectedVersion:       nil,
			expectedError:         true,
			expectedErrorContains: "VersionNotFound",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := testserver.NewServer(t, tt.channels)
			cincinnatiClient := newTestServerClient(server)

			now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
			syncer := &controlPlaneDesiredVersionSyncer{
				clock:             clocktesting.NewFakePassiveClock(now),
				resourcesDBClient: databasetesting.NewMockResourcesDBClient(),
			}

			// Empty active versions - simulating a new cluster
			activeVersions := []api.HCPClusterActiveVersion{}

			ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
			result, err := syncer.desiredControlPlaneZVersion(ctx, cincinnatiClient, api.Must(api.ToClusterResourceID("6b690bec-0c16-4ecb-8f67-781caf40bba7", "test-rg", "test-cluster")), tt.customerDesiredMinor, tt.channelGroup, activeVersions, false, nil)

			assertVersionResult(t, result, err, tt.expectedVersion, tt.expectedError, tt.expectedErrorContains)
		})
	}
}

// assertVersionResult is a helper function that validates the result of desiredControlPlaneZVersion
func assertVersionResult(t *testing.T, result *semver.Version, err error, expectedVersion *semver.Version, expectedError bool, expectedErrorContains string) {
	if expectedError {
		assert.Error(t, err)
		assert.NotEmpty(t, expectedErrorContains)
		assert.ErrorContains(t, err, expectedErrorContains)
	} else {
		assert.NoError(t, err)
		if expectedVersion == nil {
			assert.Nil(t, result)
		} else {
			assert.NotNil(t, result)
			assert.True(t, result.EQ(*expectedVersion), "Expected version %q, got %q", expectedVersion.String(), result.String())
		}
	}
}

func createTestHCPClusterWithCustomerVersion(t *testing.T, ctx context.Context, mockResourcesDBClient *databasetesting.MockResourcesDBClient, customerVersionID, channelGroup string) {
	t.Helper()
	createTestSubscription(t, ctx, mockResourcesDBClient)
	clusterResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID +
		"/resourceGroups/" + testResourceGroupName +
		"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName))
	clusterInternalID, err := api.NewInternalID(testCSClusterIDStr)
	require.NoError(t, err)
	cluster := &api.HCPOpenShiftCluster{
		CosmosMetadata: arm.CosmosMetadata{
			ResourceID:   clusterResourceID,
			PartitionKey: strings.ToLower(clusterResourceID.SubscriptionID),
		},
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   clusterResourceID,
				Name: testClusterName,
				Type: api.ClusterResourceType.String(),
			},
			Location: "eastus",
		},
		CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
			Version: api.VersionProfile{
				ID:           customerVersionID,
				ChannelGroup: channelGroup,
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ProvisioningState: arm.ProvisioningStateSucceeded,
			ClusterServiceID:  &clusterInternalID,
		},
	}
	_, err = mockResourcesDBClient.HCPClusters(testSubscriptionID, testResourceGroupName).Create(ctx, cluster, nil)
	require.NoError(t, err)
}

// newHostedClusterReadDesireListerWithHistory creates a readDesireLister whose
// HostedCluster has Status.ControlPlaneVersion.History populated from the given
// versions. This ensures that SelectControlPlaneVersion sees the correct active
// versions when called from desiredControlPlaneZVersion via SyncOnce.
func newHostedClusterReadDesireListerWithHistory(t *testing.T, versions ...string) dblisters.ReadDesireLister {
	t.Helper()
	hostedCluster := &v1beta1.HostedCluster{
		TypeMeta: metav1.TypeMeta{
			Kind:       "HostedCluster",
			APIVersion: v1beta1.GroupVersion.String(),
		},
		Spec: v1beta1.HostedClusterSpec{
			ClusterID: testClusterExternalID,
		},
	}
	for _, ver := range versions {
		hostedCluster.Status.ControlPlaneVersion.History = append(
			hostedCluster.Status.ControlPlaneVersion.History,
			v1beta1.ControlPlaneUpdateHistory{
				Version: ver,
				State:   configv1.CompletedUpdate,
			})
	}
	raw, err := json.Marshal(hostedCluster)
	require.NoError(t, err)
	return &internallistertesting.SliceReadDesireLister{
		Desires: []*kubeapplier.ReadDesire{{
			CosmosMetadata: api.CosmosMetadata{ResourceID: hostedClusterReadDesireResourceID(t)},
			Status: kubeapplier.ReadDesireStatus{
				KubeContent: &kruntime.RawExtension{Raw: raw},
			},
		}},
	}
}

func TestControlPlaneDesiredVersionSyncer_SyncOnce(t *testing.T) {
	clusterKey := controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	}
	subResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID))
	subscriptionLister := &listertesting.SliceSubscriptionLister{
		Subscriptions: []*arm.Subscription{{
			CosmosMetadata: arm.CosmosMetadata{ResourceID: subResourceID, PartitionKey: strings.ToLower(subResourceID.SubscriptionID)},
			ResourceID:     subResourceID,
			Properties:     &arm.SubscriptionProperties{},
		}},
	}

	const testChannelGroup = "stable"

	tests := []struct {
		name                   string
		customerVersion        string
		controlPlaneVersion    string
		previousDesiredVersion *semver.Version
		channels               map[string]*testserver.Graph
		wantSyncErr            bool
		wantErrContains        string
		wantDesiredVersion     *semver.Version
		wantIntentFailed       *metav1.Condition
	}{
		{
			name:                "successful resolution persists desired version and sets IntentFailed False",
			customerVersion:     "4.19",
			controlPlaneVersion: "4.19.15",
			channels: map[string]*testserver.Graph{
				// No stable-4.20 channel — next minor does not exist.
				"stable-4.19": testserver.NewGraph().
					Edges("4.19.15", "4.19.22"),
			},
			wantDesiredVersion: ptr.To(semver.MustParse("4.19.22")),
			wantIntentFailed: &metav1.Condition{
				Type:   api.ControllerConditionTypeIntentFailed,
				Status: metav1.ConditionFalse,
				Reason: api.ControllerConditionReasonAsExpected,
			},
		},
		{
			name:                   "lower resolved desired does not replace higher previously selected desired",
			customerVersion:        "4.19",
			controlPlaneVersion:    "4.19.15",
			previousDesiredVersion: ptr.To(semver.MustParse("4.19.22")),
			channels: map[string]*testserver.Graph{
				"stable-4.19": testserver.NewGraph().
					Edges("4.19.15", "4.19.18"),
			},
			wantDesiredVersion: ptr.To(semver.MustParse("4.19.22")),
			wantIntentFailed: &metav1.Condition{
				Type:   api.ControllerConditionTypeIntentFailed,
				Status: metav1.ConditionFalse,
				Reason: api.ControllerConditionReasonAsExpected,
			},
		},
		{
			name:                "validation error persists IntentFailed and does not set desired version",
			customerVersion:     "4.19",
			controlPlaneVersion: "4.20.15",
			channels:            map[string]*testserver.Graph{},
			wantDesiredVersion:  nil,
			wantIntentFailed: &metav1.Condition{
				Type:    api.ControllerConditionTypeIntentFailed,
				Status:  metav1.ConditionTrue,
				Reason:  api.VersionUpgradeNotAcceptedReason,
				Message: "invalid next y-stream upgrade path from 4.20.0 to 4.19.0: only upgrades to the next minor version are allowed, no downgrades",
			},
		},
		{
			name:                "Cincinnati upstream error does not persist IntentFailed or desired version",
			customerVersion:     "4.19",
			controlPlaneVersion: "4.19.15",
			// Empty channels: stable-4.19 does not exist on the test server.
			// The ResponseFailed error is a Cincinnati error and treated as transient.
			channels:           map[string]*testserver.Graph{},
			wantSyncErr:        true,
			wantErrContains:    "stable-4.19",
			wantDesiredVersion: nil,
			wantIntentFailed:   nil,
		},
		{
			name:                "Cincinnati VersionNotFound persists IntentFailed and does not set desired version",
			customerVersion:     "4.19",
			controlPlaneVersion: "4.19.15",
			channels: map[string]*testserver.Graph{
				// Channel exists but 4.19.15 is NOT in it.
				"stable-4.19": testserver.NewGraph().
					Edges("4.19.5", "4.19.10"),
			},
			wantDesiredVersion: nil,
			wantIntentFailed: &metav1.Condition{
				Type:    api.ControllerConditionTypeIntentFailed,
				Status:  metav1.ConditionTrue,
				Reason:  api.VersionUpgradeNotAcceptedReason,
				Message: `querying Cincinnati for upgrades from 4.19.15 in stable-4.19: VersionNotFound: currently reconciling cluster version 4.19.15 not found in the "stable-4.19" channel`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
			ctrl := gomock.NewController(t)
			mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
			mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)

			createTestHCPClusterWithCustomerVersion(t, ctx, mockResourcesDBClient, tt.customerVersion, testChannelGroup)
			createServiceProviderClusterWithActiveAndDesiredVersion(t, ctx, mockResourcesDBClient, semver.MustParse(tt.controlPlaneVersion), tt.previousDesiredVersion)

			server := testserver.NewServer(t, tt.channels)
			testClient := newTestServerClient(server)

			now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
			syncer := &controlPlaneDesiredVersionSyncer{
				clock:                clocktesting.NewFakePassiveClock(now),
				readDesireLister:     newHostedClusterReadDesireListerWithHistory(t, tt.controlPlaneVersion),
				resourcesDBClient:    mockResourcesDBClient,
				clusterServiceClient: mockCS,
				subscriptionLister:   subscriptionLister,
				cincinnatiClient:     testClient,
			}

			err := syncer.SyncOnce(ctx, clusterKey)
			if tt.wantSyncErr {
				require.Error(t, err)
				require.NotEmpty(t, tt.wantErrContains, "when wantSyncErr is true, wantErrContains must be set to a substring of the expected error")
				assert.ErrorContains(t, err, tt.wantErrContains)
			} else {
				require.NoError(t, err)
				assert.Empty(t, tt.wantErrContains, "when wantSyncErr is false, wantErrContains must be empty")
			}

			serviceProviderCluster, getServiceProviderClusterErr := mockResourcesDBClient.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, api.ServiceProviderClusterResourceName)
			require.NoError(t, getServiceProviderClusterErr)
			gotDesired := serviceProviderCluster.Spec.ControlPlaneVersion.DesiredVersion
			if tt.wantDesiredVersion != nil {
				require.NotNil(t, gotDesired)
				assert.True(t, gotDesired.EQ(*tt.wantDesiredVersion), "wanted desired version %s, got %s", tt.wantDesiredVersion.String(), gotDesired.String())
			} else {
				assert.Nil(t, gotDesired)
			}

			controlPlaneDesiredVersionControllerDoc, getControllerDocErr := mockResourcesDBClient.HCPClusters(testSubscriptionID, testResourceGroupName).
				Controllers(testClusterName).Get(ctx, controlPlaneDesiredVersionControllerName)
			if tt.wantIntentFailed != nil {
				require.NoError(t, getControllerDocErr)
				require.NotNil(t, controlPlaneDesiredVersionControllerDoc)
				intentFailedCondition := apimeta.FindStatusCondition(controlPlaneDesiredVersionControllerDoc.Status.Conditions,
					api.ControllerConditionTypeIntentFailed)
				require.NotNil(t, intentFailedCondition)
				assert.Equal(t, tt.wantIntentFailed.Type, intentFailedCondition.Type)
				assert.Equal(t, tt.wantIntentFailed.Status, intentFailedCondition.Status)
				assert.Equal(t, tt.wantIntentFailed.Reason, intentFailedCondition.Reason)
				if tt.wantIntentFailed.Status == metav1.ConditionTrue {
					require.NotEmpty(t, tt.wantIntentFailed.Message, "set wantIntentFailed.Message to the exact persisted IntentFailed message")
					assert.Equal(t, tt.wantIntentFailed.Message, intentFailedCondition.Message)
				} else {
					assert.Empty(t, intentFailedCondition.Message, "when wantIntentFailed.Status is false, intentFailedCondition.Message must be empty")
				}
			}
		})
	}
}

func createServiceProviderClusterWithActiveAndDesiredVersion(t *testing.T, ctx context.Context, mockResourcesDBClient *databasetesting.MockResourcesDBClient, activeVersion semver.Version, desiredVersion *semver.Version) {
	t.Helper()

	serviceProviderCluster := &api.ServiceProviderCluster{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID: api.Must(azcorearm.ParseResourceID(
				api.ToServiceProviderClusterResourceIDString(testSubscriptionID, testResourceGroupName, testClusterName),
			)),
		},
		Spec: api.ServiceProviderClusterSpec{
			ControlPlaneVersion: api.ServiceProviderClusterSpecVersion{
				DesiredVersion: desiredVersion,
			},
		},
		Status: api.ServiceProviderClusterStatus{
			ControlPlaneVersion: api.ServiceProviderClusterStatusVersion{
				ActiveVersions: []api.HCPClusterActiveVersion{
					{Version: ptr.To(activeVersion), State: configv1.CompletedUpdate},
				},
			},
		},
	}
	serviceProviderCluster.SetPartitionKey(testSubscriptionID)
	_, err := mockResourcesDBClient.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Create(ctx, serviceProviderCluster, nil)
	require.NoError(t, err)
}

// boomActiveOperationLister is a test double that returns the configured
// error from ListActiveOperationsForCluster. It exists so the gating helper
// can exercise its error-propagation branch without a misbehaving mock DB.
type boomActiveOperationLister struct {
	listers.ActiveOperationLister
	err error
}

func (b *boomActiveOperationLister) Get(_ context.Context, _, _ string) (*api.Operation, error) {
	return nil, b.err
}

func (b *boomActiveOperationLister) ListActiveOperationsForCluster(_ context.Context, _, _, _ string) ([]*api.Operation, error) {
	return nil, b.err
}

// seedClusterCreateOperation seeds an active Create operation rooted at the
// given ExternalID into the mock DB so the DB-backed active operation lister
// can find it.
func seedClusterCreateOperation(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient, externalID *azcorearm.ResourceID, opName string) {
	t.Helper()
	opResourceID := api.Must(azcorearm.ParseResourceID(api.ToOperationResourceIDString(externalID.SubscriptionID, opName)))
	operationID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + externalID.SubscriptionID +
			"/providers/Microsoft.RedHatOpenShift/locations/eastus/hcpOperationStatuses/" + opName,
	))
	op := &api.Operation{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID:   opResourceID,
			PartitionKey: strings.ToLower(externalID.SubscriptionID),
		},
		Status:      arm.ProvisioningStateAccepted,
		Request:     database.OperationRequestCreate,
		ExternalID:  externalID,
		OperationID: operationID,
	}
	_, err := mockDB.Operations(externalID.SubscriptionID).Create(ctx, op, nil)
	require.NoError(t, err)
}

func TestControlPlaneDesiredVersionSyncer_ShouldDetermineDesiredVersion(t *testing.T) {
	clusterResourceID := api.Must(api.ToClusterResourceID(testSubscriptionID, testResourceGroupName, testClusterName))
	now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
	listerBoom := errors.New("active operation lister exploded")

	newCluster := func(createdAt *time.Time, activeOperationID string) *api.HCPOpenShiftCluster {
		c := &api.HCPOpenShiftCluster{
			CosmosMetadata: api.CosmosMetadata{
				ResourceID: clusterResourceID,
			},
			TrackedResource: arm.TrackedResource{
				Resource: arm.Resource{
					ID:   clusterResourceID,
					Name: testClusterName,
					Type: api.ClusterResourceType.String(),
				},
			},
			ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
				ActiveOperationID: activeOperationID,
			},
		}
		if createdAt != nil {
			c.SystemData = &arm.SystemData{CreatedAt: createdAt}
		}
		return c
	}
	newSPC := func(desired *semver.Version) *api.ServiceProviderCluster {
		return &api.ServiceProviderCluster{
			Spec: api.ServiceProviderClusterSpec{
				ControlPlaneVersion: api.ServiceProviderClusterSpecVersion{DesiredVersion: desired},
			},
		}
	}

	tests := []struct {
		name           string
		cluster        *api.HCPOpenShiftCluster
		spc            *api.ServiceProviderCluster
		seedOperation  bool
		opLister       func(mockDB *databasetesting.MockResourcesDBClient) listers.ActiveOperationLister
		wantShouldRun  bool
		wantErrContain string
	}{
		{
			name:          "empty DesiredVersion runs even when create is in flight (gate 1)",
			cluster:       newCluster(ptr.To(now.Add(-5*time.Minute)), "op-create-1"),
			spc:           newSPC(nil),
			seedOperation: true,
			wantShouldRun: true,
		},
		{
			name:          "cluster older than grace period runs even with active create (gate 2)",
			cluster:       newCluster(ptr.To(now.Add(-3*time.Hour)), "op-create-1"),
			spc:           newSPC(ptr.To(semver.MustParse("4.19.15"))),
			seedOperation: true,
			wantShouldRun: true,
		},
		{
			name:          "cluster with no SystemData.CreatedAt runs (treated as old enough)",
			cluster:       newCluster(nil, "op-create-1"),
			spc:           newSPC(ptr.To(semver.MustParse("4.19.15"))),
			seedOperation: true,
			wantShouldRun: true,
		},
		{
			name:          "cluster younger than grace period with no active create runs (gate 3)",
			cluster:       newCluster(ptr.To(now.Add(-5*time.Minute)), ""),
			spc:           newSPC(ptr.To(semver.MustParse("4.19.15"))),
			seedOperation: false,
			wantShouldRun: true,
		},
		{
			name:          "young cluster + DesiredVersion set + active create skips",
			cluster:       newCluster(ptr.To(now.Add(-5*time.Minute)), "op-create-1"),
			spc:           newSPC(ptr.To(semver.MustParse("4.19.15"))),
			seedOperation: true,
			wantShouldRun: false,
		},
		{
			name:    "cluster exactly at grace period boundary still skips (boundary is strict >)",
			cluster: newCluster(ptr.To(now.Add(-clusterCreateGracePeriod)), "op-create-1"),
			spc:     newSPC(ptr.To(semver.MustParse("4.19.15"))),
			// active create present so without the boundary-is-strict gate, the
			// cluster's age would have to push us through.
			seedOperation: true,
			wantShouldRun: false,
		},
		{
			// Fail open: if we can't tell whether a Create is in flight we
			// surface the error to the caller but still report shouldRun=true
			// so a flaky lister doesn't pin the controller in skip-forever
			// mode for the rest of the grace window.
			name:          "active operation lister error is propagated and fails open to shouldRun=true",
			cluster:       newCluster(ptr.To(now.Add(-5*time.Minute)), "op-broken"),
			spc:           newSPC(ptr.To(semver.MustParse("4.19.15"))),
			seedOperation: false,
			opLister: func(_ *databasetesting.MockResourcesDBClient) listers.ActiveOperationLister {
				return &boomActiveOperationLister{err: listerBoom}
			},
			wantShouldRun:  true,
			wantErrContain: "failed to get operations",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
			mockDB := databasetesting.NewMockResourcesDBClient()
			if tt.seedOperation {
				seedClusterCreateOperation(t, ctx, mockDB, clusterResourceID, "op-create-1")
			}
			var opLister listers.ActiveOperationLister
			if tt.opLister != nil {
				opLister = tt.opLister(mockDB)
			} else {
				opLister = &listertesting.DBActiveOperationLister{ResourcesDBClient: mockDB}
			}
			syncer := &controlPlaneDesiredVersionSyncer{
				clock:                 clocktesting.NewFakePassiveClock(now),
				resourcesDBClient:     mockDB,
				activeOperationLister: opLister,
			}

			gotShouldRun, err := syncer.shouldDetermineDesiredVersion(ctx, tt.cluster, tt.spc)
			if tt.wantErrContain != "" {
				require.Error(t, err)
				assert.ErrorContains(t, err, tt.wantErrContain)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tt.wantShouldRun, gotShouldRun)
		})
	}
}

// TestControlPlaneDesiredVersionSyncer_SyncOnceSkipsWhenGated verifies the
// end-to-end skip behaviour: when shouldDetermineDesiredVersion returns false
// SyncOnce returns nil without touching the SPC DesiredVersion or writing a
// controller doc, so the cluster create can finish without an upgrade
// recomputation racing it.
func TestControlPlaneDesiredVersionSyncer_SyncOnceSkipsWhenGated(t *testing.T) {
	clusterKey := controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	}
	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
	mockDB := databasetesting.NewMockResourcesDBClient()
	now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)

	// Cluster is 5 minutes old.
	createTestHCPClusterWithCustomerVersion(t, ctx, mockDB, "4.19", "stable")
	clusterCRUD := mockDB.HCPClusters(testSubscriptionID, testResourceGroupName)
	existing, err := clusterCRUD.Get(ctx, testClusterName)
	require.NoError(t, err)
	updated := existing.DeepCopy()
	createdAt := now.Add(-5 * time.Minute)
	updated.SystemData = &arm.SystemData{CreatedAt: &createdAt}
	updated.ServiceProviderProperties.ActiveOperationID = "op-create-1"
	_, err = clusterCRUD.Replace(ctx, updated, nil)
	require.NoError(t, err)

	// SPC already has a desired version — gate 1 will not fire.
	createServiceProviderClusterWithActiveAndDesiredVersion(t, ctx, mockDB, semver.MustParse("4.19.15"), ptr.To(semver.MustParse("4.19.22")))

	// Active Create operation pinned to the cluster itself.
	clusterResourceID := api.Must(api.ToClusterResourceID(testSubscriptionID, testResourceGroupName, testClusterName))
	seedClusterCreateOperation(t, ctx, mockDB, clusterResourceID, "op-create-1")

	ctrl := gomock.NewController(t)

	syncer := &controlPlaneDesiredVersionSyncer{
		clock:                clocktesting.NewFakePassiveClock(now),
		readDesireLister:     newValidHostedClusterReadDesireLister(t),
		resourcesDBClient:    mockDB,
		clusterServiceClient: ocm.NewMockClusterServiceClientSpec(ctrl),
		subscriptionLister: &listertesting.SliceSubscriptionLister{Subscriptions: []*arm.Subscription{{
			ResourceID: api.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID)),
			Properties: &arm.SubscriptionProperties{},
		}}},
		activeOperationLister: &listertesting.DBActiveOperationLister{ResourcesDBClient: mockDB},
		cincinnatiClient:      cincinnati.NewMockClient(ctrl),
	}

	require.NoError(t, syncer.SyncOnce(ctx, clusterKey))

	// DesiredVersion is untouched.
	spc, err := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, api.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	require.NotNil(t, spc.Spec.ControlPlaneVersion.DesiredVersion)
	assert.True(t, spc.Spec.ControlPlaneVersion.DesiredVersion.EQ(semver.MustParse("4.19.22")), "DesiredVersion must not change on the skip path")

	// Controller doc was never written, since we returned before WriteController.
	_, getControllerDocErr := mockDB.HCPClusters(testSubscriptionID, testResourceGroupName).
		Controllers(testClusterName).Get(ctx, controlPlaneDesiredVersionControllerName)
	assert.True(t, database.IsNotFoundError(getControllerDocErr), "controller doc must not be written on the skip path, got err=%v", getControllerDocErr)
}
