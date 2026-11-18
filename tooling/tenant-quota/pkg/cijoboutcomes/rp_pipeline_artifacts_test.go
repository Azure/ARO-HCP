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

package cijoboutcomes

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/snapshot"
)

const rpTimestamped = `<testsuites><testsuite name="templatize-pipeline">` +
	`<testcase name="Run pipeline step svc/rg/ok" timestamp="2026-01-01T02:00:00Z" time="30"/>` +
	`<testcase name="Run pipeline step svc/rg/bad" timestamp="2026-01-01T02:01:00Z" time="5"><failure message=""> step log &amp; detail </failure></testcase>` +
	`<testcase name="Run pipeline step svc/rg/preempted" timestamp="2026-01-01T02:02:00Z" time="1"><skipped message="log"/></testcase>` +
	`</testsuite></testsuites>`

func TestParseRPPipelineResults(t *testing.T) {
	t.Run("timestamped", func(t *testing.T) {
		results, err := parseRPPipelineResults([]byte(rpTimestamped))
		require.NoError(t, err)
		start := time.Date(2026, 1, 1, 2, 0, 0, 0, time.UTC)
		require.Equal(t, []snapshot.TestResult{
			{Name: "Run pipeline step svc/rg/ok", Result: "passed", StartTime: start, EndTime: start.Add(30 * time.Second)},
			{Name: "Run pipeline step svc/rg/bad", Result: "failed", Failed: true, Error: "step log & detail",
				StartTime: start.Add(time.Minute), EndTime: start.Add(time.Minute + 5*time.Second)},
			{Name: "Run pipeline step svc/rg/preempted", Result: "skipped",
				StartTime: start.Add(2 * time.Minute), EndTime: start.Add(2*time.Minute + time.Second)},
		}, results)

		rows, names := testRowsFor("123", sourceRPProvision, results)
		require.Len(t, rows, 2, "skipped steps are dropped")
		require.Len(t, names, 2)
		require.Equal(t, sourceRPProvision, rows[0].Source)
		require.Equal(t, "passed", rows[0].Result)
		require.True(t, rows[0].StartedAt.Equal(start))
		require.True(t, rows[0].FinishedAt.Equal(start.Add(30*time.Second)))
		require.True(t, rows[1].Failed)
		require.Equal(t, "step log & detail", rows[1].Message)
	})
	t.Run("timestamp absent leaves times zero", func(t *testing.T) {
		results, err := parseRPPipelineResults([]byte(`<testsuites><testsuite><testcase name="a" time="12"/><testcase name="b" time="3"><failure>x</failure></testcase></testsuite></testsuites>`))
		require.NoError(t, err)
		require.Len(t, results, 2)
		for _, result := range results {
			require.True(t, result.StartTime.IsZero())
			require.True(t, result.EndTime.IsZero())
		}
		rows, _ := testRowsFor("123", sourceRPUpgrade, results)
		for _, row := range rows {
			require.Equal(t, sourceRPUpgrade, row.Source)
			require.True(t, row.StartedAt.IsZero() && row.FinishedAt.IsZero())
		}
	})
	t.Run("nested suites", func(t *testing.T) {
		results, err := parseRPPipelineResults([]byte(`<testsuites><testsuite><testsuite><testcase name="inner"/></testsuite><testcase name="outer"/></testsuite></testsuites>`))
		require.NoError(t, err)
		names := []string{}
		for _, result := range results {
			names = append(names, result.Name)
		}
		require.ElementsMatch(t, []string{"inner", "outer"}, names)
	})
	t.Run("error element and dedupe", func(t *testing.T) {
		results, err := parseRPPipelineResults([]byte(`<testsuite><testcase name="a"><failure message="same">same</failure><error>other</error></testcase></testsuite>`))
		require.NoError(t, err)
		require.Equal(t, "same\nother", results[0].Error)
	})
	for name, document := range map[string]string{
		"empty":         ``,
		"wrong root":    `<html/>`,
		"truncated":     `<testsuite><testcase name="x">`,
		"missing name":  `<testsuite><testcase time="1"/></testsuite>`,
		"not xml":       `nope`,
		"unclosed root": `<testsuites><testsuite>`,
	} {
		t.Run("malformed "+name, func(t *testing.T) {
			_, err := parseRPPipelineResults([]byte(document))
			require.Error(t, err)
		})
	}
}

