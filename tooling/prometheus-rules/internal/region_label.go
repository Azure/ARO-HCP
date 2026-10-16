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
	"fmt"

	"github.com/prometheus/prometheus/promql/parser"
)

// preserveLabelInAggregations parses a PromQL expression and rewrites every
// aggregation so that the given label survives to the output vector. Azure
// Managed Prometheus sets a fired alert's labels to the label set of the
// alert expression's output vector, so a label that an aggregation drops is
// lost from the alert (and therefore from downstream systems such as the
// MonitoringEvents Kusto database).
//
// The rewrite is safe because each regional Azure Monitor Workspace only ever
// contains a single value for these workspace-scoped external labels (e.g.
// region), so adding the label to a `by (...)` grouping cannot fragment series
// across values — it only re-attaches the label to the result.
//
//   - `by (...)` (including the bare `sum(x)` form) keeps ONLY the listed
//     labels, so the label is appended when missing:
//     `sum by (cluster) (x)` -> `sum by (cluster, region) (x)`
//     `sum(x)`               -> `sum by (region) (x)`
//   - `without (...)` keeps every label EXCEPT those listed, so the label is
//     removed from the list when present so it is no longer dropped:
//     `sum without (pod, region) (x)` -> `sum without (pod) (x)`
func preserveLabelInAggregations(expr string, label string) (string, error) {
	p := parser.NewParser(parser.Options{})
	parsed, err := p.ParseExpr(expr)
	if err != nil {
		return "", fmt.Errorf("failed to parse PromQL expression %q: %w", expr, err)
	}

	// Inspect visits every node in the AST, including nested aggregations and
	// aggregation parameters (e.g. the count in topk). Handling all of them
	// ensures the label flows from the leaves up to the root output vector.
	parser.Inspect(parsed, func(node parser.Node, _ []parser.Node) error {
		agg, ok := node.(*parser.AggregateExpr)
		if !ok {
			return nil
		}

		if agg.Without {
			// `without (...)` already preserves the label unless it is
			// explicitly listed; drop it from the exclusion list.
			kept := agg.Grouping[:0]
			for _, g := range agg.Grouping {
				if g != label {
					kept = append(kept, g)
				}
			}
			agg.Grouping = kept
			return nil
		}

		// `by (...)` keeps only the listed labels; add ours if absent.
		for _, g := range agg.Grouping {
			if g == label {
				return nil
			}
		}
		agg.Grouping = append(agg.Grouping, label)
		return nil
	})

	return parsed.String(), nil
}
