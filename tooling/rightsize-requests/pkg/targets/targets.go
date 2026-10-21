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

// Package targets catalogs the explicit resource-request mappings supported by
// rightsize-requests. Unknown containers are never mapped by inference.
package targets

import (
	"fmt"
	"regexp"
	"strings"

	k8sutilvalidation "k8s.io/apimachinery/pkg/util/validation"
)

// ServiceTarget maps a container identity to a config block. Empty workload,
// kind and cluster role constraints are optional; init containers require opt-in.
type ServiceTarget struct {
	Namespace string
	Container string
	Service   string
	// ResourcePath is relative to the config prefix, such as defaults.
	ResourcePath  string
	Workload      string
	Kind          string
	ClusterRole   string
	InitContainer bool
}

// MinimalTarget identifies an exact controller/workload/container supported in
// the limitClusterSizes=true/e2e_minimal sizing template branch.
type MinimalTarget struct {
	Kind      string
	Workload  string
	Container string
}

// ValidateResourceRequestOverride checks the annotation key produced by HyperShift.
// Catalog membership alone does not guarantee a valid deployment.container suffix.
func ValidateResourceRequestOverride(workload, container string) error {
	key := "resource-request-override.hypershift.openshift.io/" + workload + "." + container
	if problems := k8sutilvalidation.IsQualifiedName(key); len(problems) > 0 {
		return fmt.Errorf("invalid resource request override annotation key %q: %s", key, strings.Join(problems, "; "))
	}
	return nil
}

