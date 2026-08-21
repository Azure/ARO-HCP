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

package capacityreporting

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	fleetcontrollers "github.com/Azure/ARO-HCP/fleet/pkg/controllers/base"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/kubeappliercosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/fleetcosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/kubeappliercosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/kubeapplierlistertesting"
	capacityreportv1alpha1 "github.com/Azure/ARO-HCP/mgmt-agent/pkg/apis/capacityreport/v1alpha1"
)

const testStampIdentifier = "eastus"

func testKey() fleetcontrollers.StampKey {
	return fleetcontrollers.StampKey{StampIdentifier: testStampIdentifier}
}

func testManagementClusterResourceID() *azcorearm.ResourceID {
	return metadataapi.Must(fleetapi.ToManagementClusterResourceID(testStampIdentifier))
}

func buildTestReadDesire(report *capacityreportv1alpha1.CapacityReport) *kubeapplierapi.ReadDesire {
	desireIDString := kubeapplierapi.ToManagementClusterScopedReadDesireResourceIDString(testStampIdentifier, ReadDesireName)
	desired := controllerutil.BuildReadDesire(desireIDString, testManagementClusterResourceID(), CapacityReportTarget)
	if report != nil {
		raw, _ := json.Marshal(report)
		desired.Status.KubeContent = &runtime.RawExtension{Raw: raw}
	}
	return desired
}

// --- EnsureReadDesire tests ---

func TestEnsureReadDesire_NilClient(t *testing.T) {
	clients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
	syncer := &ensureReadDesireSyncer{kubeApplierDBClients: clients}

	err := syncer.SyncOnce(context.Background(), testKey())
	require.NoError(t, err)
}

func TestEnsureReadDesire_CreatesReadDesireOnFirstCall(t *testing.T) {
	mockClient := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
	clients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
	clients.Register(testManagementClusterResourceID(), mockClient)

	syncer := &ensureReadDesireSyncer{kubeApplierDBClients: clients}

	err := syncer.SyncOnce(context.Background(), testKey())
	require.NoError(t, err)

	crud, err := mockClient.ReadDesiresForManagementCluster(testStampIdentifier)
	require.NoError(t, err)

	existing, err := crud.Get(context.Background(), ReadDesireName)
	require.NoError(t, err)
	assert.Equal(t, CapacityReportTarget, existing.Spec.TargetItem)
}

func TestEnsureReadDesire_UpdatesStaleSpec(t *testing.T) {
	mockClient := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
	clients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
	clients.Register(testManagementClusterResourceID(), mockClient)

	staleTarget := kubeapplierapi.ResourceReference{
		Group:    "old.group",
		Version:  "v1",
		Resource: "oldresources",
		Name:     "old",
	}
	desireIDString := kubeapplierapi.ToManagementClusterScopedReadDesireResourceIDString(testStampIdentifier, ReadDesireName)
	stale := controllerutil.BuildReadDesire(desireIDString, testManagementClusterResourceID(), staleTarget)

	crud, err := mockClient.ReadDesiresForManagementCluster(testStampIdentifier)
	require.NoError(t, err)
	_, err = crud.Create(context.Background(), stale, nil)
	require.NoError(t, err)

	syncer := &ensureReadDesireSyncer{kubeApplierDBClients: clients}

	err = syncer.SyncOnce(context.Background(), testKey())
	require.NoError(t, err)

	updated, err := crud.Get(context.Background(), ReadDesireName)
	require.NoError(t, err)
	assert.Equal(t, CapacityReportTarget, updated.Spec.TargetItem)
}

func TestEnsureReadDesire_ConflictOnCreateIsSwallowed(t *testing.T) {
	clients := &conflictOnCreateDBClients{}
	syncer := &ensureReadDesireSyncer{kubeApplierDBClients: clients}

	err := syncer.SyncOnce(context.Background(), testKey())
	require.NoError(t, err)
}

// --- CapacityReporting SyncOnce tests ---

func TestSyncOnce_NotFoundReadDesire(t *testing.T) {
	lister := &kubeapplierlistertesting.SliceReadDesireLister{}
	syncer := &capacityReportingSyncer{
		readDesireLister: lister,
	}
	err := syncer.SyncOnce(context.Background(), testKey())
	require.NoError(t, err)
}

