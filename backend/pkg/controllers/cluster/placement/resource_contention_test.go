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

package placement

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/fleetlistertesting"
)

func contentionSchedulingDoc(stamp string, now time.Time) *fleetapi.ManagementClusterScheduling {
	doc := schedulingDoc(stamp, 30, 0, 0, 0)
	doc.Status.ObservedResources = fleetapi.ObservedResources{
		LastReportedAt: ptr.To(metav1.NewTime(now)),
		Capacity: corev1.ResourceList{
			corev1.ResourceCPU: resource.MustParse("4"), corev1.ResourceMemory: resource.MustParse("8Gi"),
		},
		Requests: corev1.ResourceList{
			corev1.ResourceCPU: resource.MustParse("1"), corev1.ResourceMemory: resource.MustParse("2Gi"),
		},
		Usage: corev1.ResourceList{
			corev1.ResourceCPU: resource.MustParse("500m"), corev1.ResourceMemory: resource.MustParse("1Gi"),
		},
	}
	return doc
}

func TestResourceContention(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name   string
		mutate func(*fleetapi.ManagementClusterScheduling)
		want   float64
	}{
		{name: "requests exceed usage", want: 0.25},
		{
			name: "CPU usage dominates", want: 0.75,
			mutate: func(doc *fleetapi.ManagementClusterScheduling) {
				doc.Status.ObservedResources.Usage[corev1.ResourceCPU] = resource.MustParse("3")
			},
		},
		{
			name: "memory usage dominates", want: 0.75,
			mutate: func(doc *fleetapi.ManagementClusterScheduling) {
				doc.Status.ObservedResources.Usage[corev1.ResourceMemory] = resource.MustParse("6Gi")
			},
		},
		{
			name: "max is per resource", want: 0.5,
			mutate: func(doc *fleetapi.ManagementClusterScheduling) {
				doc.Status.ObservedResources.Usage[corev1.ResourceMemory] = resource.MustParse("4Gi")
				doc.Status.ObservedResources.Requests[corev1.ResourceCPU] = resource.MustParse("1500m")
			},
		},
		{
			name: "scale ceiling is not denominator", want: 0.25,
			mutate: func(doc *fleetapi.ManagementClusterScheduling) {
				doc.Status.ScaleCeiling.Capacity[corev1.ResourceCPU] = resource.MustParse("400")
				doc.Status.ScaleCeiling.Capacity[corev1.ResourceMemory] = resource.MustParse("800Gi")
			},
		},
		{
			name: "above one is not clamped", want: 2,
			mutate: func(doc *fleetapi.ManagementClusterScheduling) {
				doc.Status.ObservedResources.Requests[corev1.ResourceMemory] = resource.MustParse("16Gi")
			},
		},
		{
			name: "zero is valid", want: 0,
			mutate: func(doc *fleetapi.ManagementClusterScheduling) {
				for _, name := range []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory} {
					doc.Status.ObservedResources.Requests[name] = resource.MustParse("0")
					doc.Status.ObservedResources.Usage[name] = resource.MustParse("0")
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := contentionSchedulingDoc("1", now)
			if tc.mutate != nil {
				tc.mutate(doc)
			}
			before := doc.DeepCopy()
			score := resourceContention(doc, now)
			require.NotNil(t, score)
			assert.Equal(t, tc.want, *score)
			assert.Equal(t, before, doc, "scoring must not mutate cached observations")
		})
	}

	assert.Nil(t, resourceContention(nil, now))
	for _, name := range []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory} {
		for _, field := range []string{"capacity", "requests", "usage"} {
			for _, value := range []string{"missing", "-1", "1e1000", "0"} {
				t.Run(string(name)+"/"+field+"/"+value, func(t *testing.T) {
					doc := contentionSchedulingDoc("1", now)
					resources := map[string]corev1.ResourceList{
						"capacity": doc.Status.ObservedResources.Capacity,
						"requests": doc.Status.ObservedResources.Requests,
						"usage":    doc.Status.ObservedResources.Usage,
					}[field]
					if value == "missing" {
						delete(resources, name)
					} else {
						resources[name] = resource.MustParse(value)
					}
					score := resourceContention(doc, now)
					if value == "0" && field != "capacity" {
						assert.NotNil(t, score, "zero consumption is valid")
					} else {
						assert.Nil(t, score, "all CPU/memory inputs must be present and valid")
					}
				})
			}
		}
	}
	for _, tc := range []struct {
		name  string
		time  *metav1.Time
		valid bool
	}{
		{name: "missing"},
		{name: "zero", time: &metav1.Time{}},
		{name: "now", time: ptr.To(metav1.NewTime(now)), valid: true},
		{name: "five minutes", time: ptr.To(metav1.NewTime(now.Add(-5 * time.Minute))), valid: true},
		{name: "stale", time: ptr.To(metav1.NewTime(now.Add(-5*time.Minute - time.Nanosecond)))},
		{name: "future", time: ptr.To(metav1.NewTime(now.Add(time.Nanosecond)))},
	} {
		t.Run("timestamp/"+tc.name, func(t *testing.T) {
			doc := contentionSchedulingDoc("1", now)
			doc.Status.ObservedResources.LastReportedAt = tc.time
			assert.Equal(t, tc.valid, resourceContention(doc, now) != nil)
		})
	}
}

