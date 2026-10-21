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

package otelaudit

import (
	"testing"

	"github.com/microsoft/go-otel-audit/audit/msgs"
	"github.com/stretchr/testify/require"
)

func TestEnsureDefaultsOnEmptyRecord(t *testing.T) {
	record := &msgs.Record{}

	ensureDefaults(record)

	require.Equal(t, Unknown, record.OperationName)
	require.Equal(t, Unknown, record.OperationAccessLevel)
	require.Equal(t, Unknown, record.CallerAgent)
	require.Equal(t, []msgs.OperationCategory{msgs.ResourceManagement}, record.OperationCategories)
	require.Equal(t, []string{Unknown}, record.CallerAccessLevels)
	require.Equal(t, map[msgs.CallerIdentityType][]msgs.CallerIdentityEntry{
		msgs.ApplicationID: {{Identity: Unknown, Description: Unknown}},
	}, record.CallerIdentities)
	require.Equal(t, map[string][]msgs.TargetResourceEntry{
		Unknown: {{Name: Unknown, Region: Unknown}},
	}, record.TargetResources)
	wantIP, err := msgs.ParseAddr("192.168.1.1")
	require.NoError(t, err)
	require.Equal(t, wantIP, record.CallerIpAddress)
}

func TestEnsureDefaultsPreservesExplicitValues(t *testing.T) {
	publicIP, err := msgs.ParseAddr("8.8.8.8")
	require.NoError(t, err)

	record := &msgs.Record{
		OperationName:        "CreateCluster",
		OperationAccessLevel: "Write",
		CallerAgent:          "my-agent",
		OperationCategories:  []msgs.OperationCategory{msgs.ResourceManagement},
		CallerAccessLevels:   []string{"Owner"},
		CallerIpAddress:      publicIP,
		CallerIdentities: map[msgs.CallerIdentityType][]msgs.CallerIdentityEntry{
			msgs.ApplicationID: {{Identity: "11111111-1111-4111-8111-111111111111", Description: "svc account"}},
		},
		TargetResources: map[string][]msgs.TargetResourceEntry{
			"Microsoft.RedHatOpenShift/hcpOpenShiftClusters": {{Name: "cluster1", Region: "eastus"}},
		},
	}

	ensureDefaults(record)

	require.Equal(t, "CreateCluster", record.OperationName)
	require.Equal(t, "Write", record.OperationAccessLevel)
	require.Equal(t, "my-agent", record.CallerAgent)
	require.Equal(t, []string{"Owner"}, record.CallerAccessLevels)
	require.Equal(t, publicIP, record.CallerIpAddress)
	require.Equal(t, "11111111-1111-4111-8111-111111111111", record.CallerIdentities[msgs.ApplicationID][0].Identity)
	require.Equal(t, "svc account", record.CallerIdentities[msgs.ApplicationID][0].Description)
	require.Contains(t, record.TargetResources, "Microsoft.RedHatOpenShift/hcpOpenShiftClusters")
}

func TestEnsureDefaultsOperationResultDescription(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result msgs.OperationResult
		want   string
	}{
		{name: "failure defaults description", result: msgs.Failure, want: Unknown},
		{name: "success leaves description empty", result: msgs.Success, want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			record := &msgs.Record{OperationResult: tc.result}
			ensureDefaults(record)
			require.Equal(t, tc.want, record.OperationResultDescription)
		})
	}
}

func TestEnsureDefaultsOtherCategoryRequiresDescription(t *testing.T) {
	record := &msgs.Record{OperationCategories: []msgs.OperationCategory{msgs.OCOther}}

	ensureDefaults(record)

	require.Equal(t, "Other", record.OperationCategoryDescription)
}

func TestEnsureDefaultsFillsBlankIdentityFields(t *testing.T) {
	record := &msgs.Record{
		CallerIdentities: map[msgs.CallerIdentityType][]msgs.CallerIdentityEntry{
			msgs.ApplicationID: {{Identity: "", Description: "real description"}},
		},
	}

	ensureDefaults(record)

	entry := record.CallerIdentities[msgs.ApplicationID][0]
	require.Equal(t, Unknown, entry.Identity)
	require.Equal(t, "real description", entry.Description)
}

func TestEnsureDefaultsReplacesUnusableCallerIP(t *testing.T) {
	loopback, err := msgs.ParseAddr("127.0.0.1")
	require.NoError(t, err)

	record := &msgs.Record{CallerIpAddress: loopback}

	ensureDefaults(record)

	want, err := msgs.ParseAddr("192.168.1.1")
	require.NoError(t, err)
	require.Equal(t, want, record.CallerIpAddress)
}
