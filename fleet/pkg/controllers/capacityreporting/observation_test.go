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
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/api/fleetapi"
	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/api/metadataapi"
	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/storage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/storage/fleetcosmosstorage"
	fleetcontrollers "github.com/Azure/ARO-HCP/fleet/pkg/controllers/base"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/fleetcosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/kubeapplierlistertesting"
	capacityreportv1alpha1 "github.com/Azure/ARO-HCP/mgmt-agent/pkg/apis/capacityreport/v1alpha1"
)

func malformedContent() *runtime.RawExtension {
	return &runtime.RawExtension{Raw: []byte(`{"status":`)}
}

type parentReadFailureDB struct {
	fleetcosmosstorage.FleetDBClient
	err error
}

func (database *parentReadFailureDB) Stamps() fleetcosmosstorage.StampsCRUD {
	return &parentReadFailureStamps{StampsCRUD: database.FleetDBClient.Stamps(), err: database.err}
}

type parentReadFailureStamps struct {
	fleetcosmosstorage.StampsCRUD
	err error
}

func (stamps *parentReadFailureStamps) ManagementClusters(stampIdentifier string) fleetcosmosstorage.ManagementClustersCRUD {
	return &parentReadFailureClusters{ManagementClustersCRUD: stamps.StampsCRUD.ManagementClusters(stampIdentifier), err: stamps.err}
}

type parentReadFailureClusters struct {
	fleetcosmosstorage.ManagementClustersCRUD
	err error
}

func (clusters *parentReadFailureClusters) Get(context.Context, string) (*fleetapi.ManagementCluster, error) {
	return nil, clusters.err
}

func TestSchedulingWritersRequireParent(t *testing.T) {
	for _, writer := range []string{"capacity", "scale"} {
		for _, failure := range []string{"missing", "read-error"} {
			for _, existing := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/existing=%t", writer, failure, existing), func(t *testing.T) {
					ctx := t.Context()
					database := fleetcosmosstoragetesting.NewMockFleetDBClient()
					var client fleetcosmosstorage.FleetDBClient = database
					readError := errors.New("injected management cluster read failure")
					if failure == "read-error" {
						managementCluster := testManagementCluster()
						managementCluster.ResourceID = testManagementClusterResourceID()
						managementCluster.PartitionKey = testStampIdentifier
						_, err := database.Stamps().ManagementClusters(testStampIdentifier).Create(ctx, managementCluster, nil)
						require.NoError(t, err)
						client = &parentReadFailureDB{FleetDBClient: database, err: readError}
					}
					crud := database.Stamps().ManagementClusters(testStampIdentifier).Scheduling()
					var before *fleetapi.ManagementClusterScheduling
					if existing {
						var err error
						before, err = fleetcosmosstorage.GetOrCreateManagementClusterScheduling(ctx, database, testStampIdentifier)
						require.NoError(t, err)
						before.Status.ObservedResources.Capacity = corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("8")}
						before, err = crud.Replace(ctx, before, nil)
						require.NoError(t, err)
					}
					lister := &kubeapplierlistertesting.SliceReadDesireLister{Desires: []*kubeapplierapi.ReadDesire{buildTestReadDesire(&capacityreportv1alpha1.CapacityReport{})}}
					var syncer fleetcontrollers.StampSyncer = &capacityReportingSyncer{fleetDBClient: client, readDesireLister: lister}
					if writer == "scale" {
						syncer = &scaleCeilingReportingSyncer{fleetDBClient: client}
					}
					err := syncer.SyncOnce(ctx, testKey())
					if failure == "missing" {
						assert.True(t, cosmosstorageutils.IsNotFoundError(err), "missing parent must return not found")
					} else {
						assert.ErrorIs(t, err, readError)
					}
					after, err := crud.Get(ctx, fleetapi.SchedulingResourceName)
					if existing {
						require.NoError(t, err)
						assert.Equal(t, before, after, "parent read failures must preserve the entire document")
						assert.Equal(t, before.GetEtag(), after.GetEtag(), "parent read failures must not write")
					} else {
						assert.True(t, cosmosstorageutils.IsNotFoundError(err), "missing scheduling must remain absent")
					}
				})
			}
		}
	}
}

