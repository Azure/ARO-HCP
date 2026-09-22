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

package dataplaneworkloads

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/azure"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/kubeappliercosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	kubeapplierlistertesting "github.com/Azure/ARO-HCP/internal/database/listertesting/kubeapplierlistertesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	testFileCSIOperator = string(azure.ClusterOperatorIdentifierFileCSIDriver)

	testHCName      = "test-hc"
	testHCNamespace = "ocm-arohcppers-abc123"
)

var testMgmtClusterResourceID = metadataapi.Must(azcorearm.ParseResourceID(
	"/providers/microsoft.redhatopenshift/stamps/1/managementclusters/default"))

// --- helpers ----------------------------------------------------------------

func testIdentityResourceID(name string) *azcorearm.ResourceID {
	return metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.ManagedIdentity/userAssignedIdentities/" + name,
	))
}

// testSPCWithMgmtCluster builds a ServiceProviderCluster with
// Status.ManagementClusterResourceID set; all other status fields are zero.
func testSPCWithMgmtCluster(clusterName string, mgmtCluster *azcorearm.ResourceID) *coreapi.ServiceProviderCluster {
	spc := newTestServiceProviderClusterWithIdentities(clusterName, nil, nil)
	spc.Status.ManagementClusterResourceID = mgmtCluster
	return spc
}

// buildFullyResolvedSPCStatus populates both ManagedIdentityDetails and
// ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation so that
// resolveAndGateClientIDs returns without error for all three data-plane slots.
func buildFullyResolvedSPCStatus(
	imageRegistryID, diskID, fileID *azcorearm.ResourceID,
	imageClientID, diskClientID, fileClientID string,
) (
	details map[string]*coreapi.ManagedIdentityMetadata,
	oidc map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus,
) {
	details = map[string]*coreapi.ManagedIdentityMetadata{
		strings.ToLower(imageRegistryID.String()): resolvedARMManagedIdentityMetadata(imageRegistryID, imageClientID, "p1", "t1"),
		strings.ToLower(diskID.String()):          resolvedARMManagedIdentityMetadata(diskID, diskClientID, "p2", "t2"),
		strings.ToLower(fileID.String()):          resolvedARMManagedIdentityMetadata(fileID, fileClientID, "p3", "t3"),
	}

	mkEnsured := func(id *azcorearm.ResourceID, clientID, operatorName string) *coreapi.ManagedIdentityDataplaneOIDCFederationStatus {
		target := coreapi.DataplaneOIDCFederationIdentityInstance{ClientID: clientID, PrincipalID: "p", TenantID: "t"}
		return oidcIdentityStatus(target, operatorName, oidcOperatorEnsured(target, nil))
	}
	oidc = map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
		strings.ToLower(imageRegistryID.String()): mkEnsured(imageRegistryID, imageClientID, testImageRegistryOp),
		strings.ToLower(diskID.String()):          mkEnsured(diskID, diskClientID, testDiskCSIOperator),
		strings.ToLower(fileID.String()):          mkEnsured(fileID, fileClientID, testFileCSIOperator),
	}
	return
}

// buildReadDesireWithHC creates a ReadDesire whose KubeContent.Raw holds
// minimal HostedCluster JSON, satisfying GetCachedHostedClusterForCluster.
func buildReadDesireWithHC(t *testing.T, hcName, hcNamespace string) *kubeapplierapi.ReadDesire {
	t.Helper()

	hcJSON, err := json.Marshal(map[string]interface{}{
		"apiVersion": hostedClusterAPIVersion,
		"kind":       hostedClusterKind,
		"metadata":   map[string]interface{}{"name": hcName, "namespace": hcNamespace},
		"spec": map[string]interface{}{
			"platform": map[string]interface{}{
				"azure": map[string]interface{}{
					"azureAuthenticationConfig": map[string]interface{}{
						"azureAuthenticationConfigType": "ManagedIdentities",
					},
				},
			},
		},
	})
	require.NoError(t, err, "failed to marshal minimal HostedCluster JSON")

	ridStr := kubeapplierapi.ToClusterScopedReadDesireResourceIDString(
		testSubscriptionID, testResourceGroupName, testClusterName,
		kubeapplierhelpers.ReadDesireNameReadonlyHostedCluster,
	)
	rid, err := azcorearm.ParseResourceID(ridStr)
	require.NoError(t, err, "failed to parse ReadDesire resource ID")

	return &kubeapplierapi.ReadDesire{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   rid,
			PartitionKey: strings.ToLower(rid.SubscriptionID),
		},
		Status: kubeapplierapi.ReadDesireStatus{
			KubeContent: &k8sruntime.RawExtension{Raw: hcJSON},
		},
	}
}

