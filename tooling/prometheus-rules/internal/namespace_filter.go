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
	"strings"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

// normalizeExpr parses a PromQL expression and re-serializes it through
// the AST's String() method, producing a canonical formatting. This ensures
// that future changes (like adding a namespace filter) produce minimal diffs.
func normalizeExpr(expr string) (string, error) {
	p := parser.NewParser(parser.Options{})
	parsed, err := p.ParseExpr(expr)
	if err != nil {
		return "", fmt.Errorf("failed to parse PromQL expression %q: %w", expr, err)
	}
	return parsed.String(), nil
}

// injectNamespaceFilter parses a PromQL expression, injects
// namespace=~"ns1|ns2|..." into every VectorSelector that does not
// already have a namespace matcher, and returns the modified expression string.
func injectNamespaceFilter(expr string, namespaces []string) (string, error) {
	p := parser.NewParser(parser.Options{})
	parsed, err := p.ParseExpr(expr)
	if err != nil {
		return "", fmt.Errorf("failed to parse PromQL expression %q: %w", expr, err)
	}

	var filtered []string
	for _, ns := range namespaces {
		if trimmed := strings.TrimSpace(ns); trimmed != "" {
			filtered = append(filtered, trimmed)
		}
	}
	if len(filtered) == 0 {
		return parsed.String(), nil
	}

	namespaceRegex := strings.Join(filtered, "|")
	namespaceMatcher, err := labels.NewMatcher(labels.MatchRegexp, "namespace", namespaceRegex)
	if err != nil {
		return "", fmt.Errorf("failed to create namespace matcher for %q: %w", namespaceRegex, err)
	}

	parser.Inspect(parsed, func(node parser.Node, _ []parser.Node) error {
		vs, ok := node.(*parser.VectorSelector)
		if !ok {
			return nil
		}
		// Skip if a namespace matcher already exists
		for _, m := range vs.LabelMatchers {
			if m.Name == "namespace" {
				return nil
			}
		}
		vs.LabelMatchers = append(vs.LabelMatchers, namespaceMatcher)
		return nil
	})

	return parsed.String(), nil
}