func TestSyncOnce_NilKubeContent(t *testing.T) {
	desire := buildTestReadDesire(nil)
	lister := &kubeapplierlistertesting.SliceReadDesireLister{
		Desires: []*kubeapplierapi.ReadDesire{desire},
	}
	syncer := &capacityReportingSyncer{
		readDesireLister: lister,
	}
	err := syncer.SyncOnce(context.Background(), testKey())
	require.NoError(t, err)
}

func TestSyncOnce_WritesCapacityToScheduling(t *testing.T) {
	ctx := context.Background()

	report := &capacityreportv1alpha1.CapacityReport{
		Status: capacityreportv1alpha1.CapacityReportStatus{
			LastReportedAt: &metav1.Time{Time: fixedTime},
			Nodes: []capacityreportv1alpha1.NodeSKUCapacity{
				{
					SKU:   "Standard_D8ds_v5",
					Ready: 1,
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("8"),
						corev1.ResourceMemory: resource.MustParse("32Gi"),
					},
				},
			},
			Conditions: []metav1.Condition{
				{
					Type:   capacityreportv1alpha1.ConditionTypeReportCurrent,
					Status: metav1.ConditionTrue,
				},
			},
		},
	}

	desire := buildTestReadDesire(report)
	lister := &kubeapplierlistertesting.SliceReadDesireLister{
		Desires: []*kubeapplierapi.ReadDesire{desire},
	}

	fleetDB := fleetcosmosstoragetesting.NewMockFleetDBClient()

	syncer := &capacityReportingSyncer{
		fleetDBClient:    fleetDB,
		readDesireLister: lister,
	}

	err := syncer.SyncOnce(ctx, testKey())
	require.NoError(t, err)

	schedulingCRUD := fleetDB.Stamps().ManagementClusters(testStampIdentifier).Scheduling()
	scheduling, err := schedulingCRUD.Get(ctx, fleetapi.SchedulingResourceName)
	require.NoError(t, err)

	require.NotNil(t, scheduling.Status.ObservedResources.LastReportedAt, "LastReportedAt must be set")
	assert.True(t, fixedTime.Equal(scheduling.Status.ObservedResources.LastReportedAt.Time), "LastReportedAt")
	assertResourceListEqual(t, corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("8"),
		corev1.ResourceMemory: resource.MustParse("32Gi"),
	}, scheduling.Status.ObservedResources.Capacity, "ObservedResources.Capacity")

	condition := meta.FindStatusCondition(scheduling.Status.Conditions, fleetapi.ConditionTypeCapacityDataCurrent)
	require.NotNil(t, condition, "CapacityDataCurrent condition must exist")
	assert.Equal(t, metav1.ConditionTrue, condition.Status, "condition status")
	assert.Equal(t, "DataCollected", condition.Reason, "condition reason")
}

// --- Test doubles for conflict-on-create scenario ---

// conflictOnCreateDBClients implements KubeApplierDBClients, returning a
// client whose ReadDesiresForManagementCluster CRUD returns NotFound on Get
// and Conflict on Create — simulating a race where another controller wins
// the create.
type conflictOnCreateDBClients struct{}

func (c *conflictOnCreateDBClients) For(_ context.Context, _ *azcorearm.ResourceID) kubeappliercosmosstorage.KubeApplierDBClient {
	return &conflictOnCreateDBClient{}
}

type conflictOnCreateDBClient struct {
	kubeappliercosmosstorage.KubeApplierDBClient // embedded nil — only ReadDesiresForManagementCluster is called
}

func (c *conflictOnCreateDBClient) ReadDesiresForManagementCluster(_ string) (cosmosstorageutils.ResourceCRUD[kubeapplierapi.ReadDesire, *kubeapplierapi.ReadDesire], error) {
	return &notFoundThenConflictCRUD{}, nil
}

type notFoundThenConflictCRUD struct {
	cosmosstorageutils.ResourceCRUD[kubeapplierapi.ReadDesire, *kubeapplierapi.ReadDesire] // embedded nil — only Get and Create are called
}

func (c *notFoundThenConflictCRUD) Get(_ context.Context, _ string) (*kubeapplierapi.ReadDesire, error) {
	return nil, cosmosstorageutils.NewNotFoundError()
}

func (c *notFoundThenConflictCRUD) Create(_ context.Context, _ *kubeapplierapi.ReadDesire, _ *azcosmos.ItemOptions) (*kubeapplierapi.ReadDesire, error) {
	return nil, &azcore.ResponseError{StatusCode: http.StatusConflict}
}