// --- TestResolveAndGateClientIDs --------------------------------------------

func TestResolveAndGateClientIDs(t *testing.T) {
	t.Parallel()

	imageRegistryID := testIdentityResourceID("image-registry-mi")
	diskID := testIdentityResourceID("disk-mi")
	fileID := testIdentityResourceID("file-mi")

	imageKey := strings.ToLower(imageRegistryID.String())
	diskKey := strings.ToLower(diskID.String())
	fileKey := strings.ToLower(fileID.String())

	resolved := func(id *azcorearm.ResourceID, clientID, principal string) *coreapi.ManagedIdentityMetadata {
		return resolvedARMManagedIdentityMetadata(id, clientID, principal, "t1")
	}

	ensuredStatus := func(clientID, principal, operatorName string) *coreapi.ManagedIdentityDataplaneOIDCFederationStatus {
		target := coreapi.DataplaneOIDCFederationIdentityInstance{ClientID: clientID, PrincipalID: principal, TenantID: "t1"}
		return oidcIdentityStatus(target, operatorName, oidcOperatorEnsured(target, nil))
	}

	allOperators := map[string]*azcorearm.ResourceID{
		testImageRegistryOp: imageRegistryID,
		testDiskCSIOperator: diskID,
		testFileCSIOperator: fileID,
	}
	fullDetails := map[string]*coreapi.ManagedIdentityMetadata{
		imageKey: resolved(imageRegistryID, "client-img", "p1"),
		diskKey:  resolved(diskID, "client-disk", "p2"),
		fileKey:  resolved(fileID, "client-file", "p3"),
	}
	fullOIDC := map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
		imageKey: ensuredStatus("client-img", "p1", testImageRegistryOp),
		diskKey:  ensuredStatus("client-disk", "p2", testDiskCSIOperator),
		fileKey:  ensuredStatus("client-file", "p3", testFileCSIOperator),
	}

	syncer := &hostedClusterDataPlaneIdentitySyncer{}

	cases := []struct {
		name        string
		dpOperators map[string]*azcorearm.ResourceID
		details     map[string]*coreapi.ManagedIdentityMetadata
		oidc        map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus
		wantErr     bool
		wantIDs     hostedClusterDataPlaneClientIDs
	}{
		{
			name:        "all resolved and OIDC ensured returns clientIDs",
			dpOperators: allOperators,
			details:     fullDetails,
			oidc:        fullOIDC,
			wantIDs: hostedClusterDataPlaneClientIDs{
				ImageRegistryMSIClientID: "client-img",
				DiskMSIClientID:          "client-disk",
				FileMSIClientID:          "client-file",
			},
		},
		{
			name: "operator missing from CustomerProperties returns error",
			dpOperators: map[string]*azcorearm.ResourceID{
				// image-registry absent
				testDiskCSIOperator: diskID,
				testFileCSIOperator: fileID,
			},
			details: fullDetails,
			oidc:    fullOIDC,
			wantErr: true,
		},
		{
			name: "operator has nil ResourceID returns error",
			dpOperators: map[string]*azcorearm.ResourceID{
				testImageRegistryOp: nil,
				testDiskCSIOperator: diskID,
				testFileCSIOperator: fileID,
			},
			details: fullDetails,
			oidc:    fullOIDC,
			wantErr: true,
		},
		{
			name:        "identity missing from ManagedIdentityDetails returns error",
			dpOperators: allOperators,
			details: map[string]*coreapi.ManagedIdentityMetadata{
				// imageRegistry absent
				diskKey: resolved(diskID, "client-disk", "p2"),
				fileKey: resolved(fileID, "client-file", "p3"),
			},
			oidc:    fullOIDC,
			wantErr: true,
		},
		{
			name:        "nil ManagedIdentityDetails entry returns error",
			dpOperators: allOperators,
			details: map[string]*coreapi.ManagedIdentityMetadata{
				imageKey: nil,
				diskKey:  resolved(diskID, "client-disk", "p2"),
				fileKey:  resolved(fileID, "client-file", "p3"),
			},
			oidc:    fullOIDC,
			wantErr: true,
		},
		{
			name:        "nil MetadataFromARMUserAssignedIdentitiesAPI returns error",
			dpOperators: allOperators,
			details: map[string]*coreapi.ManagedIdentityMetadata{
				imageKey: {ResourceID: imageRegistryID}, // ARM metadata nil
				diskKey:  resolved(diskID, "client-disk", "p2"),
				fileKey:  resolved(fileID, "client-file", "p3"),
			},
			oidc:    fullOIDC,
			wantErr: true,
		},
		{
			name:        "RetrievalError set returns error",
			dpOperators: allOperators,
			details: map[string]*coreapi.ManagedIdentityMetadata{
				imageKey: {
					ResourceID: imageRegistryID,
					MetadataFromARMUserAssignedIdentitiesAPI: &coreapi.IdentityMetadataValue{
						RetrievalError: ptr.To("azure api error"),
					},
				},
				diskKey: resolved(diskID, "client-disk", "p2"),
				fileKey: resolved(fileID, "client-file", "p3"),
			},
			oidc:    fullOIDC,
			wantErr: true,
		},
		{
			name:        "nil ClientID returns error",
			dpOperators: allOperators,
			details: map[string]*coreapi.ManagedIdentityMetadata{
				imageKey: {
					ResourceID:                               imageRegistryID,
					MetadataFromARMUserAssignedIdentitiesAPI: &coreapi.IdentityMetadataValue{ClientID: nil},
				},
				diskKey: resolved(diskID, "client-disk", "p2"),
				fileKey: resolved(fileID, "client-file", "p3"),
			},
			oidc:    fullOIDC,
			wantErr: true,
		},
		{
			name:        "OIDC identity not in federation map returns error",
			dpOperators: allOperators,
			details:     fullDetails,
			oidc: map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
				// imageRegistry absent from map
				diskKey: ensuredStatus("client-disk", "p2", testDiskCSIOperator),
				fileKey: ensuredStatus("client-file", "p3", testFileCSIOperator),
			},
			wantErr: true,
		},
		{
			name:        "OIDC operator pending (not ensured) returns error",
			dpOperators: allOperators,
			details:     fullDetails,
			oidc: map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
				imageKey: oidcIdentityStatus(
					coreapi.DataplaneOIDCFederationIdentityInstance{ClientID: "client-img", PrincipalID: "p1", TenantID: "t1"},
					testImageRegistryOp,
					oidcOperatorPending(), // EnsuredIdentity nil — not ensured
				),
				diskKey: ensuredStatus("client-disk", "p2", testDiskCSIOperator),
				fileKey: ensuredStatus("client-file", "p3", testFileCSIOperator),
			},
			wantErr: true,
		},
		{
			// ARM metadata and OIDC TargetIdentity can transiently diverge: if
			// FetchManagedIdentitiesInfo refreshes ARM before
			// DataPlaneOIDCFederationIntent has run to update TargetIdentity, the
			// ARM ClientID is ahead of what OIDC has configured. Writing the new
			// ARM ClientID in that window would set an identity on the HostedCluster
			// for which no FIC exists yet. We must wait until both agree.
			name:        "error when ARM ClientID and OIDC TargetIdentity ClientID diverge",
			dpOperators: allOperators,
			details: map[string]*coreapi.ManagedIdentityMetadata{
				imageKey: resolved(imageRegistryID, "client-img-v2", "p1"), // ARM refreshed to v2
				diskKey:  resolved(diskID, "client-disk", "p2"),
				fileKey:  resolved(fileID, "client-file", "p3"),
			},
			oidc: map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
				// TargetIdentity still at v1 (intent controller has not yet run)
				imageKey: ensuredStatus("client-img-v1", "p1", testImageRegistryOp),
				diskKey:  ensuredStatus("client-disk", "p2", testDiskCSIOperator),
				fileKey:  ensuredStatus("client-file", "p3", testFileCSIOperator),
			},
			wantErr: true, // must wait for OIDC to sync to v2 before writing
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cluster := newTestClusterWithIdentities(t, testClusterName, nil, tc.dpOperators)
			spc := newTestServiceProviderClusterWithIdentities(testClusterName, nil, nil)
			spc.Status.ManagedIdentityDetails = tc.details
			spc.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation = tc.oidc

			got, err := syncer.resolveAndGateClientIDs(cluster, spc)
			if tc.wantErr {
				require.Error(t, err, "expected an error")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantIDs, got)
		})
	}
}

