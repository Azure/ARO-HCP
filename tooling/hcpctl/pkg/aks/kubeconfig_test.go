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

package aks

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// Test the updateKubeconfigExecCommand function directly without needing to mock os.Executable
func TestUpdateKubeconfigExecCommand(t *testing.T) {
	testExecPath := "/usr/local/bin/hcpctl"

	testCases := []struct {
		name       string
		kubeconfig *clientcmdapi.Config
		validate   func(t *testing.T, config *clientcmdapi.Config)
	}{
		{
			name: "updates kubeconfig command correctly",
			kubeconfig: &clientcmdapi.Config{
				AuthInfos: map[string]*clientcmdapi.AuthInfo{
					"test-user": {
						Exec: &clientcmdapi.ExecConfig{
							Command: "kubelogin",
							Args:    []string{"get-token", "--environment", "AzurePublicCloud"},
						},
					},
				},
			},
			validate: func(t *testing.T, config *clientcmdapi.Config) {
				authInfo := config.AuthInfos["test-user"]
				require.NotNil(t, authInfo)
				require.NotNil(t, authInfo.Exec)
				assert.Equal(t, testExecPath, authInfo.Exec.Command)
				assert.Equal(t, []string{"kubelogin", "get-token", "--environment", "AzurePublicCloud"}, authInfo.Exec.Args)
				assert.Contains(t, authInfo.Exec.InstallHint, "hcpctl")
				assert.Contains(t, authInfo.Exec.InstallHint, testExecPath)
			},
		},
		{
			name: "preserves non-kubelogin auth configs",
			kubeconfig: &clientcmdapi.Config{
				AuthInfos: map[string]*clientcmdapi.AuthInfo{
					"kubectl-user": {
						Token: "some-token",
					},
					"exec-user": {
						Exec: &clientcmdapi.ExecConfig{
							Command: "aws",
							Args:    []string{"eks", "get-token"},
						},
					},
				},
			},
			validate: func(t *testing.T, config *clientcmdapi.Config) {
				// kubectl-user should be unchanged
				authInfo := config.AuthInfos["kubectl-user"]
				require.NotNil(t, authInfo)
				assert.Equal(t, "some-token", authInfo.Token)

				// exec-user should be unchanged
				execUser := config.AuthInfos["exec-user"]
				require.NotNil(t, execUser)
				require.NotNil(t, execUser.Exec)
				assert.Equal(t, "aws", execUser.Exec.Command)
				assert.Equal(t, []string{"eks", "get-token"}, execUser.Exec.Args)
			},
		},
		{
			name: "sets correct install hint",
			kubeconfig: &clientcmdapi.Config{
				AuthInfos: map[string]*clientcmdapi.AuthInfo{
					"test-user": {
						Exec: &clientcmdapi.ExecConfig{
							Command: "kubelogin",
							Args:    []string{"get-token"},
						},
					},
				},
			},
			validate: func(t *testing.T, config *clientcmdapi.Config) {
				authInfo := config.AuthInfos["test-user"]
				require.NotNil(t, authInfo)
				require.NotNil(t, authInfo.Exec)
				assert.Contains(t, authInfo.Exec.InstallHint, "\nhcpctl is not installed or not accessible.")
				assert.Contains(t, authInfo.Exec.InstallHint, "The kubeconfig is configured to use: "+testExecPath)
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Make a copy of the config to avoid modifying the test data
			config := tc.kubeconfig.DeepCopy()

			// Call updateKubeconfigExecCommand
			updateKubeconfigExecCommand(config, testExecPath)

			// Validate the changes
			if tc.validate != nil {
				tc.validate(t, config)
			}
		})
	}
}

func TestKubeloginifyKubeconfigErrors(t *testing.T) {
	testCases := []struct {
		name          string
		setup         func(t *testing.T) string
		mockExecError error
		errorContains string
	}{
		{
			name: "invalid kubeconfig returns error",
			setup: func(t *testing.T) string {
				tmpDir := t.TempDir()
				invalidPath := filepath.Join(tmpDir, "invalid.yaml")
				err := os.WriteFile(invalidPath, []byte("invalid yaml content"), 0600)
				require.NoError(t, err)
				return invalidPath
			},
			errorContains: "failed to load kubeconfig",
		},
		{
			name: "non-existent kubeconfig returns error",
			setup: func(t *testing.T) string {
				return "/non/existent/path/kubeconfig.yaml"
			},
			errorContains: "failed to load kubeconfig",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			kubeconfigPath := tc.setup(t)

			err := kubeloginifyKubeconfig(kubeconfigPath)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), tc.errorContains)
		})
	}
}
