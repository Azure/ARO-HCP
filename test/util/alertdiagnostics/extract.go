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

// Package alertdiagnostics extracts diagnostic signals, not equivalent alert
// predicates. Independent branches and vector operands can include series that
// do not fire the alert; Plan.Expression retains their original relationships.
package alertdiagnostics

import (
	"fmt"
	"math"
	"slices"

	"github.com/prometheus/prometheus/promql/parser"
)

type Plan struct {
	Expression string      `json:"expression"`
	Conditions []Condition `json:"conditions"`
}

type Condition struct {
	Expression  string      `json:"expression"`
	Path        string      `json:"path"`
	Queries     []Query     `json:"queries,omitempty"`
	Thresholds  []Threshold `json:"thresholds,omitempty"`
	Unsupported string      `json:"unsupported,omitempty"`
}

type Query struct {
	Expression string `json:"expression"`
	Role       string `json:"role"`
}

type Threshold struct {
	Operator string  `json:"operator"`
	Value    float64 `json:"value"`
}

// Extract parses an instant-vector alert expression and removes only its outer
// comparisons. Paths address branches in the normalized full expression. An
// unsupported condition has no queries or thresholds: callers should query its
// Expression unchanged, without claiming it is an unfiltered diagnostic signal.
func Extract(expression string) (Plan, error) {
	expr, err := parser.NewParser(parser.Options{}).ParseExpr(expression)
	if err != nil {
		return Plan{}, fmt.Errorf("parse alert expression: %w", err)
	}
	if expr.Type() != parser.ValueTypeVector {
		return Plan{}, fmt.Errorf("alert expression must return an instant vector, got %s", expr.Type())
	}
	return Plan{Expression: expr.String(), Conditions: extract(expr, "root", nil)}, nil
}

func extract(expr parser.Expr, path string, exclusions []*parser.BinaryExpr) []Condition {
	expr = unparen(expr)
	condition := Condition{Expression: withExclusions(expr, exclusions).String(), Path: path}
	unsupported := func(reason string) []Condition {
		condition.Unsupported = reason
		condition.Queries = nil
		condition.Thresholds = nil
		return []Condition{condition}
	}
	if binary, ok := expr.(*parser.BinaryExpr); ok {
		switch binary.Op {
		case parser.LUNLESS:
			return extract(binary.LHS, path+"/unless:left", append(slices.Clone(exclusions), binary))
		case parser.LAND, parser.LOR:
			if binary.Op == parser.LOR {
				for _, operand := range []parser.Expr{binary.LHS, binary.RHS} {
					if call, ok := unparen(operand).(*parser.Call); ok && call.Func.Name == "vector" {
						if value, ok := constantNumber(call.Args[0]); ok && value == 0 {
							return unsupported("zero-fill union must remain intact rather than being split into independent conditions")
						}
					}
				}
			}
			// AND retains LHS labels; OR prefers LHS labels for matching series.
			// Both need agreement on exclusion keys before distributing it.
			for _, exclusion := range exclusions {
				if !sameExclusionKeys(exclusion.VectorMatching, binary.VectorMatching) {
					return unsupported("unless cannot be distributed across " + binary.Op.String() + ": exclusion labels may differ on the right branch")
				}
			}
			left := extract(binary.LHS, path+"/"+binary.Op.String()+":left", exclusions)
			right := extract(binary.RHS, path+"/"+binary.Op.String()+":right", exclusions)
			return append(left, right...)
		}
	}

	signal := expr
	var comparisons []*parser.BinaryExpr
	for {
		binary, ok := unparen(signal).(*parser.BinaryExpr)
		if !ok || !binary.Op.IsComparisonOperator() {
			break
		}
		if binary.ReturnBool {
			return unsupported("bool comparison changes sample values rather than filtering; thresholds would misrepresent alert presence")
		}
		if binary.LHS.Type() == parser.ValueTypeVector && binary.RHS.Type() == parser.ValueTypeVector {
			if len(condition.Thresholds) != 0 {
				return unsupported("comparison chain over a vector comparison cannot attach thresholds unambiguously to both operands")
			}
			for _, operand := range []parser.Expr{binary.LHS, binary.RHS} {
				if nested, ok := unparen(operand).(*parser.BinaryExpr); ok && (nested.Op.IsComparisonOperator() || nested.Op.IsSetOperator()) {
					return unsupported("vector comparison operand exposes a comparison or set operation; threshold ownership and label provenance are ambiguous")
				}
			}
			for _, exclusion := range exclusions {
				// Vector comparisons can project, drop, or import output labels.
				// Only explicit exclusion keys shared by both operands are proven safe.
				if !exclusion.VectorMatching.On || !sameExclusionKeys(exclusion.VectorMatching, binary.VectorMatching) {
					return unsupported("unless cannot be applied to vector comparison operands: result and operand labels may differ")
				}
			}
			if containsAbsence(binary.LHS) || containsAbsence(binary.RHS) {
				return unsupported("absence construct has no numeric pre-absence signal without rewriting internal semantics")
			}
			condition.Queries = []Query{
				{Expression: withExclusions(binary.LHS, exclusions).String(), Role: "left"},
				{Expression: withExclusions(binary.RHS, exclusions).String(), Role: "right"},
			}
			return []Condition{condition}
		}
		op := binary.Op
		constant := binary.RHS
		signal = binary.LHS
		if binary.LHS.Type() == parser.ValueTypeScalar {
			constant, signal = binary.LHS, binary.RHS
			switch op {
			case parser.GTR:
				op = parser.LSS
			case parser.GTE:
				op = parser.LTE
			case parser.LSS:
				op = parser.GTR
			case parser.LTE:
				op = parser.GTE
			}
		}
		value, ok := constantNumber(constant)
		if !ok || math.IsNaN(value) || math.IsInf(value, 0) {
			return unsupported("comparison threshold is not a finite constant arithmetic expression")
		}
		condition.Thresholds = append(condition.Thresholds, Threshold{Operator: op.String(), Value: value})
		comparisons = append(comparisons, binary)
	}
	if binary, ok := unparen(signal).(*parser.BinaryExpr); ok && binary.Op.IsSetOperator() && len(condition.Thresholds) != 0 {
		switch binary.Op {
		case parser.LAND, parser.LUNLESS:
			// These operators preserve LHS sample values. Move the scalar
			// filters onto that operand only, retaining their original order and
			// orientation. Copies avoid mutating the original plan or exclusions.
			rewritten := *binary
			left := binary.LHS
			for i := len(comparisons) - 1; i >= 0; i-- {
				comparison := *comparisons[i]
				if comparison.LHS.Type() == parser.ValueTypeScalar {
					comparison.RHS = &parser.ParenExpr{Expr: left}
				} else {
					comparison.LHS = &parser.ParenExpr{Expr: left}
				}
				left = &comparison
			}
			rewritten.LHS = &parser.ParenExpr{Expr: left}
			// Each rewrite moves the filters below one set node, so recursion
			// terminates. Branch paths still name the original set operators.
			conditions := extract(&rewritten, path, exclusions)
			if len(conditions) == 1 && conditions[0].Unsupported != "" && conditions[0].Path == path {
				conditions[0].Expression = condition.Expression
			}
			return conditions
		case parser.LOR:
			if hasOuterComparison(binary) {
				return unsupported("comparison over or with filtered branches cannot be extracted without changing left-priority semantics")
			}
			// Do not distribute a filter over OR: a filtered-out LHS would
			// otherwise allow a matching RHS series to replace it.
		}
	}
	if containsAbsence(signal) {
		return unsupported("absence construct has no numeric pre-absence signal without rewriting internal semantics")
	}
	// Comparisons were peeled outside-in; report thresholds in source order.
	slices.Reverse(condition.Thresholds)
	condition.Queries = []Query{{Expression: withExclusions(unparen(signal), exclusions).String(), Role: "signal"}}
	return []Condition{condition}
}

