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

package agentpools

import (
	"testing"

	"github.com/stretchr/testify/require"

	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/fleet/pkg/compute"
)

func TestReadCapacityTags(t *testing.T) {
	tests := []struct {
		name    string
		tags    map[string]*string
		want    compute.CapacityByRole
		wantErr bool
	}{
		{name: "missing", want: compute.CapacityByRole{}},
		{name: "case insensitive", tags: map[string]*string{"AROHCP-CAPACITY-WORKER": ptr.To(`{"vcpus":8,"memoryGiB":32,"swiftNICs":3}`), "owner": ptr.To("team")}, want: compute.CapacityByRole{compute.PoolRoleWorker: {VCPUs: 8, MemoryGiB: 32, SwiftNICs: 3}}},
		{name: "nil", tags: map[string]*string{"arohcp-capacity-worker": nil}, wantErr: true},
		{name: "missing dimension", tags: map[string]*string{"arohcp-capacity-worker": ptr.To(`{"vcpus":8,"memoryGiB":32}`)}, wantErr: true},
		{name: "unknown dimension", tags: map[string]*string{"arohcp-capacity-worker": ptr.To(`{"vcpus":8,"memoryGiB":32,"swiftNICs":3,"extra":0}`)}, wantErr: true},
		{name: "null dimension", tags: map[string]*string{"arohcp-capacity-worker": ptr.To(`{"vcpus":8,"memoryGiB":null,"swiftNICs":3}`)}, wantErr: true},
		{name: "negative", tags: map[string]*string{"arohcp-capacity-worker": ptr.To(`{"vcpus":-8,"memoryGiB":32,"swiftNICs":3}`)}, wantErr: true},
		{name: "invalid JSON", tags: map[string]*string{"arohcp-capacity-worker": ptr.To(`broken`)}, wantErr: true},
		{name: "duplicate case", tags: map[string]*string{"arohcp-capacity-worker": ptr.To(`{"vcpus":8,"memoryGiB":32,"swiftNICs":3}`), "AROHCP-CAPACITY-WORKER": ptr.To(`{"vcpus":8,"memoryGiB":32,"swiftNICs":3}`)}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ReadCapacityTags(test.tags)
			if test.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, test.want, got)
			}
		})
	}
}
