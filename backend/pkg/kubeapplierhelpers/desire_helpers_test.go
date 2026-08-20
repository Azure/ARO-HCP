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

package kubeapplierhelpers

import (
	"context"
	"strings"
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	certificatesv1 "k8s.io/api/certificates/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/json"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/kubeappliercosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/fleetlistertesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/kubeapplierlistertesting"
	"github.com/Azure/ARO-HCP/internal/systemadmincredential"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	testSubscriptionID    = "00000000-0000-0000-0000-000000000000"
	testResourceGroupName = "test-rg"
	testClusterName       = "test-cluster"
	testCredentialName    = "test-cred"
	testControllerName    = "test-controller"
)

var testMCResourceID = metadataapi.Must(azcorearm.ParseResourceID(
	"/subscriptions/mc-sub/resourceGroups/mc-rg/providers/Microsoft.ContainerService/managedClusters/mc-cluster",
))

var testOwnerResourceID = metadataapi.Must(azcorearm.ParseResourceID(
	"/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName +
		"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
		"/systemAdminCredentialRequests/" + testCredentialName,
))

func testCSR(t *testing.T) *certificatesv1.CertificateSigningRequest {
	t.Helper()
	return systemadmincredential.BuildCSR(testOwnerResourceID, testCredentialName, "ocm-test-ns", []byte("fake-csr-pem"))
}

func testTarget() kubeapplierapi.ResourceReference {
	return kubeapplierapi.ResourceReference{
		Group:    "certificates.k8s.io",
		Version:  "v1",
		Resource: "certificatesigningrequests",
		Name:     "system-admin-credential-" + testCredentialName,
	}
}

func newMockDBAndListers(ctx context.Context, t *testing.T, resources []any) (
	*kubeappliercosmosstoragetesting.MockKubeApplierDBClient,
	*kubeapplierlistertesting.DBApplyDesireLister,
	*kubeapplierlistertesting.DBReadDesireLister,
) {
	t.Helper()
	mockDB, err := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClientWithResources(ctx, resources)
	require.NoError(t, err)

	clients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
	clients.Register(testMCResourceID, mockDB)
	mcLister := &fleetlistertesting.SliceManagementClusterLister{
		ManagementClusters: []*fleetapi.ManagementCluster{
			{CosmosMetadata: coreapi.CosmosMetadata{ResourceID: testMCResourceID}, ResourceID: testMCResourceID},
		},
	}

	return mockDB,
		&kubeapplierlistertesting.DBApplyDesireLister{Clients: clients, Lister: mcLister},
		&kubeapplierlistertesting.DBReadDesireLister{Clients: clients, Lister: mcLister}
}

