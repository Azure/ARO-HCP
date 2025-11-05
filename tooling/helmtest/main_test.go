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
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/chart/loader"
	"helm.sh/helm/v4/pkg/cli"
	"helm.sh/helm/v4/pkg/kube"
	"helm.sh/helm/v4/pkg/release"

	"sigs.k8s.io/yaml"

	"github.com/Azure/ARO-Tools/pkg/config"
	"github.com/Azure/ARO-Tools/pkg/config/types"
)

func replaceImageDigest(yamlCfg map[string]any) map[string]any {
	for key, value := range yamlCfg {
		if _, ok := value.(map[string]any); ok {
			yamlCfg[key] = replaceImageDigest(value.(map[string]any))
		}
		if key == "digest" {
			yamlCfg[key] = "sha256:1234567890"
		}
	}
	return yamlCfg
}

func loadConfigAndMerge(configPath string, configOverride map[string]any) (map[string]any, error) {
	rawCfg, err := os.ReadFile(filepath.Join(repoRoot, "config/rendered/dev/dev/westus3.yaml"))
	if err != nil {
		return nil, fmt.Errorf("error reading config, %v", err)
	}

	var cfgYaml config.Configuration
	err = yaml.Unmarshal(rawCfg, &cfgYaml)
	if err != nil {
		return nil, fmt.Errorf("error unmarshalling config, %v", err)
	}

	cfgYaml = types.MergeConfiguration(cfgYaml, configOverride)
	cfgYaml = replaceImageDigest(cfgYaml)

	return cfgYaml, nil
}

func runTest(ctx context.Context, testPath string, testCase testCase) (string, error) {
	settings := cli.New()
	cfg := action.Configuration{}
	err := cfg.Init(settings.RESTClientGetter(), testCase.Namespace, "memory")
	if err != nil {
		return "", fmt.Errorf("error initializing config, %v", err)
	}

	in := action.NewInstall(&cfg)
	in.DryRunStrategy = action.DryRunClient
	in.ReleaseName = testCase.Name
	in.WaitStrategy = kube.StatusWatcherStrategy
	in.WaitForJobs = true
	in.Namespace = testCase.Namespace
	in.ServerSideApply = true
	in.ForceConflicts = true
	in.SkipCRDs = false
	in.TakeOwnership = true

	cfgYaml, err := loadConfigAndMerge(filepath.Join(repoRoot, "config/rendered/dev/dev/westus3.yaml"), testCase.TestData)
	if err != nil {
		return "", fmt.Errorf("error loading config, %v", err)
	}

	values, err := config.PreprocessFile(filepath.Join(testPath, testCase.Values), cfgYaml)
	if err != nil {
		return "", fmt.Errorf("error preprocessing values file, %v", err)

	}
	var cval map[string]any
	err = yaml.Unmarshal(values, &cval)
	if err != nil {
		return "", fmt.Errorf("error unmarshalling config, %v", err)

	}
	chart, err := loader.Load(filepath.Join(testPath, testCase.HelmChartDir))
	if err != nil {
		return "", fmt.Errorf("error loading chart, %v", err)
	}

	rel, err := in.RunWithContext(ctx, chart, cval)
	if err != nil {
		return "", fmt.Errorf("error running helm, %v", err)
	}
	accessor, err := release.NewAccessor(rel)
	if err != nil {
		return "", fmt.Errorf("error creating accessor, %v", err)
	}
	return accessor.Manifest(), nil
}

func TestHelmTemplate(t *testing.T) {
	testCases, err := findHelmtests()
	assert.NoError(t, err)
	assert.NotNil(t, testCases)

	for _, testCasePath := range testCases {
		testCaseRaw, err := os.ReadFile(testCasePath)
		assert.NoError(t, err)

		var testCase testCase
		err = yaml.Unmarshal(testCaseRaw, &testCase)
		assert.NoError(t, err)

		t.Run(testCase.Name, func(t *testing.T) {
			manifest, err := runTest(t.Context(), filepath.Dir(testCasePath), testCase)
			assert.NoError(t, err)
			CompareWithFixture(t, manifest, WithGoldenDir(filepath.Dir(testCasePath)))
		})

	}

}
