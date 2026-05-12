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

var workloadKinds = sets.New[string](
	"Deployment",
	"StatefulSet",
	"DaemonSet",
	"Job",
	"CronJob",
)

func checkPolicyViolations(manifest string, resourceLimitsAllowlist sets.Set[string]) []string {
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

		violations = append(violations, checkPodSpecHasNoResourceLimits(w.Kind, w.Metadata.Name, podSpec, resourceLimitsAllowlist)...)
	}

	return violations
}

func checkPodSpecHasNoResourceLimits(kind, name string, podSpec corev1.PodSpec, allowlist sets.Set[string]) []string {
	var violations []string
	for _, c := range slices.Concat(podSpec.InitContainers, podSpec.Containers) {
		if len(c.Resources.Limits) > 0 {
			key := fmt.Sprintf("%s/%s/%s", kind, name, c.Name)
			if allowlist.Has(key) {
				continue
			}
			violations = append(violations, fmt.Sprintf("%s has resource limits set (add to ResourceLimitsAllowlist if intentional)", key))
		}
	}
	for _, ec := range podSpec.EphemeralContainers {
		if len(ec.Resources.Limits) > 0 {
			key := fmt.Sprintf("%s/%s/%s", kind, name, ec.Name)
			if allowlist.Has(key) {
				continue
			}
			violations = append(violations, fmt.Sprintf("%s has resource limits set (add to ResourceLimitsAllowlist if intentional)", key))
		}
	}
	return violations
}