func TestEnsureApplyDesire(t *testing.T) {
	desireName := "test-apply-desire"

	testCases := []struct {
		name            string
		existingDesires []*kubeapplierapi.ApplyDesire
		verifyDB        func(t *testing.T, ctx context.Context, db *kubeappliercosmosstoragetesting.MockKubeApplierDBClient)
		expectError     bool
	}{
		{
			name:            "creates desire when none exists",
			existingDesires: nil,
			verifyDB: func(t *testing.T, ctx context.Context, db *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
				crud, err := db.ApplyDesiresForSystemAdminCredentialRequest(testSubscriptionID, testResourceGroupName, testClusterName, testCredentialName)
				require.NoError(t, err)
				desire, err := crud.Get(ctx, desireName)
				require.NoError(t, err)
				assert.Equal(t, testTarget(), desire.Spec.TargetItem)
			},
		},
		{
			name: "no-op when spec matches",
			existingDesires: []*kubeapplierapi.ApplyDesire{
				func() *kubeapplierapi.ApplyDesire {
					d := buildTestApplyDesire(t, desireName, testCSR(t))
					d.Status = kubeapplierapi.ApplyDesireStatus{
						Conditions: []metav1.Condition{{Type: "Successful", Status: metav1.ConditionTrue}},
					}
					return d
				}(),
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
				crud, err := db.ApplyDesiresForSystemAdminCredentialRequest(testSubscriptionID, testResourceGroupName, testClusterName, testCredentialName)
				require.NoError(t, err)
				desire, err := crud.Get(ctx, desireName)
				require.NoError(t, err)
				assert.NotEmpty(t, desire.Status.Conditions, "status should be preserved when spec matches")
			},
		},
		{
			name: "replaces desire when spec differs",
			existingDesires: []*kubeapplierapi.ApplyDesire{
				func() *kubeapplierapi.ApplyDesire {
					d := buildTestApplyDesire(t, desireName, systemadmincredential.BuildCSR(testOwnerResourceID, testCredentialName, "old-namespace", []byte("old-csr")))
					d.Status = kubeapplierapi.ApplyDesireStatus{
						Conditions: []metav1.Condition{{Type: "Successful", Status: metav1.ConditionTrue}},
					}
					return d
				}(),
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
				crud, err := db.ApplyDesiresForSystemAdminCredentialRequest(testSubscriptionID, testResourceGroupName, testClusterName, testCredentialName)
				require.NoError(t, err)
				desire, err := crud.Get(ctx, desireName)
				require.NoError(t, err)
				assert.Equal(t, testTarget(), desire.Spec.TargetItem)
				assert.Empty(t, desire.Status.Conditions, "status should be cleared on replace")
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

			resources := make([]any, 0, len(tc.existingDesires))
			for _, d := range tc.existingDesires {
				resources = append(resources, d)
			}
			mockDB, applyLister, _ := newMockDBAndListers(ctx, t, resources)

			crud, err := mockDB.ApplyDesiresForSystemAdminCredentialRequest(testSubscriptionID, testResourceGroupName, testClusterName, testCredentialName)
			require.NoError(t, err)

			desire := buildTestApplyDesire(t, desireName, testCSR(t))
			err = EnsureApplyDesire(ctx, crud, applyLister, desire)

			if tc.expectError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			if tc.verifyDB != nil {
				tc.verifyDB(t, ctx, mockDB)
			}
		})
	}
}

// TestEnsureApplyDesireTagsDrift proves that a change confined to the top-level
// .Tags map (spec byte-for-byte identical) is still detected as drift and
// triggers a replacement that persists the new tags — guarding the field the
// spec-only equality check ignores.
func TestEnsureApplyDesireTagsDrift(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	desireName := "test-apply-desire-tags"

	// Existing desire: identical spec, but stale tags and a populated status.
	existing := buildTestApplyDesire(t, desireName, testCSR(t))
	existing.Tags = map[string]string{kubeapplierapi.TagControllerName: testControllerName, "owner": "old"}
	existing.Status = kubeapplierapi.ApplyDesireStatus{
		Conditions: []metav1.Condition{{Type: "Successful", Status: metav1.ConditionTrue}},
	}

	mockDB, applyLister, _ := newMockDBAndListers(ctx, t, []any{existing})
	crud, err := mockDB.ApplyDesiresForSystemAdminCredentialRequest(testSubscriptionID, testResourceGroupName, testClusterName, testCredentialName)
	require.NoError(t, err)

	// Desired: same spec, new tags — so only the tags have drifted.
	desire := buildTestApplyDesire(t, desireName, testCSR(t))
	desire.Tags = map[string]string{kubeapplierapi.TagControllerName: testControllerName, "owner": "new"}
	require.NoError(t, EnsureApplyDesire(ctx, crud, applyLister, desire))

	got, err := crud.Get(ctx, desireName)
	require.NoError(t, err)
	assert.Equal(t, desire.Tags, got.Tags, "tags drift must be persisted onto the stored desire")
	assert.Empty(t, got.Status.Conditions, "status should be cleared on replace triggered by tags drift")
}