// ServiceTargets returns an independent copy of the supported service catalog.
// Container names are pod spec names, as emitted by cAdvisor/kube-state-metrics.
func ServiceTargets() []ServiceTarget {
	result := []ServiceTarget{
		{Namespace: "aro-hcp", Container: "aro-hcp-backend", Service: "backend", ResourcePath: "backend.k8s.resources"},
		{Namespace: "aro-hcp", Container: "aro-hcp-frontend", Service: "frontend", ResourcePath: "frontend.k8s.resources"},
		{Namespace: "aro-hcp-admin-api", Container: "service", Service: "adminApi", ResourcePath: "adminApi.k8s.resources"},
		{Namespace: "aro-hcp-exporter", Container: "aro-hcp-exporter", Service: "customExporter", ResourcePath: "customExporter.k8s.resources"},
		{Namespace: "clusters-service", Container: "clusters-service-server", Service: "clustersService", ResourcePath: "clustersService.k8s.resources"},
		{Namespace: "fleet", Container: "fleet-controller", Service: "fleet", ResourcePath: "fleet.k8s.resources"},
		{Namespace: "kube-applier", Container: "kube-applier", Service: "kubeApplier", ResourcePath: "kubeApplier.k8s.resources"},
		{Namespace: "maestro", Container: "maestro-server", Service: "maestro.server", ResourcePath: "maestro.server.k8s.resources"},
		{Namespace: "mgmt-agent", Container: "mgmt-agent-controller", Service: "mgmtAgent", ResourcePath: "mgmtAgent.k8s.resources"},
		{Namespace: "secret-sync-controller", Container: "provider-azure-installer", Service: "secretSyncController", ResourcePath: "secretSyncController.k8s.resources"},
		{Namespace: "sessiongate", Container: "sessiongate-controller", Service: "sessiongate", ResourcePath: "sessiongate.k8s.resources"},
		{Namespace: "monitoring", Container: "kube-events", Service: "kubeEvents", ResourcePath: "kubeEvents.k8s.resources"},
		{Namespace: "arobit", Container: "fluentbit", ClusterRole: "svc", Service: "svc.arobit.forwarder", ResourcePath: "svc.arobit.forwarder.resources"},
		{Namespace: "arobit", Container: "fluentbit", ClusterRole: "mgmt", Service: "mgmt.arobit.forwarder", ResourcePath: "mgmt.arobit.forwarder.resources"},
		{Namespace: "acrpull", Kind: "Deployment", Workload: "acrpull", Container: "acrpull-controller", Service: "acrPull", ResourcePath: "acrPull.k8s.resources"},
		{Namespace: "velero", Kind: "Deployment", Workload: "velero", Container: "velero", ClusterRole: "mgmt", Service: "velero.server", ResourcePath: "velero.server.resources"},
		{Namespace: "velero", Kind: "DaemonSet", Workload: "node-agent", Container: "node-agent", ClusterRole: "mgmt", Service: "velero.nodeAgent", ResourcePath: "velero.nodeAgent.resources"},
		{Namespace: "velero", Kind: "Deployment", Workload: "velero", Container: "oadp-oadp-velero-plugin-for-microsoft-azure-rhel9", ClusterRole: "mgmt", InitContainer: true, Service: "velero.azurePlugin", ResourcePath: "velero.azurePlugin.resources"},
		{Namespace: "velero", Kind: "Deployment", Workload: "velero", Container: "oadp-oadp-hypershift-velero-plugin-rhel9", ClusterRole: "mgmt", InitContainer: true, Service: "velero.hypershiftPlugin", ResourcePath: "velero.hypershiftPlugin.resources"},
		{Namespace: "velero", Kind: "Job", Workload: "velero-install", Container: "generate-manifest", ClusterRole: "mgmt", InitContainer: true, Service: "velero.installer.generate", ResourcePath: "velero.installer.generate.resources"},
		{Namespace: "velero", Kind: "Job", Workload: "velero-install", Container: "apply-manifest", ClusterRole: "mgmt", Service: "velero.installer.apply", ResourcePath: "velero.installer.apply.resources"},
		{Namespace: "swift-recorder", Kind: "DaemonSet", Workload: "swift-recorder", Container: "swift-recorder", Service: "swiftRecorder", ResourcePath: "swiftRecorder.k8s.resources"},
		{Namespace: "maestro", Kind: "Deployment", Workload: "maestro-agent", Container: "maestro-agent", ClusterRole: "mgmt", Service: "maestro.agent", ResourcePath: "maestro.agent.k8s.resources"},
		{Namespace: "maestro", Kind: "Deployment", Workload: "maestro-agent", Container: "metrics-proxy", ClusterRole: "mgmt", Service: "maestro.agent.metricsProxy", ResourcePath: "maestro.agent.metricsProxy.resources"},
		{Namespace: "maestro", Kind: "Deployment", Workload: "maestro-agent", Container: "init", ClusterRole: "mgmt", InitContainer: true, Service: "maestro.agent.init", ResourcePath: "maestro.agent.init.resources"},
		{Namespace: "secret-sync-controller", Kind: "Deployment", Workload: "secrets-store-sync-controller-manager", Container: "manager", Service: "secretSyncController.controller", ResourcePath: "secretSyncController.k8s.controllerResources"},
		{Namespace: "aks-istio-ingress", Kind: "Deployment", Workload: "frontend-certificate-refresher", Container: "init-container-msg-container-init", ClusterRole: "svc", Service: "frontend.certificateRefresher", ResourcePath: "frontend.certificateRefresher.resources"},
		{Namespace: "aks-istio-ingress", Kind: "Deployment", Workload: "admin-api-certificate-refresher", Container: "init-container-msg-container-init", ClusterRole: "svc", Service: "adminApi.certificateRefresher", ResourcePath: "adminApi.certificateRefresher.resources"},
		{Namespace: "aks-istio-ingress", Kind: "Deployment", Workload: "sessiongate-certificate-refresher", Container: "init-container-msg-container-init", ClusterRole: "svc", Service: "sessiongate.certificateRefresher", ResourcePath: "sessiongate.certificateRefresher.resources"},
		{Namespace: "aks-istio-ingress", Kind: "Deployment", Workload: "ops-ingress-gateway-istio", Container: "istio-proxy", ClusterRole: "svc", Service: "svc.opsIngress.gateway", ResourcePath: "svc.opsIngress.gateway.resources"},
		{Namespace: "clusters-service", Kind: "Deployment", Workload: "ocm-cs-db", Container: "postgresql", ClusterRole: "svc", Service: "clustersService.postgres", ResourcePath: "clustersService.postgres.containerizedDb.resources"},
		{Namespace: "clusters-service", Kind: "Deployment", Workload: "clusters-service", Container: "clusters-service-init", ClusterRole: "svc", InitContainer: true, Service: "clustersService.init", ResourcePath: "clustersService.initResources"},
		{Namespace: "maestro", Kind: "Deployment", Workload: "maestro-db", Container: "postgresql", ClusterRole: "svc", Service: "maestro.postgres", ResourcePath: "maestro.postgres.containerizedDb.resources"},
		{Namespace: "maestro", Kind: "Deployment", Workload: "maestro", Container: "maestro-server-migration", ClusterRole: "svc", InitContainer: true, Service: "maestro.server.migration", ResourcePath: "maestro.server.migrationResources"},
		{Namespace: "maestro", Kind: "Deployment", Workload: "maestro", Container: "wait-for-db", ClusterRole: "svc", InitContainer: true, Service: "maestro.server.waitForDB", ResourcePath: "maestro.server.waitForDBResources"},
		{Namespace: "kube-system", Kind: "DaemonSet", Workload: "set-kubelet-parameters-for-scale", Container: "kubelet-parameters", ClusterRole: "mgmt", Service: "mgmt.kubeletFixes", ResourcePath: "mgmt.kubeletFixes.resources"},
		{Namespace: "kube-system", Kind: "DaemonSet", Workload: "set-kubelet-parameters-for-scale", Container: "apply-kubelet-parameters", ClusterRole: "mgmt", InitContainer: true, Service: "mgmt.kubeletFixes.init", ResourcePath: "mgmt.kubeletFixes.initResources"},
		{Namespace: "default-sc", Kind: "Job", Workload: "unannotate-default-sc", Container: "finalize", ClusterRole: "mgmt", Service: "mgmt.storageClassHooks", ResourcePath: "mgmt.storageClassHooks.resources"},
		{Namespace: "default-sc", Kind: "Job", Workload: "delete-sc-pre-upgrade", Container: "delete-sc", ClusterRole: "mgmt", Service: "mgmt.storageClassHooks", ResourcePath: "mgmt.storageClassHooks.resources"},
		{Namespace: "ocm-arohcp*", Kind: "Deployment", Workload: "kube-state-metrics-hcp", Container: "kube-state-metrics", ClusterRole: "mgmt", Service: "mgmtAgent.guestKSM", ResourcePath: "mgmtAgent.guestKSMResources"},
		{Namespace: "hypershift", Kind: "Deployment", Workload: "operator", Container: "operator", ClusterRole: "mgmt", Service: "hypershift.operator", ResourcePath: "hypershift.operatorResources"},
	}
	for _, role := range []string{"svc", "mgmt", "opstool"} {
		for _, component := range []struct {
			kind, workload, container, path string
			init                            bool
		}{
			// The pipelines use arohcp-monitor; chart fixtures also use prometheus.
			{"Deployment", "arohcp-monitor-kube-state-metrics", "kube-state-metrics", "kubeStateMetrics", false},
			{"Deployment", "prometheus-kube-state-metrics", "kube-state-metrics", "kubeStateMetrics", false},
			{"Deployment", "prometheus-operator", "kube-prometheus-stack", "prometheusOperator", false},
			{"StatefulSet", "prom-agent-prometheus", "config-reloader", "prometheusConfigReloader", false},
			{"StatefulSet", "prom-agent-prometheus", "init-config-reloader", "prometheusConfigReloader", true},
			{"StatefulSet", "prom-agent-prometheus", "prometheus", "prometheusSpec", false},
		} {
			path := role + ".prometheus." + component.path + ".resources"
			if role == "opstool" {
				// Opstool shares svc auxiliary policies, but not agent sizing.
				path = "svc.prometheus." + component.path + ".resources"
				if component.path == "prometheusSpec" {
					path = "svc.prometheus.opstoolResources"
				}
			}
			result = append(result, ServiceTarget{Namespace: "prometheus", Kind: component.kind, Workload: component.workload, Container: component.container, ClusterRole: role, InitContainer: component.init, Service: strings.TrimSuffix(path, ".resources"), ResourcePath: path})
		}
	}
	for _, component := range []struct{ kind, workload, container, key string }{
		{"Deployment", "multicluster-engine-operator", "backplane-operator", "backplaneOperator"},
		{"Deployment", "grc-policy-addon-controller", "manager", "policyAddonController"},
		{"Deployment", "grc-policy-propagator", "governance-policy-propagator", "policyPropagator"},
		{"Deployment", "klusterlet-addon-controller-v2", "klusterlet-addon-controller", "klusterletAddonController"},
		{"Job", "finalize-mce", "finalize", "finalize"},
		{"Job", "finalize-mce-config", "finalize", "finalize"},
	} {
		result = append(result, ServiceTarget{Namespace: "multicluster-engine", Kind: component.kind, Workload: component.workload, Container: component.container, ClusterRole: "mgmt", Service: "acm." + component.key, ResourcePath: "acm.resources." + component.key})
	}
	for _, namespace := range []string{"klusterlet-*", "open-cluster-management-agent-addon"} {
		for _, component := range []struct{ workload, container, key string }{
			{"config-policy-controller", "config-policy-controller", "configPolicyController"},
			{"governance-policy-framework", "governance-policy-framework-addon", "governancePolicyFramework"},
			{"klusterlet-addon-workmgr", "acm-agent", "workManager"},
			{"hypershift-addon-agent", "hypershift-addon-agent", "hypershiftAddonAgent"},
		} {
			result = append(result, ServiceTarget{Namespace: namespace, Kind: "Deployment", Workload: component.workload, Container: component.container, ClusterRole: "mgmt", Service: "acm." + component.key, ResourcePath: "acm.resources." + component.key})
		}
	}
	return result
}

