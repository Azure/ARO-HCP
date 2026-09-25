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

package snapshot

import (
	"strings"
	"testing"
)

const (
	testSub         = "11111111-1111-1111-1111-111111111111"
	testClusterID   = "/subscriptions/11111111-1111-1111-1111-111111111111/resourceGroups/rg-test/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/c1"
	testNodePoolID  = "/subscriptions/11111111-1111-1111-1111-111111111111/resourceGroups/rg-test/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/c1/nodePools/np1"
	testStart       = "2026-09-24T11:00:00Z"
	testEnd         = "2026-09-24T14:30:00Z"
	testKustoName   = "hcp-dev-us-2"
	testKustoRegion = "eastus2"
)

// baseFromClusterOptions returns options with the shared Kusto and time-window
// flags populated, so tests only need to vary the identity inputs.
func baseFromClusterOptions() *RawFromClusterOptions {
	o := defaultFromClusterOptions()
	o.Kusto = testKustoName
	o.Region = testKustoRegion
	o.StartTime = testStart
	o.EndTime = testEnd
	return o
}

func TestFromClusterOptionsValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*RawFromClusterOptions)
		wantErr string // substring; "" means expect success
		check   func(*testing.T, *validatedFromClusterOptions)
	}{
		{
			name:    "no identity input",
			mutate:  func(o *RawFromClusterOptions) {},
			wantErr: "one of --cluster-resource-id",
		},
		{
			name: "both identity modes",
			mutate: func(o *RawFromClusterOptions) {
				o.ClusterResourceID = testClusterID
				o.Subscription = testSub
				o.ResourceGroup = "rg-test"
			},
			wantErr: "mutually exclusive",
		},
		{
			name: "subscription without resource group",
			mutate: func(o *RawFromClusterOptions) {
				o.Subscription = testSub
			},
			wantErr: "--resource-group is required",
		},
		{
			name: "resource group without subscription",
			mutate: func(o *RawFromClusterOptions) {
				o.ResourceGroup = "rg-test"
			},
			wantErr: "--subscription is required",
		},
		{
			name: "malformed cluster resource id",
			mutate: func(o *RawFromClusterOptions) {
				o.ClusterResourceID = "not-an-arm-id"
			},
			wantErr: "invalid --cluster-resource-id",
		},
		{
			name: "cluster resource id of wrong type",
			mutate: func(o *RawFromClusterOptions) {
				o.ClusterResourceID = testNodePoolID
			},
			wantErr: "not an HCP cluster",
		},
		{
			name: "explicit cluster resource id",
			mutate: func(o *RawFromClusterOptions) {
				o.ClusterResourceID = testClusterID
			},
			check: func(t *testing.T, v *validatedFromClusterOptions) {
				if v.seedClusterResourceID != testClusterID {
					t.Errorf("seedClusterResourceID = %q, want %q", v.seedClusterResourceID, testClusterID)
				}
				if v.seedSubscriptionID != testSub {
					t.Errorf("seedSubscriptionID = %q, want %q (parsed from id)", v.seedSubscriptionID, testSub)
				}
				if v.resourceGroup != "rg-test" {
					t.Errorf("resourceGroup = %q, want rg-test (parsed from id)", v.resourceGroup)
				}
			},
		},
		{
			name: "subscription and resource group",
			mutate: func(o *RawFromClusterOptions) {
				o.Subscription = testSub
				o.ResourceGroup = "rg-test"
			},
			check: func(t *testing.T, v *validatedFromClusterOptions) {
				if v.seedClusterResourceID != "" {
					t.Errorf("seedClusterResourceID = %q, want empty (resolve mode)", v.seedClusterResourceID)
				}
				if v.seedSubscriptionID != testSub {
					t.Errorf("seedSubscriptionID = %q, want %q", v.seedSubscriptionID, testSub)
				}
				if v.resourceGroup != "rg-test" {
					t.Errorf("resourceGroup = %q, want rg-test", v.resourceGroup)
				}
			},
		},
		{
			name: "malformed start time",
			mutate: func(o *RawFromClusterOptions) {
				o.ClusterResourceID = testClusterID
				o.StartTime = "not-a-time"
			},
			wantErr: "invalid --start-time",
		},
		{
			name: "start not before end",
			mutate: func(o *RawFromClusterOptions) {
				o.ClusterResourceID = testClusterID
				o.StartTime = testEnd
				o.EndTime = testStart
			},
			wantErr: "--start-time must be before --end-time",
		},
		{
			name: "via-region without via-kusto",
			mutate: func(o *RawFromClusterOptions) {
				o.ClusterResourceID = testClusterID
				o.ViaRegion = "westus"
			},
			wantErr: "--via-region requires --via-kusto",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := baseFromClusterOptions()
			tt.mutate(o)
			got, err := o.validate()
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error %q does not contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.check != nil {
				tt.check(t, got)
			}
		})
	}
}
