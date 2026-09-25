# Alert Diagnostics

`Extract(expression) (Plan, error)` is a pure PromQL AST transformation using
the workspace's Prometheus v0.312.0 parser. It performs no queries or I/O.

## Contract

- `Plan.Expression` retains the normalized full alert, including parentheses,
  vector matching, and boolean/set-operator context.
- `Condition.Path` identifies outer `and`/`or` branches and `unless` left sides.
  Branches are independent diagnostic views, not equivalent firing predicates.
- Outer literal comparisons (including constant arithmetic thresholds) are
  removed; chains yield one signal and thresholds in source order. Scalar-left
  operators are reversed. Vector comparisons yield `left` and `right` queries.
- Arithmetic, aggregations, functions, internal comparisons, subqueries,
  lookbacks, offsets, and `@` modifiers remain intact. Scalar comparison chains
  over `and`/`unless` move onto the value-producing left operand using copied
  AST nodes, never onto the right-side gate. Conditions retain branch paths;
  their expressions include inherited comparisons, while `Plan.Expression`
  retains the original normalized grouping. This rewrite handles scalar-left
  orientation and nested sets, but not `bool` comparisons.
- Comparisons over `or` keep the union as one numeric signal if its exposed set
  branches contain no comparisons. Otherwise extraction falls back: distributing
  a comparison could change left-priority semantics. Zero-fill is never split.
  Outer `or vector(0)` unions without a comparison remain intact as fallback.
- Vector comparisons with exposed comparison/set operands fall back, since
  threshold ownership and label provenance have not been established. None of
  these rewrites descend into arithmetic, aggregations, or functions.
- Outer `unless` clauses retain their complete RHS and matching modifiers.
  Distribution across `and`/`or` or vector comparisons requires proof that the
  exclusion keys agree. Ambiguous result-label projection or imported labels
  produces an unsupported condition, rather than silently changing exclusions.
- Unsupported conditions have no queries or thresholds. Query their normalized
  `Condition.Expression` unchanged as fallback. Reasons include absence
  functions, bool comparisons, nonconstant/nonfinite thresholds, ambiguous
  vector-comparison chains, filtered `or` branches, zero-fill unions, and unsafe
  exclusion label relationships.
- Only invalid PromQL and non-instant-vector alert result types return errors.

Extracted queries intentionally include below-threshold samples and may contain
unmatched series from independent branches or vector operands. They are not a
reimplementation of alert evaluation, and recording-rule names are not expanded.
An unchanged numeric expression can still contain intentional internal filters.

## Corpus

`TestRepositoryCorpus` walks the repository, discovers YAML/Bicep `alert:`
declarations, and records every occurrence sorted by source/group/alert/occurrence.
It includes disabled/authored alerts, duplicate alert names, the customized
Kubernetes rules, hand-authored tenant-quota Bicep, and committed generated Bicep.
The latter covers deployed expression variants, including aggregation-label
rewrites and injected `unless on(subscription_id) internal_subscription:info`.
It does not invoke Bicep compilation, regenerate rules, or inspect live Azure.

Excluded copies: `.upstream.yaml` snapshots, `testdata`, promtool `_test.yaml`
inputs, `zz_fixture_*` Helm/rendered fixtures, and `rendered` configuration trees.
Dependency/build trees (`.git`, `vendor`, `node_modules`) are not traversed.
Production YAML is decoded structurally before selecting `PrometheusRule`
documents, including quoted keys and flow-style mappings. Non-rule documents
and promtool test inputs are not treated as alert declarations. Unrendered Helm
templates/chart files and templated `values.yaml` are excluded, but alert-key
syntax in them fails loudly, requiring explicit rendering coverage.
For Bicep, declaration counts must equal parsed alert counts, preventing silent
omissions when a new authoring format appears. The reader deliberately supports
only the current literal single-line expression and literal variable
interpolation subset. It requires alert before expression at the same indentation
in the same object; a closing object rejects a pending alert instead of borrowing
the next recording rule's expression. Unknown syntax/escapes/interpolation fail
discovery, not extraction. This is a restricted scanner, not a Bicep parser.

The human-readable golden includes the original decoded PromQL input and each
resulting plan or error. Every full expression, condition fallback, and query
is reparsed, checked for instant-vector type, and checked for JSON serialization.
Focused tests also evaluate queries with Prometheus against synthetic series,
including unsafe exclusion counterexamples.

From `test/`:

```sh
UPDATE=1 go test ./util/alertdiagnostics
go test ./util/alertdiagnostics
go test ./util/alertdiagnostics -run TestRepositoryCorpus -v
```

The corpus test logs counts and every unsupported reason. After semantic review:
477 alert occurrences from 42 files (191 YAML, 286 Bicep), 52 inputs containing
`unless`, including 15 generated subscription exclusions; 703 conditions,
746 diagnostic queries, no parse/type errors, and five fallback conditions.
All 29 formerly unsupported set/comparison cases are extracted: 12 backend
async-operation source alerts and their 12 generated variants, two access-cluster
stuck-operation variants, and three `KubeNodeUnreachable` variants. Elapsed-time
thresholds belong only to the duration branch; phase gates have their own charts
and thresholds. These independent diagnostics do not claim to retain phase
scoping individually; the full plan retains the alert's relationship. Node-taint
and injected subscription exclusions remain on diagnostic queries.
Nested vector comparisons, ambiguous filtered `or`, and zero-fill fallbacks are
covered by focused tests but do not occur at the extracted boundary in this corpus.
The five absence fallbacks are tenant-quota telemetry-presence alerts: `TenantQuotaCollectorUp`,
`TenantQuotaMetricsStale`, `AzureQuotaMetricsStale`,
`AzureQuotaLimitMetricsStale`, and `E2EResourceGroupMetricsStale`.