func TestEnsureReadDesire(t *testing.T) {
	desireName := "test-read-desire"
	target := testTarget()

	testCases := []struct {
		name            string
		existingDesires []*kubeapplierapi.ReadDesire
		target          kubeapplierapi.ResourceReference
		verifyDB        func(t *testing.T, ctx context.Context, db *kubeappliercosmosstoragetesting.MockKubeApplierDBClient)
		expectError     bool
	}{
		{
			name:            "creates desire when none exists",
			existingDesires: nil,
			target:          target,
			verifyDB: func(t *testing.T, ctx context.Context, db *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
				crud, err := db.ReadDesiresForSystemAdminCredentialRequest(testSubscriptionID, testResourceGroupName, testClusterName, testCredentialName)
				require.NoError(t, err)
				desire, err := crud.Get(ctx, desireName)
				require.NoError(t, err)
				assert.Equal(t, target, desire.Spec.TargetItem)
			},
		},
		{
			name: "no-op when spec matches",
			existingDesires: []*kubeapplierapi.ReadDesire{
				func() *kubeapplierapi.ReadDesire {
					d := buildTestReadDesire(t, desireName, target)
					d.Status = kubeapplierapi.ReadDesireStatus{
						Conditions: []metav1.Condition{{Type: "Successful", Status: metav1.ConditionTrue}},
					}
					return d
				}(),
			},
			target: target,
			verifyDB: func(t *testing.T, ctx context.Context, db *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
				crud, err := db.ReadDesiresForSystemAdminCredentialRequest(testSubscriptionID, testResourceGroupName, testClusterName, testCredentialName)
				require.NoError(t, err)
				desire, err := crud.Get(ctx, desireName)
				require.NoError(t, err)
				assert.NotEmpty(t, desire.Status.Conditions, "status should be preserved when spec matches")
			},
		},
		{
			name: "replaces desire when spec differs",
			existingDesires: []*kubeapplierapi.ReadDesire{
				func() *kubeapplierapi.ReadDesire {
					oldTarget := kubeapplierapi.ResourceReference{
						Group:    "certificates.k8s.io",
						Version:  "v1",
						Resource: "certificatesigningrequests",
						Name:     "old-name",
					}
					d := buildTestReadDesire(t, desireName, oldTarget)
					d.Status = kubeapplierapi.ReadDesireStatus{
						Conditions: []metav1.Condition{{Type: "Successful", Status: metav1.ConditionTrue}},
					}
					return d
				}(),
			},
			target: target,
			verifyDB: func(t *testing.T, ctx context.Context, db *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
				crud, err := db.ReadDesiresForSystemAdminCredentialRequest(testSubscriptionID, testResourceGroupName, testClusterName, testCredentialName)
				require.NoError(t, err)
				desire, err := crud.Get(ctx, desireName)
				require.NoError(t, err)
				assert.Equal(t, target, desire.Spec.TargetItem)
				assert.Empty(t, desire.Status.Conditions, "status should be cleared on replace")
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

			resources := make([]any, 0, len(tc.existingDesires))
			for _, d := range tc.existingDesires {
				resources = append(resources, d)
			}
			mockDB, _, readLister := newMockDBAndListers(ctx, t, resources)

			crud, err := mockDB.ReadDesiresForSystemAdminCredentialRequest(testSubscriptionID, testResourceGroupName, testClusterName, testCredentialName)
			require.NoError(t, err)

			desire := buildTestReadDesire(t, desireName, tc.target)
			err = EnsureReadDesire(ctx, crud, readLister, desire)

			if tc.expectError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			if tc.verifyDB != nil {
				tc.verifyDB(t, ctx, mockDB)
			}
		})
	}
}

// TestEnsureReadDesireTagsDrift proves that a change confined to the top-level
// .Tags map (spec byte-for-byte identical) is still detected as drift and
// triggers a replacement that persists the new tags — guarding the field the
// spec-only equality check ignores.
func TestEnsureReadDesireTagsDrift(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	desireName := "test-read-desire-tags"
	target := testTarget()

	// Existing desire: identical spec, but stale tags and a populated status.
	existing := buildTestReadDesire(t, desireName, target)
	existing.Tags = map[string]string{kubeapplierapi.TagControllerName: testControllerName, "owner": "old"}
	existing.Status = kubeapplierapi.ReadDesireStatus{
		Conditions: []metav1.Condition{{Type: "Successful", Status: metav1.ConditionTrue}},
	}

	mockDB, _, readLister := newMockDBAndListers(ctx, t, []any{existing})
	crud, err := mockDB.ReadDesiresForSystemAdminCredentialRequest(testSubscriptionID, testResourceGroupName, testClusterName, testCredentialName)
	require.NoError(t, err)

	// Desired: same spec, new tags — so only the tags have drifted.
	desire := buildTestReadDesire(t, desireName, target)
	desire.Tags = map[string]string{kubeapplierapi.TagControllerName: testControllerName, "owner": "new"}
	require.NoError(t, EnsureReadDesire(ctx, crud, readLister, desire))

	got, err := crud.Get(ctx, desireName)
	require.NoError(t, err)
	assert.Equal(t, desire.Tags, got.Tags, "tags drift must be persisted onto the stored desire")
	assert.Empty(t, got.Status.Conditions, "status should be cleared on replace triggered by tags drift")
}

