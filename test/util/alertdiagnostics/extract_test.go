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

package alertdiagnostics

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/prometheus/prometheus/promql/parser"
	"github.com/prometheus/prometheus/promql/promqltest"
)

func TestExtract(t *testing.T) {
	for _, tc := range []struct {
		name       string
		input      string
		queries    []Query
		thresholds []Threshold
	}{
		{"threshold", `rate(errors[5m] offset 1h) > 2`, []Query{{`rate(errors[5m] offset 1h)`, "signal"}}, []Threshold{{">", 2}}},
		{"chain", `used / ignoring(type) (capacity > 0) > .9 < 1`, []Query{{`used / ignoring (type) (capacity > 0)`, "signal"}}, []Threshold{{">", .9}, {"<", 1}}},
		{"scalar left", `5 < metric`, []Query{{"metric", "signal"}}, []Threshold{{">", 5}}},
		{"scalar left negative", `-5 >= metric`, []Query{{"metric", "signal"}}, []Threshold{{"<=", -5}}},
		{"scalar left equal", `5 == metric`, []Query{{"metric", "signal"}}, []Threshold{{"==", 5}}},
		{"arithmetic threshold", `metric > (2 * 60 + 4 ^ 2 - 8 % 3) / 2`, []Query{{"metric", "signal"}}, []Threshold{{">", 67}}},
		{"vectors", `desired != on(namespace) group_left(kind) actual`, []Query{{"desired", "left"}, {"actual", "right"}}, nil},
		{"internal guards", `sum by (cluster) (rate(errors[5m]) and (up == 1)) / (sum(up) > 0) > .2`, []Query{{`sum by (cluster) (rate(errors[5m]) and (up == 1)) / (sum(up) > 0)`, "signal"}}, []Threshold{{">", .2}}},
		{"function guard", `max_over_time((metric > 0)[1h:5m] offset 1d) > 2`, []Query{{`max_over_time((metric > 0)[1h:5m] offset 1d)`, "signal"}}, []Threshold{{">", 2}}},
		{"no comparison", `sum(metric == 0)`, []Query{{`sum(metric == 0)`, "signal"}}, nil},
		{"constant vector", `vector(1)`, []Query{{`vector(1)`, "signal"}}, nil},
		{"at modifier", `metric @ 1234 offset 5m > 3`, []Query{{`metric @ 1234.000 offset 5m`, "signal"}}, []Threshold{{">", 3}}},
		{"unless", `(metric > 5) unless on(subscription_id) internal_subscription:info`, []Query{{`(metric) unless on (subscription_id) (internal_subscription:info)`, "signal"}}, []Threshold{{">", 5}}},
		{"nested unless", `metric > 5 unless on(a) (excluded == 1) unless on(b) other`, []Query{{`((metric) unless on (a) ((excluded == 1))) unless on (b) (other)`, "signal"}}, []Threshold{{">", 5}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := Extract(tc.input)
			if err != nil {
				t.Fatal(err)
			}
			validatePlan(t, plan)
			if len(plan.Conditions) != 1 {
				t.Fatalf("expected one condition: %+v", plan)
			}
			condition := plan.Conditions[0]
			if condition.Unsupported != "" {
				t.Fatalf("unexpected unsupported condition: %+v", condition)
			}
			if diff := cmp.Diff(tc.queries, condition.Queries); diff != "" {
				t.Errorf("queries (-want +got): %s", diff)
			}
			if diff := cmp.Diff(tc.thresholds, condition.Thresholds); diff != "" {
				t.Errorf("thresholds (-want +got): %s", diff)
			}
		})
	}
}