// --- TestBuildDataPlaneIdentityDesire ---------------------------------------

func TestBuildDataPlaneIdentityDesire(t *testing.T) {
	t.Parallel()

	clientIDs := hostedClusterDataPlaneClientIDs{
		ImageRegistryMSIClientID: "client-img",
		DiskMSIClientID:          "client-disk",
		FileMSIClientID:          "client-file",
	}

	desire, err := buildDataPlaneIdentityDesire(
		testSubscriptionID, testResourceGroupName, testClusterName,
		testMgmtClusterResourceID,
		testHCName, testHCNamespace,
		clientIDs,
	)
	require.NoError(t, err)
	require.NotNil(t, desire)

	// Field manager must be the dedicated MI controller name, not the default
	// kube-applier manager, so SSA ownership is cleanly partitioned.
	require.NotNil(t, desire.Spec.ServerSideApply, "ServerSideApply config must be set")
	assert.Equal(t, ptr.To(fieldManagerDataPlaneIdentities), desire.Spec.ServerSideApply.FieldManager,
		"field manager must be %q", fieldManagerDataPlaneIdentities)

	// TargetItem must identify the HostedCluster object precisely.
	target := desire.Spec.TargetItem
	assert.Equal(t, hostedClusterGroup, target.Group, "target group")
	assert.Equal(t, hostedClusterVersion, target.Version, "target version")
	assert.Equal(t, hostedClusterResource, target.Resource, "target resource")
	assert.Equal(t, testHCName, target.Name, "target name")
	assert.Equal(t, testHCNamespace, target.Namespace, "target namespace")

	assert.Equal(t, kubeapplierapi.ApplyDesireTypeServerSideApply, desire.Spec.Type, "desire type")
	assert.Equal(t, HostedClusterDataPlaneIdentitiesControllerName, desire.Tags[kubeapplierapi.TagControllerName],
		"controller name tag")

	// Manifest must contain only the three dataPlane ClientID fields. Any
	// additional field (Location, VnetID, subscriptionID, etc.) would claim
	// SSA ownership of those fields from the base HostedCluster desire.
	require.NotNil(t, desire.Spec.ServerSideApply.KubeContent)
	var manifest map[string]interface{}
	require.NoError(t, json.Unmarshal(desire.Spec.ServerSideApply.KubeContent.Raw, &manifest))

	azure := manifest["spec"].(map[string]interface{})["platform"].(map[string]interface{})["azure"].(map[string]interface{})
	dataPlane := azure["azureAuthenticationConfig"].(map[string]interface{})["managedIdentities"].(map[string]interface{})["dataPlane"].(map[string]interface{})

	assert.Equal(t, "client-img", dataPlane["imageRegistryMSIClientID"], "imageRegistryMSIClientID")
	assert.Equal(t, "client-disk", dataPlane["diskMSIClientID"], "diskMSIClientID")
	assert.Equal(t, "client-file", dataPlane["fileMSIClientID"], "fileMSIClientID")

	// No fields that would bleed ownership from the base desire.
	assert.NotContains(t, azure, "location",
		"location must not be present — would steal ownership from the base desire")
	assert.NotContains(t, azure, "subscriptionID",
		"subscriptionID must not be present")
	managedIdentities := azure["azureAuthenticationConfig"].(map[string]interface{})["managedIdentities"].(map[string]interface{})
	assert.NotContains(t, managedIdentities, "controlPlane",
		"controlPlane must not be present — only owned by the base desire")
}

