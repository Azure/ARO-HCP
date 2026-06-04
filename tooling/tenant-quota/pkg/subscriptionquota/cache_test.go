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

package subscriptionquota

import (
	"testing"
)

func TestCacheKey(t *testing.T) {
	type testCase struct {
		name           string
		source         string
		subscriptionID string
		region         string
		quotaName      string
		metricType     string
		want           string
	}

	testCases := []testCase{
		{
			name:           "regional usage metric",
			source:         "compute",
			subscriptionID: "sub-1",
			region:         "eastus",
			quotaName:      "cores",
			metricType:     "usage",
			want:           "compute/sub-1/eastus/cores/usage",
		},
		{
			name:           "non regional limit metric",
			source:         "rbac",
			subscriptionID: "sub-2",
			region:         "",
			quotaName:      "roleAssignments",
			metricType:     "limit",
			want:           "rbac/sub-2//roleAssignments/limit",
		},
	}

	for _, tc := range testCases {

		t.Run(tc.name, func(t *testing.T) {
			if got := cacheKey(tc.source, tc.subscriptionID, tc.region, tc.quotaName, tc.metricType); got != tc.want {
				t.Fatalf("cacheKey() = %q, want %q", got, tc.want)
			}
		})
	}
}