func TestBranches(t *testing.T) {
	plan, err := Extract(`((a > 1 and on(subscription_id) b < 2) or c == 3) unless on(subscription_id) excluded`)
	if err != nil {
		t.Fatal(err)
	}
	validatePlan(t, plan)
	want := []Condition{
		{Expression: `(a > 1) unless on (subscription_id) (excluded)`, Path: "root/unless:left/or:left/and:left", Queries: []Query{{`(a) unless on (subscription_id) (excluded)`, "signal"}}, Thresholds: []Threshold{{">", 1}}},
		{Expression: `(b < 2) unless on (subscription_id) (excluded)`, Path: "root/unless:left/or:left/and:right", Queries: []Query{{`(b) unless on (subscription_id) (excluded)`, "signal"}}, Thresholds: []Threshold{{"<", 2}}},
		{Expression: `(c == 3) unless on (subscription_id) (excluded)`, Path: "root/unless:left/or:right", Queries: []Query{{`(c) unless on (subscription_id) (excluded)`, "signal"}}, Thresholds: []Threshold{{"==", 3}}},
	}
	if diff := cmp.Diff(want, plan.Conditions); diff != "" {
		t.Fatal(diff)
	}
	if !strings.Contains(plan.Expression, "and on (subscription_id)") || !strings.Contains(plan.Expression, ") or c") {
		t.Fatalf("full expression lost grouping/matching: %s", plan.Expression)
	}
}

func TestUnsupported(t *testing.T) {
	for _, tc := range []struct{ input, reason string }{
		{`absent(metric)`, "absence construct"},
		{`absent_over_time(metric[1h]) == 1`, "absence construct"},
		{`sum(absent(metric)) > 0`, "absence construct"},
		{`a != absent(b)`, "absence construct"},
		{`a > bool 5`, "bool comparison"},
		{`(a > bool 5) == 1`, "bool comparison"},
		{`a > time()`, "not a finite constant"},
		{`a > Inf`, "not a finite constant"},
		{`a > (0 / 0)`, "not a finite constant"},
		{`(a != b) > 1`, "comparison chain over a vector"},
		{`((a > 5) or on(job) (b > 1)) > 0`, "left-priority semantics"},
		{`(a or on(job) (b > 1)) > 5`, "left-priority semantics"},
		{`((a > 5) or b) > 1`, "left-priority semantics"},
		{`(a and on(job) (phase == 1)) > bool 5`, "bool comparison"},
		{`((a and on(job) (phase == 1)) > bool 5) == 1`, "bool comparison"},
		{`((a and on(job) (phase == 1)) > 5) unless on(extra) excluded`, "exclusion labels may differ"},
		{`(a > 5) != b`, "vector comparison operand exposes"},
		{`a != (b > 5)`, "vector comparison operand exposes"},
		{`(a != on(job) b) != on(job) c`, "vector comparison operand exposes"},
		{`(a and on(job) b) != c`, "vector comparison operand exposes"},
		{`a or vector(0)`, "zero-fill union"},
		{`(a > on(job) b) unless excluded`, "result and operand labels may differ"},
		{`(a > ignoring(extra) b) unless on(extra) excluded`, "result and operand labels may differ"},
		{`(a > on(job) group_left(extra) b) unless on(extra) excluded`, "result and operand labels may differ"},
		{`(a > on(job) group_right(extra) b) unless on(extra) excluded`, "result and operand labels may differ"},
		{`(a > b) unless on(__name__) excluded`, "result and operand labels may differ"},
		{`(a and on(job) b) unless on(extra) excluded`, "exclusion labels may differ on the right branch"},
		{`(a and ignoring(extra) b) unless excluded`, "exclusion labels may differ on the right branch"},
		{`(a or on(job) b) unless on(extra) excluded`, "exclusion labels may differ on the right branch"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			plan, err := Extract(tc.input)
			if err != nil {
				t.Fatal(err)
			}
			validatePlan(t, plan)
			if len(plan.Conditions) != 1 || !strings.Contains(plan.Conditions[0].Unsupported, tc.reason) {
				t.Fatalf("expected reason %q: %+v", tc.reason, plan)
			}
			if plan.Conditions[0].Path == "root" && plan.Conditions[0].Expression != plan.Expression {
				t.Fatalf("whole-expression fallback must preserve original grouping: %+v", plan)
			}
		})
	}
}

