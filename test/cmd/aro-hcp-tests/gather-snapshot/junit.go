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

package gathersnapshot

import (
	"fmt"
	"strings"

	"github.com/Azure/ARO-HCP/test/util/junit"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/snapshot"
)

// reportsToJUnit converts a slice of VerificationReports into a jUnit TestSuites
// structure suitable for CI consumption. It emits one test case per unique
// (resourceType, queryKey) pair, providing stable test identifiers that don't
// change with resource names, correlation IDs, etc. Skipped cases are omitted.
func reportsToJUnit(reports []*snapshot.VerificationReport) *junit.TestSuites {
	testSuite := junit.TestSuite{
		Name: "aro-hcp-snapshot",
	}

	// Aggregate cases by (resourceType, query) key. Track whether any case
	// in the group failed, and collect failure messages.
	type groupKey struct {
		resourceType string
		query        string
	}
	type groupState struct {
		failed   bool
		messages []string
	}
	groups := make(map[groupKey]*groupState)
	var groupOrder []groupKey

	for _, report := range reports {
		for _, c := range report.Cases {
			if c.Status == snapshot.VerificationSkipped {
				continue
			}

			key := groupKey{
				resourceType: c.ResourceType,
				query:        c.Query,
			}
			gs, ok := groups[key]
			if !ok {
				gs = &groupState{}
				groups[key] = gs
				groupOrder = append(groupOrder, key)
			}

			if c.Status == snapshot.VerificationFail {
				gs.failed = true
				gs.messages = append(gs.messages, c.Message)
			}
		}
	}

	for _, key := range groupOrder {
		gs := groups[key]
		tc := &junit.TestCase{
			Name:      fmt.Sprintf("[aro-hcp-snapshot] [%s] %s returns results", key.resourceType, key.query),
			Classname: key.resourceType,
		}

		if gs.failed {
			tc.FailureOutput = &junit.FailureOutput{
				Message: "query returned no results",
				Output:  strings.Join(gs.messages, "\n"),
			}
			testSuite.NumFailed++
		}

		testSuite.TestCases = append(testSuite.TestCases, tc)
		testSuite.NumTests++
	}

	return &junit.TestSuites{
		Suites: []*junit.TestSuite{&testSuite},
	}
}
