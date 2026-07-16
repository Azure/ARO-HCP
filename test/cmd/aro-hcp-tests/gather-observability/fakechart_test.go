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

package gatherobservability

import (
	"fmt"
	"math"
	"math/rand"
	"os"
	"testing"
	"time"

	"github.com/Azure/ARO-HCP/test/util/timing"
)

func TestGenerateFakeChart(t *testing.T) {
	if os.Getenv("GENERATE_FAKE_CHART") == "" {
		t.Skip("set GENERATE_FAKE_CHART=1 to generate")
	}

	start := time.Date(2026, 7, 16, 13, 51, 0, 0, time.UTC)
	end := time.Date(2026, 7, 16, 15, 18, 0, 0, time.UTC)
	tw := timing.TimeWindow{Start: start, End: end}

	mc1 := "/providers/microsoft.redhatopenshift/stamps/1/managementclusters/default"
	mc2 := "/providers/microsoft.redhatopenshift/stamps/2/managementclusters/default"
	phases := []string{"accepted", "provisioning", "succeeded", "deleting", "updating"}

	rng := rand.New(rand.NewSource(42))

	var results []PrometheusResult
	for _, mc := range []string{mc1, mc2, ""} {
		for _, phase := range phases {
			var values [][]any
			baseCount := 0.0
			switch phase {
			case "succeeded":
				baseCount = 25
			case "provisioning":
				baseCount = 8
			case "accepted":
				baseCount = 5
			case "deleting":
				baseCount = 3
			case "updating":
				baseCount = 2
			}
			if mc == mc2 {
				baseCount *= 0.8
			}
			if mc == "" {
				baseCount = 1
			}

			for ts := start.Unix(); ts <= end.Unix(); ts += 60 {
				elapsed := float64(ts-start.Unix()) / float64(end.Unix()-start.Unix())
				count := baseCount * (0.3 + 0.7*elapsed)
				count += rng.Float64()*3 - 1.5
				count = math.Max(0, math.Round(count))
				values = append(values, []any{float64(ts), fmt.Sprintf("%d", int(count))})
			}

			results = append(results, PrometheusResult{
				Metric: map[string]string{
					"management_cluster_resource_id": mc,
					"phase":                          phase,
				},
				Values: values,
			})
		}
	}

	cd := buildChartData(
		"Hosted Control Planes by Management Cluster and Phase",
		"Faceted stacked area chart with fake data",
		"count by (management_cluster_resource_id, phase) (backend_cluster_provision_state)",
		"clusters",
		"",
		results,
		tw,
		0,
		chartTypeFacetedStackedArea,
		"management_cluster_resource_id",
	)

	pageData := panelPageData{
		Title:  "Backend Metrics",
		Charts: []chartData{cd},
	}
	pageData.TimeWindow.Start = tw.Start.UTC().Format(time.RFC3339)
	pageData.TimeWindow.End = tw.End.UTC().Format(time.RFC3339)

	outputPath := "/tmp/faceted-stacked-area-preview.html"
	if err := renderPanel(outputPath, pageData); err != nil {
		t.Fatalf("render failed: %v", err)
	}
	t.Logf("wrote preview to %s", outputPath)
}
