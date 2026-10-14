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
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/snapshot"
)

const observabilityArtifactPath = "aro-hcp-gather-observability/artifacts/"

func fetchObservabilityRows(ctx context.Context, client *http.Client, prowURL string) ([]ciTestResult, []ciTestName, error) {
	logger := utils.LoggerFromContext(ctx).WithValues("source", "observability")
	ctx = utils.ContextWithLogger(ctx, logger)
	info, err := snapshot.ParseProwURL(prowURL)
	if err != nil {
		return nil, nil, err
	}
	data, err := snapshot.FetchProwJobArtifact(ctx, client, info, observabilityArtifactPath+"junit_alerts.xml")
	if err != nil {
		return nil, nil, err
	}
	if len(data) == 0 {
		snapshot.ReportProwArtifactProblem(ctx, "observability", "absent")
		return nil, nil, nil
	}
	results, err := parseObservabilityFailures(data)
	if err != nil {
		logger.Error(err, "Observability JUnit absent or malformed, skipping source")
		snapshot.ReportProwArtifactProblem(ctx, "observability", "malformed")
		return nil, nil, nil
	}
	rows, names := testRowsFor(info.ProwID, results)
	if len(rows) == 0 {
		return rows, names, nil
	}

	data, err = snapshot.FetchProwJobArtifact(ctx, client, info, observabilityArtifactPath+"alerts.json")
	if err != nil {
		return nil, names, err
	}
	if len(data) == 0 {
		snapshot.ReportProwArtifactProblem(ctx, "observability", "absent")
		return rows, names, nil
	}
	var alerts struct {
		TimeWindow struct {
			Start time.Time `json:"start"`
			End   time.Time `json:"end"`
		} `json:"timeWindow"`
	}
	if err := json.Unmarshal(data, &alerts); err != nil {
		logger.Error(err, "Observation window absent or malformed, keeping failures without timing")
		snapshot.ReportProwArtifactProblem(ctx, "observability", "malformed")
	} else {
		for i := range rows {
			rows[i].StartedAt = alerts.TimeWindow.Start.UTC()
			rows[i].FinishedAt = alerts.TimeWindow.End.UTC()
		}
	}
	return rows, names, nil
}

// Parse the whole document before emitting any rows, including collection failures.
// A test's elapsed time is not the observation window and is deliberately ignored.
func parseObservabilityFailures(data []byte) ([]snapshot.TestResult, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	results := make([]snapshot.TestResult, 0)
	depth := 0
	rootSeen := false
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			if !rootSeen || depth != 0 {
				return nil, fmt.Errorf("incomplete JUnit document")
			}
			return results, nil
		}
		if err != nil {
			return nil, err
		}
		switch token := token.(type) {
		case xml.StartElement:
			if depth == 0 {
				if rootSeen || (token.Name.Local != "testsuite" && token.Name.Local != "testsuites") {
					return nil, fmt.Errorf("unexpected JUnit root %q", token.Name.Local)
				}
				rootSeen = true
			}
			if token.Name.Local != "testcase" {
				depth++
				continue
			}
			// Decode failure and error elements alike, retaining their document order.
			var testcase struct {
				Name     string    `xml:"name,attr"`
				Skipped  *struct{} `xml:"skipped"`
				Problems []struct {
					XMLName xml.Name
					Message string `xml:"message,attr"`
					Body    string `xml:",chardata"`
				} `xml:",any"`
			}
			if err := decoder.DecodeElement(&testcase, &token); err != nil {
				return nil, err
			}
			if testcase.Skipped != nil {
				continue
			}
			failed := false
			var messages []string
			for _, problem := range testcase.Problems {
				if problem.XMLName.Local != "failure" && problem.XMLName.Local != "error" {
					continue
				}
				failed = true
				for _, message := range []string{problem.Message, problem.Body} {
					message = strings.TrimSpace(message)
					if message != "" && !slices.Contains(messages, message) {
						messages = append(messages, message)
					}
				}
			}
			if failed {
				if strings.TrimSpace(testcase.Name) == "" {
					return nil, fmt.Errorf("failed JUnit testcase is missing a name")
				}
				results = append(results, snapshot.TestResult{Name: testcase.Name, Result: "failed", Failed: true, Error: strings.Join(messages, "\n")})
			}
		case xml.EndElement:
			depth--
		case xml.CharData:
			if depth == 0 && strings.TrimSpace(string(token)) != "" {
				return nil, fmt.Errorf("unexpected text outside JUnit root")
			}
		}
	}
}
