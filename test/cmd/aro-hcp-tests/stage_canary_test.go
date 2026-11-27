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

package main

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/Azure/ARO-HCP/test/util/framework"
)

func TestStageCanarySelectsExactlyThreeVersions(t *testing.T) {
	t.Setenv("ARO_HCP_CLOUD", "public")
	t.Setenv("ARO_HCP_DEPLOY_ENV", "stg")
	t.Setenv("ARO_HCP_SUITE_NAME", "stage/parallel")
	t.Setenv("ARO_HCP_SUITE_PARALLELISM", "")
	root, err := setupCli()
	if err != nil {
		t.Fatal(err)
	}
	output, err := os.CreateTemp(t.TempDir(), "specs-*")
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	root.SetArgs([]string{"list", "tests", "--suite", "stage/parallel", "--output", "names"})
	original := os.Stdout
	func() {
		os.Stdout = output
		defer func() { os.Stdout = original }()
		err = root.Execute()
	}()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(output.Name())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		framework.StageCanarySpecPrefix + "4.20",
		framework.StageCanarySpecPrefix + "4.21",
		framework.StageCanarySpecPrefix + "4.22",
	}
	got := strings.Split(strings.TrimSpace(string(data)), "\n")
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("expected exactly %q, got %q", want, got)
	}
}
