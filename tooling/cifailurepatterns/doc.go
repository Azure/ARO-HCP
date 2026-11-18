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

// Package cifailurepatterns selects and normalizes deterministic CI failure evidence.
// It has no network, credential, storage, or runtime model dependencies. Ordinary
// errors yield one primary finding; observability input can yield several alert
// subjects. Findings describe symptoms, not verified root causes.
//
// Selection has explicit precedence: source-specific alerts, assertion context,
// named error rules with pipeline refinements, Azure details, contextual timeouts,
// and conservative fallback lines. RuleID records the final evidence-selection
// path. Matched means a supported rule selected evidence, not high confidence;
// fallback marks heuristic text selection and missing_evidence marks absent input.
// QualityReasons separately identifies generic wrappers and upstream missing detail.
// Gomega assertion details retain complete lines, including dotted hostnames and
// versions; a standalone ellipsis is not assertion evidence. Azure error codes
// and details refine assertion picks rather than grouping SDK request lines.
// MatchError assertions select the actual error, not the expected regexp.
// Without an Azure code, HTTP status refines only request-line-only assertion
// picks; meaningful assertion context retains precedence.
// Bare-deadline assertion picks still pass through timeout-detail refinement.
// Azure JSON detail messages retain decoded quoting and occurrence association,
// including repeated codes with absent or empty messages. The root-message
// fallback uses the last decoded root occurrence, not an earlier sibling.
// Decoded outer Azure codes are independent of JSON property order; non-JSON
// evidence retains textual code selection.
// Short messages remain evidence; only explicit boilerplate is filtered.
//
// Keys use complete normalized evidence, not truncated display strings or other
// observations in a query window. Callers own input fingerprints, grouping,
// aggregation, storage, and application provenance. Version identifies extraction
// behavior independently of those concerns.
// Locations are raw input byte spans; composed, decoded, or ambiguously repeated evidence
// can reference the whole input rather than inventing an exact selected span.
//
// The ordinary-error rules and semantic regression fixtures are adapted from
// github.com/roivaz/ARO-HCP-CIHealth/pkg/failurepatterns/extractor at revision
// 5bc0fb98113d84769735861bae4c8b561932105b. The source's extraction and generated-name
// behavior is retained, but legacy lookup helpers and display-based identity are
// intentionally excluded. Alerts use explicit source-aware subject rules instead
// of the legacy description-only grouping.
package cifailurepatterns
