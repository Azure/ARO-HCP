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

package release

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/release"
	helmtime "helm.sh/helm/v3/pkg/time"
)

func TestIsAKSManagedManifest(t *testing.T) {
	tests := []struct {
		name     string
		labels   map[string]string
		expected bool
	}{
		{
			name: "AKS managed workload",
			labels: map[string]string{
				"kubernetes.azure.com/managedby": "aks",
			},
			expected: true,
		},
		{
			name: "Non-AKS managed workload",
			labels: map[string]string{
				"kubernetes.azure.com/managedby": "other",
			},
			expected: false,
		},
		{
			name: "No managedby label",
			labels: map[string]string{
				"app": "test",
			},
			expected: false,
		},
		{
			name:     "No labels",
			labels:   map[string]string{},
			expected: false,
		},
		{
			name:     "Nil labels",
			labels:   nil,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isAKSManagedManifest(tt.labels)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExtractWorkloadInfoFromYAML(t *testing.T) {
	tests := []struct {
		name             string
		yamlDoc          string
		releaseNamespace string
		expected         *WorkloadInfo
	}{
		{
			name: "Valid Deployment",
			yamlDoc: `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: test-deployment
  namespace: test-ns
spec:
  template:
    spec:
      containers:
      - name: app
        image: nginx:latest
`,
			releaseNamespace: "release-ns",
			expected: &WorkloadInfo{
				Name:         "test-deployment",
				Namespace:    "test-ns",
				Kind:         "Deployment",
				DesiredImage: "nginx:latest",
				CurrentImage: "",
			},
		},
		{
			name: "Valid DaemonSet with fallback namespace",
			yamlDoc: `
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: test-daemonset
spec:
  template:
    spec:
      containers:
      - name: app
        image: redis:alpine
`,
			releaseNamespace: "release-ns",
			expected: &WorkloadInfo{
				Name:         "test-daemonset",
				Namespace:    "release-ns",
				Kind:         "DaemonSet",
				DesiredImage: "redis:alpine",
				CurrentImage: "",
			},
		},
		{
			name: "Valid StatefulSet",
			yamlDoc: `
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: test-statefulset
  namespace: test-ns
spec:
  template:
    spec:
      containers:
      - name: app
        image: postgres:13
`,
			releaseNamespace: "release-ns",
			expected: &WorkloadInfo{
				Name:         "test-statefulset",
				Namespace:    "test-ns",
				Kind:         "StatefulSet",
				DesiredImage: "postgres:13",
				CurrentImage: "",
			},
		},
		{
			name: "AKS managed workload - filtered out",
			yamlDoc: `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: aks-deployment
  namespace: test-ns
  labels:
    kubernetes.azure.com/managedby: aks
spec:
  template:
    spec:
      containers:
      - name: app
        image: nginx:latest
`,
			releaseNamespace: "release-ns",
			expected:         nil,
		},
		{
			name: "Non-workload kind - filtered out",
			yamlDoc: `
apiVersion: v1
kind: Service
metadata:
  name: test-service
  namespace: test-ns
spec:
  ports:
  - port: 80
`,
			releaseNamespace: "release-ns",
			expected:         nil,
		},
		{
			name: "Missing containers",
			yamlDoc: `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: test-deployment
  namespace: test-ns
spec:
  template:
    spec: {}
`,
			releaseNamespace: "release-ns",
			expected:         nil,
		},
		{
			name: "Invalid YAML",
			yamlDoc: `
invalid: yaml: content:
  - this is not valid
`,
			releaseNamespace: "release-ns",
			expected:         nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractWorkloadInfoFromYAML(tt.yamlDoc, tt.releaseNamespace)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExtractWorkloadsFromManifest(t *testing.T) {
	tests := []struct {
		name             string
		manifest         string
		releaseNamespace string
		expectedCount    int
		expectError      bool
	}{
		{
			name: "Single deployment manifest",
			manifest: `apiVersion: apps/v1
kind: Deployment
metadata:
  name: test-deployment
  namespace: test-ns
spec:
  template:
    spec:
      containers:
      - image: nginx:latest`,
			releaseNamespace: "default",
			expectedCount:    1,
			expectError:      false,
		},
		{
			name: "Multiple workloads manifest",
			manifest: `apiVersion: apps/v1
kind: Deployment
metadata:
  name: deployment1
spec:
  template:
    spec:
      containers:
      - image: nginx:latest
---
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: daemonset1
spec:
  template:
    spec:
      containers:
      - image: redis:alpine`,
			releaseNamespace: "default",
			expectedCount:    2,
			expectError:      false,
		},
		{
			name: "Mixed workloads and services",
			manifest: `apiVersion: apps/v1
kind: Deployment
metadata:
  name: deployment1
spec:
  template:
    spec:
      containers:
      - image: nginx:latest
---
apiVersion: v1
kind: Service
metadata:
  name: service1
spec:
  ports:
  - port: 80`,
			releaseNamespace: "default",
			expectedCount:    1, // Only deployment counted
			expectError:      false,
		},
		{
			name: "AKS managed workloads filtered out",
			manifest: `apiVersion: apps/v1
kind: Deployment
metadata:
  name: user-deployment
spec:
  template:
    spec:
      containers:
      - image: nginx:latest
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: aks-deployment
  labels:
    kubernetes.azure.com/managedby: aks
spec:
  template:
    spec:
      containers:
      - image: aks:latest`,
			releaseNamespace: "default",
			expectedCount:    1, // Only user deployment counted
			expectError:      false,
		},
		{
			name:             "Empty manifest",
			manifest:         "",
			releaseNamespace: "default",
			expectedCount:    0,
			expectError:      false,
		},
		{
			name: "Invalid YAML",
			manifest: `apiVersion: apps/v1
kind: Deployment
metadata:
  name: test
  invalid: [unclosed`,
			releaseNamespace: "default",
			expectedCount:    0,
			expectError:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := extractWorkloadsFromManifest(tt.manifest, tt.releaseNamespace)

			if tt.expectError {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Len(t, result, tt.expectedCount)

			// Verify all workloads have required fields
			for _, workload := range result {
				assert.NotEmpty(t, workload.Name)
				assert.NotEmpty(t, workload.Kind)
				assert.NotEmpty(t, workload.DesiredImage)
				assert.Contains(t, []string{"Deployment", "DaemonSet", "StatefulSet"}, workload.Kind)
			}
		})
	}
}

func TestGetChartName(t *testing.T) {
	tests := []struct {
		name     string
		release  *release.Release
		expected string
	}{
		{
			name: "Valid chart with metadata",
			release: &release.Release{
				Chart: &chart.Chart{
					Metadata: &chart.Metadata{
						Name: "test-chart",
					},
				},
			},
			expected: "test-chart",
		},
		{
			name: "Chart without metadata",
			release: &release.Release{
				Chart: &chart.Chart{},
			},
			expected: "unknown",
		},
		{
			name: "Release without chart",
			release: &release.Release{
				Chart: nil,
			},
			expected: "unknown",
		},
		{
			name:     "Nil release",
			release:  nil,
			expected: "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getChartName(tt.release)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetDeploymentTimestamp(t *testing.T) {
	now := time.Now()
	earlier := now.Add(-1 * time.Hour)

	tests := []struct {
		name     string
		release  *release.Release
		expected time.Time
	}{
		{
			name: "Valid last deployed time",
			release: &release.Release{
				Info: &release.Info{
					LastDeployed: helmtime.Time{Time: now},
				},
			},
			expected: now,
		},
		{
			name: "Fallback to first deployed",
			release: &release.Release{
				Info: &release.Info{
					FirstDeployed: helmtime.Time{Time: earlier},
				},
			},
			expected: earlier,
		},
		{
			name: "Last deployed takes precedence over first deployed",
			release: &release.Release{
				Info: &release.Info{
					FirstDeployed: helmtime.Time{Time: earlier},
					LastDeployed:  helmtime.Time{Time: now},
				},
			},
			expected: now,
		},
		{
			name: "Release without info - returns current time",
			release: &release.Release{
				Info: nil,
			},
			expected: time.Now(), // This will be compared with tolerance
		},
		{
			name:     "Nil release - returns current time",
			release:  nil,
			expected: time.Now(), // This will be compared with tolerance
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getDeploymentTimestamp(tt.release)

			// For current time cases, check within 1 second tolerance
			if tt.name == "Release without info - returns current time" || tt.name == "Nil release - returns current time" {
				assert.WithinDuration(t, time.Now(), result, time.Second)
			} else {
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}
