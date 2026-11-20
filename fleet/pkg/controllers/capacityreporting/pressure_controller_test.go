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
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"

	fleetcontrollers "github.com/Azure/ARO-HCP/fleet/pkg/controllers/base"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/fleetcosmosstoragetesting"
)

type pressureRequeue struct{ delays []time.Duration }

func (q *pressureRequeue) EnqueueAfter(_ any, delay time.Duration) {
	q.delays = append(q.delays, delay)
}

func TestPressurePersistenceAndTransitions(t *testing.T) {
	ctx := t.Context()
	doc := testSchedulingDoc()
	doc.Status = validPressureStatus()
	db, err := fleetcosmosstoragetesting.NewMockFleetDBClientWithResources(ctx, []any{testManagementCluster(), doc})
	require.NoError(t, err)
	clock := clocktesting.NewFakePassiveClock(pressureTestNow)
	key := fleetcontrollers.StampKey{StampIdentifier: scaleCeilingTestStampIdentifier}
	crud := db.Stamps().ManagementClusters(key.StampIdentifier).Scheduling()
	newSyncer := func() *pressureSyncer {
		return &pressureSyncer{fleetDBClient: db, clock: clock, settings: defaultPressureSettings()}
	}
	syncer := newSyncer()
	get := func() *fleetapi.ManagementClusterScheduling {
		value, err := crud.Get(ctx, fleetapi.SchedulingResourceName)
		require.NoError(t, err)
		return value
	}
	reconcile := func() metav1.Condition {
		require.NoError(t, syncer.SyncOnce(ctx, key))
		condition := meta.FindStatusCondition(get().Status.Conditions, fleetapi.ConditionTypePressure)
		require.NotNil(t, condition)
		return *condition
	}
	initial := reconcile()
	require.Equal(t, metav1.ConditionFalse, initial.Status)
	require.True(t, initial.LastTransitionTime.Equal(&metav1.Time{Time: pressureTestNow}))
	first := get()
	clock.SetTime(pressureTestNow.Add(time.Minute))
	syncer = newSyncer() // A restarted process has no in-memory transition history.
	repeated := reconcile()
	require.Equal(t, initial.LastTransitionTime, repeated.LastTransitionTime)
	require.Equal(t, first.CosmosETag, get().CosmosETag, "repeated unchanged observations converge without another Cosmos event")

	underPressure := get()
	underPressure.Status.ObservedResources.Usage[corev1.ResourceCPU] = resource.MustParse("95")
	_, err = crud.Replace(ctx, underPressure, nil)
	require.NoError(t, err)
	pressure := reconcile()
	require.Equal(t, metav1.ConditionTrue, pressure.Status)
	require.True(t, pressure.LastTransitionTime.After(initial.LastTransitionTime.Time))

	clock.SetTime(pressureTestNow.Add(2 * time.Minute))
	failed := get()
	meta.FindStatusCondition(failed.Status.Conditions, fleetapi.ConditionTypeCapacityDataCurrent).Status = metav1.ConditionFalse
	_, err = crud.Replace(ctx, failed, nil)
	require.NoError(t, err)
	unknown := reconcile()
	require.Equal(t, metav1.ConditionUnknown, unknown.Status, "a failed collection overrides recent retained measurements")
	require.Equal(t, "CapacityCollectionNotCurrent", unknown.Reason)
	require.True(t, unknown.LastTransitionTime.After(pressure.LastTransitionTime.Time))
	// The three-state contract must not let Unknown count as regional pressure.
	require.False(t, meta.IsStatusConditionTrue(get().Status.Conditions, fleetapi.ConditionTypePressure))

	clock.SetTime(pressureTestNow.Add(3 * time.Minute))
	changedReason := get()
	meta.FindStatusCondition(changedReason.Status.Conditions, fleetapi.ConditionTypeCapacityDataCurrent).Status = metav1.ConditionTrue
	changedReason.Status.ScaleCeiling.LastReportedAt = nil
	_, err = crud.Replace(ctx, changedReason, nil)
	require.NoError(t, err)
	syncer = newSyncer()
	stillUnknown := reconcile()
	require.Equal(t, metav1.ConditionUnknown, stillUnknown.Status)
	require.NotEqual(t, unknown.Reason, stillUnknown.Reason)
	require.Equal(t, unknown.LastTransitionTime, stillUnknown.LastTransitionTime, "reason changes and restart preserve the status transition time")

	for _, want := range []metav1.ConditionStatus{metav1.ConditionFalse, metav1.ConditionTrue} {
		clock.SetTime(clock.Now().Add(time.Minute))
		recovered := get()
		now := metav1.NewTime(clock.Now())
		recovered.Status.ScaleCeiling.LastReportedAt, recovered.Status.ObservedResources.LastReportedAt = &now, &now
		cpu := "0"
		if want == metav1.ConditionTrue {
			cpu = "95"
		}
		recovered.Status.ObservedResources.Usage[corev1.ResourceCPU] = resource.MustParse(cpu)
		_, err = crud.Replace(ctx, recovered, nil)
		require.NoError(t, err)
		condition := reconcile()
		require.Equal(t, want, condition.Status)
		require.True(t, condition.LastTransitionTime.Equal(&now))
	}
}

