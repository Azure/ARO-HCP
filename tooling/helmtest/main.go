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
	"os"

	"github.com/Azure/ARO-HCP/tooling/helmtest/internal"
)

func main() {
	allTests, err := internal.FindHelmtestFiles()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	fmt.Println("Found", len(allTests), "helmtests")
	for _, test := range allTests {
		fmt.Println(test)
	}

	helmSteps, err := internal.FindHelmSteps()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	fmt.Println("Found", len(helmSteps), "helm steps")
	for _, step := range helmSteps {
		fmt.Printf("Name: %s, Path: %s\n", step.HelmStep.Name, step.ChartDirFromRoot())
	}
	fmt.Println("Use 'go test -run TestHelmTemplate -count=1' to run these tests")
}
