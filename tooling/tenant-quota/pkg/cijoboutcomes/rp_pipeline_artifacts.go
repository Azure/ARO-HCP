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
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/snapshot"
)

// Step-relative paths of the templatize pipeline JUnit reports.
//
// rp-provision candidates are tried in order and the first non-empty one wins:
// e2e-parallel jobs provision PR-branch code, while
// e2e-parallel-inplace-upgrade jobs provision main first and publish the
// baseline report. The job (ciJobOutcomes.jobName/family) tells which.
var rpProvisionArtifactPaths = []string{
	"aro-hcp-provision-environment/artifacts/junit_entrypoint.xml",
	"aro-hcp-provision-from-main/artifacts/junit_entrypoint_baseline.xml",
}

// rp-upgrade exists only in the in-place-upgrade job.
var rpUpgradeArtifactPaths = []string{
	"aro-hcp-test-local-upgrade/artifacts/junit_entrypoint.xml",
}

func fetchRPProvisionRows(ctx context.Context, client *http.Client, prowURL string) ([]ciTestResult, []ciTestName, error) {
	return fetchRPPipelineRows(ctx, client, prowURL, sourceRPProvision, rpProvisionArtifactPaths)
}

func fetchRPUpgradeRows(ctx context.Context, client *http.Client, prowURL string) ([]ciTestResult, []ciTestName, error) {
	return fetchRPPipelineRows(ctx, client, prowURL, sourceRPUpgrade, rpUpgradeArtifactPaths)
}

func fetchRPPipelineRows(ctx context.Context, client *http.Client, prowURL, source string, paths []string) ([]ciTestResult, []ciTestName, error) {
	logger := utils.LoggerFromContext(ctx).WithValues("source", source)
	ctx = utils.ContextWithLogger(ctx, logger)
	info, err := snapshot.ParseProwURL(prowURL)
	if err != nil {
		return nil, nil, err
	}
	var data []byte
	for _, path := range paths {
		data, err = snapshot.FetchProwJobArtifact(ctx, client, info, path)
		if err != nil {
			return nil, nil, err
		}
		if len(data) != 0 {
			break
		}
	}
	if len(data) == 0 {
		snapshot.ReportProwArtifactProblem(ctx, source, "absent")
		return nil, nil, nil
	}
	results, err := parseRPPipelineResults(data)
	if err != nil {
		logger.Error(err, "Pipeline JUnit malformed, skipping source")
		snapshot.ReportProwArtifactProblem(ctx, source, "malformed")
		return nil, nil, nil
	}
	rows, names := testRowsFor(info.ProwID, source, results)
	return rows, names, nil
}

type rpJUnitSuite struct {
	XMLName xml.Name
	Suites  []rpJUnitSuite `xml:"testsuite"`
	Cases   []rpJUnitCase  `xml:"testcase"`
}

type rpJUnitCase struct {
	Name      string        `xml:"name,attr"`
	Timestamp string        `xml:"timestamp,attr"`
	Time      float64       `xml:"time,attr"`
	Skipped   *struct{}     `xml:"skipped"`
	Problems  []rpJUnitProb `xml:",any"`
}

type rpJUnitProb struct {
	XMLName xml.Name
	Message string `xml:"message,attr"`
	Body    string `xml:",chardata"`
}

// parseRPPipelineResults converts a templatize pipeline JUnit report into test
// results. Passed and failed steps are kept; skipped (preempted) steps are
// reported with the skipped verdict, which testRowsFor drops. Start and end
// times come from the testcase timestamp attribute and are left zero when a
// report predates it.
func parseRPPipelineResults(data []byte) ([]snapshot.TestResult, error) {
	var root rpJUnitSuite
	if err := xml.NewDecoder(bytes.NewReader(data)).Decode(&root); err != nil {
		return nil, err
	}
	if root.XMLName.Local != "testsuite" && root.XMLName.Local != "testsuites" {
		return nil, fmt.Errorf("unexpected JUnit root %q", root.XMLName.Local)
	}
	results := make([]snapshot.TestResult, 0)
	var walk func(rpJUnitSuite) error
	walk = func(suite rpJUnitSuite) error {
		for _, testcase := range suite.Cases {
			if strings.TrimSpace(testcase.Name) == "" {
				return fmt.Errorf("JUnit testcase is missing a name")
			}
			result := snapshot.TestResult{Name: testcase.Name, Result: "passed"}
			var messages []string
			for _, problem := range testcase.Problems {
				if problem.XMLName.Local != "failure" && problem.XMLName.Local != "error" {
					continue
				}
				result.Result, result.Failed = "failed", true
				for _, message := range []string{problem.Message, problem.Body} {
					message = strings.TrimSpace(message)
					if message != "" && !slices.Contains(messages, message) {
						messages = append(messages, message)
					}
				}
			}
			result.Error = strings.Join(messages, "\n")
			if testcase.Skipped != nil && !result.Failed {
				result.Result = skippedResult
			}
			if start, err := time.Parse(time.RFC3339, testcase.Timestamp); err == nil {
				result.StartTime = start.UTC()
				result.EndTime = result.StartTime.Add(time.Duration(testcase.Time * float64(time.Second)))
			}
			results = append(results, result)
		}
		for _, child := range suite.Suites {
			if err := walk(child); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(root); err != nil {
		return nil, err
	}
	return results, nil
}
