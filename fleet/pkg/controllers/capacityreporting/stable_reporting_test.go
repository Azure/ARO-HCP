// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License 2.0.

package capacityreporting

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/fleetcosmosstoragetesting"
)

func TestCapacityReportingConverges(t *testing.T) {
	ctx := t.Context()
	db := fleetcosmosstoragetesting.NewMockFleetDBClient()
	s := &capacityReportingSyncer{fleetDBClient: db}
	crud := db.Stamps().ManagementClusters(testStampIdentifier).Scheduling()
	first := metav1.NewTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	observed := fleetapi.ObservedResources{LastReportedAt: &first, Capacity: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2")}}
	condition := metav1.Condition{Type: fleetapi.ConditionTypeCapacityDataCurrent, Status: metav1.ConditionTrue, Reason: "DataCollected", LastTransitionTime: first}
	a := "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/a"
	b := "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/b"
	require.NoError(t, s.persistObservedResources(ctx, testStampIdentifier, observed, []string{b, a}, nil, condition))
	initial, err := crud.Get(ctx, fleetapi.SchedulingResourceName)
	require.NoError(t, err)
	require.NotEmpty(t, initial.CosmosETag)
	// Equivalent quantities, order, casing and duplicates must not create a
	// new version/changefeed event. ETag checks the actual persistence boundary.
	observed.Capacity[corev1.ResourceCPU] = resource.MustParse("2000m")
	require.NoError(t, s.persistObservedResources(ctx, testStampIdentifier, observed, []string{strings.ToUpper(a), b, a}, []string{}, condition))
	stable, err := crud.Get(ctx, fleetapi.SchedulingResourceName)
	require.NoError(t, err)
	require.Equal(t, initial.CosmosETag, stable.CosmosETag)
	require.Equal(t, initial.InstanceVersion, stable.InstanceVersion)
	// Source report freshness must still advance even when capacity is equal.
	next := metav1.NewTime(first.Add(time.Minute))
	observed.LastReportedAt = &next
	require.NoError(t, s.persistObservedResources(ctx, testStampIdentifier, observed, []string{a, b}, nil, condition))
	fresh, err := crud.Get(ctx, fleetapi.SchedulingResourceName)
	require.NoError(t, err)
	require.NotEqual(t, stable.CosmosETag, fresh.CosmosETag)
	require.True(t, next.Equal(fresh.Status.ObservedResources.LastReportedAt))
	require.True(t, first.Equal(&meta.FindStatusCondition(fresh.Status.Conditions, condition.Type).LastTransitionTime))
	// A concurrent placement reservation and another reporter's fields must
	// survive; observed reservations are removed even with unchanged inputs.
	fresh.Status.PendingAssignedClusters = []*azcorearm.ResourceID{metadataapi.Must(azcorearm.ParseResourceID(a)), metadataapi.Must(azcorearm.ParseResourceID(b + "-pending"))}
	fresh.Status.ScaleCeiling.Capacity = corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("40")}
	_, err = crud.Replace(ctx, fresh, nil)
	require.NoError(t, err)
	require.NoError(t, s.persistObservedResources(ctx, testStampIdentifier, observed, []string{a, b}, nil, condition))
	cleaned, err := crud.Get(ctx, fleetapi.SchedulingResourceName)
	require.NoError(t, err)
	require.Len(t, cleaned.Status.PendingAssignedClusters, 1)
	require.Equal(t, "b-pending", cleaned.Status.PendingAssignedClusters[0].Name)
	require.Equal(t, int64(40), cleaned.Status.ScaleCeiling.Capacity.Cpu().Value())
	require.NoError(t, s.persistObservedResources(ctx, testStampIdentifier, observed, []string{b, a}, nil, condition))
	settled, err := crud.Get(ctx, fleetapi.SchedulingResourceName)
	require.NoError(t, err)
	require.Equal(t, cleaned.CosmosETag, settled.CosmosETag)
	// Meaningful condition transitions and membership/capacity changes persist.
	condition.Status = metav1.ConditionFalse
	condition.Reason = "ReportNotCurrent"
	condition.LastTransitionTime = next
	observed.Capacity[corev1.ResourceCPU] = resource.MustParse("3")
	require.NoError(t, s.persistObservedResources(ctx, testStampIdentifier, observed, []string{a}, []string{b}, condition))
	changed, err := crud.Get(ctx, fleetapi.SchedulingResourceName)
	require.NoError(t, err)
	require.NotEqual(t, settled.CosmosETag, changed.CosmosETag)
	require.True(t, meta.IsStatusConditionFalse(changed.Status.Conditions, condition.Type))
	require.Len(t, changed.Status.NotReadyResourceIDs, 1)
	require.Equal(t, int64(3), changed.Status.ObservedResources.Capacity.Cpu().Value())
}
