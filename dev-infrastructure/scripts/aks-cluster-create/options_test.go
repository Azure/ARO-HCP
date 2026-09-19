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
	"maps"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fullEnv() map[string]string {
	return map[string]string{
		"SUBSCRIPTION_ID":                "sub1",
		"RESOURCE_GROUP":                 "rg1",
		"CLUSTER_NAME":                   "cluster1",
		"REGION":                         "eastus",
		"REGION_AVAILABILITY_ZONE_COUNT": "4",
		"NODE_SUBNET_ID":                 "/subscriptions/sub1/.../node-subnet",
		"POD_SUBNET_ID":                  "/subscriptions/sub1/.../pod-subnet",
		"NETWORK_DATAPLANE":              "cilium",
		"NETWORK_POLICY":                 "cilium",
		"OUTBOUND_IP_RESOURCE_ID":        "/subscriptions/sub1/.../outbound-ip",
		"MANAGED_IDENTITY_ID":            "/subscriptions/sub1/.../mi1",
		"ETCD_KMS_KEY_URI":               "https://kv1.vault.azure.net/keys/aks-etcd-encryption/abc123",
		"KUBERNETES_VERSION":             "1.31.1",
		"CLUSTER_TAGS":                   "clusterType=mgmt,persist=true",
		"OWNING_TEAM_TAG_VALUE":          "team-from-monitoring",

		"SYSTEM_POOL_NAME":            "s1abc1234567",
		"SYSTEM_POOL_VM_SIZE":         "Standard_D4ds_v6",
		"SYSTEM_POOL_OS_DISK_SIZE_GB": "32",
		"SYSTEM_POOL_MIN_COUNT":       "1",
		"SYSTEM_POOL_MAX_COUNT":       "3",
		"SYSTEM_POOL_ZONES":           "1,2,3",

		"USER_POOL_NAME":            "w",
		"USER_POOL_VM_SIZE":         "Standard_E16ds_v6",
		"USER_POOL_OS_DISK_SIZE_GB": "100",
		"USER_POOL_MIN_COUNT":       "0",
		"USER_POOL_MAX_COUNT":       "5",
		"USER_POOL_COUNT":           "3",
		"USER_POOL_ZONES":           "1,2,3",

		"INFRA_POOL_NAME":            "i",
		"INFRA_POOL_VM_SIZE":         "Standard_D4ds_v6",
		"INFRA_POOL_OS_DISK_SIZE_GB": "64",
		"INFRA_POOL_MIN_COUNT":       "1",
		"INFRA_POOL_MAX_COUNT":       "3",
		"INFRA_POOL_COUNT":           "1",
	}
}

func envFunc(overrides map[string]string) func(string) string {
	env := fullEnv()
	maps.Copy(env, overrides)
	return func(key string) string { return env[key] }
}

