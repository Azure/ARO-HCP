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

package main

import (
	"bytes"
	"github.com/go-logr/logr"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/fleet/pkg/azure/skucache"
	"github.com/Azure/ARO-HCP/fleet/pkg/compute"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
)

func TestResolvePoolZones(t *testing.T) {
	for _, test := range []struct {
		name     string
		mode     compute.PoolMode
		defaults []string
		explicit []string
		existing []*armcontainerservice.ManagedClusterAgentPoolProfile
		want     []string
	}{
		{name: "explicit zones override SKU zones", explicit: []string{"4", "2"}, defaults: []string{"1", "2", "4"}, want: []string{"4", "2"}},
		{name: "matching explicit zones need no warning", explicit: []string{"3"},
			existing: []*armcontainerservice.ManagedClusterAgentPoolProfile{{NodeLabels: map[string]*string{compute.RoleLabel: ptr.To("worker")}, AvailabilityZones: []*string{ptr.To("3")}}}, want: []string{"3"}},
		{name: "fresh pool uses defaults", defaults: []string{"2", "4"}, want: []string{"2", "4"}},
		{name: "existing zones override explicit and SKU zones", explicit: []string{"2", "4"}, defaults: []string{"4", "2"},
			existing: []*armcontainerservice.ManagedClusterAgentPoolProfile{{Name: ptr.To("fleet-generated-name"), NodeLabels: map[string]*string{compute.RoleLabel: ptr.To("worker")}, AvailabilityZones: []*string{ptr.To("3")}}},
			want:     []string{"3"}},
		{name: "existing zones override defaults in stable order", defaults: []string{"2", "4"},
			existing: []*armcontainerservice.ManagedClusterAgentPoolProfile{
				{Name: ptr.To("worker3"), NodeLabels: map[string]*string{compute.RoleLabel: ptr.To("worker")}, AvailabilityZones: []*string{ptr.To("3")}},
				{Name: ptr.To("worker1"), NodeLabels: map[string]*string{compute.RoleLabel: ptr.To("worker")}, AvailabilityZones: []*string{ptr.To("1")}},
			}, want: []string{"1", "3"}},
		{name: "existing nonzonal pool stays nonzonal", defaults: []string{"2", "4"},
			existing: []*armcontainerservice.ManagedClusterAgentPoolProfile{{Name: ptr.To("workernz7"), NodeLabels: map[string]*string{compute.RoleLabel: ptr.To("worker")}}}},
		{name: "regional pool retains zone order", mode: compute.PoolModeRegional, defaults: []string{"2", "4"},
			existing: []*armcontainerservice.ManagedClusterAgentPoolProfile{{Name: ptr.To("different-system-name"), Mode: ptr.To(armcontainerservice.AgentPoolModeSystem), AvailabilityZones: []*string{ptr.To("3"), ptr.To("1")}}},
			want:     []string{"3", "1"}},
		{name: "unrelated pools are ignored", defaults: []string{"2", "4"},
			existing: []*armcontainerservice.ManagedClusterAgentPoolProfile{
				nil, {},
				{Name: ptr.To("infra1"), AvailabilityZones: []*string{ptr.To("1")}},
				{Name: ptr.To("worker1"), AvailabilityZones: []*string{ptr.To("3")}},
				{Name: ptr.To("worker4"), AvailabilityZones: []*string{ptr.To("4")}, NodeLabels: map[string]*string{compute.RoleLabel: ptr.To("infra")}},
			}, want: []string{"2", "4"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			mode := test.mode
			if len(mode) == 0 {
				mode = compute.PoolModePerZone
			}
			role := compute.PoolRoleWorker
			if mode == compute.PoolModeRegional {
				role = compute.PoolRoleSystem
			}
			cfg := validatedPoolConfig{name: "worker", explicitZones: test.explicit, mode: mode, class: compute.PoolClass{Role: role}}
			var logs bytes.Buffer
			ctx := logr.NewContext(t.Context(), logr.FromSlogHandler(slog.NewTextHandler(&logs, nil)))
			got, _ := resolvePoolZones(ctx, cfg, test.defaults, test.existing)
			if test.name == "existing zones override explicit and SKU zones" {
				require.Contains(t, logs.String(), "WARNING: ignoring configured zones")
			} else {
				require.Empty(t, logs.String())
			}
			require.Equal(t, test.want, got)
		})
	}
}

