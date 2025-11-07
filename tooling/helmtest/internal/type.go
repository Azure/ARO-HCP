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

package internal

import (
	"path/filepath"

	"github.com/Azure/ARO-Tools/pkg/types"
)

var RepoRoot = "../.."

type TestCase struct {
	Name         string         `yaml:"name"`
	Namespace    string         `yaml:"namespace"`
	Values       string         `yaml:"values"`
	HelmChartDir string         `yaml:"helmChartDir"`
	TestData     map[string]any `yaml:"testData"`
}

type HelmStepWithPath struct {
	HelmStep     *types.HelmStep
	PipelinePath string
}

func (h *HelmStepWithPath) ValuesFileFromRoot() string {
	return filepath.Join(RepoRoot, filepath.Dir(h.PipelinePath), h.HelmStep.ValuesFile)
}

func (h *HelmStepWithPath) ChartDirFromRoot() string {
	return filepath.Join(RepoRoot, filepath.Dir(h.PipelinePath), h.HelmStep.ChartDir)
}
