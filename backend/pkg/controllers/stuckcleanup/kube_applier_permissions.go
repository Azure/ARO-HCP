// Copyright 2026 Microsoft Corporation
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

package stuckcleanup

// kubeApplierDeletableResources mirrors the delete-capable rules of
// kube-applier/deploy/templates/clusterrole.yaml.
//
// Why this exists: the kube-applier's apply controller takes the GVR verbatim
// from a desire and applies no code-side allowlist, so that ClusterRole is the
// only gate on what a future mutation release could touch. Reporting, per
// remaining group/resource, whether the gate is already open turns the
// inspection from "here is what is stuck" into "here is what we could not
// retire even if we tried" — which is the question the mutation release needs
// answered before it can be scoped.
//
// This is a mirror, so it can drift from the ClusterRole. It is deliberately
// advisory: it annotates findings and gates nothing. A stale entry produces a
// misleading report, never an unsafe action. kube_applier_permissions_test.go
// parses the real ClusterRole and fails if the two disagree.
//
// The empty string key is the core API group.
var kubeApplierDeletableResources = map[string]map[string]bool{
	"": {
		"namespaces": true,
		"secrets":    true,
		"configmaps": true,
		// services is granted get/list/watch only, so it is absent here.
	},
	"hypershift.openshift.io":              {"*": true},
	"certificates.hypershift.openshift.io": {"*": true},
	"certificates.k8s.io":                  {"certificatesigningrequests": true},
	"secrets-store.csi.x-k8s.io":           {"secretproviderclasses": true},
	"secret-sync.x-k8s.io":                 {"secretsyncs": true},
	"cluster.open-cluster-management.io":   {"managedclusters": true},
	"velero.io":                            {"schedules": true, "backups": true},
	"multitenancy.acn.azure.com":           {"podnetworks": true, "podnetworkinstances": true},
	// mgmtagent.aro-hcp.azure.com/capacityreports is read-only, so it is absent.
}

// kubeApplierCanDelete reports whether the kube-applier's ClusterRole grants
// delete on the given group and resource today.
func kubeApplierCanDelete(group, resource string) bool {
	resources, groupFound := kubeApplierDeletableResources[group]
	if !groupFound {
		return false
	}
	if resources["*"] {
		return true
	}
	return resources[resource]
}