// MinimalTargets returns an independent copy of the supported minimal-HCP catalog.
// The selected template must still contain an entry before the CLI can edit it.
func MinimalTargets() []MinimalTarget {
	return []MinimalTarget{
		{Kind: "Deployment", Workload: "kube-apiserver", Container: "kube-apiserver"},
		{Kind: "Deployment", Workload: "openshift-controller-manager", Container: "openshift-controller-manager"},
		{Kind: "Deployment", Workload: "cluster-policy-controller", Container: "cluster-policy-controller"},
		{Kind: "Deployment", Workload: "kube-controller-manager", Container: "kube-controller-manager"},
		{Kind: "Deployment", Workload: "openshift-apiserver", Container: "openshift-apiserver"},
		{Kind: "StatefulSet", Workload: "etcd", Container: "etcd"},
		{Kind: "Deployment", Workload: "ovnkube-control-plane", Container: "ovnkube-control-plane"},
	}
}

// AdditionalMinimalTargets returns an independent copy of the audited regular-HCP
// capabilities beyond the original seven. This static catalog mirrors
// hypershiftoperator/deploy/regular-resource-targets.yaml (checked by test).
// These targets require a configured baseline in hypershift.additionalMinimalResourceRequests;
// inclusion here does not establish that such a baseline exists.
// ValidateResourceRequestOverride must also pass before configuring a target;
// route-controller and csi-snapshot-controller-operator exceed the suffix limit.
func AdditionalMinimalTargets() []MinimalTarget {
	return []MinimalTarget{
		{"Deployment", "control-plane-operator", "control-plane-operator"},
		{"Deployment", "cluster-api", "manager"},
		{"Deployment", "capi-provider", "manager"},
		{"Deployment", "azure-cloud-controller-manager", "cloud-controller-manager"},
		{"Deployment", "catalog-operator", "catalog-operator"},
		{"Deployment", "catalog-operator", "konnectivity-proxy-socks5"},
		{"Deployment", "certified-operators-catalog", "registry"},
		{"Deployment", "cluster-autoscaler", "cluster-autoscaler"},
		{"Deployment", "cluster-image-registry-operator", "cluster-image-registry-operator"},
		{"Deployment", "cluster-image-registry-operator", "apiserver-token-minter"},
		{"Deployment", "cluster-network-operator", "cluster-network-operator"},
		{"Deployment", "cluster-network-operator", "client-token-minter"},
		{"Deployment", "cluster-network-operator", "konnectivity-proxy-socks5"},
		{"Deployment", "cluster-node-tuning-operator", "cluster-node-tuning-operator"},
		{"Deployment", "cluster-storage-operator", "cluster-storage-operator"},
		{"Deployment", "cluster-version-operator", "cluster-version-operator"},
		{"Deployment", "community-operators-catalog", "registry"},
		{"Deployment", "control-plane-pki-operator", "control-plane-pki-operator"},
		{"Deployment", "csi-snapshot-controller-operator", "csi-snapshot-controller-operator"},
		{"Deployment", "dns-operator", "dns-operator"},
		{"StatefulSet", "etcd", "etcd-defrag"},
		{"StatefulSet", "etcd", "etcd-metrics"},
		{"StatefulSet", "etcd", "healthz"},
		{"Deployment", "hosted-cluster-config-operator", "hosted-cluster-config-operator"},
		{"Deployment", "ignition-server", "ignition-server"},
		{"Deployment", "ignition-server-proxy", "haproxy"},
		{"Deployment", "ingress-operator", "ingress-operator"},
		{"Deployment", "ingress-operator", "konnectivity-proxy-https"},
		{"Deployment", "konnectivity-agent", "konnectivity-agent"},
		{"Deployment", "kube-apiserver", "audit-logs"},
		{"Deployment", "kube-apiserver", "azure-kms-provider-active"},
		{"Deployment", "kube-apiserver", "azure-kms-provider-backup"},
		{"Deployment", "kube-apiserver", "azure-workload-identity-webhook"},
		{"Deployment", "kube-apiserver", "bootstrap"},
		{"Deployment", "kube-apiserver", "konnectivity-server"},
		{"Deployment", "kube-controller-manager", "availability-prober"},
		{"Deployment", "kube-scheduler", "kube-scheduler"},
		{"Deployment", "kube-storage-version-migrator", "migrator"},
		{"Deployment", "machine-approver", "machine-approver"},
		{"Deployment", "olm-operator", "olm-operator"},
		{"Deployment", "olm-operator", "konnectivity-proxy-socks5"},
		{"Deployment", "openshift-apiserver", "audit-logs"},
		{"Deployment", "openshift-apiserver", "kas-readiness-check"},
		{"Deployment", "openshift-apiserver", "konnectivity-proxy-https"},
		{"Deployment", "openshift-route-controller-manager", "openshift-route-controller-manager"},
		{"Deployment", "packageserver", "packageserver"},
		{"Deployment", "packageserver", "kas-readiness-check"},
		{"Deployment", "packageserver", "konnectivity-proxy-socks5"},
		{"Deployment", "redhat-marketplace-catalog", "registry"},
		{"Deployment", "redhat-operators-catalog", "registry"},
		{"Deployment", "router", "router"},
	}
}

