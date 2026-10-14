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
	"strings"
	"testing"
	"time"

	"github.com/prometheus/prometheus/promql/promqltest"
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

// TestUnlessOnMatchesWithoutRegion evaluates the rewrite of
// hostedClusterAPI_kubeapiserver_available:sli_sum_15m. region is added to
// every by clause and left off unless on. The availability series have
// region="westus3"; hostedClusterAPI_deleting does not. At 35m the
// sum_over_time of the [15m:1m] subquery is 15 for a kept cluster. The
// deleting cluster is absent. Adding region only to that unless on keeps it.
func TestUnlessOnMatchesWithoutRegion(t *testing.T) {
	const source = `(
		sum by (name, namespace, _id, resource_id, cluster) (
			sum_over_time(
				(
					hostedClusterAPI_kubeapiserver_available{status="True"}
					and on (name, namespace, _id, resource_id, cluster)
					max by (name, namespace, _id, resource_id, cluster) ((hostedClusterAPI_kubeapiserver_available offset 15m) >= 0)
				)[15m:1m]
			)
		)
		and on (name, namespace, _id, resource_id, cluster)
		count by (name, namespace, _id, resource_id, cluster) (hostedClusterAPI_kubeapiserver_available)
	)
	unless on (name, namespace, _id, resource_id, cluster)
	(hostedClusterAPI_deleting{deletion_timestamp!=""})`
	rewritten, err := preserveLabelInAggregations(source, "region")
	require.NoError(t, err)
	again, err := preserveLabelInAggregations(rewritten, "region")
	require.NoError(t, err)
	require.Equal(t, rewritten, again)

	flat := strings.Join(strings.Fields(rewritten), " ")
	require.Contains(t, flat, "sum by (name, namespace, _id, resource_id, cluster, region)")
	require.Contains(t, flat, "max by (name, namespace, _id, resource_id, cluster, region)")
	require.Contains(t, flat, "count by (name, namespace, _id, resource_id, cluster, region)")
	require.Contains(t, flat, "sum_over_time")
	require.Contains(t, flat, "offset 15m")
	require.Contains(t, flat, "and on (name, namespace, _id, resource_id, cluster)")
	const unlessOn = "unless on (name, namespace, _id, resource_id, cluster)"
	require.Contains(t, flat, unlessOn+" (hostedClusterAPI_deleting{deletion_timestamp!=\"\"})")
	require.NotContains(t, flat, "unless on (name, namespace, _id, resource_id, cluster, region)")

	withRegionOn := strings.Replace(flat, unlessOn, "unless on (name, namespace, _id, resource_id, cluster, region)", 1)
	require.NotEqual(t, flat, withRegionOn)

	engine := promqltest.NewTestEngine(t, false, 5*time.Minute, promqltest.DefaultMaxSamplesPerQuery)
	promqltest.RunTest(t, `
		load 1m
			hostedClusterAPI_kubeapiserver_available{status="True",name="a",namespace="ns",_id="id-a",resource_id="rid-a",cluster="mc",region="westus3"} 1+0x40
			hostedClusterAPI_kubeapiserver_available{status="True",name="b",namespace="ns",_id="id-b",resource_id="rid-b",cluster="mc",region="westus3"} 1+0x40
			hostedClusterAPI_deleting{name="a",namespace="ns",_id="id-a",resource_id="rid-a",cluster="mc",deletion_timestamp="2026-07-10T08:36:42Z"} 1+0x40

		# Rewritten sli_sum_15m. Cluster a is absent.
		eval instant at 35m `+flat+`
			{_id="id-b",cluster="mc",name="b",namespace="ns",region="westus3",resource_id="rid-b"} 15

		# region added to unless on. Cluster a is present.
		eval instant at 35m `+withRegionOn+`
			{_id="id-a",cluster="mc",name="a",namespace="ns",region="westus3",resource_id="rid-a"} 15
			{_id="id-b",cluster="mc",name="b",namespace="ns",region="westus3",resource_id="rid-b"} 15
	`, engine)
}