func TestComparisonOverSet(t *testing.T) {
	for _, tc := range []struct {
		input      string
		paths      []string
		queries    []string
		thresholds [][]Threshold
	}{
		{
			input: `((a > 5) and on(job) (b > 1)) > 0`,
			paths: []string{"root/and:left", "root/and:right"}, queries: []string{"a", "b"},
			thresholds: [][]Threshold{{{">", 5}, {">", 0}}, {{">", 1}}},
		},
		{
			input: `(a and on(job) (phase == 1)) > 5 < 20`,
			paths: []string{"root/and:left", "root/and:right"}, queries: []string{"a", "phase"},
			thresholds: [][]Threshold{{{">", 5}, {"<", 20}}, {{"==", 1}}},
		},
		{
			input: `20 > (5 < (a and on(job) (phase == 1)))`,
			paths: []string{"root/and:left", "root/and:right"}, queries: []string{"a", "phase"},
			thresholds: [][]Threshold{{{">", 5}, {"<", 20}}, {{"==", 1}}},
		},
		{
			input:      `((a and on(job) (phase == 1)) unless on(job) excluded) > 5`,
			paths:      []string{"root/unless:left/and:left", "root/unless:left/and:right"},
			queries:    []string{`(a) unless on (job) (excluded)`, `(phase) unless on (job) (excluded)`},
			thresholds: [][]Threshold{{{">", 5}}, {{"==", 1}}},
		},
		{
			input:      `((a unless on(job) excluded) and on(job) (phase == 1)) > 5`,
			paths:      []string{"root/and:left/unless:left", "root/and:right"},
			queries:    []string{`(a) unless on (job) (excluded)`, "phase"},
			thresholds: [][]Threshold{{{">", 5}}, {{"==", 1}}},
		},
		{
			input:      `((a and on(job) b > 1) and on(job) c > 2) > 5`,
			paths:      []string{"root/and:left/and:left", "root/and:left/and:right", "root/and:right"},
			queries:    []string{"a", "b", "c"},
			thresholds: [][]Threshold{{{">", 5}}, {{">", 1}}, {{">", 2}}},
		},
		{
			input: `((a or on(job) b) and on(job) phase == 1) > 5`,
			paths: []string{"root/and:left", "root/and:right"}, queries: []string{`a or on (job) b`, "phase"},
			thresholds: [][]Threshold{{{">", 5}}, {{"==", 1}}},
		},
		{
			input: `(a unless ignoring(key, value) excluded) == 1`,
			paths: []string{"root/unless:left"}, queries: []string{`(a) unless ignoring (key, value) (excluded)`},
			thresholds: [][]Threshold{{{"==", 1}}},
		},
	} {
		t.Run(tc.input, func(t *testing.T) {
			expr, err := parser.NewParser(parser.Options{}).ParseExpr(tc.input)
			if err != nil {
				t.Fatal(err)
			}
			original := expr.String()
			plan, err := Extract(tc.input)
			if err != nil {
				t.Fatal(err)
			}
			validatePlan(t, plan)
			first := extract(expr, "root", nil)
			second := extract(expr, "root", nil)
			if expr.String() != original || plan.Expression != original {
				t.Fatal("rewriting mutated original AST or plan expression")
			}
			if diff := cmp.Diff(first, second); diff != "" {
				t.Fatalf("repeated extraction changed: %s", diff)
			}
			if len(plan.Conditions) != len(tc.paths) {
				t.Fatalf("expected %d conditions: %+v", len(tc.paths), plan)
			}
			for i, condition := range plan.Conditions {
				if condition.Unsupported != "" || condition.Path != tc.paths[i] {
					t.Fatalf("unexpected condition at %s: %+v", tc.paths[i], condition)
				}
				if diff := cmp.Diff([]Query{{Expression: tc.queries[i], Role: "signal"}}, condition.Queries); diff != "" {
					t.Errorf("%s queries (-want +got): %s", condition.Path, diff)
				}
				if diff := cmp.Diff(tc.thresholds[i], condition.Thresholds); diff != "" {
					t.Errorf("%s threshold ownership (-want +got): %s", condition.Path, diff)
				}
			}
		})
	}
}

