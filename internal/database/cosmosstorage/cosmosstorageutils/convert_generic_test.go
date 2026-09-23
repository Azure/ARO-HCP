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

package cosmosstorageutils

import (
	"testing"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

func TestTimeToLiveForInternal(t *testing.T) {
	tests := []struct {
		name string
		obj  any
		want int
	}{
		{
			name: "operations are TTL-governed",
			obj:  &coreapi.Operation{},
			want: operationTimeToLive,
		},
		{
			name: "clusters have no TTL",
			obj:  &coreapi.HCPOpenShiftCluster{},
			want: 0,
		},
		{
			name: "node pools have no TTL",
			obj:  &coreapi.HCPOpenShiftClusterNodePool{},
			want: 0,
		},
		{
			name: "subscriptions have no TTL",
			obj:  &coreapi.Subscription{},
			want: 0,
		},
		{
			name: "nil has no TTL",
			obj:  nil,
			want: 0,
		},
		{
			name: "typed nil operation pointer has no TTL",
			obj:  (*coreapi.Operation)(nil),
			want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := TimeToLiveForInternal(tt.obj); got != tt.want {
				t.Errorf("TimeToLiveForInternal(%T) = %d, want %d", tt.obj, got, tt.want)
			}
		})
	}
}
