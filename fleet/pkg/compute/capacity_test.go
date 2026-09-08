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

package compute

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/api/resource"
)

func memoryBytes(value string) int64 {
	quantity := resource.MustParse(value)
	return quantity.Value()
}

func TestPoolCapacities(t *testing.T) {
	tests := []struct {
		name    string
		pools   []Pool
		wantErr string
	}{
		{name: "empty"},
		{name: "roles", pools: []Pool{
			{Name: "sys", Role: PoolRoleSystem, Spec: VMSpec{VCPUs: 4, MemoryBytes: memoryBytes("16Gi"), SecondaryNICs: 3}, MaxCount: 3, EnableSwift: true},
			{Name: "infra", Role: PoolRoleInfra, Spec: VMSpec{VCPUs: 8, MemoryBytes: memoryBytes("32Gi")}, MaxCount: 2},
			{Name: "old", Role: PoolRoleWorker, Spec: VMSpec{VCPUs: 16, MemoryBytes: memoryBytes("128Gi"), SecondaryNICs: 7}, MaxCount: 5, EnableSwift: true},
			{Name: "new", Role: PoolRoleWorker, Spec: VMSpec{VCPUs: 32, MemoryBytes: memoryBytes("256Gi"), SecondaryNICs: 7}, MaxCount: 2, EnableSwift: true},
			{Name: "plain", Role: PoolRoleWorker, Spec: VMSpec{VCPUs: 4, MemoryBytes: memoryBytes("16Gi"), SecondaryNICs: 3}, MaxCount: 1},
		}},
		{name: "unknown SKU", pools: []Pool{{Name: "unknown", Role: PoolRoleWorker, MaxCount: 3}}, wantErr: "cannot determine capacity"},
		{name: "unknown role", pools: []Pool{{Name: "unknown", Role: "other", Spec: VMSpec{VCPUs: 4, MemoryBytes: memoryBytes("16Gi")}}}, wantErr: "cannot determine capacity"},
		{name: "unknown Swift capacity", pools: []Pool{{Name: "unknown", Role: PoolRoleWorker, Spec: VMSpec{VCPUs: 4, MemoryBytes: memoryBytes("16Gi")}, EnableSwift: true}}, wantErr: "cannot determine Swift"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := PoolCapacities(test.pools)
			if len(test.wantErr) > 0 {
				require.ErrorContains(t, err, test.wantErr)
				return
			}
			require.NoError(t, err)
			actual, err := json.MarshalIndent(got, "", "  ")
			require.NoError(t, err)
			expected, err := os.ReadFile("testdata/capacity-" + test.name + ".json")
			require.NoError(t, err)
			require.Equal(t, string(expected), string(actual)+"\n")
		})
	}
}