// RequiresConfiguredBaseline reports an additional minimal-HCP capability, not
// whether its baseline is present. Callers must verify configuration before edits.
func RequiresConfiguredBaseline(namespace, kind, workload, container, cluster string, init bool) bool {
	if init || clusterRole(cluster) != "mgmt" || !strings.HasPrefix(namespace, "ocm-arohcp") || len(namespace) <= len("ocm-arohcp") {
		return false
	}
	for _, target := range AdditionalMinimalTargets() {
		if kind == target.Kind && workload == target.Workload && container == target.Container {
			return true
		}
	}
	return false
}

// Editable reports whether an observed identity has a supported request mapping.
// It does not establish measurement eligibility, HCP size class, or edit safety;
// callers must not discard unresolved evidence that could block a CLI update.
func Editable(namespace, kind, workload, container string, init bool) bool {
	return EditableInCluster(namespace, kind, workload, container, "", init)
}

// EditableInCluster checks supported capability, not configured baselines,
// measurement eligibility or size class. Additional targets require an explicit
// management-cluster role and a baseline checked separately by the caller.
func EditableInCluster(namespace, kind, workload, container, cluster string, init bool) bool {
	if _, ok := Resolve(namespace, kind, workload, container, cluster, init); ok {
		return true
	}
	if !init && strings.HasPrefix(namespace, "ocm-arohcp") && len(namespace) > len("ocm-arohcp") {
		for _, target := range MinimalTargets() {
			if kind == target.Kind && workload == target.Workload && container == target.Container {
				return true
			}
		}
	}
	return RequiresConfiguredBaseline(namespace, kind, workload, container, cluster, init)
}