func TestComparisonOverSetEvaluation(t *testing.T) {
	engine := promqltest.NewTestEngine(t, false, 5*time.Minute, 10000)
	// The elapsed threshold must never be applied to the 0/1 phase signal.
	// Independent charts intentionally include inactive and below-threshold data,
	// while rejoining their condition predicates must reproduce the alert.
	for _, input := range []string{
		`((elapsed and on(job) (phase == 1)) > 5 < 20) unless on(job) excluded`,
		`20 > (5 < ((elapsed and on(job) (phase == 1)) unless on(job) excluded))`,
	} {
		t.Run(input, func(t *testing.T) {
			plan, err := Extract(input)
			if err != nil {
				t.Fatal(err)
			}
			validatePlan(t, plan)
			if len(plan.Conditions) != 2 || len(plan.Conditions[0].Queries) != 1 || len(plan.Conditions[1].Queries) != 1 {
				t.Fatalf("expected elapsed and phase diagnostics: %+v", plan)
			}
			left, right := plan.Conditions[0], plan.Conditions[1]
			script := `load 1m
elapsed{job="active"} 10
elapsed{job="below"} 2
elapsed{job="inactive"} 10
elapsed{job="internal"} 10
elapsed{job="above"} 30
phase{job="active"} 1
phase{job="below"} 1
phase{job="inactive"} 0
phase{job="internal"} 1
phase{job="above"} 1
excluded{job="internal"} 1

`
			for _, expression := range []string{plan.Expression, fmt.Sprintf("(%s) and on(job) (%s)", left.Expression, right.Expression)} {
				script += fmt.Sprintf("eval instant at 0m %s\nelapsed{job=\"active\"} 10\n\n", expression)
			}
			script += fmt.Sprintf(`eval instant at 0m %s
elapsed{job="active"} 10
elapsed{job="below"} 2
elapsed{job="inactive"} 10
elapsed{job="above"} 30

eval instant at 0m %s
phase{job="active"} 1
phase{job="below"} 1
phase{job="inactive"} 0
phase{job="above"} 1

`, left.Queries[0].Expression, right.Queries[0].Expression)
			promqltest.RunTest(t, script, engine)
		})
	}
}

func TestSafeExclusionMatching(t *testing.T) {
	for _, input := range []string{
		`(a > on(job) b) unless on(job) excluded`,
		`(a > ignoring(extra) b) unless on(job) excluded`,
		`(a > on(job) group_right(extra) b) unless on(job) excluded`,
		`(a and b) unless excluded`,
		`(a and ignoring(extra) b) unless ignoring(extra) excluded`,
		`(a or on(job) b) unless on(job) excluded`,
	} {
		t.Run(input, func(t *testing.T) {
			plan, err := Extract(input)
			if err != nil {
				t.Fatal(err)
			}
			validatePlan(t, plan)
			for _, condition := range plan.Conditions {
				if condition.Unsupported != "" {
					t.Fatalf("unexpected fallback: %+v", condition)
				}
				for _, query := range condition.Queries {
					if !strings.Contains(query.Expression, "unless") {
						t.Fatalf("lost exclusion: %+v", query)
					}
				}
			}
		})
	}
}

func TestInvalid(t *testing.T) {
	for _, input := range []string{"", "a >", "a[5m]", `"hello"`, "1", "time()", "1 > bool 0"} {
		if _, err := Extract(input); err == nil {
			t.Errorf("expected invalid alert expression %q to fail", input)
		}
	}
}