type errorReadDesireLister struct {
	*kubeapplierlistertesting.SliceReadDesireLister
	err error
}

func (lister *errorReadDesireLister) GetForManagementCluster(ctx context.Context, stampIdentifier, name string) (*kubeapplierapi.ReadDesire, error) {
	if lister.err != nil {
		return nil, lister.err
	}
	return lister.SliceReadDesireLister.GetForManagementCluster(ctx, stampIdentifier, name)
}

func TestCapacityReadFailuresLeaveSchedulingUnchanged(t *testing.T) {
	malformed := buildTestReadDesire(nil)
	malformed.Status.KubeContent = malformedContent()
	readError := errors.New("injected ReadDesire read failure")
	for _, test := range []struct {
		name      string
		desire    *kubeapplierapi.ReadDesire
		readError error
	}{
		{name: "missing"},
		{name: "absent", desire: buildTestReadDesire(nil)},
		{name: "read-error", readError: readError},
		{name: "malformed", desire: malformed},
	} {
		for _, prior := range []struct {
			name   string
			exists bool
		}{{"initial", false}, {"existing", true}} {
			t.Run(test.name+"/"+prior.name, func(t *testing.T) {
				ctx := t.Context()
				database := fleetcosmosstoragetesting.NewMockFleetDBClient()
				lister := &errorReadDesireLister{SliceReadDesireLister: &kubeapplierlistertesting.SliceReadDesireLister{}}
				syncer := &capacityReportingSyncer{fleetDBClient: database, readDesireLister: lister}
				crud := database.Stamps().ManagementClusters(testStampIdentifier).Scheduling()
				var before *fleetapi.ManagementClusterScheduling
				if prior.exists {
					managementCluster := testManagementCluster()
					managementCluster.ResourceID = testManagementClusterResourceID()
					managementCluster.PartitionKey = testStampIdentifier
					_, err := database.Stamps().ManagementClusters(testStampIdentifier).Create(ctx, managementCluster, nil)
					require.NoError(t, err)
					pending := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/pending"))
					report := &capacityreportv1alpha1.CapacityReport{Status: capacityreportv1alpha1.CapacityReportStatus{
						LastReportedAt: &metav1.Time{Time: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
						Conditions:     []metav1.Condition{{Type: capacityreportv1alpha1.ConditionTypeReportCurrent, Status: metav1.ConditionTrue}},
						HostedControlPlanes: capacityreportv1alpha1.HostedControlPlanes{
							Resources:           &capacityreportv1alpha1.HostedControlPlaneResourceTotals{Usage: withZeroFloor(nil), Requested: withZeroFloor(nil)},
							ReadyResourceIDs:    []string{pending.String() + "-ready"},
							NotReadyResourceIDs: []string{pending.String() + "-not-ready"},
						},
					}}
					lister.Desires = []*kubeapplierapi.ReadDesire{buildTestReadDesire(report)}
					require.NoError(t, syncer.SyncOnce(ctx, testKey()))
					before, err = crud.Get(ctx, fleetapi.SchedulingResourceName)
					require.NoError(t, err)
					before.Status.PendingAssignedClusters = []*azcorearm.ResourceID{pending}
					before, err = crud.Replace(ctx, before, nil)
					require.NoError(t, err)
				}
				lister.Desires, lister.err = nil, test.readError
				if test.desire != nil {
					lister.Desires = []*kubeapplierapi.ReadDesire{test.desire}
				}
				err := syncer.SyncOnce(ctx, testKey())
				switch test.name {
				case "read-error":
					require.ErrorIs(t, err, readError)
				case "malformed":
					var syntaxError *json.SyntaxError
					require.ErrorAs(t, err, &syntaxError)
				default:
					require.NoError(t, err)
				}
				after, err := crud.Get(ctx, fleetapi.SchedulingResourceName)
				if prior.exists {
					require.NoError(t, err)
					require.Equal(t, before, after)
				} else {
					require.True(t, cosmosstorageutils.IsNotFoundError(err))
				}
			})
		}
	}
}

func TestCapacityReadDesireConditionsDoNotGateContent(t *testing.T) {
	for _, test := range []struct {
		name       string
		conditions []metav1.Condition
	}{
		{name: "unset"},
		{name: "unknown", conditions: []metav1.Condition{{Type: kubeapplierapi.ConditionTypeSuccessful, Status: metav1.ConditionUnknown}}},
		{name: "failed", conditions: []metav1.Condition{{Type: kubeapplierapi.ConditionTypeSuccessful, Status: metav1.ConditionFalse}}},
		{name: "degraded", conditions: []metav1.Condition{{Type: kubeapplierapi.ConditionTypeSuccessful, Status: metav1.ConditionTrue}, {Type: kubeapplierapi.ConditionTypeDegraded, Status: metav1.ConditionTrue}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := t.Context()
			report := &capacityreportv1alpha1.CapacityReport{Status: capacityreportv1alpha1.CapacityReportStatus{
				Conditions:          []metav1.Condition{{Type: capacityreportv1alpha1.ConditionTypeReportCurrent, Status: metav1.ConditionTrue}},
				HostedControlPlanes: capacityreportv1alpha1.HostedControlPlanes{Resources: &capacityreportv1alpha1.HostedControlPlaneResourceTotals{Usage: withZeroFloor(nil), Requested: withZeroFloor(nil)}},
			}}
			desire := buildTestReadDesire(report)
			desire.Status.Conditions = test.conditions
			lister := &kubeapplierlistertesting.SliceReadDesireLister{Desires: []*kubeapplierapi.ReadDesire{desire}}
			database := fleetcosmosstoragetesting.NewMockFleetDBClient()
			managementCluster := testManagementCluster()
			managementCluster.ResourceID = testManagementClusterResourceID()
			managementCluster.PartitionKey = testStampIdentifier
			_, createErr := database.Stamps().ManagementClusters(testStampIdentifier).Create(ctx, managementCluster, nil)
			require.NoError(t, createErr)
			syncer := &capacityReportingSyncer{fleetDBClient: database, readDesireLister: lister}
			require.NoError(t, syncer.SyncOnce(ctx, testKey()))
			scheduling, err := database.Stamps().ManagementClusters(testStampIdentifier).Scheduling().Get(ctx, fleetapi.SchedulingResourceName)
			require.NoError(t, err)
			require.True(t, meta.IsStatusConditionTrue(scheduling.Status.Conditions, fleetapi.ConditionTypeCapacityDataCurrent))
		})
	}
}

func TestCapacitySourceFailuresPreserveObservationsUntilRecovery(t *testing.T) {
	for _, test := range []struct {
		name          string
		sourceStatus  metav1.ConditionStatus
		missingTotals bool
		status        metav1.ConditionStatus
		reason        string
	}{
		{"source-failed", metav1.ConditionFalse, false, metav1.ConditionFalse, "ReportNotCurrent"},
		{"source-unknown", metav1.ConditionUnknown, false, metav1.ConditionFalse, "ReportNotCurrent"},
		{"source-unset", "", false, metav1.ConditionFalse, "ReportNotCurrent"},
		{"missing-totals", metav1.ConditionTrue, true, metav1.ConditionUnknown, "MissingResourceTotals"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := t.Context()
			database := fleetcosmosstoragetesting.NewMockFleetDBClient()
			managementCluster := testManagementCluster()
			managementCluster.ResourceID = testManagementClusterResourceID()
			managementCluster.PartitionKey = testStampIdentifier
			_, createErr := database.Stamps().ManagementClusters(testStampIdentifier).Create(ctx, managementCluster, nil)
			require.NoError(t, createErr)
			lister := &kubeapplierlistertesting.SliceReadDesireLister{}
			syncer := &capacityReportingSyncer{fleetDBClient: database, readDesireLister: lister}
			first := metav1.NewTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
			pending := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/pending"))
			report := &capacityreportv1alpha1.CapacityReport{Status: capacityreportv1alpha1.CapacityReportStatus{
				LastReportedAt: &first,
				Conditions:     []metav1.Condition{{Type: capacityreportv1alpha1.ConditionTypeReportCurrent, Status: metav1.ConditionTrue}},
				HostedControlPlanes: capacityreportv1alpha1.HostedControlPlanes{
					Resources:           &capacityreportv1alpha1.HostedControlPlaneResourceTotals{Usage: withZeroFloor(nil), Requested: withZeroFloor(nil)},
					ReadyResourceIDs:    []string{pending.String() + "-ready"},
					NotReadyResourceIDs: []string{pending.String() + "-not-ready"},
				},
			}}
			lister.Desires = []*kubeapplierapi.ReadDesire{buildTestReadDesire(report)}
			require.NoError(t, syncer.SyncOnce(ctx, testKey()))
			crud := database.Stamps().ManagementClusters(testStampIdentifier).Scheduling()
			good, err := crud.Get(ctx, fleetapi.SchedulingResourceName)
			require.NoError(t, err)
			require.True(t, meta.IsStatusConditionTrue(good.Status.Conditions, fleetapi.ConditionTypeCapacityDataCurrent))
			good.Status.PendingAssignedClusters = []*azcorearm.ResourceID{pending}
			good, err = crud.Replace(ctx, good, nil)
			require.NoError(t, err)
			report.Status.HostedControlPlanes.ReadyResourceIDs = []string{pending.String()}
			report.Status.HostedControlPlanes.Resources.Usage[corev1.ResourceCPU] = resource.MustParse("100")
			report.Status.LastReportedAt = &metav1.Time{Time: first.Add(time.Hour)}
			failedReport := report.DeepCopy()
			failedReport.Status.Conditions[0].Status = test.sourceStatus
			if test.sourceStatus == "" {
				failedReport.Status.Conditions = nil
			}
			if test.missingTotals {
				failedReport.Status.HostedControlPlanes.Resources = nil
			}
			lister.Desires = []*kubeapplierapi.ReadDesire{buildTestReadDesire(failedReport)}
			require.NoError(t, syncer.SyncOnce(ctx, testKey()))
			failed, err := crud.Get(ctx, fleetapi.SchedulingResourceName)
			require.NoError(t, err)
			condition := meta.FindStatusCondition(failed.Status.Conditions, fleetapi.ConditionTypeCapacityDataCurrent)
			require.NotNil(t, condition)
			require.Equal(t, test.status, condition.Status)
			require.Equal(t, test.reason, condition.Reason)
			expected := good.DeepCopy()
			meta.SetStatusCondition(&expected.Status.Conditions, *condition)
			require.Equal(t, expected.Status, failed.Status, "source failures update only the condition")
			require.NoError(t, syncer.SyncOnce(ctx, testKey()))
			stable, err := crud.Get(ctx, fleetapi.SchedulingResourceName)
			require.NoError(t, err)
			require.Equal(t, failed, stable, "unchanged source failures do not write")
			lister.Desires = []*kubeapplierapi.ReadDesire{buildTestReadDesire(report)}
			require.NoError(t, syncer.SyncOnce(ctx, testKey()))
			recovered, err := crud.Get(ctx, fleetapi.SchedulingResourceName)
			require.NoError(t, err)
			require.True(t, meta.IsStatusConditionTrue(recovered.Status.Conditions, fleetapi.ConditionTypeCapacityDataCurrent))
			assertResourceListEqual(t, report.Status.HostedControlPlanes.Resources.Usage, recovered.Status.ObservedResources.Usage, "recovered usage")
			require.True(t, report.Status.LastReportedAt.Equal(recovered.Status.ObservedResources.LastReportedAt))
			require.Len(t, recovered.Status.ReadyResourceIDs, 1)
			require.True(t, strings.EqualFold(pending.String(), recovered.Status.ReadyResourceIDs[0].String()), "recovered identity matches the reservation")
			require.Empty(t, recovered.Status.PendingAssignedClusters)
		})
	}
}
