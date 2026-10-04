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

package rightsize

import (
	"sort"

	"github.com/Azure/ARO-HCP/tooling/rightsize-requests/pkg/targets"
)

// Target maps an observed (namespace, container) pair to the location of its
// resource block in the config tree.
type Target struct {
	// Service is a human-readable name used in output.
	Service string
	// ResourcePath is the dotted path to the container's `resources` node,
	// RELATIVE to the config prefix (e.g. "backend.k8s.resources" or
	// "svc.arobit.forwarder.resources"). Request/limit scalar paths are derived from
	// it, and a configurable prefix (e.g. "defaults" or
	// "clouds.public.defaults") is prepended at edit time.
	ResourcePath string
}

// requestPath returns the dotted path (under prefix) to a request scalar.
func (t Target) requestPath(prefix, resource string) string {
	return prefix + "." + t.ResourcePath + ".requests." + resource
}

// limitPath returns the dotted path (under prefix) to a limit scalar.
func (t Target) limitPath(prefix, resource string) string {
	return prefix + "." + t.ResourcePath + ".limits." + resource
}

// Namespaces returns the distinct namespaces referenced by the mapping. These
// scope the Grafana queries so we only pull metrics for known services.
func Namespaces() []string {
	seen := map[string]struct{}{}
	var out []string
	for _, target := range targets.ServiceTargets() {
		if _, ok := targets.Lookup(target.Namespace, target.Container); !ok {
			continue
		}
		if _, ok := seen[target.Namespace]; ok {
			continue
		}
		seen[target.Namespace] = struct{}{}
		out = append(out, target.Namespace)
	}
	sort.Strings(out)
	return out
}

// Lookup returns the config target for a (namespace, container) pair.
func Lookup(namespace, container string) (Target, bool) {
	target, ok := targets.Lookup(namespace, container)
	return Target{Service: target.Service, ResourcePath: target.ResourcePath}, ok
}

func Resolve(namespace, kind, workload, container, cluster string, init bool) (Target, bool) {
	target, ok := targets.Resolve(namespace, kind, workload, container, cluster, init)
	return Target{Service: target.Service, ResourcePath: target.ResourcePath}, ok
}

func CandidateTargets(namespace, workload, container, cluster string, init bool) []Target {
	var result []Target
	for _, target := range targets.CandidateTargets(namespace, workload, container, cluster, init) {
		result = append(result, Target{Service: target.Service, ResourcePath: target.ResourcePath})
	}
	return result
}
