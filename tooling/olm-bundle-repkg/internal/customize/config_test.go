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

package customize

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MakeNowJust/heredoc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadFromFile_EmptyPath(t *testing.T) {
	_, err := LoadFromFile("")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "a configuration file must be provided")
}

func TestLoadFromFile_NonExistentFile(t *testing.T) {
	_, err := LoadFromFile("/non/existent/path.yaml")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "configuration file does not exist")
}

func TestLoadFromFile_ValidConfig(t *testing.T) {
	configYAML := heredoc.Doc(`
		chartName: "my-operator"
		chartDescription: "A custom operator chart"
		operatorDeploymentNames:
		  - "my-operator"
		  - "my-controller"
		operandImageEnvPrefixes:
		  - "OPERAND_IMAGE_"
		  - "RELATED_IMAGE_"
		imageRegistryParam: "registry"
		requiredEnvVarPrefixes:
		  - "OPERAND_IMAGE_"
		requiredResources:
		  - "Deployment"
		  - "ServiceAccount"
		annotationPrefixesToRemove:
		  - "custom.annotation"
		  - "operator.io"
	`)

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	err := os.WriteFile(configPath, []byte(configYAML), 0644)
	require.NoError(t, err)

	config, err := LoadFromFile(configPath)
	require.NoError(t, err)

	assert.Equal(t, "my-operator", config.ChartName)
	assert.Equal(t, "A custom operator chart", config.ChartDescription)
	assert.Equal(t, []string{"my-operator", "my-controller"}, config.OperatorDeploymentNames)
	assert.Equal(t, []string{"OPERAND_IMAGE_", "RELATED_IMAGE_"}, config.OperandImageEnvPrefixes)
	assert.Equal(t, "registry", config.ImageRegistryParam)
	assert.Equal(t, []string{"OPERAND_IMAGE_"}, config.RequiredEnvVarPrefixes)
	assert.Equal(t, []string{"Deployment", "ServiceAccount"}, config.RequiredResources)
	assert.Equal(t, []string{"custom.annotation", "operator.io"}, config.AnnotationPrefixesToRemove)
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		config  *BundleConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid config",
			config: &BundleConfig{
				ChartName:               "Test",
				ChartDescription:        "Test",
				OperatorDeploymentNames: []string{"test"},
				ImageRegistryParam:      "registry",
			},
			wantErr: false,
		},
		{
			name: "empty chart name",
			config: &BundleConfig{
				ChartDescription:        "Test",
				OperatorDeploymentNames: []string{"test"},
				ImageRegistryParam:      "registry",
			},
			wantErr: true,
			errMsg:  "chartName cannot be empty",
		},
		{
			name: "empty chart description",
			config: &BundleConfig{
				ChartName:               "test",
				OperatorDeploymentNames: []string{"test"},
				ImageRegistryParam:      "registry",
			},
			wantErr: true,
			errMsg:  "chartDescription cannot be empty",
		},
		{
			name: "empty operator deployment selector",
			config: &BundleConfig{
				ChartName:        "test",
				ChartDescription: "Test",
			},
			wantErr: true,
			errMsg:  "at least one of operatorDeploymentNames or operatorDeploymentSelector must be specified",
		},
		{
			name: "both tag and digest params configured - should fail",
			config: &BundleConfig{
				ChartName:               "test",
				ChartDescription:        "Test",
				OperatorDeploymentNames: []string{"test"},
				ImageTagParam:           "imageTag",
				ImageDigestParam:        "imageDigest",
			},
			wantErr: true,
			errMsg:  "imageTagParam and imageDigestParam are mutually exclusive",
		},
		{
			name: "only tag param configured - should pass",
			config: &BundleConfig{
				ChartName:               "test",
				ChartDescription:        "Test",
				OperatorDeploymentNames: []string{"test"},
				ImageTagParam:           "imageTag",
			},
			wantErr: false,
		},
		{
			name: "only digest param configured - should pass",
			config: &BundleConfig{
				ChartName:               "test",
				ChartDescription:        "Test",
				OperatorDeploymentNames: []string{"test"},
				ImageDigestParam:        "imageDigest",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
