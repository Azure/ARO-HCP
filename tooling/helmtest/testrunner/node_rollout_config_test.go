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

func TestCollectUnknownFlags(t *testing.T) {
	tests := []struct {
		name     string
		script   string
		expected []string
	}{
		{
			name: "one flag per line with continuations",
			script: "hypershift install \\\n" +
				"  --enable-conversion-webhook=false \\\n" +
				"  --metrics-set=SRE \\\n" +
				"  --enable-cpo-overrides",
			expected: []string{
				"--metrics-set=SRE",
				"--enable-cpo-overrides",
			},
		},
		{
			name:   "multiple flags on a single line",
			script: "hypershift install --enable-conversion-webhook=false --metrics-set=SRE --enable-cpo-overrides",
			expected: []string{
				"--metrics-set=SRE",
				"--enable-cpo-overrides",
			},
		},
		{
			name: "space-separated value",
			script: "hypershift install \\\n" +
				"  --managed-service ARO-HCP \\\n" +
				"  --unknown-flag somevalue",
			expected: []string{
				"--unknown-flag somevalue",
			},
		},
		{
			name: "classified env var is skipped",
			script: "hypershift install \\\n" +
				"  --additional-operator-env-vars SHARED_INGRESS_AZURE_PIP_IP_TAGS=tag1",
			expected: nil,
		},
		{
			name: "unclassified env var is collected",
			script: "hypershift install \\\n" +
				"  --additional-operator-env-vars NEW_ENV=value",
			expected: []string{
				"--additional-operator-env-vars NEW_ENV=value",
			},
		},
		{
			name: "all flags classified returns nil",
			script: "hypershift install \\\n" +
				"  --enable-conversion-webhook=false \\\n" +
				"  --managed-service ARO-HCP \\\n" +
				"  --platform-monitoring=None",
			expected: nil,
		},
		{
			name: "boolean flag without value",
			script: "hypershift install \\\n" +
				"  --enable-conversion-webhook=false \\\n" +
				"  --enable-cpo-overrides",
			expected: []string{
				"--enable-cpo-overrides",
			},
		},
		{
			name: "quoted value for classified flag",
			script: "hypershift install \\\n" +
				`  --registry-overrides "quay.io/a=arohcp.azurecr.io/a,quay.io/b=arohcp.azurecr.io/b"`,
			expected: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := collectUnknownFlags(tc.script)
			assert.Equal(t, tc.expected, result)
		})
	}
}