func TestExclusionEvaluation(t *testing.T) {
	engine := promqltest.NewTestEngine(t, false, 5*time.Minute, 10000)
	for _, tc := range []struct {
		name, input, data string
		want              []string
		fallback          bool
	}{
		{
			name:  "threshold removed but excluded subscription stays excluded",
			input: `(a > 5) unless on(subscription_id) excluded`,
			data: `a{subscription_id="internal"} 10
a{subscription_id="customer"} 2
excluded{subscription_id="internal"} 1`,
			want: []string{`a{subscription_id="customer"} 2`},
		},
		{
			name:  "both vector operands retain safe exclusion keys",
			input: `(a > on(subscription_id) b) unless on(subscription_id) excluded`,
			data: `a{subscription_id="internal",extra="left"} 10
a{subscription_id="customer",extra="left"} 2
b{subscription_id="internal",extra="right"} 5
b{subscription_id="customer",extra="right"} 5
excluded{subscription_id="internal"} 1`,
			want: []string{`a{subscription_id="customer",extra="left"} 2`, `b{subscription_id="customer",extra="right"} 5`},
		},
		{
			name:  "vector comparison output drops labels used by exclusion",
			input: `(a > on(job) b) unless on(extra) excluded`,
			data: `a{job="j",extra="left"} 10
b{job="j",extra="right"} 5
excluded{extra="left"} 1`,
			// Comparison output has only job, so excluding a directly would
			// incorrectly remove the diagnostic. The fallback must still fire.
			want: []string{`{job="j"} 10`}, fallback: true,
		},
		{
			name:  "and output uses left labels not right labels",
			input: `(a > 5 and on(job) b > 1) unless on(extra) excluded`,
			data: `a{job="j",extra="left"} 10
b{job="j",extra="right"} 5
excluded{extra="left"} 1`,
			// Applying exclusion independently to b would leak a diagnostic
			// from an excluded alert label set. Conservatively query the whole.
			want: []string{""}, fallback: true,
		},
		{
			name:  "or prioritizes left labels for matching series",
			input: `(a > 5 or on(job) b > 1) unless on(extra) excluded`,
			data: `a{job="j",extra="left"} 10
b{job="j",extra="right"} 5
excluded{extra="left"} 1`,
			want: []string{""}, fallback: true,
		},
		{
			name:  "numeric or retains left priority",
			input: `(a or on(job) b) > 5`,
			data: `a{job="j"} 2
b{job="j"} 10`,
			want: []string{`a{job="j"} 2`},
		},
		{
			name:  "filtered or retains left priority via fallback",
			input: `((a > 1) or on(job) b) > 5`,
			data: `a{job="j"} 2
b{job="j"} 10`,
			want: []string{""}, fallback: true,
		},
		{
			name:  "zero fill exposed by comparison remains one signal",
			input: `(sum(missing) or vector(0)) > 5`,
			data:  `unrelated 1`,
			want:  []string{`{} 0`},
		},
		{
			name:  "asymmetric comparison chain stays a fallback",
			input: `(a > 5) != b`,
			data: `a{job="below"} 2
a{job="active"} 10
b{job="below"} 1
b{job="active"} 1`,
			want: []string{`a{job="active"} 10`}, fallback: true,
		},
		{
			name:  "nested vector comparison label provenance stays a fallback",
			input: `(a != on(job) b) != on(job) c`,
			data: `a{job="j",extra="left"} 10
b{job="j",extra="middle"} 5
c{job="j",extra="right"} 3`,
			want: []string{`{job="j"} 10`}, fallback: true,
		},
		{
			name:  "zero fill under arithmetic is not split",
			input: `(sum(missing) or vector(0)) + 1 > 5`,
			data:  `unrelated 1`,
			want:  []string{`{} 1`},
		},
		{
			name:  "zero fill under function is not split",
			input: `clamp_min((sum(missing) or vector(0)), 1) > 5`,
			data:  `unrelated 1`,
			want:  []string{`{} 1`},
		},
		{
			name:  "outer zero fill remains intact",
			input: `sum(missing) or vector(0)`,
			data:  `unrelated 1`,
			want:  []string{`{} 0`}, fallback: true,
		},
		{
			name:  "group right imported exclusion labels require fallback",
			input: `(a > on(job) group_right(extra) b) unless on(extra) excluded`,
			data: `a{job="j",extra="left"} 10
b{job="j",extra="right",id="1"} 5
excluded{extra="left"} 1`,
			want: []string{""}, fallback: true,
		},
		{
			name:  "group right excludes both operands by shared keys",
			input: `(a > on(job) group_right(extra) b) unless on(job) excluded`,
			data: `a{job="internal",extra="left"} 10
a{job="customer",extra="left"} 2
b{job="internal",extra="right",id="1"} 5
b{job="customer",extra="right",id="1"} 5
b{job="customer",extra="right",id="2"} 6
excluded{job="internal"} 1`,
			want: []string{`a{job="customer",extra="left"} 2`, "b{job=\"customer\",extra=\"right\",id=\"1\"} 5\nb{job=\"customer\",extra=\"right\",id=\"2\"} 6"},
		},
		{
			name:  "internal denominator guard and outer chain",
			input: `a / ignoring(kind) (b > 0) > .9 < 1`,
			data: `a{job="zero",kind="used"} 2
a{job="valid",kind="used"} 2
b{job="zero",kind="capacity"} 0
b{job="valid",kind="capacity"} 4`,
			want: []string{`{job="valid"} 0.5`},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := Extract(tc.input)
			if err != nil {
				t.Fatal(err)
			}
			validatePlan(t, plan)
			if len(plan.Conditions) != 1 {
				t.Fatalf("expected one intact condition: %+v", plan)
			}
			condition := plan.Conditions[0]
			queries := condition.Queries
			if tc.fallback {
				if condition.Unsupported == "" || len(queries) != 0 {
					t.Fatalf("expected conservative fallback: %+v", condition)
				}
				queries = []Query{{Expression: condition.Expression}}
			} else if condition.Unsupported != "" {
				t.Fatalf("unexpected fallback: %+v", condition)
			}
			if len(queries) != len(tc.want) {
				t.Fatalf("expected %d queries, got %+v", len(tc.want), queries)
			}
			script := "load 1m\n" + tc.data + "\n\n"
			for i, query := range queries {
				script += fmt.Sprintf("eval instant at 0m %s\n%s\n\n", query.Expression, tc.want[i])
			}
			promqltest.RunTest(t, script, engine)
		})
	}
}