// --- TestHostedClusterDataPlaneIdentitiesSyncOnce ---------------------------

// testCtx returns a context with a test logger attached, suppressing the
// "no logr.Logger was present" noise that EnsureApplyDesire logs when run
// without a logger in context.
func testCtx(t *testing.T) context.Context {
	t.Helper()
	return utils.ContextWithLogger(context.Background(), testr.New(t))
}

// buildHappyPathSyncer sets up a fully wired syncer with all gates satisfied.
// The returned mockKAClient is used to retrieve stored desires via its CRUD.
func buildHappyPathSyncer(
	t *testing.T,
	ctx context.Context,
	cluster *coreapi.HCPOpenShiftCluster,
	spc *coreapi.ServiceProviderCluster,
	existingDesires []*kubeapplierapi.ApplyDesire,
) (*hostedClusterDataPlaneIdentitySyncer, *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
	t.Helper()

	mockDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, spc})
	require.NoError(t, err)

	mockKAClient := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
	mockKAClients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
	mockKAClients.Register(testMgmtClusterResourceID, mockKAClient)

	readDesire := buildReadDesireWithHC(t, testHCName, testHCNamespace)

	return &hostedClusterDataPlaneIdentitySyncer{
		clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: mockDB},
		serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockDB},
		kubeApplierDBClients:         mockKAClients,
		applyDesireLister:            &kubeapplierlistertesting.SliceApplyDesireLister{Desires: existingDesires},
		readDesireLister:             &kubeapplierlistertesting.SliceReadDesireLister{Desires: []*kubeapplierapi.ReadDesire{readDesire}},
	}, mockKAClient
}