func TestFetchRPPipelineRows(t *testing.T) {
	provisionPath := artifactTestRoot + rpProvisionArtifactPaths[0]
	baselinePath := artifactTestRoot + rpProvisionArtifactPaths[1]
	upgradePath := artifactTestRoot + rpUpgradeArtifactPaths[0]
	listing := artifactTestPrefix + "/artifacts/"
	const jobs = `{"prefixes":["` + artifactTestRoot + `"]}`
	const other = `<testsuite><testcase name="other"/></testsuite>`

	for _, tc := range []struct {
		name        string
		objects     map[string]string
		upgrade     bool
		wantRows    int
		wantNames   []string
		wantProblem string
	}{
		{name: "provision PR candidate wins", objects: map[string]string{provisionPath: rpTimestamped, baselinePath: other}, wantRows: 2,
			wantNames: []string{"Run pipeline step svc/rg/ok", "Run pipeline step svc/rg/bad"}},
		{name: "provision falls back to baseline", objects: map[string]string{baselinePath: other}, wantRows: 1, wantNames: []string{"other"}},
		{name: "provision all absent", objects: map[string]string{}, wantProblem: "absent"},
		{name: "provision malformed", objects: map[string]string{provisionPath: `<testsuite>`}, wantProblem: "malformed"},
		{name: "upgrade present", upgrade: true, objects: map[string]string{upgradePath: other, provisionPath: rpTimestamped}, wantRows: 1, wantNames: []string{"other"}},
		{name: "upgrade absent", upgrade: true, objects: map[string]string{provisionPath: rpTimestamped}, wantProblem: "absent"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			objects := map[string]string{listing: jobs}
			for path, body := range tc.objects {
				objects[path] = body
			}
			source, fetch := sourceRPProvision, fetchRPProvisionRows
			if tc.upgrade {
				source, fetch = sourceRPUpgrade, fetchRPUpgradeRows
			}
			var problems []string
			ctx := snapshot.WithProwArtifactProblemHandler(t.Context(), func(source, reason string) { problems = append(problems, source+"/"+reason) })
			statuses := map[string]int{}
			for _, path := range []string{provisionPath, baselinePath, upgradePath} {
				if _, present := objects[path]; !present {
					statuses[path] = http.StatusNotFound
				}
			}
			rows, names, err := fetch(ctx, artifactClient(t, objects, statuses), artifactTestURL)
			require.NoError(t, err)
			if tc.wantProblem != "" {
				require.Equal(t, []string{source + "/" + tc.wantProblem}, problems)
				require.Empty(t, rows)
				require.Empty(t, names)
				return
			}
			require.Empty(t, problems)
			require.Len(t, rows, tc.wantRows)
			got := []string{}
			for _, name := range names {
				got = append(got, name.Name)
			}
			require.Equal(t, tc.wantNames, got)
			for _, row := range rows {
				require.Equal(t, "123", row.BuildID)
				require.Equal(t, source, row.Source)
			}
		})
	}
}

func TestRPPipelineRowsEncodeSource(t *testing.T) {
	results, err := parseRPPipelineResults([]byte(rpTimestamped))
	require.NoError(t, err)
	rows, _ := testRowsFor("123", sourceRPUpgrade, results)
	payload, err := encodeRows(rows)
	require.NoError(t, err)
	require.Contains(t, payload.String(), `"source":"rp-upgrade"`)
	require.Contains(t, payload.String(), `"startedAt":"2026-01-01T02:00:00Z"`)
}