func TestPressureExpiryWithoutDocumentChanges(t *testing.T) {
	ctx := t.Context()
	doc := testSchedulingDoc()
	doc.Status = validPressureStatus()
	db, err := fleetcosmosstoragetesting.NewMockFleetDBClientWithResources(ctx, []any{testManagementCluster(), doc})
	require.NoError(t, err)
	clock := clocktesting.NewFakePassiveClock(pressureTestNow)
	queue := &pressureRequeue{}
	syncer := &pressureSyncer{fleetDBClient: db, clock: clock, settings: defaultPressureSettings(), enqueueAfter: queue}
	key := fleetcontrollers.StampKey{StampIdentifier: scaleCeilingTestStampIdentifier}
	require.NoError(t, syncer.SyncOnce(ctx, key))
	require.Contains(t, queue.delays, 5*time.Minute+time.Nanosecond, "expiry must enqueue even without another observation")
	clock.SetTime(pressureTestNow.Add(5*time.Minute + time.Second))
	require.NoError(t, syncer.SyncOnce(ctx, key))
	stored, err := db.Stamps().ManagementClusters(key.StampIdentifier).Scheduling().Get(ctx, fleetapi.SchedulingResourceName)
	require.NoError(t, err)
	condition := meta.FindStatusCondition(stored.Status.Conditions, fleetapi.ConditionTypePressure)
	require.Equal(t, metav1.ConditionUnknown, condition.Status)
	require.Equal(t, "StaleCapacityReport", condition.Reason)
	require.True(t, pressureTestNow.Equal(stored.Status.ObservedResources.LastReportedAt.Time), "condition writes do not refresh observation age")
}

func TestPressureMissingInputsAndAbsentManagementCluster(t *testing.T) {
	for _, present := range []bool{false, true} {
		t.Run(map[bool]string{false: "absent MC", true: "missing scheduling inputs"}[present], func(t *testing.T) {
			ctx := t.Context()
			db := fleetcosmosstoragetesting.NewMockFleetDBClient()
			if present {
				_, err := db.Stamps().ManagementClusters(scaleCeilingTestStampIdentifier).Create(ctx, testManagementCluster(), nil)
				require.NoError(t, err)
			}
			syncer := &pressureSyncer{fleetDBClient: db, clock: clocktesting.NewFakePassiveClock(pressureTestNow), settings: defaultPressureSettings()}
			require.NoError(t, syncer.SyncOnce(ctx, fleetcontrollers.StampKey{StampIdentifier: scaleCeilingTestStampIdentifier}))
			stored, err := db.Stamps().ManagementClusters(scaleCeilingTestStampIdentifier).Scheduling().Get(ctx, fleetapi.SchedulingResourceName)
			if !present {
				require.True(t, cosmosstorageutils.IsNotFoundError(err))
				return
			}
			require.NoError(t, err)
			require.True(t, meta.IsStatusConditionPresentAndEqual(stored.Status.Conditions, fleetapi.ConditionTypePressure, metav1.ConditionUnknown))
		})
	}
}
