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
	"os"
	"strings"
	"testing"

	"github.com/Azure/ARO-HCP/tooling/helmtest/testrunner"
)

func TestHelmTemplate(t *testing.T) {
	testrunner.RunTestHelmTemplate(t, "settings.yaml")
}

func TestACRValues(t *testing.T) {
	testrunner.RunTestACRValues(t, "settings.yaml")
}

func TestNodeRolloutConfig(t *testing.T) {
	testrunner.RunTestNodeRolloutConfig(t, "settings.yaml")
}

func TestShoeboxForwardUsesMDSDCompatibleTimestamp(t *testing.T) {
	fixture, err := os.ReadFile("../../observability/arobit/testdata/zz_fixture_TestHelmTemplate_helmtest_mdsd_and_kusto_enabled_mgmt.yaml")
	if err != nil {
		t.Fatalf("failed to read Arobit management fixture: %v", err)
	}

	output := string(fixture)
	aliasIndex := strings.Index(output, "Alias           forward.shoebox")
	if aliasIndex == -1 {
		t.Fatal("Arobit management fixture does not contain the Shoebox Forward output")
	}

	output = output[aliasIndex:]
	blockEnd := strings.Index(output, "\n\n")
	if blockEnd != -1 {
		output = output[:blockEnd]
	}

	if !strings.Contains(output, "Retain_Metadata_In_Forward_Mode false") {
		t.Fatal("Shoebox Forward output must disable metadata retention for MDSD timestamp compatibility")
	}
}
