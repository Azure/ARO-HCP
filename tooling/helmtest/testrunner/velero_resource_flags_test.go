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
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"helm.sh/helm/v4/pkg/chart/common"
	"helm.sh/helm/v4/pkg/chart/loader"
	"helm.sh/helm/v4/pkg/engine"

	batchv1 "k8s.io/api/batch/v1"

	"sigs.k8s.io/yaml"
)

func TestVeleroResourceFlags(t *testing.T) {
	chartDir := "../../../velero/deploy"
	chart, err := loader.Load(chartDir)
	require.NoError(t, err)
	defaults, err := os.ReadFile(filepath.Join(chartDir, "values.yaml"))
	require.NoError(t, err)

	cases := []struct {
		value any
		want  string
	}{
		{value: "NONE", want: "0"},
		{value: "unlimited", want: "0"},
		{value: "0", want: "0"},
		{value: 0, want: "0"},
		{value: 2, want: "2"},
		{value: 0.5, want: "0.5"},
		{value: "1000m", want: "1000m"},
		{value: "1024Mi", want: "1024Mi"},
		{value: "2Gi", want: "2Gi"},
		{value: ".5", want: ".5"},
		{value: "1e-3", want: "1e-3"},
		{value: "1E+6", want: "1E+6"},
		{value: "+1", want: "+1"},
		{value: "+0", want: "+0"},
		{value: "1.", want: "1."},
		{value: "+1.Mi", want: "+1.Mi"},
		{value: "+.5", want: "+.5"},
		{value: "+1.e-3", want: "+1.e-3"},
		{value: "1k", want: "1k"},
		{value: "1Ki", want: "1Ki"},
		{value: "1$(touch injected)"},
		{value: "1`touch injected`"},
		{value: "1'; touch injected; '"},
		{value: "1\"; touch injected; \""},
		{value: "1\ntouch injected"},
		{value: "1\n"},
		{value: "1;touch injected"},
		{value: "1|touch injected"},
		{value: "1 && touch injected"},
		{value: "1Mi --extra-flag"},
		{value: "NONE$(touch injected)"},
		{value: "unlimited\ntouch injected"},
		{value: "-1"},
		{value: -1},
		{value: ""},
		{value: " 1"},
		{value: "1 "},
		{value: "1foo"},
		{value: "1ni"},
		{value: "1K"},
		{value: "1ki"},
		{value: "+"},
		{value: "."},
		{value: "++1"},
		{value: "+1$(touch injected)"},
		{value: true},
	}
	for _, component := range []string{"veleroServer", "nodeAgent"} {
		for _, kind := range []string{"requests", "limits"} {
			for _, resource := range []string{"cpu", "memory"} {
				for _, tc := range cases {
					t.Run(fmt.Sprintf("%s/%s/%s/%v", component, kind, resource, tc.value), func(t *testing.T) {
						values := map[string]any{}
						require.NoError(t, yaml.Unmarshal(defaults, &values))
						for _, name := range []string{"veleroServer", "nodeAgent", "azurePlugin", "hypershiftPlugin"} {
							values[name].(map[string]any)["resources"] = map[string]any{
								"requests": map[string]any{"cpu": "NONE", "memory": "NONE"},
								"limits":   map[string]any{"cpu": "NONE", "memory": "NONE"},
							}
						}
						for _, name := range []string{"generate", "apply"} {
							values["installer"].(map[string]any)[name].(map[string]any)["resources"] = map[string]any{}
						}
						values[component].(map[string]any)["resources"].(map[string]any)[kind].(map[string]any)[resource] = tc.value
						rendered, err := engine.Render(chart, common.Values{
							"Values": values, "Release": map[string]any{"Namespace": "velero"},
						})
						if tc.want == "" {
							require.ErrorContains(t, err, "invalid Velero resource quantity")
							require.Empty(t, rendered, "invalid quantities must not produce an executable install script")
							return
						}
						require.NoError(t, err)
						var job batchv1.Job
						manifest, ok := rendered["velero-hcp-cli/templates/install-job.yaml"]
						require.True(t, ok, "install Job must be rendered")
						require.NoError(t, yaml.Unmarshal([]byte(manifest), &job))
						require.Len(t, job.Spec.Template.Spec.InitContainers, 1)
						script := job.Spec.Template.Spec.InitContainers[0].Command[2]
						prefix := map[string]string{"veleroServer": "velero", "nodeAgent": "node-agent"}[component]
						unit := map[string]string{"cpu": "cpu", "memory": "mem"}[resource]
						flag := fmt.Sprintf("--%s-pod-%s-%s", prefix, unit, strings.TrimSuffix(kind, "s"))
						require.Contains(t, script, flag+" '"+tc.want+"' \\")
						var argumentLine string
						for _, line := range strings.Split(script, "\n") {
							if strings.HasPrefix(strings.TrimSpace(line), flag+" ") {
								argumentLine = strings.TrimSuffix(strings.TrimSpace(line), " \\")
							}
						}
						require.NotEmpty(t, argumentLine)
						// Exercise shell argument parsing without invoking the installer.
						cmd := exec.CommandContext(t.Context(), "sh", "-c", "set -- "+argumentLine+"; printf '%s\\n' \"$@\"")
						cmd.Dir = t.TempDir()
						output, err := cmd.CombinedOutput()
						require.NoError(t, err)
						require.Equal(t, flag+"\n"+tc.want+"\n", string(output))
					})
				}
			}
		}
	}
}
