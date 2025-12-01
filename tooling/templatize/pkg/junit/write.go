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

package junit

import (
	"encoding/xml"
	"fmt"
	"os"
	"sort"
)

// Write ensures stable sort order and emits jUnit data to disk.
func Write(intoPath string, suites *TestSuites) error {
	if suites == nil {
		return nil
	}
	sort.Slice(suites.Suites, func(i, j int) bool {
		return suites.Suites[i].Name < suites.Suites[j].Name
	})
	for i := range suites.Suites {
		sortSuite(suites.Suites[i])
	}
	out, err := xml.MarshalIndent(suites, "", "  ")
	if err != nil {
		return fmt.Errorf("could not marshal jUnit XML: %w", err)
	}
	return os.WriteFile(intoPath, out, 0644)
}

func sortSuite(suite *TestSuite) {
	sort.Slice(suite.Properties, func(i, j int) bool {
		return suite.Properties[i].Name < suite.Properties[j].Name
	})
	sort.Slice(suite.Children, func(i, j int) bool {
		return suite.Children[i].Name < suite.Children[j].Name
	})
	sort.Slice(suite.TestCases, func(i, j int) bool {
		return suite.TestCases[i].Name < suite.TestCases[j].Name
	})
	for i := range suite.Children {
		sortSuite(suite.Children[i])
	}
}