// listApplyDesiresForCluster retrieves all ApplyDesires for the test cluster
// from the mock kube-applier client using the CRUD List operation. This is
// the correct way to read desires — GetAllDocuments() returns raw bytes that
// may not decode correctly because of Cosmos envelope fields.
func listApplyDesiresForCluster(t *testing.T, ctx context.Context, client *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) []*kubeapplierapi.ApplyDesire {
	t.Helper()
	crud, err := client.ApplyDesiresForCluster(testSubscriptionID, testResourceGroupName, testClusterName)
	require.NoError(t, err, "failed to get ApplyDesire CRUD")
	iter, err := crud.List(ctx, nil)
	require.NoError(t, err, "failed to list ApplyDesires")
	var desires []*kubeapplierapi.ApplyDesire
	for _, d := range iter.Items(ctx) {
		desires = append(desires, d)
	}
	require.NoError(t, iter.GetError(), "ApplyDesire iterator error")
	return desires
}

func newFullyResolvedClusterAndSPC(t *testing.T) (*coreapi.HCPOpenShiftCluster, *coreapi.ServiceProviderCluster) {
	t.Helper()

	imageRegistryID := testIdentityResourceID("image-registry-mi")
	diskID := testIdentityResourceID("disk-mi")
	fileID := testIdentityResourceID("file-mi")

	cluster := newTestClusterWithIdentities(t, testClusterName, nil, map[string]*azcorearm.ResourceID{
		testImageRegistryOp: imageRegistryID,
		testDiskCSIOperator: diskID,
		testFileCSIOperator: fileID,
	})
	cluster.ServiceProviderProperties.ClusterServiceID = testClusterServiceID()

	spc := testSPCWithMgmtCluster(testClusterName, testMgmtClusterResourceID)
	details, oidcFederation := buildFullyResolvedSPCStatus(
		imageRegistryID, diskID, fileID, "client-img", "client-disk", "client-file",
	)
	spc.Status.ManagedIdentityDetails = details
	spc.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation = oidcFederation

	return cluster, spc
}

