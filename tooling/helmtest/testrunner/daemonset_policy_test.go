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

package testrunner

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"sigs.k8s.io/yaml"
)

// Exercises the public runner in a subprocess so expected policy failures do not fail the parent test.
func TestHelmTemplateDaemonSetPolicy(t *testing.T) {
	binary, err := os.Executable()
	require.NoError(t, err)
	pipeline, err := os.ReadFile("../testdata/pipeline_with_helmstep.yaml")
	require.NoError(t, err)

	rolling := func(maxUnavailable string) string {
		return fmt.Sprintf("  updateStrategy:\n    type: RollingUpdate\n    rollingUpdate:\n      maxUnavailable: %s\n", maxUnavailable)
	}
	for _, tc := range []struct {
		name          string
		setting       string
		daemonSetName string
		namespace     string
		strategy      string
		wantError     string
	}{
		{name: "default preserves 25 percent", daemonSetName: "agent", namespace: "namespace", strategy: rolling("25%")},
		{name: "default preserves omitted strategy", daemonSetName: "agent", namespace: "namespace"},
		{name: "default preserves OnDelete", daemonSetName: "agent", namespace: "namespace", strategy: "  updateStrategy:\n    type: OnDelete\n"},
		{name: "disabled preserves 25 percent", setting: "false", daemonSetName: "agent", namespace: "namespace", strategy: rolling("25%")},
		{name: "enabled rejects 25 percent", setting: "true", daemonSetName: "agent", namespace: "namespace", strategy: rolling("25%"), wantError: "DaemonSet namespace/agent must set spec.updateStrategy.type=RollingUpdate and spec.updateStrategy.rollingUpdate.maxUnavailable=100%"},
		{name: "enabled rejects omitted strategy", setting: "true", daemonSetName: "agent", namespace: "namespace", wantError: "DaemonSet namespace/agent must set spec.updateStrategy.type=RollingUpdate and spec.updateStrategy.rollingUpdate.maxUnavailable=100%"},
		{name: "enabled accepts 100 percent", setting: "true", daemonSetName: "agent", namespace: "namespace", strategy: rolling("100%")},
		{name: "enabled accepts kubelet exception", setting: "true", daemonSetName: "set-kubelet-parameters-for-scale", namespace: "kube-system", strategy: rolling("25%")},
		{name: "enabled enforces kubelet exception", setting: "true", daemonSetName: "set-kubelet-parameters-for-scale", namespace: "kube-system", strategy: rolling("100%"), wantError: "DaemonSet kube-system/set-kubelet-parameters-for-scale must set spec.updateStrategy.type=RollingUpdate and spec.updateStrategy.rollingUpdate.maxUnavailable=25%"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			require.NoError(t, os.MkdirAll(filepath.Join(dir, "deploy", "templates"), 0755))
			write := func(name, contents string) {
				require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(contents), 0600))
			}
			write("pipeline.yaml", string(pipeline))
			write("topology.yaml", "services:\n- serviceGroup: Microsoft.Test.Infra\n  pipelinePath: pipeline.yaml\n")
			write("config.yaml", "{}\n")
			write("values-testing-foo.yaml", "{}\n")
			write("deploy/Chart.yaml", "apiVersion: v2\nname: daemonset-policy-test\nversion: 0.1.0\n")
			manifest := fmt.Sprintf(`apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: %s
  namespace: %s
spec:
%s  selector:
    matchLabels:
      app: agent
  template:
    metadata:
      labels:
        app: agent
    spec:
      containers:
      - name: agent
        image: example.invalid/agent:test
        resources:
          requests:
            memory: 16Mi
          limits:
            memory: 32Mi
`, tc.daemonSetName, tc.namespace, tc.strategy)
			write("deploy/templates/daemonset.yaml", manifest)
			settings := fmt.Sprintf("ConfigPath: %s\nTopologyDir: %s\n", filepath.Join(dir, "config.yaml"), dir)
			if tc.setting != "" {
				settings += "ValidateDaemonSetUpdateStrategies: " + tc.setting + "\n"
			}
			write("settings.yaml", settings)

			cmd := exec.CommandContext(t.Context(), binary, "-test.run=^TestHelmTemplateDaemonSetPolicyHelper$", "-test.v")
			cmd.Env = append(os.Environ(), "HELMTEST_DAEMONSET_POLICY_SETTINGS="+filepath.Join(dir, "settings.yaml"))
			output, err := cmd.CombinedOutput()
			if tc.wantError == "" {
				require.NoError(t, err, "%s", output)
			} else {
				var exitError *exec.ExitError
				require.ErrorAs(t, err, &exitError, "%s", output)
				require.Equal(t, 1, exitError.ExitCode(), "%s", output)
				require.Contains(t, string(output), tc.wantError)
			}

			// The fixture is emitted even on policy failure; compare its strategy with the chart's input.
			fixture, err := os.ReadFile(filepath.Join(dir, sanitizeFilename("TestHelmTemplateDaemonSetPolicyHelper/foo-bar-testing")+".yaml"))
			require.NoError(t, err, "%s", output)
			var expected, actual struct {
				Spec struct {
					UpdateStrategy map[string]any `json:"updateStrategy"`
				} `json:"spec"`
			}
			require.NoError(t, yaml.Unmarshal([]byte(manifest), &expected))
			require.NoError(t, yaml.Unmarshal(fixture, &actual))
			require.Equal(t, expected.Spec.UpdateStrategy, actual.Spec.UpdateStrategy, "validation must not rewrite the consumer's rollout strategy")
		})
	}
}

// Runs only as the child process of TestHelmTemplateDaemonSetPolicy.
func TestHelmTemplateDaemonSetPolicyHelper(t *testing.T) {
	settingsPath := os.Getenv("HELMTEST_DAEMONSET_POLICY_SETTINGS")
	if settingsPath == "" {
		t.Skip("invoked by TestHelmTemplateDaemonSetPolicy")
	}
	// Fixtures stay in the temporary consumer tree, independent of the caller's UPDATE setting.
	t.Setenv("UPDATE", "true")
	RunTestHelmTemplate(t, settingsPath)
}
