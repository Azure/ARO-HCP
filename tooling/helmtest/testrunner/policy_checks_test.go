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

	"k8s.io/apimachinery/pkg/util/sets"
)

var emptyAllowlist = sets.New[string]()

var testAllowlist = sets.New[string](
	"DaemonSet/test-allowlisted-ds/test-container",
)

func TestCheckPolicyViolations_DeploymentWithLimits(t *testing.T) {
	manifest := `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: my-app
spec:
  template:
    spec:
      containers:
      - name: web
        resources:
          limits:
            memory: "128Mi"
`
	violations := checkPolicyViolations(manifest, emptyAllowlist)
	assert.Equal(t, []string{`Deployment/my-app/web has resource limits set (add to ResourceLimitsAllowlist if intentional)`}, violations)
}

func TestCheckPolicyViolations_DeploymentWithoutLimits(t *testing.T) {
	manifest := `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: my-app
spec:
  template:
    spec:
      containers:
      - name: web
        resources:
          requests:
            memory: "64Mi"
`
	violations := checkPolicyViolations(manifest, emptyAllowlist)
	assert.Empty(t, violations)
}

func TestCheckPolicyViolations_InitContainerWithLimits(t *testing.T) {
	manifest := `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: my-app
spec:
  template:
    spec:
      initContainers:
      - name: init
        resources:
          limits:
            cpu: "500m"
      containers:
      - name: web
`
	violations := checkPolicyViolations(manifest, emptyAllowlist)
	assert.Equal(t, []string{`Deployment/my-app/init has resource limits set (add to ResourceLimitsAllowlist if intentional)`}, violations)
}

func TestCheckPolicyViolations_CronJobWithLimits(t *testing.T) {
	manifest := `
apiVersion: batch/v1
kind: CronJob
metadata:
  name: cleanup
spec:
  jobTemplate:
    spec:
      template:
        spec:
          containers:
          - name: cleaner
            resources:
              limits:
                memory: "256Mi"
`
	violations := checkPolicyViolations(manifest, emptyAllowlist)
	assert.Equal(t, []string{`CronJob/cleanup/cleaner has resource limits set (add to ResourceLimitsAllowlist if intentional)`}, violations)
}

func TestCheckPolicyViolations_DaemonSetWithLimits(t *testing.T) {
	manifest := `
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: agent
spec:
  template:
    spec:
      containers:
      - name: collector
        resources:
          limits:
            memory: "512Mi"
`
	violations := checkPolicyViolations(manifest, emptyAllowlist)
	assert.Equal(t, []string{`DaemonSet/agent/collector has resource limits set (add to ResourceLimitsAllowlist if intentional)`}, violations)
}

func TestCheckPolicyViolations_NonWorkloadSkipped(t *testing.T) {
	manifest := `
apiVersion: v1
kind: ConfigMap
metadata:
  name: my-config
data:
  key: value
`
	violations := checkPolicyViolations(manifest, emptyAllowlist)
	assert.Empty(t, violations)
}

func TestCheckPolicyViolations_MultiDocument(t *testing.T) {
	manifest := `
apiVersion: v1
kind: Service
metadata:
  name: my-svc
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: my-app
spec:
  template:
    spec:
      containers:
      - name: web
        resources:
          limits:
            memory: "128Mi"
`
	violations := checkPolicyViolations(manifest, emptyAllowlist)
	assert.Equal(t, []string{`Deployment/my-app/web has resource limits set (add to ResourceLimitsAllowlist if intentional)`}, violations)
}

func TestCheckPolicyViolations_AllowlistedSkipped(t *testing.T) {
	manifest := `
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: test-allowlisted-ds
spec:
  template:
    spec:
      containers:
      - name: test-container
        resources:
          limits:
            memory: "1Gi"
`
	violations := checkPolicyViolations(manifest, testAllowlist)
	assert.Empty(t, violations)
}

func TestCheckPolicyViolations_EphemeralContainerWithLimits(t *testing.T) {
	manifest := `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: my-app
spec:
  template:
    spec:
      containers:
      - name: web
      ephemeralContainers:
      - name: debugger
        resources:
          limits:
            memory: "256Mi"
`
	violations := checkPolicyViolations(manifest, emptyAllowlist)
	assert.Equal(t, []string{`Deployment/my-app/debugger has resource limits set (add to ResourceLimitsAllowlist if intentional)`}, violations)
}

func TestCheckPolicyViolations_EphemeralContainerWithoutLimits(t *testing.T) {
	manifest := `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: my-app
spec:
  template:
    spec:
      containers:
      - name: web
      ephemeralContainers:
      - name: debugger
`
	violations := checkPolicyViolations(manifest, emptyAllowlist)
	assert.Empty(t, violations)
}

func TestCheckPolicyViolations_MixedAllowlistedAndViolating(t *testing.T) {
	manifest := `
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: test-allowlisted-ds
spec:
  template:
    spec:
      containers:
      - name: test-container
        resources:
          limits:
            memory: "1Gi"
      - name: not-allowlisted
        resources:
          limits:
            cpu: "100m"
`
	violations := checkPolicyViolations(manifest, testAllowlist)
	assert.Equal(t, []string{`DaemonSet/test-allowlisted-ds/not-allowlisted has resource limits set (add to ResourceLimitsAllowlist if intentional)`}, violations)
}