func testKey() controllerutils.HCPClusterKey {
	return controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	}
}

func TestHostedClusterDataPlaneIdentitiesSyncOnceClusterNotFound(t *testing.T) {
	t.Parallel()
	ctx := testCtx(t)

	mockDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, nil)
	require.NoError(t, err)

	syncer := &hostedClusterDataPlaneIdentitySyncer{
		clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: mockDB},
		serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockDB},
		kubeApplierDBClients:         kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients(),
		applyDesireLister:            &kubeapplierlistertesting.SliceApplyDesireLister{},
		readDesireLister:             &kubeapplierlistertesting.SliceReadDesireLister{},
	}
	assert.NoError(t, syncer.SyncOnce(ctx, testKey()),
		"cluster not found must be a silent no-op")
}

func TestHostedClusterDataPlaneIdentitiesSyncOnceSPCNotFound(t *testing.T) {
	t.Parallel()
	ctx := testCtx(t)

	cluster := newTestClusterWithIdentities(t, testClusterName, nil, nil)
	mockDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster})
	require.NoError(t, err)

	syncer := &hostedClusterDataPlaneIdentitySyncer{
		clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: mockDB},
		serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockDB},
		kubeApplierDBClients:         kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients(),
		applyDesireLister:            &kubeapplierlistertesting.SliceApplyDesireLister{},
		readDesireLister:             &kubeapplierlistertesting.SliceReadDesireLister{},
	}
	assert.NoError(t, syncer.SyncOnce(ctx, testKey()),
		"SPC not found must be a silent no-op")
}

func TestHostedClusterDataPlaneIdentitiesSyncOnceManagementClusterNotSet(t *testing.T) {
	t.Parallel()
	ctx := testCtx(t)

	cluster := newTestClusterWithIdentities(t, testClusterName, nil, nil)
	// SPC with no management cluster set.
	spc := newTestServiceProviderClusterWithIdentities(testClusterName, nil, nil)
	mockDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, spc})
	require.NoError(t, err)

	syncer := &hostedClusterDataPlaneIdentitySyncer{
		clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: mockDB},
		serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockDB},
		kubeApplierDBClients:         kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients(),
		applyDesireLister:            &kubeapplierlistertesting.SliceApplyDesireLister{},
		readDesireLister:             &kubeapplierlistertesting.SliceReadDesireLister{},
	}
	assert.NoError(t, syncer.SyncOnce(ctx, testKey()),
		"nil management cluster must be a silent no-op")
}

func TestHostedClusterDataPlaneIdentitiesSyncOnceClusterDeleting(t *testing.T) {
	t.Parallel()
	ctx := testCtx(t)

	cluster, spc := newFullyResolvedClusterAndSPC(t)
	now := metav1.Now()
	cluster.ServiceProviderProperties.DeletionTimestamp = &now

	mockDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, spc})
	require.NoError(t, err)

	mockKAClient := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
	mockKAClients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
	mockKAClients.Register(testMgmtClusterResourceID, mockKAClient)

	syncer := &hostedClusterDataPlaneIdentitySyncer{
		clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: mockDB},
		serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockDB},
		kubeApplierDBClients:         mockKAClients,
		applyDesireLister:            &kubeapplierlistertesting.SliceApplyDesireLister{},
		readDesireLister:             &kubeapplierlistertesting.SliceReadDesireLister{},
	}
	require.NoError(t, syncer.SyncOnce(ctx, testKey()), "deletion path must not error")
	// Empty store + no-op delete: document count stays zero.
	assert.Empty(t, listApplyDesiresForCluster(t, ctx, mockKAClient),
		"no desire document should remain after deletion with empty store")
}