func TestSelectByCapacity_Contention(t *testing.T) {
	for _, tc := range []struct {
		name       string
		scores     [2]*float64
		pending    [2]int
		nics       [2]int64
		demand     int64
		want       string
		ineligible bool
	}{
		{name: "lower CPU memory contention beats NIC headroom", scores: [2]*float64{ptr.To(0.75), ptr.To(0.25)}, nics: [2]int64{30, 3}, want: "2"},
		{name: "known zero beats unknown", scores: [2]*float64{nil, ptr.To(0.0)}, nics: [2]int64{30, 0}, want: "2"},
		{name: "known nonzero beats unknown", scores: [2]*float64{nil, ptr.To(2.0)}, nics: [2]int64{30, 0}, want: "2"},
		{name: "above 100 percent ranks", scores: [2]*float64{ptr.To(2.0), ptr.To(1.5)}, want: "2"},
		{name: "equal scores prefer fewer pending", scores: [2]*float64{ptr.To(0.25), ptr.To(0.25)}, pending: [2]int{2, 1}, nics: [2]int64{30, 3}, want: "2"},
		{name: "pending does not override lower score", scores: [2]*float64{ptr.To(0.0), ptr.To(0.25)}, pending: [2]int{10, 0}, want: "1"},
		{name: "equal scores and pending use ID not NICs", scores: [2]*float64{ptr.To(0.25), ptr.To(0.25)}, nics: [2]int64{0, 30}, want: "1"},
		{name: "all unknown retain NIC ranking not pending", pending: [2]int{0, 10}, nics: [2]int64{3, 30}, want: "2"},
		{name: "all unknown retain ID tie break not pending", pending: [2]int{10, 0}, want: "1"},
		{name: "SWIFT HA ignores scores", scores: [2]*float64{ptr.To(0.75), ptr.To(0.0)}, nics: [2]int64{30, 3}, demand: 3, want: "1"},
		{name: "SWIFT single replica ignores scores", scores: [2]*float64{nil, ptr.To(0.0)}, nics: [2]int64{30, 3}, demand: 1, want: "1"},
		{name: "SWIFT ties ignore pending", scores: [2]*float64{ptr.To(0.75), ptr.To(0.0)}, pending: [2]int{10, 0}, nics: [2]int64{3, 3}, demand: 3, want: "1"},
		{name: "zero demand still rejects negative NICs", scores: [2]*float64{ptr.To(0.0), nil}, nics: [2]int64{-1, 0}, want: "2"},
		{name: "valid score does not override eligibility", scores: [2]*float64{ptr.To(0.0), nil}, want: "2", ineligible: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidates := []managementClusterEvaluation{eligibleCandidate("1", tc.nics[0]), eligibleCandidate("2", tc.nics[1])}
			for i := range candidates {
				candidates[i].contention = tc.scores[i]
				candidates[i].pendingAssignments = tc.pending[i]
			}
			if tc.ineligible {
				candidates[0].eligibility = ineligible
			}
			for range 2 {
				chosen, condition := selectByCapacity(candidates, tc.demand)
				require.NotNil(t, chosen)
				assert.Equal(t, mcForStamp(tc.want, true, true).ResourceID.String(), chosen.String())
				assert.Equal(t, metav1.ConditionTrue, condition.Status)
				candidates[0], candidates[1] = candidates[1], candidates[0]
			}
		})
	}
}