var clusterRolePattern = regexp.MustCompile(`(?:^|-)(svc(?:-[0-9]+)?|mgmt-[0-9]+|opstool(?:-[0-9]+)?)$`)
var prometheusWorkloadPattern = regexp.MustCompile(`^prom-agent-prometheus(?:-shard-[0-9]+)?$`)

func clusterRole(cluster string) string {
	match := clusterRolePattern.FindStringSubmatch(cluster)
	if len(match) == 0 {
		return ""
	}
	return strings.SplitN(match[1], "-", 2)[0]
}

func knownWorkload(workload string) bool {
	return strings.TrimSpace(workload) != "" && !strings.EqualFold(workload, "unknown")
}

func (t ServiceTarget) matches(namespace, workload, container, role string, init bool) bool {
	namespaceMatches := t.Namespace == namespace
	// Prefix matching is only enabled for explicitly cataloged namespace families.
	if t.Namespace == "ocm-arohcp*" || t.Namespace == "klusterlet-*" {
		prefix := strings.TrimSuffix(t.Namespace, "*")
		namespaceMatches = strings.HasPrefix(namespace, prefix) && len(namespace) > len(prefix)
	}
	workloadMatches := t.Workload == "" || t.Workload == workload
	if t.Kind == "StatefulSet" && t.Workload == "prom-agent-prometheus" {
		workloadMatches = prometheusWorkloadPattern.MatchString(workload)
	}
	return namespaceMatches && workloadMatches && t.Container == container && t.InitContainer == init && (t.ClusterRole == "" || t.ClusterRole == role)
}