func TestHostedClusterDataPlaneIdentitiesSyncOnceClusterServiceIDMissing(t *testing.T) {
	t.Parallel()
	ctx := testCtx(t)

	cluster, spc := newFullyResolvedClusterAndSPC(t)
	cluster.ServiceProviderProperties.ClusterServiceID = nil

	mockDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, spc})
	require.NoError(t, err)

	syncer := &hostedClusterDataPlaneIdentitySyncer{
		clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: mockDB},
		serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockDB},
		kubeApplierDBClients:         kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients(),
		applyDesireLister:            &kubeapplierlistertesting.SliceApplyDesireLister{},
		readDesireLister:             &kubeapplierlistertesting.SliceReadDesireLister{},
	}
	assert.NoError(t, syncer.SyncOnce(ctx, testKey()),
		"no ClusterServiceID must be a silent no-op")
}

func TestHostedClusterDataPlaneIdentitiesSyncOnceGateFailsWhenClientIDMissing(t *testing.T) {
	t.Parallel()
	ctx := testCtx(t)

	cluster, spc := newFullyResolvedClusterAndSPC(t)
	imageRegistryID := testIdentityResourceID("image-registry-mi")
	// Remove imageRegistry from details so ClientID lookup fails.
	delete(spc.Status.ManagedIdentityDetails, strings.ToLower(imageRegistryID.String()))

	mockDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, spc})
	require.NoError(t, err)

	mockKAClients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
	mockKAClients.Register(testMgmtClusterResourceID, kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient())

	syncer := &hostedClusterDataPlaneIdentitySyncer{
		clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: mockDB},
		serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockDB},
		kubeApplierDBClients:         mockKAClients,
		applyDesireLister:            &kubeapplierlistertesting.SliceApplyDesireLister{},
		readDesireLister:             &kubeapplierlistertesting.SliceReadDesireLister{},
	}
	require.Error(t, syncer.SyncOnce(ctx, testKey()),
		"missing ClientID must return an error so the workqueue retries")
}

func TestHostedClusterDataPlaneIdentitiesSyncOnceGateFailsWhenOIDCNotEnsured(t *testing.T) {
	t.Parallel()
	ctx := testCtx(t)

	cluster, spc := newFullyResolvedClusterAndSPC(t)
	imageRegistryID := testIdentityResourceID("image-registry-mi")
	imageKey := strings.ToLower(imageRegistryID.String())
	// Mark imageRegistry operator as pending (not yet ensured).
	if entry := spc.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation[imageKey]; entry != nil {
		entry.Operators[testImageRegistryOp] = oidcOperatorPending()
	}

	mockDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, spc})
	require.NoError(t, err)

	mockKAClients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
	mockKAClients.Register(testMgmtClusterResourceID, kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient())

	syncer := &hostedClusterDataPlaneIdentitySyncer{
		clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: mockDB},
		serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockDB},
		kubeApplierDBClients:         mockKAClients,
		applyDesireLister:            &kubeapplierlistertesting.SliceApplyDesireLister{},
		readDesireLister:             &kubeapplierlistertesting.SliceReadDesireLister{},
	}
	require.Error(t, syncer.SyncOnce(ctx, testKey()),
		"OIDC not ensured must return an error so the workqueue retries")
}

