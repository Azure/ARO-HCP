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

var emptyAllowlist []string

var testAllowlist = []string{
	"DaemonSet/test-allowlisted-ds/test-container",
}

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
          requests:
            memory: "64Mi"
          limits:
            memory: "128Mi"
`
	violations := checkPolicyViolations(manifest, emptyAllowlist, emptyAllowlist)
	assert.Empty(t, violations) // Passes because both requests and limits ARE set
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
	violations := checkPolicyViolations(manifest, emptyAllowlist, emptyAllowlist)
	assert.Equal(t, []string{`Deployment/my-app/web container is missing a memory limit (resources.limits.memory not set; add to ResourceMemoryLimitsAllowlist if intentionally unlimited)`}, violations) // Fails because memory limits are NOT set (requests are set)
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
	violations := checkPolicyViolations(manifest, emptyAllowlist, emptyAllowlist)
	// Both init and web containers are missing memory requests AND limits
	assert.Equal(t, []string{
		`Deployment/my-app/init container is missing a memory request (resources.requests.memory not set; add to ResourceRequestsAllowlist if intentionally unset)`,
		`Deployment/my-app/init container is missing a memory limit (resources.limits.memory not set; add to ResourceMemoryLimitsAllowlist if intentionally unlimited)`,
		`Deployment/my-app/web container is missing a memory request (resources.requests.memory not set; add to ResourceRequestsAllowlist if intentionally unset)`,
		`Deployment/my-app/web container is missing a memory limit (resources.limits.memory not set; add to ResourceMemoryLimitsAllowlist if intentionally unlimited)`,
	}, violations)
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
              requests:
                memory: "128Mi"
              limits:
                memory: "256Mi"
`
	violations := checkPolicyViolations(manifest, emptyAllowlist, emptyAllowlist)
	assert.Empty(t, violations) // Passes because both requests and limits ARE set
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
          requests:
            memory: "256Mi"
          limits:
            memory: "512Mi"
`
	violations := checkPolicyViolations(manifest, emptyAllowlist, emptyAllowlist)
	assert.Empty(t, violations) // Passes because both requests and limits ARE set
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
	violations := checkPolicyViolations(manifest, emptyAllowlist, emptyAllowlist)
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
          requests:
            memory: "64Mi"
          limits:
            memory: "128Mi"
`
	violations := checkPolicyViolations(manifest, emptyAllowlist, emptyAllowlist)
	assert.Empty(t, violations) // Passes because both requests and limits ARE set
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
          requests:
            memory: "512Mi"
          limits:
            memory: "1Gi"
`
	violations := checkPolicyViolations(manifest, testAllowlist, testAllowlist)
	assert.Empty(t, violations) // Allowlist still works - exempt from requiring limits/requests
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
          requests:
            memory: "128Mi"
          limits:
            memory: "256Mi"
`
	violations := checkPolicyViolations(manifest, emptyAllowlist, emptyAllowlist)
	// web container is missing both requests and limits, debugger has them
	assert.Equal(t, []string{
		`Deployment/my-app/web container is missing a memory request (resources.requests.memory not set; add to ResourceRequestsAllowlist if intentionally unset)`,
		`Deployment/my-app/web container is missing a memory limit (resources.limits.memory not set; add to ResourceMemoryLimitsAllowlist if intentionally unlimited)`,
	}, violations)
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
	violations := checkPolicyViolations(manifest, emptyAllowlist, emptyAllowlist)
	// Both web and debugger containers are missing memory requests AND limits
	assert.Equal(t, []string{
		`Deployment/my-app/web container is missing a memory request (resources.requests.memory not set; add to ResourceRequestsAllowlist if intentionally unset)`,
		`Deployment/my-app/web container is missing a memory limit (resources.limits.memory not set; add to ResourceMemoryLimitsAllowlist if intentionally unlimited)`,
		`Deployment/my-app/debugger container is missing a memory request (resources.requests.memory not set; add to ResourceRequestsAllowlist if intentionally unset)`,
		`Deployment/my-app/debugger container is missing a memory limit (resources.limits.memory not set; add to ResourceMemoryLimitsAllowlist if intentionally unlimited)`,
	}, violations)
}

func TestCheckPolicyViolations_WildcardAllowlist(t *testing.T) {
	manifest := `
apiVersion: batch/v1
kind: Job
metadata:
  name: dev-westus3-svc-1-fleet-reg
spec:
  template:
    spec:
      containers:
      - name: fleet-registration
        resources:
          requests:
            memory: "128Mi"
          limits:
            memory: "256Mi"
`
	wildcardAllowlist := []string{"Job/*/fleet-registration"}
	violations := checkPolicyViolations(manifest, wildcardAllowlist, wildcardAllowlist)
	assert.Empty(t, violations) // Allowlist still works - exempt from requiring limits/requests
}