// Resolve requires complete owner identity. Unknown roles never select role-specific paths.
func Resolve(namespace, kind, workload, container, cluster string, init bool) (ServiceTarget, bool) {
	if namespace == "" || container == "" || !knownWorkload(workload) {
		return ServiceTarget{}, false
	}
	switch kind {
	case "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet", "Job", "CronJob":
	default:
		return ServiceTarget{}, false
	}
	role := clusterRole(cluster)
	var found ServiceTarget
	for _, target := range ServiceTargets() {
		if (target.Kind == "" || target.Kind == kind) && target.matches(namespace, workload, container, role, init) {
			if found.ResourcePath != "" && found.ResourcePath != target.ResourcePath {
				return ServiceTarget{}, false
			}
			found = target
		}
	}
	return found, found.ResourcePath != ""
}

// CandidateTargets returns paths to block, never edit. Missing workload/role
// evidence is conservative; known fields cannot block an unrelated target.
func CandidateTargets(namespace, workload, container, cluster string, init bool) []ServiceTarget {
	role := clusterRole(cluster)
	var result []ServiceTarget
	seen := map[string]bool{}
	for _, target := range ServiceTargets() {
		w, r := workload, role
		if !knownWorkload(w) {
			w = target.Workload
		}
		if r == "" {
			r = target.ClusterRole
		}
		if target.matches(namespace, w, container, r, init) && !seen[target.ResourcePath] {
			result = append(result, target)
			seen[target.ResourcePath] = true
		}
	}
	return result
}

// Lookup is for legacy Grafana queries without owner or cluster labels. Only
// unambiguous concrete namespace/container mappings are safe for those queries.
func Lookup(namespace, container string) (ServiceTarget, bool) {
	var found ServiceTarget
	for _, target := range ServiceTargets() {
		if target.Namespace != namespace || target.Container != container || target.InitContainer || strings.Contains(target.Namespace, "*") {
			continue
		}
		if target.ClusterRole != "" || (found.ResourcePath != "" && found.ResourcePath != target.ResourcePath) {
			return ServiceTarget{}, false
		}
		found = target
	}
	return found, found.ResourcePath != ""
}
