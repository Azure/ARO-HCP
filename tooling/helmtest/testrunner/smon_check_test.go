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

func TestCheckAzmonitorAndCoreOsSmonsExists(t *testing.T) {
	tests := []struct {
		name           string
		manifest       string
		skipNamespaces []string
		wantSkip       bool
		wantBoth       bool
		wantErr        string
	}{
		{
			name:     "both smons present",
			manifest: coreosSmon("test-smon", "test-ns") + azmonitorSmon("test-smon-azmonitor", "test-ns"),
			wantSkip: false,
			wantBoth: true,
		},
		{
			name:     "no smons at all returns skip",
			manifest: nonSmonResource(),
			wantSkip: true,
			wantBoth: false,
		},
		{
			name:     "empty manifest returns skip",
			manifest: "",
			wantSkip: true,
			wantBoth: false,
		},
		{
			name:     "only coreos smon returns error",
			manifest: coreosSmon("test-smon", "test-ns"),
			wantSkip: false,
			wantBoth: false,
			wantErr:  "azmonitor smon does not exist in the manifest",
		},
		{
			name:     "only azmonitor smon returns error",
			manifest: azmonitorSmon("test-smon-azmonitor", "test-ns"),
			wantSkip: false,
			wantBoth: false,
			wantErr:  "coreos smon does not exist in the manifest",
		},
		{
			name:     "unknown api version returns error",
			manifest: smonWithApiVersion("test-smon", "test-ns", "monitoring.unknown.io/v1"),
			wantSkip: false,
			wantBoth: false,
			wantErr:  "unknown smon api version: monitoring.unknown.io/v1",
		},
		{
			name:           "smon in skipped namespace returns skip",
			manifest:       coreosSmon("test-smon", "kube-system"),
			skipNamespaces: []string{"kube-system"},
			wantSkip:       true,
			wantBoth:       false,
		},
		{
			name:           "smon not in skipped namespace proceeds normally",
			manifest:       coreosSmon("test-smon", "test-ns") + azmonitorSmon("test-smon-azmonitor", "test-ns"),
			skipNamespaces: []string{"kube-system"},
			wantSkip:       false,
			wantBoth:       true,
		},
		{
			name:     "mixed smons and non-smon resources",
			manifest: nonSmonResource() + coreosSmon("test-smon", "test-ns") + azmonitorSmon("test-smon-azmonitor", "test-ns"),
			wantSkip: false,
			wantBoth: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			skip, both, err := checkAzmonitorAndCoreOsSmonsExists(tc.manifest, tc.skipNamespaces)
			assert.Equal(t, tc.wantSkip, skip, "skip")
			assert.Equal(t, tc.wantBoth, both, "both")
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

func smonWithApiVersion(name, namespace, apiVersion string) string {
	return "---\napiVersion: " + apiVersion + "\nkind: ServiceMonitor\nmetadata:\n  name: " + name + "\n  namespace: " + namespace + "\n"
}

func nonSmonResource() string {
	return "---\napiVersion: v1\nkind: Service\nmetadata:\n  name: test-svc\n  namespace: test-ns\n"
}
