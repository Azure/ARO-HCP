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
	"fmt"
	"os"
	"path/filepath"

	"sigs.k8s.io/yaml"

	"github.com/Azure/ARO-Tools/pkg/types"
)

var TestDataFromChartDir = "../testdata"

type TestCase struct {
	Name         string         `yaml:"name"`
	Namespace    string         `yaml:"namespace"`
	Values       string         `yaml:"values"`
	HelmChartDir string         `yaml:"helmChartDir,omitempty"`
	TestData     map[string]any `yaml:"testData"`

	// An implicit test case is one generated for a use of a Helm step, not one that
	// is written by hand for some specific use-case.
	Implicit bool `yaml:"-"`
}

type HelmStepWithPath struct {
	HelmStep     *types.HelmStep
	PipelinePath string
	AKSCluster   string
}

func (h *HelmStepWithPath) ValuesFileFromRoot(topologyDir string) string {
	return filepath.Join(topologyDir, filepath.Dir(h.PipelinePath), h.HelmStep.ValuesFile)
}

func (h *HelmStepWithPath) ChartDirFromRoot(topologyDir string) string {
	return filepath.Join(topologyDir, filepath.Dir(h.PipelinePath), h.HelmStep.ChartDir)
}

type Settings struct {
	ConfigPath  string
	TopologyDir string
	Replace     []Replace
}

type Replace struct {
	Regex       string
	Replacement string
}

func LoadSettings(settingsPath string) (*Settings, error) {
	rawCfg, err := os.ReadFile(settingsPath)
	if err != nil {
		return nil, fmt.Errorf("error reading settings, %v", err)
	}

	var settings Settings
	err = yaml.Unmarshal(rawCfg, &settings)
	if err != nil {
		return nil, fmt.Errorf("error unmarshalling settings, %v", err)
	}
	return &settings, nil
}