// Only inspect exposed set branches, not intentional filters inside numeric
// arithmetic, aggregations, or functions.
func hasOuterComparison(expr parser.Expr) bool {
	binary, ok := unparen(expr).(*parser.BinaryExpr)
	if !ok {
		return false
	}
	return binary.Op.IsComparisonOperator() || (binary.Op.IsSetOperator() && (hasOuterComparison(binary.LHS) || hasOuterComparison(binary.RHS)))
}

func unparen(expr parser.Expr) parser.Expr {
	for {
		paren, ok := expr.(*parser.ParenExpr)
		if !ok {
			return expr
		}
		expr = paren.Expr
	}
}

func withExclusions(expr parser.Expr, exclusions []*parser.BinaryExpr) parser.Expr {
	for i := len(exclusions) - 1; i >= 0; i-- {
		exclusion := *exclusions[i]
		exclusion.LHS = &parser.ParenExpr{Expr: expr}
		exclusion.RHS = &parser.ParenExpr{Expr: exclusion.RHS}
		expr = &exclusion
	}
	return expr
}

func sameExclusionKeys(exclusion, matching *parser.VectorMatching) bool {
	if exclusion.On {
		for _, label := range exclusion.MatchingLabels {
			if label == "__name__" || (matching.On != slices.Contains(matching.MatchingLabels, label)) {
				return false
			}
		}
		return true
	}
	if matching.On {
		return false
	}
	for _, ignored := range matching.MatchingLabels {
		if ignored != "__name__" && !slices.Contains(exclusion.MatchingLabels, ignored) {
			return false
		}
	}
	return true
}

func containsAbsence(expr parser.Expr) bool {
	found := false
	parser.Inspect(expr, func(node parser.Node, _ []parser.Node) error {
		if call, ok := node.(*parser.Call); ok && (call.Func.Name == "absent" || call.Func.Name == "absent_over_time") {
			found = true
		}
		return nil
	})
	return found
}

// Evaluate only literal arithmetic, never functions, selectors, or comparisons.
func constantNumber(expr parser.Expr) (float64, bool) {
	switch expr := unparen(expr).(type) {
	case *parser.NumberLiteral:
		return expr.Val, true
	case *parser.UnaryExpr:
		value, ok := constantNumber(expr.Expr)
		if expr.Op == parser.SUB {
			value = -value
		}
		return value, ok
	case *parser.BinaryExpr:
		left, lok := constantNumber(expr.LHS)
		right, rok := constantNumber(expr.RHS)
		if !lok || !rok {
			return 0, false
		}
		switch expr.Op {
		case parser.ADD:
			return left + right, true
		case parser.SUB:
			return left - right, true
		case parser.MUL:
			return left * right, true
		case parser.DIV:
			return left / right, true
		case parser.MOD:
			return math.Mod(left, right), true
		case parser.POW:
			return math.Pow(left, right), true
		}
	}
	return 0, false
}