func TestBuildPoolsRejectsInsufficientZones(t *testing.T) {
	for _, test := range []struct {
		name    string
		zones   []string
		wantErr string
	}{
		{name: "pool count exceeds zones", zones: []string{"2", "4"}, wantErr: "pool count 3 exceeds available zones 2"},
		{name: "no zones", wantErr: "pool count 3 exceeds available zones 0"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := poolConfig{validatedPoolConfig: validatedPoolConfig{name: "worker", vmSize: "sku", poolCount: 3,
				mode: compute.PoolModePerZone, class: compute.PoolClass{Role: compute.PoolRoleWorker}}, zones: test.zones}
			_, err := buildPools(cfg, compute.PoolRoleWorker, map[string]*skucache.SKUMetadata{"sku": {Zones: test.zones}})
			require.ErrorContains(t, err, test.wantErr)
		})
	}
}

func TestPoolZoneValidation(t *testing.T) {
	for _, test := range []struct {
		name         string
		existing     bool
		restrictions []*armcompute.ResourceSKURestrictions
		wantErr      string
	}{
		{name: "unavailable zone", wantErr: "zone \"3\" is unavailable"},
		{name: "restricted region", restrictions: []*armcompute.ResourceSKURestrictions{{Type: ptr.To(armcompute.ResourceSKURestrictionsTypeLocation), Values: []*string{ptr.To("eastus")}}}, wantErr: "unavailable in this region"},
		{name: "existing zone is preserved", existing: true},
		{name: "existing pool in restricted region is preserved", existing: true, restrictions: []*armcompute.ResourceSKURestrictions{{Type: ptr.To(armcompute.ResourceSKURestrictionsTypeLocation), Values: []*string{ptr.To("eastus")}}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, service := range []bool{false, true} {
				o := &validatedOptions{
					subscriptionID: "sub1",
					systemPoolOnly: true,
					system:         validatedPoolConfig{name: "system", vmSize: "sku", explicitZones: []string{"3"}, mode: compute.PoolModeRegional, class: compute.PoolClass{Role: compute.PoolRoleSystem}},
				}
				skuCache := newRunTestSKUCache(t, []*armcompute.ResourceSKU{{
					Name: ptr.To("sku"), ResourceType: ptr.To("virtualMachines"),
					Capabilities: []*armcompute.ResourceSKUCapabilities{{Name: ptr.To("vCPUs"), Value: ptr.To("4")}, {Name: ptr.To("MemoryGB"), Value: ptr.To("16")}},
					LocationInfo: []*armcompute.ResourceSKULocationInfo{{Location: ptr.To("eastus"), Zones: []*string{ptr.To("2"), ptr.To("4")}}},
					Restrictions: test.restrictions,
				}})
				if service {
					o.serviceMeshProfile = &armcontainerservice.ServiceMeshProfile{}
				}
				var cluster *armcontainerservice.ManagedCluster
				if test.existing {
					cluster = &armcontainerservice.ManagedCluster{Properties: &armcontainerservice.ManagedClusterProperties{AgentPoolProfiles: []*armcontainerservice.ManagedClusterAgentPoolProfile{{Mode: ptr.To(armcontainerservice.AgentPoolModeSystem), AvailabilityZones: []*string{ptr.To("3")}}}}}
				}
				system, _, _, err := completePoolConfigs(t.Context(), o, skuCache, cluster)
				if len(test.wantErr) > 0 {
					require.ErrorContains(t, err, test.wantErr)
				} else {
					require.NoError(t, err)
					require.Equal(t, []string{"3"}, system.zones)
				}
			}
		})
	}
}
