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

package base

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	testclock "k8s.io/utils/clock/testing"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/fleet"
	"github.com/Azure/ARO-HCP/internal/database"
)

type fakeManagementClusterLister struct {
	clusters map[string]*fleet.ManagementCluster
}

func (f *fakeManagementClusterLister) List(_ context.Context) ([]*fleet.ManagementCluster, error) {
	result := make([]*fleet.ManagementCluster, 0, len(f.clusters))
	for _, managementCluster := range f.clusters {
		result = append(result, managementCluster)
	}
	return result, nil
}

func (f *fakeManagementClusterLister) Get(_ context.Context, stampIdentifier string) (*fleet.ManagementCluster, error) {
	managementCluster, ok := f.clusters[stampIdentifier]
	if !ok {
		return nil, database.NewNotFoundError()
	}
	return managementCluster, nil
}

func (f *fakeManagementClusterLister) GetByCSProvisionShardID(_ context.Context, _ string) (*fleet.ManagementCluster, error) {
	return nil, database.NewNotFoundError()
}

func boolPtr(b bool) *bool { return &b }

func testManagementCluster(stampIdentifier string, ready *bool) *fleet.ManagementCluster {
	resourceID, _ := fleet.ToManagementClusterResourceID(stampIdentifier)
	managementCluster := &fleet.ManagementCluster{
		CosmosMetadata: api.CosmosMetadata{ResourceID: resourceID},
	}
	if ready != nil {
		status := metav1.ConditionFalse
		if *ready {
			status = metav1.ConditionTrue
		}
		managementCluster.Status.Conditions = []metav1.Condition{
			{
				Type:   string(fleet.ManagementClusterConditionReady),
				Status: status,
			},
		}
	}
	return managementCluster
}

func TestRegistrationAwareCooldown(t *testing.T) {
	registeredCooldown := DefaultRegisteredCooldown
	unregisteredCooldown := DefaultUnregisteredCooldown

	tests := []struct {
		name       string
		clusters   map[string]*fleet.ManagementCluster
		key        any
		elapsed    time.Duration
		wantFirst  bool
		wantSecond bool
	}{
		{
			name: "ready cluster uses registered (slow) cooldown - blocked within window",
			clusters: map[string]*fleet.ManagementCluster{
				"ready-mc": testManagementCluster("ready-mc", boolPtr(true)),
			},
			key:        StampKey{StampIdentifier: "ready-mc"},
			elapsed:    registeredCooldown / 2,
			wantFirst:  true,
			wantSecond: false,
		},
		{
			name: "ready cluster uses registered (slow) cooldown - allowed after window",
			clusters: map[string]*fleet.ManagementCluster{
				"ready-mc": testManagementCluster("ready-mc", boolPtr(true)),
			},
			key:        StampKey{StampIdentifier: "ready-mc"},
			elapsed:    registeredCooldown + time.Second,
			wantFirst:  true,
			wantSecond: true,
		},
		{
			name: "not-ready cluster uses unregistered (fast) cooldown - blocked within window",
			clusters: map[string]*fleet.ManagementCluster{
				"pending-mc": testManagementCluster("pending-mc", boolPtr(false)),
			},
			key:        StampKey{StampIdentifier: "pending-mc"},
			elapsed:    unregisteredCooldown / 2,
			wantFirst:  true,
			wantSecond: false,
		},
		{
			name: "not-ready cluster uses unregistered (fast) cooldown - allowed after window",
			clusters: map[string]*fleet.ManagementCluster{
				"pending-mc": testManagementCluster("pending-mc", boolPtr(false)),
			},
			key:        StampKey{StampIdentifier: "pending-mc"},
			elapsed:    unregisteredCooldown + time.Second,
			wantFirst:  true,
			wantSecond: true,
		},
		{
			name: "missing Ready condition uses unregistered (fast) cooldown",
			clusters: map[string]*fleet.ManagementCluster{
				"no-condition-mc": testManagementCluster("no-condition-mc", nil),
			},
			key:        StampKey{StampIdentifier: "no-condition-mc"},
			elapsed:    unregisteredCooldown + time.Second,
			wantFirst:  true,
			wantSecond: true,
		},
		{
			name: "Ready=False uses unregistered (fast) cooldown",
			clusters: map[string]*fleet.ManagementCluster{
				"false-mc": testManagementCluster("false-mc", boolPtr(false)),
			},
			key:        StampKey{StampIdentifier: "false-mc"},
			elapsed:    unregisteredCooldown + time.Second,
			wantFirst:  true,
			wantSecond: true,
		},
		{
			name:       "unknown cluster uses unregistered (fast) cooldown",
			clusters:   map[string]*fleet.ManagementCluster{},
			key:        StampKey{StampIdentifier: "unknown-mc"},
			elapsed:    unregisteredCooldown + time.Second,
			wantFirst:  true,
			wantSecond: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := time.Now()
			fakeClock := testclock.NewFakeClock(now)

			lister := &fakeManagementClusterLister{clusters: tt.clusters}
			cooldown := NewRegistrationAwareCooldown(lister, registeredCooldown, unregisteredCooldown)
			cooldown.SetClock(fakeClock)

			ctx := context.Background()

			first := cooldown.CanSync(ctx, tt.key)
			if first != tt.wantFirst {
				t.Errorf("first CanSync: got %v, want %v", first, tt.wantFirst)
			}

			fakeClock.SetTime(now.Add(tt.elapsed))

			second := cooldown.CanSync(ctx, tt.key)
			if second != tt.wantSecond {
				t.Errorf("second CanSync after %v: got %v, want %v", tt.elapsed, second, tt.wantSecond)
			}
		})
	}
}
