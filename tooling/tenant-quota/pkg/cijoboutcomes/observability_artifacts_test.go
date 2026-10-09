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
	"testing"
	"time"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/snapshot"
)

func TestParseObservabilityFailures(t *testing.T) {
	for _, tc := range []struct {
		name, document, message string
		wantRows                int
		wantErr                 bool
	}{
		{name: "attribute", document: `<testsuite><testcase name="exact name "><failure message="summary"/></testcase></testsuite>`, message: "summary", wantRows: 1},
		{name: "body", document: `<testsuites><testsuite><testcase name="exact name "><failure>details &amp; context</failure></testcase></testsuite></testsuites>`, message: "details & context", wantRows: 1},
		{name: "attribute and body", document: `<testsuite><testcase name="exact name "><failure message="summary">details</failure></testcase></testsuite>`, message: "summary\ndetails", wantRows: 1},
		{name: "deduplicate problems", document: `<testsuite><testcase name="exact name "><failure message="same"> same </failure><error message="same">other</error><failure>other</failure></testcase></testsuite>`, message: "same\nother", wantRows: 1},
		{name: "error", document: `<testsuite><testcase name="exact name "><error message="collection incomplete">timeout</error></testcase></testsuite>`, message: "collection incomplete\ntimeout", wantRows: 1},
		{name: "empty failure", document: `<testsuite><testcase name="exact name "><failure/></testcase></testsuite>`, wantRows: 1},
		{name: "missing failed name", document: `<testsuite><testcase><failure/></testcase></testsuite>`, wantErr: true},
		{name: "empty error name", document: `<testsuite><testcase name=""><error/></testcase></testsuite>`, wantErr: true},
		{name: "blank failed name", document: `<testsuite><testcase name="  "><failure/></testcase></testsuite>`, wantErr: true},
		{name: "pass and skip", document: `<testsuite><testcase name="pass"/><testcase name="skip"><skipped/></testcase><testcase name="skip overrides failure"><skipped/><failure/></testcase></testsuite>`},
		{name: "empty", wantErr: true},
		{name: "wrong root", document: `<html/>`, wantErr: true},
		{name: "multiple roots", document: `<testsuite/><testsuite/>`, wantErr: true},
		{name: "trailing text", document: `<testsuite/>broken`, wantErr: true},
		{name: "truncated after failure", document: `<testsuite><testcase name="x"><failure/></testcase>`, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			results, err := parseObservabilityFailures([]byte(tc.document))
			if (err != nil) != tc.wantErr || len(results) != tc.wantRows {
				t.Fatalf("results=%+v err=%v", results, err)
			}
			if len(results) == 0 {
				return
			}
			rows, names := testRowsFor("123", results)
			if len(rows) != 1 || len(names) != 1 || rows[0].Message != tc.message || names[0].Name != "exact name " || rows[0].TestID != testIDFor("exact name ") || !rows[0].Failed || rows[0].Result != "failed" {
				t.Fatalf("rows=%+v names=%+v", rows, names)
			}
		})
	}
}