func validatePlan(t *testing.T, plan Plan) {
	t.Helper()
	if _, err := json.Marshal(plan); err != nil {
		t.Fatalf("plan must be JSON serializable: %v", err)
	}
	parse := func(expression string) {
		t.Helper()
		expr, err := parser.NewParser(parser.Options{}).ParseExpr(expression)
		if err != nil {
			t.Fatalf("reparse %q: %v", expression, err)
		}
		if expr.Type() != parser.ValueTypeVector {
			t.Fatalf("query %q has type %s", expression, expr.Type())
		}
	}
	parse(plan.Expression)
	if len(plan.Conditions) == 0 {
		t.Fatal("plan has no conditions")
	}
	paths := map[string]bool{}
	for _, condition := range plan.Conditions {
		parse(condition.Expression)
		if paths[condition.Path] || !strings.HasPrefix(condition.Path, "root") {
			t.Fatalf("invalid/duplicate condition path %q", condition.Path)
		}
		paths[condition.Path] = true
		if condition.Unsupported != "" {
			if len(condition.Queries) != 0 || len(condition.Thresholds) != 0 {
				t.Fatalf("unsupported condition makes extraction claims: %+v", condition)
			}
			continue
		}
		if len(condition.Queries) == 0 {
			t.Fatalf("supported condition has no queries: %+v", condition)
		}
		for _, query := range condition.Queries {
			parse(query.Expression)
			if query.Role != "signal" && query.Role != "left" && query.Role != "right" {
				t.Fatalf("invalid query role %q", query.Role)
			}
		}
	}
}