func TestCheckPolicyViolations_WildcardNoMatch(t *testing.T) {
	manifest := `
apiVersion: batch/v1
kind: Job
metadata:
  name: dev-westus3-svc-1-fleet-reg
spec:
  template:
    spec:
      containers:
      - name: some-other-container
        resources:
          requests:
            memory: "128Mi"
          limits:
            memory: "256Mi"
`
	wildcardAllowlist := []string{"Job/*/fleet-registration"}
	violations := checkPolicyViolations(manifest, wildcardAllowlist, wildcardAllowlist)
	assert.Empty(t, violations) // Container has both memory requests and limits, so it passes
}

func TestCheckPolicyViolations_MalformedPattern(t *testing.T) {
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
          limits:
            memory: "128Mi"
`
	badAllowlist := []string{"Deployment/my-app/[bad"}
	violations := checkPolicyViolations(manifest, badAllowlist, badAllowlist)
	assert.Len(t, violations, 2)                                       // Two errors: one for requests check, one for limits check
	assert.Contains(t, violations[0], "ResourceRequestsAllowlist")     // Error indicates which allowlist failed
	assert.Contains(t, violations[0], "invalid pattern")               // Error handling still works
	assert.Contains(t, violations[1], "ResourceMemoryLimitsAllowlist") // Error indicates which allowlist failed
	assert.Contains(t, violations[1], "invalid pattern")               // Error handling still works
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
          requests:
            memory: "512Mi"
          limits:
            memory: "1Gi"
      - name: not-allowlisted
        resources:
          limits:
            cpu: "100m"
`
	violations := checkPolicyViolations(manifest, testAllowlist, testAllowlist)
	// test-container is allowlisted, not-allowlisted has no memory requests or limits
	assert.Equal(t, []string{
		`DaemonSet/test-allowlisted-ds/not-allowlisted container is missing a memory request (resources.requests.memory not set; add to ResourceRequestsAllowlist if intentionally unset)`,
		`DaemonSet/test-allowlisted-ds/not-allowlisted container is missing a memory limit (resources.limits.memory not set; add to ResourceMemoryLimitsAllowlist if intentionally unlimited)`,
	}, violations)
}

func TestCheckPolicyViolations_AllowlistedContainerWithoutMemoryLimits(t *testing.T) {
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
          requests:
            memory: "128Mi"
`
	violations := checkPolicyViolations(manifest, testAllowlist, testAllowlist)
	assert.Empty(t, violations) // Allowlisted containers can have no limits
}

func TestCheckPolicyViolations_OnlyCPULimitsNoMemory(t *testing.T) {
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
            cpu: "500m"
`
	violations := checkPolicyViolations(manifest, emptyAllowlist, emptyAllowlist)
	assert.Equal(t, []string{
		`Deployment/my-app/web container is missing a memory request (resources.requests.memory not set; add to ResourceRequestsAllowlist if intentionally unset)`,
		`Deployment/my-app/web container is missing a memory limit (resources.limits.memory not set; add to ResourceMemoryLimitsAllowlist if intentionally unlimited)`,
	}, violations)
}

func TestCheckPolicyViolations_BothCPUAndMemoryLimits(t *testing.T) {
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
            cpu: "250m"
            memory: "128Mi"
          limits:
            cpu: "500m"
            memory: "256Mi"
`
	violations := checkPolicyViolations(manifest, emptyAllowlist, emptyAllowlist)
	assert.Empty(t, violations) // Has both memory requests and limits, so passes
}

func TestCheckPolicyViolations_MissingRequestsOnly(t *testing.T) {
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
            memory: "256Mi"
`
	violations := checkPolicyViolations(manifest, emptyAllowlist, emptyAllowlist)
	// Has limits but missing requests - should fail the requests check
	assert.Equal(t, []string{
		`Deployment/my-app/web container is missing a memory request (resources.requests.memory not set; add to ResourceRequestsAllowlist if intentionally unset)`,
	}, violations)
}

func TestCheckPolicyViolations_MissingLimitsOnly(t *testing.T) {
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
            memory: "128Mi"
`
	violations := checkPolicyViolations(manifest, emptyAllowlist, emptyAllowlist)
	// Has requests but missing limits - should fail the limits check
	assert.Equal(t, []string{
		`Deployment/my-app/web container is missing a memory limit (resources.limits.memory not set; add to ResourceMemoryLimitsAllowlist if intentionally unlimited)`,
	}, violations)
}
