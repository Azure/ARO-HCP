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

package render

import (
	"fmt"
	"strings"
	"time"

	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/analysis"
)

func nowStr() string {
	return time.Now().UTC().Format("2006-01-02 15:04 UTC")
}

func renderFetchWarnings(errs map[string]int) string {
	var parts []string
	for _, kind := range []string{"timeout", "http", "network", "parse"} {
		if n := errs[kind]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, kind))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return fmt.Sprintf("**Warning**: fetch errors (%s) — some data may be incomplete", strings.Join(parts, ", "))
}

// Summary renders a SummaryResult as markdown.
func Summary(data *analysis.SummaryResult) string {
	var lines []string
	lines = append(lines, fmt.Sprintf("# CI Summary — %s", nowStr()))
	if w := renderFetchWarnings(data.FetchErrors); w != "" {
		lines = append(lines, "", w)
	}
	lines = append(lines, "")
	lines = append(lines, "| Env | Type | Passed | Failed | Pass Rate | Top Failures |")
	lines = append(lines, "|-----|------|--------|--------|-----------|--------------|")
	for _, r := range data.Envs {
		completed := r.Passed + r.Failed
		if completed == 0 {
			continue
		}
		pct := fmt.Sprintf("%.0f%%", r.PassRate*100)
		top := "—"
		if len(r.TopFailures) > 0 {
			top = strings.Join(r.TopFailures[:min(3, len(r.TopFailures))], ", ")
		}
		lines = append(lines, fmt.Sprintf("| %s | %s | %d | %d | %s | %s |",
			r.Env, r.Type, r.Passed, r.Failed, pct, top))
	}
	lines = append(lines, "")

	if len(data.FleetFailures) > 0 {
		lines = append(lines, "## Fleet-Wide Failures", "")
		for _, f := range data.FleetFailures {
			lines = append(lines, fmt.Sprintf("- **%s** (%s)", f.Test, strings.Join(f.Envs, ", ")))
		}
		lines = append(lines, "")
	}

	return strings.Join(lines, "\n")
}