func TestEvaluateManagementClusters_ContentionSelection(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(a, b *fleetapi.ManagementClusterScheduling)
		want   string
	}{
		{
			name: "dominant memory beats lower CPU", want: "2",
			mutate: func(a, b *fleetapi.ManagementClusterScheduling) {
				a.Status.ObservedResources.Usage[corev1.ResourceMemory] = resource.MustParse("6Gi")
				b.Status.ObservedResources.Usage[corev1.ResourceCPU] = resource.MustParse("2")
			},
		},
		{
			name: "heterogeneous current capacity normalizes consumption", want: "2",
			mutate: func(a, b *fleetapi.ManagementClusterScheduling) {
				b.Status.ObservedResources.Capacity[corev1.ResourceCPU] = resource.MustParse("16")
				b.Status.ObservedResources.Capacity[corev1.ResourceMemory] = resource.MustParse("32Gi")
				b.Status.ObservedResources.Requests[corev1.ResourceCPU] = resource.MustParse("2")
				b.Status.ObservedResources.Requests[corev1.ResourceMemory] = resource.MustParse("4Gi")
				a.Status.ScaleCeiling.Capacity[corev1.ResourceCPU] = resource.MustParse("400")
				a.Status.ScaleCeiling.Capacity[corev1.ResourceMemory] = resource.MustParse("800Gi")
			},
		},
		{
			name: "pending counts distinct nonnil case insensitive IDs", want: "2",
			mutate: func(a, b *fleetapi.ManagementClusterScheduling) {
				a.Status.PendingAssignedClusters = dummyResourceIDs(2)
				b.Status.PendingAssignedClusters = []*azcorearm.ResourceID{
					nil, clusterResourceIDWithName("pending"), clusterResourceIDWithName("PENDING"), clusterResourceIDWithName("pending"),
				}
			},
		},
		{
			name: "stale score stays eligible but loses to known", want: "2",
			mutate: func(a, _ *fleetapi.ManagementClusterScheduling) {
				a.Status.ObservedResources.LastReportedAt = ptr.To(metav1.NewTime(time.Now().Add(-6 * time.Minute)))
			},
		},
		{
			name: "all unknown keeps NIC ordering", want: "1",
			mutate: func(a, b *fleetapi.ManagementClusterScheduling) {
				a.Status.ObservedResources.Usage = nil
				b.Status.ObservedResources.Capacity = nil
				b.Status.ScaleCeiling.Capacity = swiftResourceList(3)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now()
			a, b := contentionSchedulingDoc("1", now), contentionSchedulingDoc("2", now)
			tc.mutate(a, b)
			beforeA, beforeB := a.DeepCopy(), b.DeepCopy()
			clusters := &fleetlistertesting.SliceManagementClusterLister{ManagementClusters: []*fleetapi.ManagementCluster{
				mcForStamp("1", true, true), mcForStamp("2", true, true),
			}}
			syncer := &placementSyncer{
				managementClusterLister: clusters,
				managementClusterSchedulingLister: &fleetlistertesting.SliceManagementClusterSchedulingLister{
					Schedulings: []*fleetapi.ManagementClusterScheduling{a, b},
				},
			}
			for range 2 {
				evaluations, err := syncer.evaluateManagementClusters(context.Background())
				require.NoError(t, err)
				require.Len(t, evaluations, 2)
				for i, evaluation := range evaluations {
					assert.Equal(t, clusters.ManagementClusters[i].ResourceID, evaluation.resourceID, "evaluation preserves lister order")
					assert.Equal(t, eligible, evaluation.eligibility, "scoring does not change eligibility")
					if tc.name == "pending counts distinct nonnil case insensitive IDs" {
						want := 2
						if stampIdentifierFromResourceID(evaluation.resourceID) == "2" {
							want = 1
						}
						assert.Equal(t, want, evaluation.pendingAssignments)
					}
				}
				chosen, condition := selectByCapacity(evaluations, 0)
				require.NotNil(t, chosen)
				assert.Equal(t, mcForStamp(tc.want, true, true).ResourceID, chosen)
				assert.Equal(t, metav1.ConditionTrue, condition.Status)
				clusters.ManagementClusters[0], clusters.ManagementClusters[1] = clusters.ManagementClusters[1], clusters.ManagementClusters[0]
			}
			assert.Equal(t, beforeA, a)
			assert.Equal(t, beforeB, b)
		})
	}
}