func TestFetchObservabilityRows(t *testing.T) {
	const xmlPath = artifactTestRoot + observabilityArtifactPath + "junit_alerts.xml"
	const jsonPath = artifactTestRoot + observabilityArtifactPath + "alerts.json"
	const failed = `<testsuites><testsuite><testcase name="[aro-hcp-observability] [svc] alert collection is complete"><failure message="alert collection incomplete">timeout</failure></testcase><testcase name="[aro-hcp-observability] [hcp] alert X does not fire" time="123"><failure message="fired">details</failure></testcase><testcase name="pass"/><testcase name="skip"><skipped/></testcase></testsuite></testsuites>`
	const window = `{"timeWindow":{"start":"2026-01-01T02:00:00+02:00","end":"2026-01-01T03:00:00+02:00"}}`
	for _, tc := range []struct {
		name, xml, json             string
		xmlStatus, jsonStatus       int
		wantRows                    int
		wantErr, wantTiming, noJSON bool
		wantEmpty                   bool
		wantProblem                 string
	}{
		{name: "failures with window", xml: failed, json: window, wantRows: 2, wantTiming: true},
		{name: "no failures", xml: `<testsuite><testcase name="pass"/><testcase name="skip"><skipped/></testcase></testsuite>`, noJSON: true, wantEmpty: true},
		{name: "empty suite", xml: `<testsuites/>`, noJSON: true, wantEmpty: true},
		{name: "missing XML", xmlStatus: 404, noJSON: true, wantProblem: "absent"},
		{name: "malformed XML", xml: `<testsuite>`, noJSON: true},
		{name: "malformed after failures", xml: failed + `<`, noJSON: true},
		{name: "missing name after valid failure", xml: `<testsuite><testcase name="valid"><failure/></testcase><testcase><error/></testcase></testsuite>`, noJSON: true, wantProblem: "malformed"},
		{name: "transient XML", xmlStatus: 503, wantErr: true, noJSON: true},
		{name: "throttled XML", xmlStatus: 429, wantErr: true, noJSON: true},
		{name: "network XML", xmlStatus: -1, wantErr: true, noJSON: true},
		{name: "interrupted XML", xmlStatus: -2, wantErr: true, noJSON: true},
		{name: "missing window", xml: failed, jsonStatus: 404, wantRows: 2, wantProblem: "absent"},
		{name: "malformed window JSON", xml: failed, json: `{`, wantRows: 2, wantProblem: "malformed"},
		{name: "bad window timestamp", xml: failed, json: `{"timeWindow":{"start":"2026-01-01T00:00:00Z","end":"bad"}}`, wantRows: 2},
		{name: "empty window", xml: failed, json: `{}`, wantRows: 2},
		{name: "transient window", xml: failed, jsonStatus: 503, wantErr: true},
		{name: "interrupted window", xml: failed, jsonStatus: -2, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			objects := map[string]string{
				artifactTestPrefix + "/artifacts/": `{"prefixes":["` + artifactTestRoot + `"]}`,
				xmlPath:                            tc.xml,
			}
			if !tc.noJSON {
				objects[jsonPath] = tc.json
			}
			var problems []string
			ctx := snapshot.WithProwArtifactProblemHandler(t.Context(), func(source, reason string) { problems = append(problems, source+"/"+reason) })
			rows, names, err := fetchObservabilityRows(ctx, artifactClient(t, objects, map[string]int{xmlPath: tc.xmlStatus, jsonPath: tc.jsonStatus}), artifactTestURL)
			if tc.wantProblem != "" && (len(problems) != 1 || problems[0] != "observability/"+tc.wantProblem) {
				t.Fatalf("problems=%v, want observability/%s", problems, tc.wantProblem)
			}
			if (tc.wantErr || tc.wantEmpty || tc.wantTiming) && len(problems) != 0 {
				t.Fatalf("unexpected permanent problem: %v", problems)
			}
			wantNames := tc.wantRows
			if tc.wantErr && !tc.noJSON {
				wantNames = 2
			}
			if (err != nil) != tc.wantErr || len(rows) != tc.wantRows || len(names) != wantNames {
				t.Fatalf("rows=%+v names=%+v err=%v", rows, names, err)
			}
			if (rows != nil) != (tc.wantRows > 0 || tc.wantEmpty) || (names != nil) != (wantNames > 0 || tc.wantEmpty) {
				t.Fatalf("nil/empty contract violated: rows=%#v names=%#v", rows, names)
			}
			for _, name := range names {
				if name.TestID != testIDFor(name.Name) || name.Name == "" {
					t.Fatalf("invalid retained name: %+v", name)
				}
			}
			for _, row := range rows {
				if row.BuildID != "123" || row.Result != "failed" || !row.Failed || row.Message == "" {
					t.Fatalf("failure row = %+v", row)
				}
				if tc.wantTiming {
					if !row.StartedAt.Equal(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) || !row.FinishedAt.Equal(row.StartedAt.Add(time.Hour)) || row.StartedAt.Location() != time.UTC {
						t.Fatalf("observation window = %+v", row)
					}
				} else if !row.StartedAt.IsZero() || !row.FinishedAt.IsZero() {
					t.Fatalf("unavailable window should be zero: %+v", row)
				}
				if !row.SetupFinishTime.IsZero() || !row.TestStartTime.IsZero() || !row.CleanupStartTime.IsZero() || row.ResourceGroup != "" {
					t.Fatalf("unexpected E2E enrichment: %+v", row)
				}
			}
		})
	}
}