// Evidence renders a FailuresResult as markdown.
func Evidence(data *analysis.FailuresResult) string {
	var lines []string
	lines = append(lines, fmt.Sprintf("# CI Evidence Packet — %s", nowStr()))
	if w := renderFetchWarnings(data.FetchErrors); w != "" {
		lines = append(lines, "", w)
	}
	lines = append(lines, "")

	// Job summary table
	lines = append(lines, "## Jobs")
	lines = append(lines, "| Env | Type | Passed | Failed | Aborted | Pass Rate |")
	lines = append(lines, "|-----|------|--------|--------|---------|-----------|")
	for _, er := range data.Results {
		completed := er.Passed + er.Failed
		if completed == 0 {
			continue
		}
		pct := fmt.Sprintf("%.0f%%", er.PassRate*100)
		lines = append(lines, fmt.Sprintf("| %s | %s | %d | %d | %d | %s |",
			er.Env, er.Type, er.Passed, er.Failed, er.Aborted, pct))
	}
	lines = append(lines, "")

	// Per-job test results
	for _, er := range data.Results {
		if len(er.PerJobTests) == 0 {
			continue
		}
		lines = append(lines, fmt.Sprintf("## Per-Job — %s/%s", er.Env, er.Type))
		for _, job := range er.PerJobTests {
			rev := ""
			if job.Revision != "" {
				rev = fmt.Sprintf(" @%s", job.Revision)
			}
			lines = append(lines, fmt.Sprintf("  %s %dP/%dF%s %s",
				job.Started, job.Passed, job.Failed, rev, job.Job))
		}
	}
	lines = append(lines, "")

	// Failure groups
	for _, er := range data.Results {
		if len(er.FailureGroups) == 0 {
			continue
		}
		totalRuns := er.Passed + er.Failed
		lines = append(lines, fmt.Sprintf("## Failures — %s/%s (%d/%d passed)",
			er.Env, er.Type, er.Passed, totalRuns))
		lines = append(lines, "")

		for _, fg := range er.FailureGroups {
			rate := ""
			if totalRuns > 0 {
				rate = fmt.Sprintf(" (%d/%d)", fg.Count, totalRuns)
			}
			lines = append(lines, fmt.Sprintf("**%s** — %dx%s", fg.Test, fg.Count, rate))

			var parts []string
			if fg.FirstSeen != "" {
				parts = append(parts, fmt.Sprintf("since %s", fg.FirstSeen))
			}
			if fg.LastPassed != "" {
				parts = append(parts, fmt.Sprintf("last pass %s", fg.LastPassed))
			} else if fg.FirstSeen != "" {
				parts = append(parts, "no pass in window")
			}
			if len(parts) > 0 {
				lines = append(lines, fmt.Sprintf("  %s", strings.Join(parts, " | ")))
			}

			for _, entry := range fg.Messages {
				suffix := ""
				if entry.Count > 1 {
					suffix = fmt.Sprintf(" (x%d)", entry.Count)
				}
				msgLines := strings.Split(entry.Msg, "\n")
				lines = append(lines, fmt.Sprintf("  msg%s: %s", suffix, msgLines[0]))
				for _, ml := range msgLines[1:] {
					if strings.TrimSpace(ml) != "" {
						lines = append(lines, fmt.Sprintf("       %s", ml))
					}
				}
			}

			if len(fg.PRs) > 0 {
				prStrs := make([]string, len(fg.PRs))
				for i, p := range fg.PRs {
					prStrs[i] = fmt.Sprintf("#%d", p)
				}
				lines = append(lines, fmt.Sprintf("  prs: %s", strings.Join(prStrs, ", ")))
			}

			if len(fg.Jobs) > 0 {
				lines = append(lines, fmt.Sprintf("  jobs: %s", strings.Join(fg.Jobs, ", ")))
			}
			lines = append(lines, "")
		}
	}

	return strings.Join(lines, "\n")
}

// PR renders a PRResult as markdown.
func PR(data *analysis.PRResult) string {
	var lines []string
	lines = append(lines, fmt.Sprintf("# PR #%d — CI Results — %s", data.PR, nowStr()))
	if w := renderFetchWarnings(data.FetchErrors); w != "" {
		lines = append(lines, "", w)
	}
	lines = append(lines, "")

	if len(data.Envs) == 0 {
		lines = append(lines, "No presubmit jobs found for this PR.")
		return strings.Join(lines, "\n")
	}

	for _, er := range data.Envs {
		status := fmt.Sprintf("%d/%d passed", er.Passed, er.Total)
		lines = append(lines, fmt.Sprintf("## %s: %s", er.Env, status))
		lines = append(lines, "")

		if len(er.Failures) == 0 {
			if er.Failed > 0 {
				lines = append(lines, "Failed jobs but no test-level failures (infrastructure issue?).")
			} else {
				lines = append(lines, "All runs passed.")
			}
			lines = append(lines, "")
			continue
		}

		for _, f := range er.Failures {
			tag := "baseline"
			if !f.Baseline {
				tag = "NEW"
			}
			lines = append(lines, fmt.Sprintf("**%s** — %dx [%s]", f.Test, f.Count, tag))
			for _, entry := range f.Messages {
				suffix := ""
				if entry.Count > 1 {
					suffix = fmt.Sprintf(" (x%d)", entry.Count)
				}
				msgLine := strings.SplitN(entry.Msg, "\n", 2)[0]
				lines = append(lines, fmt.Sprintf("  msg%s: %s", suffix, msgLine))
			}
			if len(f.Jobs) > 0 {
				lines = append(lines, fmt.Sprintf("  jobs: %s", strings.Join(f.Jobs, ", ")))
			}
			lines = append(lines, "")
		}
	}

	return strings.Join(lines, "\n")
}