// buildTestApplyDesire constructs a credential-request-scoped ApplyDesire the way
// a caller would in its own package — using the dedicated kubeapplierapi scoped
// resource-ID builder rather than any helper on DesireParent (which no longer
// builds desires).
func buildTestApplyDesire(t *testing.T, desireName string, obj systemadmincredential.KubeObject) *kubeapplierapi.ApplyDesire {
	t.Helper()
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		kubeapplierapi.ToSystemAdminCredentialRequestScopedApplyDesireResourceIDString(
			testSubscriptionID, testResourceGroupName, testClusterName, testCredentialName, desireName),
	))
	target := targetRefForKubeObject(obj)

	rawJSON, err := json.Marshal(obj)
	require.NoError(t, err)

	return &kubeapplierapi.ApplyDesire{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(testMCResourceID.String()),
		},
		Spec: kubeapplierapi.ApplyDesireSpec{
			ManagementCluster: testMCResourceID,
			Type:              kubeapplierapi.ApplyDesireTypeServerSideApply,
			TargetItem:        target,
			ServerSideApply: &kubeapplierapi.ServerSideApplyConfig{
				KubeContent: &runtime.RawExtension{Raw: rawJSON},
			},
		},
		Tags: map[string]string{kubeapplierapi.TagControllerName: testControllerName},
	}
}

// buildTestReadDesire constructs a credential-request-scoped ReadDesire the way a
// caller would in its own package.
func buildTestReadDesire(t *testing.T, desireName string, target kubeapplierapi.ResourceReference) *kubeapplierapi.ReadDesire {
	t.Helper()
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		kubeapplierapi.ToSystemAdminCredentialRequestScopedReadDesireResourceIDString(
			testSubscriptionID, testResourceGroupName, testClusterName, testCredentialName, desireName),
	))
	return &kubeapplierapi.ReadDesire{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(testMCResourceID.String()),
		},
		Spec: kubeapplierapi.ReadDesireSpec{
			ManagementCluster: testMCResourceID,
			TargetItem:        target,
		},
		Tags: map[string]string{kubeapplierapi.TagControllerName: testControllerName},
	}
}

// newTestReadDesire builds a ReadDesire for an already-computed resource-ID
// string, letting scope-specific tests pass the exact key from the matching
// kubeapplierapi scoped builder.
func newTestReadDesire(t *testing.T, resourceIDStr string, target kubeapplierapi.ResourceReference, tags map[string]string) *kubeapplierapi.ReadDesire {
	t.Helper()
	return &kubeapplierapi.ReadDesire{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   metadataapi.Must(azcorearm.ParseResourceID(resourceIDStr)),
			PartitionKey: strings.ToLower(testMCResourceID.String()),
		},
		Spec: kubeapplierapi.ReadDesireSpec{
			ManagementCluster: testMCResourceID,
			TargetItem:        target,
		},
		Tags: tags,
	}
}

// TestDesireParentScope asserts that every DesireParent constructor resolves to a
// valid DesireScope, and — critically — that a zero-value DesireParent produces
// an error rather than silently defaulting to a scope. A desire with no declared
// parent is a programming error, so guessing a scope (e.g. cluster) would be
// nonsensical.
func TestDesireParentScope(t *testing.T) {
	const (
		sub        = "00000000-0000-0000-0000-000000000000"
		rg         = "test-rg"
		cluster    = "test-cluster"
		nodePool   = "test-nodepool"
		credReq    = "test-cred"
		revocation = "test-rev"
	)

	t.Run("zero value errors", func(t *testing.T) {
		_, err := DesireParent{}.desireScope(sub, rg, cluster)
		require.Error(t, err, "a zero-value DesireParent has no scope and must error rather than default to one")
	})

	cases := []struct {
		name   string
		parent DesireParent
	}{
		{name: "cluster", parent: ClusterDesireParent()},
		{name: "nodepool", parent: NodePoolDesireParent(nodePool)},
		{name: "credentialRequest", parent: CredentialRequestDesireParent(credReq)},
		{name: "revocation", parent: RevocationDesireParent(revocation)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.parent.desireScope(sub, rg, cluster)
			require.NoError(t, err, "a constructor-built DesireParent must resolve to a valid scope")
		})
	}
}