func TestRawOptionsValidate(t *testing.T) {
	tests := []struct {
		name      string
		overrides map[string]string
		wantErr   string
	}{
		{
			name: "valid options",
		},
		{
			name:      "missing subscription id",
			overrides: map[string]string{"SUBSCRIPTION_ID": ""},
			wantErr:   "SUBSCRIPTION_ID",
		},
		{
			name:      "missing multiple required fields",
			overrides: map[string]string{"SUBSCRIPTION_ID": "", "REGION": ""},
			wantErr:   "SUBSCRIPTION_ID, REGION",
		},
		{
			name:      "missing system pool name",
			overrides: map[string]string{"SYSTEM_POOL_NAME": ""},
			wantErr:   "SYSTEM_POOL_NAME is required",
		},
		{
			name:      "missing user pool VM size",
			overrides: map[string]string{"USER_POOL_VM_SIZE": ""},
			wantErr:   "USER_POOL_VM_SIZE is required",
		},
		{
			name:      "malformed pool integer",
			overrides: map[string]string{"USER_POOL_MAX_COUNT": "many"},
			wantErr:   `USER_POOL_MAX_COUNT: "many" is not a valid integer`,
		},
		{
			name:      "regional zone count below the 3-zone minimum is rejected",
			overrides: map[string]string{"REGION_AVAILABILITY_ZONE_COUNT": "2"},
			wantErr:   "REGION_AVAILABILITY_ZONE_COUNT must be at least 3, got 2",
		},
		{
			name:      "explicit pool zone outside region range is rejected",
			overrides: map[string]string{"USER_POOL_ZONES": "1,2,5"},
			wantErr:   "USER_POOL_ZONES: zone 5 is outside the region's availability zones [1,4]",
		},
		{
			name:      "duplicate explicit pool zone is rejected",
			overrides: map[string]string{"SYSTEM_POOL_ZONES": "1,1,2"},
			wantErr:   "SYSTEM_POOL_ZONES: zone list \"1,1,2\" contains duplicate zone 1",
		},
		{
			name:      "non-integer explicit pool zone is rejected",
			overrides: map[string]string{"INFRA_POOL_ZONES": "a"},
			wantErr:   `INFRA_POOL_ZONES: zone "a" is not a valid integer`,
		},
		{
			name:      "malformed cluster tags",
			overrides: map[string]string{"CLUSTER_TAGS": "clusterType"},
			wantErr:   `malformed tag entry "clusterType"`,
		},
		{
			name:      "min count exceeding max count is rejected",
			overrides: map[string]string{"USER_POOL_MIN_COUNT": "6"},
			wantErr:   "USER_POOL_MIN_COUNT (6) must not exceed USER_POOL_MAX_COUNT (5)",
		},
		{
			name:      "negative pool count is rejected",
			overrides: map[string]string{"USER_POOL_COUNT": "-1"},
			wantErr:   "USER_POOL_COUNT must be at least 1",
		},
		{
			name:      "zero pool count is rejected",
			overrides: map[string]string{"USER_POOL_COUNT": "0"},
			wantErr:   "USER_POOL_COUNT must be at least 1",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw := newRawOptionsFromEnv(envFunc(test.overrides))
			validated, err := raw.Validate()
			if len(test.wantErr) > 0 {
				require.Error(t, err)
				assert.ErrorContains(t, err, test.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, "sub1", validated.subscriptionID)
			assert.Equal(t, "s1abc1234567", validated.system.name)
			assert.Equal(t, []string{"1", "2", "3"}, validated.system.zones)
			assert.Equal(t, int32(5), validated.user.maxCount)
			assert.Equal(t, 3, validated.user.poolCount)
		})
	}
}

func TestOwningTeamTagOverridesCSV(t *testing.T) {
	raw := newRawOptionsFromEnv(envFunc(map[string]string{
		"CLUSTER_TAGS": "clusterType=mgmt,owningTeam=stale-team",
	}))
	o, err := raw.Validate()
	require.NoError(t, err)
	bootstrap := testSystemPool()
	cluster, _, err := o.desiredClusterSpec(nil, &bootstrap)
	require.NoError(t, err)
	require.Equal(t, "team-from-monitoring", *cluster.Tags["owningTeam"])
}

func TestParseCSVList(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []string
	}{
		{name: "empty", raw: "", want: nil},
		{name: "single", raw: "1", want: []string{"1"}},
		{name: "multiple", raw: "1,2,3", want: []string{"1", "2", "3"}},
		{name: "whitespace trimmed", raw: " 1 , 2 ,3 ", want: []string{"1", "2", "3"}},
		{name: "empty entries dropped", raw: "1,,2,", want: []string{"1", "2"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, parseCSVList(test.raw))
		})
	}
}

func TestParseTags(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    map[string]string
		wantErr string
	}{
		{name: "empty", raw: "", want: map[string]string{}},
		{name: "single", raw: "a=b", want: map[string]string{"a": "b"}},
		{name: "multiple", raw: "a=b,c=d", want: map[string]string{"a": "b", "c": "d"}},
		{name: "value with equals sign", raw: "a=b=c", want: map[string]string{"a": "b=c"}},
		{name: "missing equals", raw: "a", wantErr: `malformed tag entry "a"`},
		{name: "empty key", raw: "=b", wantErr: `malformed tag entry "=b"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseTags(test.raw)
			if len(test.wantErr) > 0 {
				require.Error(t, err)
				assert.ErrorContains(t, err, test.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.want, got)
		})
	}
}
