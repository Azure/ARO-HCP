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
	"context"
	"maps"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/fleet/pkg/compute"
)

func fullEnv() map[string]string {
	return map[string]string{
		"SUBSCRIPTION_ID":                      "sub1",
		"RESOURCE_GROUP":                       "rg1",
		"CLUSTER_NAME":                         "cluster1",
		"REGION":                               "eastus",
		"PROFILE":                              compute.ProfileDevelopment,
		"ZONES":                                "1,2,3",
		"AZURE_REGION_AVAILABILITY_ZONE_COUNT": "3",
		"NODE_SUBNET_ID":                       "/subscriptions/sub1/.../node-subnet",
		"POD_SUBNET_ID":                        "/subscriptions/sub1/.../pod-subnet",
		"NETWORK_DATAPLANE":                    "cilium",
		"NETWORK_POLICY":                       "cilium",
		"OUTBOUND_IP_RESOURCE_ID":              "/subscriptions/sub1/.../outbound-ip",
		"MANAGED_IDENTITY_ID":                  "/subscriptions/sub1/.../mi1",
		"ETCD_KMS_KEY_URI":                     "https://kv1.vault.azure.net/keys/aks-etcd-encryption/abc123",
		"KUBERNETES_VERSION":                   "1.31.1",
		"CLUSTER_TAGS":                         "clusterType=mgmt,persist=true",
	}
}

func envFunc(overrides map[string]string) func(string) string {
	env := fullEnv()
	maps.Copy(env, overrides)
	return func(key string) string { return env[key] }
}

func TestNewRawOptionsFromEnvDefaults(t *testing.T) {
	o := newRawOptionsFromEnv(envFunc(map[string]string{"ZONES": ""}))
	assert.Empty(t, o.zones, "ZONES has no default; an empty list derives the region's zones in Validate")
	assert.Equal(t, "3", o.zoneCount, "AZURE_REGION_AVAILABILITY_ZONE_COUNT should be read from the environment")
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
			name:      "unknown profile",
			overrides: map[string]string{"PROFILE": "does-not-exist"},
			wantErr:   `unknown profile "does-not-exist"`,
		},
		{
			name:      "empty zones derives the region's zones",
			overrides: map[string]string{"ZONES": ""},
		},
		{
			name:      "explicit zone outside the region range",
			overrides: map[string]string{"ZONES": "1,3,4"},
			wantErr:   "outside the region's availability zones",
		},
		{
			name:      "missing zone count",
			overrides: map[string]string{"AZURE_REGION_AVAILABILITY_ZONE_COUNT": ""},
			wantErr:   "AZURE_REGION_AVAILABILITY_ZONE_COUNT",
		},
		{
			name:      "zero zone count",
			overrides: map[string]string{"AZURE_REGION_AVAILABILITY_ZONE_COUNT": "0"},
			wantErr:   "region has no availability zones",
		},
		{
			name:      "malformed cluster tags",
			overrides: map[string]string{"CLUSTER_TAGS": "clusterType"},
			wantErr:   `malformed tag entry "clusterType"`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw := newRawOptionsFromEnv(envFunc(test.overrides))
			validated, err := raw.Validate(context.Background())
			if len(test.wantErr) > 0 {
				require.Error(t, err)
				assert.ErrorContains(t, err, test.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, "sub1", validated.subscriptionID)
			assert.Equal(t, []string{"1", "2", "3"}, validated.zones)
			assert.Equal(t, map[string]string{"clusterType": "mgmt", "persist": "true"}, validated.clusterTags)
			wantProfile, ok := compute.LookupProfile(compute.ProfileDevelopment)
			require.True(t, ok)
			assert.Equal(t, wantProfile.Tiers, validated.profile.Tiers)
			// BudgetStrategy is a func value; reflect.DeepEqual (assert.Equal)
			// never treats non-nil funcs as equal, so compare by code pointer.
			assert.Equal(t,
				reflect.ValueOf(wantProfile.BudgetStrategy).Pointer(),
				reflect.ValueOf(validated.profile.BudgetStrategy).Pointer(),
				"budget strategy")
		})
	}
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

func TestValidatedOptionsComplete(t *testing.T) {
	// Selects a credential type that NewDefaultAzureCredential can construct
	// without erroring or making network calls, so Complete's happy path is
	// testable without a real Azure environment. Actual authentication is only
	// attempted on first token request, which this test never triggers.
	t.Setenv("AZURE_TOKEN_CREDENTIALS", "AzureCLICredential")

	o := testValidatedOptions()

	completed, err := o.Complete(context.Background())

	require.NoError(t, err)
	assert.Same(t, o, completed.validatedOptions)
	assert.NotNil(t, completed.clustersClient)
	assert.NotNil(t, completed.poolsClient)
	assert.NotNil(t, completed.usageClient)
	assert.NotNil(t, completed.skuCache)
}
