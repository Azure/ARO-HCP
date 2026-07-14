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
	parent := CredentialRequestDesireParent(testCredentialName)

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

			err = EnsureApplyDesire(ctx, crud, applyLister, parent,
				testSubscriptionID, testResourceGroupName, testClusterName, desireName,
				testMCResourceID, testTarget(), testCSR(t), nil)

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

func TestEnsureReadDesire(t *testing.T) {
	desireName := "test-read-desire"
	parent := CredentialRequestDesireParent(testCredentialName)
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

			err = EnsureReadDesire(ctx, crud, readLister, parent,
				testSubscriptionID, testResourceGroupName, testClusterName, desireName,
				testMCResourceID, tc.target)

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

func buildTestApplyDesire(t *testing.T, desireName string, obj systemadmincredential.KubeObject) *kubeapplierapi.ApplyDesire {
	t.Helper()
	parent := CredentialRequestDesireParent(testCredentialName)
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		parent.applyDesireResourceIDString(testSubscriptionID, testResourceGroupName, testClusterName, desireName),
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
	}
}

func buildTestReadDesire(t *testing.T, desireName string, target kubeapplierapi.ResourceReference) *kubeapplierapi.ReadDesire {
	t.Helper()
	parent := CredentialRequestDesireParent(testCredentialName)
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		parent.readDesireResourceIDString(testSubscriptionID, testResourceGroupName, testClusterName, desireName),
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
	}
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