func TestHostedClusterDataPlaneIdentitiesSyncOnceSilentSkipWhenCachedHCAbsent(t *testing.T) {
	t.Parallel()
	ctx := testCtx(t)

	cluster, spc := newFullyResolvedClusterAndSPC(t)

	mockDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, spc})
	require.NoError(t, err)

	mockKAClient := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
	mockKAClients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
	mockKAClients.Register(testMgmtClusterResourceID, mockKAClient)

	syncer := &hostedClusterDataPlaneIdentitySyncer{
		clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: mockDB},
		serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockDB},
		kubeApplierDBClients:         mockKAClients,
		applyDesireLister:            &kubeapplierlistertesting.SliceApplyDesireLister{},
		readDesireLister:             &kubeapplierlistertesting.SliceReadDesireLister{}, // empty — HC not yet observed
	}
	assert.NoError(t, syncer.SyncOnce(ctx, testKey()),
		"absent cached HC must be a silent no-op, not an error")
	assert.Empty(t, listApplyDesiresForCluster(t, ctx, mockKAClient),
		"no desire should be written when the cached HC is absent")
}

func TestHostedClusterDataPlaneIdentitiesSyncOnceHappyPathCreatesDesire(t *testing.T) {
	t.Parallel()
	ctx := testCtx(t)

	cluster, spc := newFullyResolvedClusterAndSPC(t)
	syncer, mockKAClient := buildHappyPathSyncer(t, ctx, cluster, spc, nil)

	require.NoError(t, syncer.SyncOnce(ctx, testKey()))

	desires := listApplyDesiresForCluster(t, ctx, mockKAClient)
	require.Len(t, desires, 1, "exactly one ApplyDesire must be created")
	created := desires[0]

	assert.Equal(t, kubeapplierapi.ApplyDesireTypeServerSideApply, created.Spec.Type,
		"desire type must be ServerSideApply")
	require.NotNil(t, created.Spec.ServerSideApply)
	assert.Equal(t, ptr.To(fieldManagerDataPlaneIdentities), created.Spec.ServerSideApply.FieldManager,
		"field manager must be aro-hcp-mi-controller, not the default")
	assert.Equal(t, testHCName, created.Spec.TargetItem.Name,
		"target name must match the cached HostedCluster name")
	assert.Equal(t, testHCNamespace, created.Spec.TargetItem.Namespace,
		"target namespace must match the cached HostedCluster namespace")
	assert.Equal(t, hostedClusterResource, created.Spec.TargetItem.Resource,
		"target resource must be hostedclusters")

	// Verify the three ClientIDs are encoded in the manifest.
	require.NotNil(t, created.Spec.ServerSideApply.KubeContent)
	var manifest map[string]interface{}
	require.NoError(t, json.Unmarshal(created.Spec.ServerSideApply.KubeContent.Raw, &manifest))
	dataPlane := manifest["spec"].(map[string]interface{})["platform"].(map[string]interface{})["azure"].(map[string]interface{})["azureAuthenticationConfig"].(map[string]interface{})["managedIdentities"].(map[string]interface{})["dataPlane"].(map[string]interface{})
	assert.Equal(t, "client-img", dataPlane["imageRegistryMSIClientID"])
	assert.Equal(t, "client-disk", dataPlane["diskMSIClientID"])
	assert.Equal(t, "client-file", dataPlane["fileMSIClientID"])
}

func TestHostedClusterDataPlaneIdentitiesSyncOnceIdempotentWhenUnchanged(t *testing.T) {
	t.Parallel()
	ctx := testCtx(t)

	cluster, spc := newFullyResolvedClusterAndSPC(t)
	syncer, mockKAClient := buildHappyPathSyncer(t, ctx, cluster, spc, nil)
	key := testKey()

	// First sync — creates the desire.
	require.NoError(t, syncer.SyncOnce(ctx, key))
	firstDesires := listApplyDesiresForCluster(t, ctx, mockKAClient)
	require.Len(t, firstDesires, 1, "first sync must create one desire")

	// Populate the lister cache with the created desire (simulates the
	// informer catching up after the write).
	syncer.applyDesireLister = &kubeapplierlistertesting.SliceApplyDesireLister{
		Desires: firstDesires,
	}

	// Second sync — EnsureApplyDesire detects no content change and skips.
	require.NoError(t, syncer.SyncOnce(ctx, key))
	assert.Len(t, listApplyDesiresForCluster(t, ctx, mockKAClient), 1,
		"second sync with identical content must not write a new desire")
}
