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

import "sort"

// Target maps an observed (namespace, container) pair to the location of its
// resource block in the config tree.
type Target struct {
	// Service is a human-readable name used in output.
	Service string
	// ResourcePath is the dotted path to the container's `resources` node,
	// RELATIVE to the config prefix (e.g. "backend.k8s.resources" or
	// "arobit.forwarder.resources"). Request/limit scalar paths are derived from
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

// key uniquely identifies a workload container by namespace and container name.
type key struct {
	namespace string
	container string
}

// mapping is the authoritative table linking Kubernetes (namespace, container)
// pairs to their resource block in config.yaml (under `defaults`).
//
// This table is intentionally explicit: the tool NEVER guesses a mapping. Any
// (namespace, container) pair observed in Grafana that is not present here is
// reported as unmapped so a human can extend this table rather than silently
// editing the wrong field.
//
// Container names match the `container` label emitted by cAdvisor /
// kube-state-metrics (i.e. the pod spec container name), verified against the
// int-westus3-svc-1/mgmt-2 clusters and the prod services datasources.
var mapping = map[key]Target{
	{"aro-hcp", "aro-hcp-backend"}:                  {Service: "backend", ResourcePath: "backend.k8s.resources"},
	{"aro-hcp", "aro-hcp-frontend"}:                 {Service: "frontend", ResourcePath: "frontend.k8s.resources"},
	{"aro-hcp-admin-api", "service"}:                {Service: "adminApi", ResourcePath: "adminApi.k8s.resources"},
	{"aro-hcp-exporter", "aro-hcp-exporter"}:        {Service: "customExporter", ResourcePath: "customExporter.k8s.resources"},
	{"clusters-service", "clusters-service-server"}: {Service: "clustersService", ResourcePath: "clustersService.k8s.resources"},
	{"fleet", "fleet-controller"}:                   {Service: "fleet", ResourcePath: "fleet.k8s.resources"},
	{"kube-applier", "kube-applier"}:                {Service: "kubeApplier", ResourcePath: "kubeApplier.k8s.resources"},
	{"maestro", "maestro-server"}:                   {Service: "maestro.server", ResourcePath: "maestro.server.k8s.resources"},
	{"mgmt-agent", "mgmt-agent-controller"}:         {Service: "mgmtAgent", ResourcePath: "mgmtAgent.k8s.resources"},
	{"secret-sync-controller", "manager"}:           {Service: "secretSyncController", ResourcePath: "secretSyncController.k8s.resources"},
	{"sessiongate", "sessiongate-controller"}:       {Service: "sessiongate", ResourcePath: "sessiongate.k8s.resources"},
	{"monitoring", "kube-events"}:                   {Service: "kubeEvents", ResourcePath: "kubeEvents.k8s.resources"},
	{"arobit", "fluentbit"}:                         {Service: "arobit.forwarder", ResourcePath: "arobit.forwarder.resources"},
}

// Namespaces returns the distinct namespaces referenced by the mapping. These
// scope the Grafana queries so we only pull metrics for known services.
func Namespaces() []string {
	seen := map[string]struct{}{}
	var out []string
	for k := range mapping {
		if _, ok := seen[k.namespace]; ok {
			continue
		}
		seen[k.namespace] = struct{}{}
		out = append(out, k.namespace)
	}
	sort.Strings(out)
	return out
}

// Lookup returns the config target for a (namespace, container) pair.
func Lookup(namespace, container string) (Target, bool) {
	t, ok := mapping[key{namespace, container}]
	return t, ok
}
