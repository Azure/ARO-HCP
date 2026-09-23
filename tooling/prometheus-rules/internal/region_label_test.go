// Copyright 2025 Microsoft Corporation
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

package internal

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPreserveLabelInAggregations(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{
			name: "bare aggregation gets by clause",
			in:   "sum(workqueue_depth)",
			want: "sum by (region) (workqueue_depth)",
		},
		{
			name: "by clause gains region",
			in:   "max by (name, cluster) (workqueue_depth) > 10",
			want: "max by (name, cluster, region) (workqueue_depth) > 10",
		},
		{
			name: "by clause already has region is unchanged",
			in:   "max by (cluster, region) (workqueue_depth)",
			want: "max by (cluster, region) (workqueue_depth)",
		},
		{
			name: "without clause keeps region implicitly",
			in:   "sum without (pod) (workqueue_depth)",
			want: "sum without (pod) (workqueue_depth)",
		},
		{
			name: "without clause drops explicit region exclusion",
			in:   "sum without (pod, region) (workqueue_depth)",
			want: "sum without (pod) (workqueue_depth)",
		},
		{
			name: "without clause with only region becomes empty",
			in:   "sum without (region) (workqueue_depth)",
			want: "sum without () (workqueue_depth)",
		},
		{
			name: "nested aggregations all get region",
			in:   "sum by (cluster) (max by (cluster) (workqueue_depth))",
			want: "sum by (cluster, region) (max by (cluster, region) (workqueue_depth))",
		},
		{
			name: "topk with param",
			in:   "topk(5, sum by (cluster) (rate(http_requests_total[5m])))",
			want: "topk by (region) (5, sum by (cluster, region) (rate(http_requests_total[5m])))",
		},
		{
			name: "no aggregation is untouched",
			in:   "up == 0",
			want: "up == 0",
		},
		{
			name: "binary op aggregations on both sides",
			in:   "sum by (cluster) (a) / sum by (cluster) (b) > 0.8",
			want: "sum by (cluster, region) (a) / sum by (cluster, region) (b) > 0.8",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := preserveLabelInAggregations(tc.in, "region")
			require.NoError(t, err)
			require.Equal(t, tc.want, got)

			// Idempotency: applying the rewrite again must be a no-op.
			again, err := preserveLabelInAggregations(got, "region")
			require.NoError(t, err)
			require.Equal(t, got, again)
		})
	}
}

func TestPreserveLabelInAggregations_InvalidExpr(t *testing.T) {
	_, err := preserveLabelInAggregations("sum(", "region")
	require.Error(t, err)
}
