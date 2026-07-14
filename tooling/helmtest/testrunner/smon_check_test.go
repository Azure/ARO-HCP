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

package testrunner

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCheckAzmonitorAndCoreOsMonitorsExist(t *testing.T) {
	tests := []struct {
		name           string
		manifest       string
		skipNamespaces []string
		wantSkip       bool
		wantErr        string
	}{
		{
			name:     "both servicemonitors present",
			manifest: coreosSmon("test-smon", "test-ns") + azmonitorSmon("test-smon-azmonitor", "test-ns"),
			wantSkip: false,
		},
		{
			name:     "both podmonitors present",
			manifest: coreosPmon("test-pmon", "test-ns") + azmonitorPmon("test-pmon-azmonitor", "test-ns"),
			wantSkip: false,
		},
		{
			name:     "both servicemonitors and podmonitors present",
			manifest: coreosSmon("test-smon", "test-ns") + azmonitorSmon("test-smon-azmonitor", "test-ns") + coreosPmon("test-pmon", "test-ns") + azmonitorPmon("test-pmon-azmonitor", "test-ns"),
			wantSkip: false,
		},
		{
			name:     "no monitors at all returns skip",
			manifest: nonSmonResource(),
			wantSkip: true,
		},
		{
			name:     "empty manifest returns skip",
			manifest: "",
			wantSkip: true,
		},
		{
			name:     "only coreos servicemonitor returns error",
			manifest: coreosSmon("test-smon", "test-ns"),
			wantSkip: false,
			wantErr:  "azmonitor ServiceMonitor does not exist in the manifest",
		},
		{
			name:     "only azmonitor servicemonitor returns error",
			manifest: azmonitorSmon("test-smon-azmonitor", "test-ns"),
			wantSkip: false,
			wantErr:  "coreos ServiceMonitor does not exist in the manifest",
		},
		{
			name:     "only coreos podmonitor returns error",
			manifest: coreosPmon("test-pmon", "test-ns"),
			wantSkip: false,
			wantErr:  "azmonitor PodMonitor does not exist in the manifest",
		},
		{
			name:     "only azmonitor podmonitor returns error",
			manifest: azmonitorPmon("test-pmon-azmonitor", "test-ns"),
			wantSkip: false,
			wantErr:  "coreos PodMonitor does not exist in the manifest",
		},
		{
			name:     "unknown servicemonitor api version returns error",
			manifest: monitorWithApiVersion("ServiceMonitor", "test-smon", "test-ns", "monitoring.unknown.io/v1"),
			wantSkip: false,
			wantErr:  "unknown ServiceMonitor api version: monitoring.unknown.io/v1",
		},
		{
			name:     "unknown podmonitor api version returns error",
			manifest: monitorWithApiVersion("PodMonitor", "test-pmon", "test-ns", "monitoring.unknown.io/v1"),
			wantSkip: false,
			wantErr:  "unknown PodMonitor api version: monitoring.unknown.io/v1",
		},
		{
			name:           "servicemonitor in skipped namespace returns skip",
			manifest:       coreosSmon("test-smon", "kube-system"),
			skipNamespaces: []string{"kube-system"},
			wantSkip:       true,
		},
		{
			name:           "servicemonitor not in skipped namespace proceeds normally",
			manifest:       coreosSmon("test-smon", "test-ns") + azmonitorSmon("test-smon-azmonitor", "test-ns"),
			skipNamespaces: []string{"kube-system"},
			wantSkip:       false,
		},
		{
			name:     "mixed monitors and non-monitor resources",
			manifest: nonSmonResource() + coreosSmon("test-smon", "test-ns") + azmonitorSmon("test-smon-azmonitor", "test-ns"),
			wantSkip: false,
		},
		{
			name:     "servicemonitor error takes precedence over podmonitor skip",
			manifest: coreosSmon("test-smon", "test-ns"),
			wantSkip: false,
			wantErr:  "azmonitor ServiceMonitor does not exist in the manifest",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			skip, err := checkAzmonitorAndCoreOsMonitorsExist(tc.manifest, tc.skipNamespaces)
			assert.Equal(t, tc.wantSkip, skip, "skip")
			if tc.wantErr != "" {
				assert.EqualError(t, err, tc.wantErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func coreosSmon(name, namespace string) string {
	return "---\napiVersion: monitoring.coreos.com/v1\nkind: ServiceMonitor\nmetadata:\n  name: " + name + "\n  namespace: " + namespace + "\n"
}

func azmonitorSmon(name, namespace string) string {
	return "---\napiVersion: azmonitoring.coreos.com/v1\nkind: ServiceMonitor\nmetadata:\n  name: " + name + "\n  namespace: " + namespace + "\n"
}

func coreosPmon(name, namespace string) string {
	return "---\napiVersion: monitoring.coreos.com/v1\nkind: PodMonitor\nmetadata:\n  name: " + name + "\n  namespace: " + namespace + "\n"
}

func azmonitorPmon(name, namespace string) string {
	return "---\napiVersion: azmonitoring.coreos.com/v1\nkind: PodMonitor\nmetadata:\n  name: " + name + "\n  namespace: " + namespace + "\n"
}

func monitorWithApiVersion(kind, name, namespace, apiVersion string) string {
	return "---\napiVersion: " + apiVersion + "\nkind: " + kind + "\nmetadata:\n  name: " + name + "\n  namespace: " + namespace + "\n"
}

func nonSmonResource() string {
	return "---\napiVersion: v1\nkind: Service\nmetadata:\n  name: test-svc\n  namespace: test-ns\n"
}
