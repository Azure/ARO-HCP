// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License 2.0.

package capacityreporting

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/fleetcosmosstoragetesting"
)

func TestScaleCeilingConverges(t *testing.T) {
	ctx := t.Context()
	db := fleetcosmosstoragetesting.NewMockFleetDBClient()
	s := &scaleCeilingReportingSyncer{fleetDBClient: db}
	crud := db.Stamps().ManagementClusters(scaleCeilingTestStampIdentifier).Scheduling()
	first := metav1.NewTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	capacity := corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2")}
	require.NoError(t, s.persistMaxCapacity(ctx, scaleCeilingTestStampIdentifier, capacity, &first))
	initial, err := crud.Get(ctx, fleetapi.SchedulingResourceName)
	require.NoError(t, err)
	require.NotEmpty(t, initial.CosmosETag)
	next := metav1.NewTime(first.Add(time.Hour))
	capacity[corev1.ResourceCPU] = resource.MustParse("2000m")
	require.NoError(t, s.persistMaxCapacity(ctx, scaleCeilingTestStampIdentifier, capacity, &next))
	stable, err := crud.Get(ctx, fleetapi.SchedulingResourceName)
	require.NoError(t, err)
	require.Equal(t, initial.CosmosETag, stable.CosmosETag, "a new wall clock alone must not publish another report")
	require.Equal(t, initial.InstanceVersion, stable.InstanceVersion)
	require.True(t, first.Equal(stable.Status.ScaleCeiling.LastReportedAt))
	// Changes owned by other actors are preserved and do not cause a rewrite.
	stable.Status.ObservedResources.Capacity = corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1")}
	other, err := crud.Replace(ctx, stable, nil)
	require.NoError(t, err)
	require.NoError(t, s.persistMaxCapacity(ctx, scaleCeilingTestStampIdentifier, capacity, &next))
	unchanged, err := crud.Get(ctx, fleetapi.SchedulingResourceName)
	require.NoError(t, err)
	require.Equal(t, other.CosmosETag, unchanged.CosmosETag)
	// Capacity changes publish a new timestamp, but keep condition transition
	// time when the boolean condition itself has not changed.
	capacity[corev1.ResourceCPU] = resource.MustParse("3")
	require.NoError(t, s.persistMaxCapacity(ctx, scaleCeilingTestStampIdentifier, capacity, &next))
	changed, err := crud.Get(ctx, fleetapi.SchedulingResourceName)
	require.NoError(t, err)
	require.NotEqual(t, unchanged.CosmosETag, changed.CosmosETag)
	require.True(t, next.Equal(changed.Status.ScaleCeiling.LastReportedAt))
	require.True(t, first.Equal(&meta.FindStatusCondition(changed.Status.Conditions, fleetapi.ConditionTypeScalingDataCurrent).LastTransitionTime))
	require.Equal(t, int64(1), changed.Status.ObservedResources.Capacity.Cpu().Value())
	require.NoError(t, s.persistMaxCapacity(ctx, scaleCeilingTestStampIdentifier, capacity, &next))
	settled, err := crud.Get(ctx, fleetapi.SchedulingResourceName)
	require.NoError(t, err)
	require.Equal(t, changed.CosmosETag, settled.CosmosETag)
	// Recovering a false condition must persist even if the numeric capacity
	// remains unchanged, then converge on the next reconciliation.
	meta.SetStatusCondition(&settled.Status.Conditions, metav1.Condition{Type: fleetapi.ConditionTypeScalingDataCurrent, Status: metav1.ConditionFalse, Reason: "Stale", LastTransitionTime: next})
	_, err = crud.Replace(ctx, settled, nil)
	require.NoError(t, err)
	later := metav1.NewTime(next.Add(time.Hour))
	require.NoError(t, s.persistMaxCapacity(ctx, scaleCeilingTestStampIdentifier, capacity, &later))
	recovered, err := crud.Get(ctx, fleetapi.SchedulingResourceName)
	require.NoError(t, err)
	require.True(t, later.Equal(recovered.Status.ScaleCeiling.LastReportedAt))
	require.True(t, later.Equal(&meta.FindStatusCondition(recovered.Status.Conditions, fleetapi.ConditionTypeScalingDataCurrent).LastTransitionTime))
	// Legacy documents with matching data but no timestamp are initialized once.
	recovered.Status.ScaleCeiling.LastReportedAt = nil
	_, err = crud.Replace(ctx, recovered, nil)
	require.NoError(t, err)
	require.NoError(t, s.persistMaxCapacity(ctx, scaleCeilingTestStampIdentifier, capacity, &later))
	initialized, err := crud.Get(ctx, fleetapi.SchedulingResourceName)
	require.NoError(t, err)
	require.True(t, later.Equal(initialized.Status.ScaleCeiling.LastReportedAt))
	require.NoError(t, s.persistMaxCapacity(ctx, scaleCeilingTestStampIdentifier, capacity, &next))
	last, err := crud.Get(ctx, fleetapi.SchedulingResourceName)
	require.NoError(t, err)
	require.Equal(t, initialized.CosmosETag, last.CosmosETag)
}