// TestEnsureDesireScopes exercises EnsureReadDesire end-to-end for the cluster
// and node-pool parents that this refactor newly routes through the generic
// helper, proving the DesireScope-derived CRUD, the generated resource ID, and
// the lister-based GetByResourceID lookup all agree for each scope.
func TestEnsureDesireScopes(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	const nodePoolName = "test-nodepool"
	target := testTarget()

	t.Run("cluster scope creates then no-ops on re-run", func(t *testing.T) {
		mockDB, _, readLister := newMockDBAndListers(ctx, t, nil)
		crud, err := mockDB.ReadDesiresForCluster(testSubscriptionID, testResourceGroupName, testClusterName)
		require.NoError(t, err)

		desire := newTestReadDesire(t,
			kubeapplierapi.ToClusterScopedReadDesireResourceIDString(testSubscriptionID, testResourceGroupName, testClusterName, "clusterscoped"),
			target, map[string]string{kubeapplierapi.TagControllerName: testControllerName})
		require.NoError(t, EnsureReadDesire(ctx, crud, readLister, desire))

		got, err := crud.Get(ctx, "clusterscoped")
		require.NoError(t, err)
		assert.Equal(t, target, got.Spec.TargetItem)
		assert.Equal(t,
			kubeapplierapi.ToClusterScopedReadDesireResourceIDString(testSubscriptionID, testResourceGroupName, testClusterName, "clusterscoped"),
			strings.ToLower(got.ResourceID.String()))

		// The DB-backed lister now observes the created desire, so a second
		// reconcile must be a no-op (no error, still exactly the same desire).
		require.NoError(t, EnsureReadDesire(ctx, crud, readLister, desire))
	})

	t.Run("node pool scope creates a node-pool-scoped desire with tags", func(t *testing.T) {
		mockDB, _, readLister := newMockDBAndListers(ctx, t, nil)
		crud, err := mockDB.ReadDesiresForNodePool(testSubscriptionID, testResourceGroupName, testClusterName, nodePoolName)
		require.NoError(t, err)
		tags := map[string]string{kubeapplierapi.TagControllerName: testControllerName, "owner": "nodepool"}

		desire := newTestReadDesire(t,
			kubeapplierapi.ToNodePoolScopedReadDesireResourceIDString(testSubscriptionID, testResourceGroupName, testClusterName, nodePoolName, "nodepoolscoped"),
			target, tags)
		require.NoError(t, EnsureReadDesire(ctx, crud, readLister, desire))

		got, err := crud.Get(ctx, "nodepoolscoped")
		require.NoError(t, err)
		assert.Equal(t, target, got.Spec.TargetItem)
		assert.Equal(t, tags, got.Tags, "tags must be stamped onto the desire")
		assert.Equal(t,
			kubeapplierapi.ToNodePoolScopedReadDesireResourceIDString(testSubscriptionID, testResourceGroupName, testClusterName, nodePoolName, "nodepoolscoped"),
			strings.ToLower(got.ResourceID.String()),
			"desire must be nested under the node pool")
	})
}

func targetRefForKubeObject(obj systemadmincredential.KubeObject) kubeapplierapi.ResourceReference {
	gvk := obj.GetObjectKind().GroupVersionKind()
	resource := strings.ToLower(gvk.Kind) + "s"
	return kubeapplierapi.ResourceReference{
		Group:     gvk.Group,
		Version:   gvk.Version,
		Resource:  resource,
		Namespace: obj.GetNamespace(),
		Name:      obj.GetName(),
	}
}
