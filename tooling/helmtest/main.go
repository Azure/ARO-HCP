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

package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

var repoRoot = "../.."

type testCase struct {
	Name         string         `yaml:"name"`
	Namespace    string         `yaml:"namespace"`
	Values       string         `yaml:"values"`
	HelmChartDir string         `yaml:"helmChartDir"`
	TestData     map[string]any `yaml:"testData"`
}

func findHelmtests() ([]string, error) {
	allTests := make([]string, 0)
	err := filepath.WalkDir(repoRoot, func(path string, d fs.DirEntry, err error) error {
		if d.IsDir() {
			return nil
		}
		if strings.HasPrefix(d.Name(), "helmtest_") {
			allTests = append(allTests, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("error walking directory: %v", err)
	}
	return allTests, nil
}

func main() {
	allTests, err := findHelmtests()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	fmt.Println("Found", len(allTests), "helmtests")
	for _, test := range allTests {
		fmt.Println(test)
	}
	fmt.Println("Use 'go test -run TestHelmTemplate -count=1' to run these tests")
}
