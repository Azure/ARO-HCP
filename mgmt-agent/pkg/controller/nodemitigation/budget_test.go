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

package nodemitigation

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	api "github.com/Azure/ARO-HCP/mgmt-agent/pkg/apis/capacityreport/v1alpha1"
)

func testConfig() Config {
	return Config{Mode: Enforce, ClusterResourceID: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/mgmt",
		Mitigators: []string{"swift", "never-ready"}, Rescue: true, Drain: true, DeleteNode: true,
		Window: metav1.Duration{Duration: time.Hour}, RetryInterval: metav1.Duration{Duration: time.Second},
		ObservationMaxAge: metav1.Duration{Duration: time.Minute}, MaxUnavailableCluster: 3, MaxUnavailablePool: 2,
		MaxUnavailableZone: 3, MinHealthyPool: 1, MinHealthyZone: 1}
}

func TestDeletionLimit(t *testing.T) {
	for _, test := range []struct {
		size int32
		want int
	}{{0, 0}, {1, 1}, {6, 1}, {9, 1}, {10, 1}, {19, 1}, {20, 2}} {
		if got := deletionLimit(test.size); got != test.want {
			t.Errorf("size %d: %d, want %d", test.size, got, test.want)
		}
	}
}

func TestBaselineTransitions(t *testing.T) {
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	cfg := testConfig()
	observe := func(size int32, stable bool, ready int) PoolObservation {
		return PoolObservation{ID: "pool", Target: size, Stable: stable, Ready: ready, ObservedAt: now}
	}
	baseline, err := updateBaseline(nil, observe(20, true, 20), cfg, "a", now)
	if err != nil || baseline.Size != 20 {
		t.Fatalf("initial baseline: %+v, %v", baseline, err)
	}
	baseline, err = updateBaseline(&baseline, observe(10, true, 10), cfg, "a", now)
	if err != nil || baseline.Size != 10 {
		t.Fatalf("scale down: %+v, %v", baseline, err)
	}
	baseline, err = updateBaseline(&baseline, observe(20, true, 20), cfg, "a", now)
	if err != nil || baseline.Size != 10 || baseline.StableSince == nil {
		t.Fatalf("premature increase: %+v, %v", baseline, err)
	}
	for i := 0; i < 60; i++ {
		now = now.Add(time.Minute)
		baseline, err = updateBaseline(&baseline, observe(20, true, 20), cfg, "a", now)
		if err != nil {
			t.Fatal(err)
		}
	}
	if baseline.Size != 20 {
		t.Fatalf("stable scale-up did not increase baseline: %+v", baseline)
	}
}

func TestBaselineRejectsUnknownIntervals(t *testing.T) {
	now := time.Now()
	cfg := testConfig()
	old := metav1.NewTime(now.Add(-2 * time.Hour))
	for _, test := range []struct {
		name     string
		observer string
		observed time.Time
		stable   bool
	}{
		{"restart", "new", now, true}, {"stale gap", "old", now.Add(-2 * time.Minute), true}, {"active upgrade", "old", now, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			previous := api.PoolBaseline{Size: 10, Target: 20, ObservedAt: metav1.NewTime(test.observed), StableSince: &old, Observer: "old"}
			result, err := updateBaseline(&previous, PoolObservation{ID: "pool", Target: 20, Stable: test.stable, Ready: 20, ObservedAt: now}, cfg, test.observer, now)
			if err != nil || result.Size != 10 {
				t.Fatalf("unknown interval raised baseline: %+v, %v", result, err)
			}
		})
	}
}

func TestReservationHistory(t *testing.T) {
	now := time.Now()
	old := metav1.NewTime(now.Add(-2 * time.Hour))
	recent := metav1.NewTime(now.Add(-time.Minute))
	budget := api.NodeMitigationBudgetStatus{Pools: map[string]api.PoolBaseline{"pool": {Size: 20}}, Reservations: map[string]api.MitigationReservation{
		"unknown": {PoolID: "pool", DeleteStartedAt: &old},
		"recent":  {PoolID: "pool", DeleteStartedAt: &recent, ReleasedAt: &recent},
		"expired": {PoolID: "pool", DeleteStartedAt: &old, ReleasedAt: &old},
	}}
	if got := allowance(budget, "pool", "", now, time.Hour); got != 0 {
		t.Fatalf("allowance=%d; active unknown outcome must not expire", got)
	}
	if got := allowance(budget, "pool", "unknown", now, time.Hour); got != 1 {
		t.Fatalf("retry consumed another reservation: %d", got)
	}
	budget.Pools["pool"] = api.PoolBaseline{Size: 10}
	if got := allowance(budget, "pool", "", now, time.Hour); got != -1 {
		t.Fatalf("scale-down reset history: %d", got)
	}
}
