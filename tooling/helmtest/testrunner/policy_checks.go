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
	"fmt"
	"io"
	"path"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
)

type workloadMeta struct {
	Kind     string `json:"kind"`
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Spec struct {
		Template corev1.PodTemplateSpec `json:"template"`
		// CronJob nesting
		JobTemplate struct {
			Spec struct {
				Template corev1.PodTemplateSpec `json:"template"`
			} `json:"spec"`
		} `json:"jobTemplate"`
	} `json:"spec"`
}

var workloadKinds = sets.New(
	"Deployment",
	"StatefulSet",
	"DaemonSet",
	"Job",
	"CronJob",
)

func matchesAllowlist(key string, patterns []string) (bool, error) {
	for _, p := range patterns {
		matched, err := path.Match(p, key)
		if err != nil {
			return false, fmt.Errorf("invalid allowlist pattern %q: %w", p, err)
		}
		if matched {
			return true, nil
		}
	}
	return false, nil
}

func checkPolicyViolations(manifest string, resourceRequestsAllowlist, resourceMemoryLimitsAllowlist []string) []string {
	var violations []string

	decoder := utilyaml.NewYAMLToJSONDecoder(strings.NewReader(manifest))
	for {
		var w workloadMeta
		if err := decoder.Decode(&w); err != nil {
			// if we've reached the end of the manifest, break
			if err == io.EOF {
				break
			}

			// if we failed to decode the manifest document, add a violation
			violations = append(violations, fmt.Sprintf("failed to decode manifest document: %v", err))
			continue
		}

		if !workloadKinds.Has(w.Kind) {
			continue
		}

		// get pod spec from workload (or job template for CronJob)
		podSpec := w.Spec.Template.Spec
		if w.Kind == "CronJob" {
			podSpec = w.Spec.JobTemplate.Spec.Template.Spec
		}

		violations = append(violations, checkPodSpecMemoryResources(w.Kind, w.Metadata.Name, podSpec, resourceRequestsAllowlist, resourceMemoryLimitsAllowlist)...)
	}

	return violations
}

func checkPodSpecMemoryResources(kind, name string, podSpec corev1.PodSpec, requestsAllowlist, limitsAllowlist []string) []string {
	var violations []string
	for _, c := range slices.Concat(podSpec.InitContainers, podSpec.Containers) {
		key := fmt.Sprintf("%s/%s/%s", kind, name, c.Name)

		// Check 1: Fail if memory requests are NOT set (unless in requests allowlist)
		requestsMatched, err := matchesAllowlist(key, requestsAllowlist)
		if err != nil {
			violations = append(violations, err.Error())
		} else if !requestsMatched {
			memoryRequest := c.Resources.Requests.Memory()
			if memoryRequest == nil || memoryRequest.IsZero() {
				violations = append(violations, fmt.Sprintf("%s container is missing a memory request (resources.requests.memory not set; add to ResourceRequestsAllowlist if intentionally unlimited)", key))
			}
		}

		// Check 2: Fail if memory limits are NOT set (unless in limits allowlist)
		limitsMatched, err := matchesAllowlist(key, limitsAllowlist)
		if err != nil {
			violations = append(violations, err.Error())
		} else if !limitsMatched {
			memoryLimit := c.Resources.Limits.Memory()
			if memoryLimit == nil || memoryLimit.IsZero() {
				violations = append(violations, fmt.Sprintf("%s container is missing a memory limit (resources.limits.memory not set; add to ResourceMemoryLimitsAllowlist if intentionally unlimited)", key))
			}
		}
	}
	for _, ec := range podSpec.EphemeralContainers {
		key := fmt.Sprintf("%s/%s/%s", kind, name, ec.Name)

		// Check 1: Fail if memory requests are NOT set (unless in requests allowlist)
		requestsMatched, err := matchesAllowlist(key, requestsAllowlist)
		if err != nil {
			violations = append(violations, err.Error())
		} else if !requestsMatched {
			memoryRequest := ec.Resources.Requests.Memory()
			if memoryRequest == nil || memoryRequest.IsZero() {
				violations = append(violations, fmt.Sprintf("%s container is missing a memory request (resources.requests.memory not set; add to ResourceRequestsAllowlist if intentionally unlimited)", key))
			}
		}

		// Check 2: Fail if memory limits are NOT set (unless in limits allowlist)
		limitsMatched, err := matchesAllowlist(key, limitsAllowlist)
		if err != nil {
			violations = append(violations, err.Error())
		} else if !limitsMatched {
			memoryLimit := ec.Resources.Limits.Memory()
			if memoryLimit == nil || memoryLimit.IsZero() {
				violations = append(violations, fmt.Sprintf("%s container is missing a memory limit (resources.limits.memory not set; add to ResourceMemoryLimitsAllowlist if intentionally unlimited)", key))
			}
		}
	}
	return violations
}
