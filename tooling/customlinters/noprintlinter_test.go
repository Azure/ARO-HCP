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

package customlinters

import (
	"testing"

	"github.com/golangci/plugin-module-register/register"
	"golang.org/x/tools/go/analysis/analysistest"
)

func TestNoPrintLinter(t *testing.T) {
	newPlugin, err := register.GetPlugin("noprint")
	if err != nil {
		t.Fatalf("Failed to get plugin: %v", err)
	}

	plugin, err := newPlugin(nil)
	if err != nil {
		t.Fatalf("Failed to create plugin: %v", err)
	}

	analyzers, err := plugin.BuildAnalyzers()
	if err != nil {
		t.Fatalf("Failed to build analyzers: %v", err)
	}

	if len(analyzers) != 1 {
		t.Fatalf("Expected 1 analyzer, got %d", len(analyzers))
	}

	analysistest.Run(t, analysistest.TestData(), analyzers[0], "noprint")
}
