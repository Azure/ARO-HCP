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

package targets_test

import (
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/Azure/ARO-HCP/tooling/rightsize-requests/pkg/targets"
)

func TestServiceTargets(t *testing.T) {
	want := []targets.ServiceTarget{
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
	}
	if got := targets.ServiceTargets(); !slices.Equal(got[:len(want)], want) {
		t.Fatalf("service catalog changed: got %+v, want %+v", got, want)
	}
	for _, target := range want {
		for _, kind := range []string{"Deployment", "StatefulSet", "DaemonSet", "ReplicaSet", "Job", "CronJob"} {
			t.Run(target.Service+"/"+kind, func(t *testing.T) {
				if !targets.Editable(target.Namespace, kind, "any-workload", target.Container, false) {
					t.Fatal("supported service identity was rejected")
				}
				if targets.Editable(target.Namespace, kind, "any-workload", target.Container, true) {
					t.Fatal("init container was accepted")
				}
			})
		}
	}
}

func TestResolveRoleAndWorkload(t *testing.T) {
	for _, tc := range []struct{ cluster, role string }{
		{"int-uksouth-svc-1", "svc"}, {"prod-westus3-mgmt-12", "mgmt"},
		{"pers-usw3test-svc", "svc"}, {"ci00-j7654321-mgmt-1", "mgmt"},
		{"svc-1", "svc"}, {"mgmt-2", "mgmt"}, {"unknown", ""}, {"", ""},
		{"x-svc-1-copy", ""}, {"x-mgmt-1-extra", ""}, {"x-notsvc-1", ""},
		{"x-notmgmt-2", ""}, {"x-svc-a", ""}, {"x-mgmt", ""},
		{"x-mgmt-1-svc", "svc"}, {"x-svc-1-mgmt-2", "mgmt"},
		{"x-opstool", "opstool"}, {"x-opstool-1", "opstool"}, {"opstool", "opstool"}, {"opstool-12", "opstool"},
		{"x-notopstool", ""}, {"x-opstool-a", ""}, {"x-opstool-1-copy", ""}, {"foo", ""},
	} {
		got, ok := targets.Resolve("prometheus", "Deployment", "prometheus-kube-state-metrics", "kube-state-metrics", tc.cluster, false)
		pathRole := tc.role
		if pathRole == "opstool" {
			pathRole = "svc"
		}
		if tc.role == "" {
			if ok {
				t.Errorf("unknown cluster role %q resolved to %+v", tc.cluster, got)
			}
		} else if !ok || got.ClusterRole != tc.role || got.ResourcePath != pathRole+".prometheus.kubeStateMetrics.resources" {
			t.Errorf("cluster %s resolved to %+v, %v", tc.cluster, got, ok)
		}
		got, ok = targets.Resolve("arobit", "DaemonSet", "arobit-forwarder", "fluentbit", tc.cluster, false)
		if tc.role == "svc" || tc.role == "mgmt" {
			if !ok || got.ClusterRole != tc.role || got.Service != tc.role+".arobit.forwarder" || got.ResourcePath != tc.role+".arobit.forwarder.resources" {
				t.Errorf("arobit cluster %s resolved to %+v, %v", tc.cluster, got, ok)
			}
		} else if ok {
			t.Errorf("unsupported arobit cluster %q resolved to %+v", tc.cluster, got)
		}
	}
	for workload, service := range map[string]string{
		"frontend-certificate-refresher": "frontend", "admin-api-certificate-refresher": "adminApi", "sessiongate-certificate-refresher": "sessiongate",
	} {
		got, ok := targets.Resolve("aks-istio-ingress", "Deployment", workload, "init-container-msg-container-init", "int-uksouth-svc-1", false)
		if !ok || got.ResourcePath != service+".certificateRefresher.resources" {
			t.Fatalf("wrong refresher: %+v, %v", got, ok)
		}
	}
	for container, path := range map[string]string{"provider-azure-installer": "secretSyncController.k8s.resources", "manager": "secretSyncController.k8s.controllerResources"} {
		got, ok := targets.Resolve("secret-sync-controller", "Deployment", "secrets-store-sync-controller-manager", container, "int-uksouth-mgmt-1", false)
		if !ok || got.ResourcePath != path {
			t.Fatalf("wrong secret sync target: %+v, %v", got, ok)
		}
	}
}

func TestResolveExplicitCatalog(t *testing.T) {
	for _, target := range targets.ServiceTargets() {
		if target.Workload == "" {
			continue
		}
		ns := strings.ReplaceAll(target.Namespace, "*", "test-hcp")
		cluster := "int-uksouth-" + target.ClusterRole + "-1"
		got, ok := targets.Resolve(ns, target.Kind, target.Workload, target.Container, cluster, target.InitContainer)
		if !ok || got != target {
			t.Fatalf("catalog identity %+v resolved to %+v, %v", target, got, ok)
		}
		if _, ok := targets.Resolve(ns, target.Kind, target.Workload, target.Container, cluster, !target.InitContainer); ok {
			t.Fatalf("wrong init type accepted for %+v", target)
		}
		if _, ok := targets.Resolve(ns, "Unknown", target.Workload, target.Container, cluster, target.InitContainer); ok {
			t.Fatalf("unknown kind accepted for %+v", target)
		}
		if _, ok := targets.Resolve(ns, target.Kind, "other-"+target.Workload, target.Container, cluster, target.InitContainer); ok {
			t.Fatalf("wrong workload accepted for %+v", target)
		}
		if _, ok := targets.Resolve("unrelated-"+ns, target.Kind, target.Workload, target.Container, cluster, target.InitContainer); ok {
			t.Fatalf("wrong namespace accepted for %+v", target)
		}
		if target.ClusterRole != "" {
			for _, role := range []string{"svc", "mgmt", "opstool", "unknown"} {
				if role == target.ClusterRole {
					continue
				}
				if got, ok := targets.Resolve(ns, target.Kind, target.Workload, target.Container, "int-uksouth-"+role+"-1", target.InitContainer); ok && got.ClusterRole != role {
					t.Fatalf("wrong role %s accepted for %+v", role, target)
				}
			}
		}
	}
	for _, workload := range []string{"prom-agent-prometheus", "prom-agent-prometheus-shard-1", "prom-agent-prometheus-shard-12"} {
		for _, container := range []string{"prometheus", "config-reloader", "init-config-reloader"} {
			if !targets.EditableInCluster("prometheus", "StatefulSet", workload, container, "int-uksouth-mgmt-1", container == "init-config-reloader") {
				t.Errorf("valid shard %s/%s rejected", workload, container)
			}
		}
	}
	for _, workload := range []string{"prom-agent-prometheus-0", "prom-agent-prometheus-shard-1-0", "prom-agent-prometheus-other", "prom-agent-prometheus-shard-x"} {
		if targets.EditableInCluster("prometheus", "StatefulSet", workload, "prometheus", "int-uksouth-mgmt-1", false) {
			t.Errorf("invalid shard %s accepted", workload)
		}
	}
	if targets.EditableInCluster("hypershift", "Deployment", "operator", "init", "int-uksouth-mgmt-1", true) {
		t.Fatal("unconfigured HSO init accepted")
	}
}

func TestResolveChartPaths(t *testing.T) {
	for _, tc := range []struct {
		namespace, kind, workload, container, role, path string
		init                                             bool
	}{
		{"prometheus", "Deployment", "arohcp-monitor-kube-state-metrics", "kube-state-metrics", "svc", "svc.prometheus.kubeStateMetrics.resources", false},
		{"prometheus", "Deployment", "prometheus-kube-state-metrics", "kube-state-metrics", "mgmt", "mgmt.prometheus.kubeStateMetrics.resources", false},
		{"prometheus", "StatefulSet", "prom-agent-prometheus-shard-2", "init-config-reloader", "mgmt", "mgmt.prometheus.prometheusConfigReloader.resources", true},
		{"velero", "Deployment", "velero", "oadp-oadp-velero-plugin-for-microsoft-azure-rhel9", "mgmt", "velero.azurePlugin.resources", true},
		{"velero", "Deployment", "velero", "oadp-oadp-hypershift-velero-plugin-rhel9", "mgmt", "velero.hypershiftPlugin.resources", true},
		{"velero", "Job", "velero-install", "generate-manifest", "mgmt", "velero.installer.generate.resources", true},
		{"velero", "Job", "velero-install", "apply-manifest", "mgmt", "velero.installer.apply.resources", false},
		{"aks-istio-ingress", "Deployment", "ops-ingress-gateway-istio", "istio-proxy", "svc", "svc.opsIngress.gateway.resources", false},
		{"aks-istio-ingress", "Deployment", "aks-istio-ingressgateway-external", "istio-proxy", "svc", "", false},
		{"prometheus", "Deployment", "other-kube-state-metrics", "kube-state-metrics", "mgmt", "", false},
	} {
		t.Run(tc.workload+"/"+tc.container, func(t *testing.T) {
			got, ok := targets.Resolve(tc.namespace, tc.kind, tc.workload, tc.container, "int-uksouth-"+tc.role+"-1", tc.init)
			if ok != (tc.path != "") || got.ResourcePath != tc.path {
				t.Fatalf("resolved %+v, %v; want %s", got, ok, tc.path)
			}
		})
	}
}

func TestCandidateTargets(t *testing.T) {
	for _, tc := range []struct {
		cluster, workload string
		count             int
		role              string
	}{
		{"int-uksouth-svc-1", "prom-agent-prometheus", 1, "svc"},
		{"int-uksouth-mgmt-1", "prom-agent-prometheus-shard-2", 1, "mgmt"},
		{"dev-westus3-opstool", "prom-agent-prometheus", 1, "opstool"},
		{"dev-westus3-opstool-12", "prom-agent-prometheus-shard-2", 1, "opstool"},
		{"unknown", "prom-agent-prometheus", 3, ""}, {"", "", 3, ""},
		{"foo", "prom-agent-prometheus", 3, ""},
		{"int-uksouth-svc-1", "unrelated", 0, ""},
	} {
		got := targets.CandidateTargets("prometheus", tc.workload, "prometheus", tc.cluster, false)
		if len(got) != tc.count {
			t.Fatalf("%+v: candidates %+v", tc, got)
		}
		for _, target := range got {
			if tc.role != "" && target.ClusterRole != tc.role {
				t.Fatalf("wrong role candidate %+v", target)
			}
		}
	}
	got := targets.CandidateTargets("aks-istio-ingress", "frontend-certificate-refresher", "init-container-msg-container-init", "int-uksouth-svc-1", false)
	if len(got) != 1 || got[0].ResourcePath != "frontend.certificateRefresher.resources" {
		t.Fatalf("wrong workload candidates %+v", got)
	}
	got = targets.CandidateTargets("multicluster-engine", "", "finalize", "int-uksouth-mgmt-1", false)
	if len(got) != 1 || got[0].ResourcePath != "acm.resources.finalize" {
		t.Fatalf("shared path not deduplicated: %+v", got)
	}
	if got := targets.CandidateTargets("maestro", "maestro-agent", "init", "int-uksouth-svc-1", true); len(got) != 0 {
		t.Fatalf("wrong role candidates %+v", got)
	}
	for _, cluster := range []string{"dev-westus3-opstool", "dev-westus3-opstool-12"} {
		got := targets.CandidateTargets("prometheus", "arohcp-monitor-kube-state-metrics", "kube-state-metrics", cluster, false)
		if len(got) != 1 || got[0].ClusterRole != "opstool" || got[0].ResourcePath != "svc.prometheus.kubeStateMetrics.resources" {
			t.Fatalf("opstool auxiliary candidates: %+v", got)
		}
		if got := targets.CandidateTargets("velero", "velero-install", "apply-manifest", cluster, false); len(got) != 0 {
			t.Fatalf("known opstool role blocked mgmt-only policy: %+v", got)
		}
		main, ok := targets.Resolve("prometheus", "StatefulSet", "prom-agent-prometheus", "prometheus", cluster, false)
		if !ok || main.ResourcePath != "svc.prometheus.opstoolResources" {
			t.Fatalf("opstool agent resolved to %+v, %v", main, ok)
		}
	}
	if got := targets.CandidateTargets("prometheus", "", "init-config-reloader", "foo", true); len(got) != 2 {
		t.Fatalf("unknown role must block both distinct auxiliary paths, deduplicating svc/opstool: %+v", got)
	}
}

func TestArobitCandidateTargets(t *testing.T) {
	for _, tc := range []struct {
		cluster string
		roles   []string
	}{
		{"test-svc-1", []string{"svc"}},
		{"test-mgmt-1", []string{"mgmt"}},
		{"test-opstool-1", nil},
		{"unknown", []string{"svc", "mgmt"}},
		{"", []string{"svc", "mgmt"}},
	} {
		for _, workload := range []string{"arobit-forwarder", "unknown", ""} {
			got := targets.CandidateTargets("arobit", workload, "fluentbit", tc.cluster, false)
			if len(got) != len(tc.roles) {
				t.Fatalf("cluster %q workload %q: candidates %+v, want roles %v", tc.cluster, workload, got, tc.roles)
			}
			for i, role := range tc.roles {
				if got[i].ClusterRole != role || got[i].ResourcePath != role+".arobit.forwarder.resources" {
					t.Fatalf("cluster %q workload %q: wrong candidate %+v", tc.cluster, workload, got[i])
				}
			}
			if got := targets.CandidateTargets("arobit", workload, "fluentbit", tc.cluster, true); len(got) != 0 {
				t.Fatalf("init container blocked regular arobit policy: %+v", got)
			}
		}
	}
}

func TestLegacyLookupRejectsRoleTargets(t *testing.T) {
	for _, pair := range [][2]string{{"arobit", "fluentbit"}, {"prometheus", "prometheus"}, {"aks-istio-ingress", "init-container-msg-container-init"}, {"ocm-arohcp*", "kube-state-metrics"}, {"maestro", "init"}} {
		if got, ok := targets.Lookup(pair[0], pair[1]); ok {
			t.Fatalf("legacy lookup guessed %+v", got)
		}
	}
}

func TestMinimalTargets(t *testing.T) {
	want := []targets.MinimalTarget{
		{"Deployment", "kube-apiserver", "kube-apiserver"},
		{"Deployment", "openshift-controller-manager", "openshift-controller-manager"},
		{"Deployment", "cluster-policy-controller", "cluster-policy-controller"},
		{"Deployment", "kube-controller-manager", "kube-controller-manager"},
		{"Deployment", "openshift-apiserver", "openshift-apiserver"},
		{"StatefulSet", "etcd", "etcd"},
		{"Deployment", "ovnkube-control-plane", "ovnkube-control-plane"},
	}
	if got := targets.MinimalTargets(); !slices.Equal(got, want) {
		t.Fatalf("minimal catalog changed: got %+v, want %+v", got, want)
	}
	for _, target := range want {
		for _, namespace := range []string{"ocm-arohcpci01-hcp", "ocm-arohcpint-hcp", "ocm-arohcpstg-hcp", "ocm-arohcpprod-hcp"} {
			for _, kind := range []string{"Deployment", "StatefulSet", "DaemonSet", "ReplicaSet", "Job", "CronJob", "Pod", "deployment", "Unknown", ""} {
				t.Run(target.Workload+"/"+namespace+"/"+kind, func(t *testing.T) {
					if got := targets.Editable(namespace, kind, target.Workload, target.Container, false); got != (kind == target.Kind) {
						t.Fatalf("Editable = %t, want kind %s only", got, target.Kind)
					}
				})
			}
			if targets.Editable(namespace, target.Kind, target.Workload, target.Container, true) ||
				targets.Editable(namespace, target.Kind, target.Workload+"-123", target.Container, false) ||
				targets.Editable(namespace, target.Kind, target.Workload, "sidecar", false) {
				t.Fatalf("accepted init container, workload alias or sidecar for %+v", target)
			}
		}
	}
}

func TestEditableRejectsUnsupported(t *testing.T) {
	for _, tc := range []struct {
		name, namespace, kind, workload, container string
	}{
		{"AKS coredns", "kube-system", "Deployment", "coredns", "coredns"},
		{"AKS agent", "kube-system", "DaemonSet", "ama-logs", "ama-logs"},
		{"service sidecar", "aro-hcp", "Deployment", "aro-hcp-backend", "sidecar"},
		{"controller", "aro-hcp", "Deployment", "aro-hcp-controller", "aro-hcp-controller"},
		{"hypershift operator", "hypershift", "Deployment", "operator", "operator"},
		{"HCP operator", "ocm-arohcpint-hcp", "Deployment", "hcp-operator", "operator"},
		{"scheduler", "ocm-arohcpint-hcp", "Deployment", "kube-scheduler", "kube-scheduler"},
		{"HCP service container", "ocm-arohcpint-hcp", "Deployment", "aro-hcp-backend", "aro-hcp-backend"},
		{"non-HCP namespace", "ocm-other-hcp", "StatefulSet", "etcd", "etcd"},
		{"namespace substring", "xocm-arohcpint-hcp", "StatefulSet", "etcd", "etcd"},
		{"namespace case", "OCM-arohcpint-hcp", "StatefulSet", "etcd", "etcd"},
		{"namespace suffix", "aro-hcp-extra", "Deployment", "backend", "aro-hcp-backend"},
		{"container suffix", "aro-hcp", "Deployment", "backend", "aro-hcp-backend-extra"},
		{"namespace case service", "ARO-HCP", "Deployment", "backend", "aro-hcp-backend"},
		{"container case", "aro-hcp", "Deployment", "backend", "ARO-HCP-BACKEND"},
		{"kind case", "aro-hcp", "deployment", "backend", "aro-hcp-backend"},
		{"pod", "aro-hcp", "Pod", "backend", "aro-hcp-backend"},
		{"unknown kind", "aro-hcp", "CustomController", "backend", "aro-hcp-backend"},
		{"missing kind", "aro-hcp", "", "backend", "aro-hcp-backend"},
		{"missing workload", "aro-hcp", "Deployment", "", "aro-hcp-backend"},
		{"blank workload", "aro-hcp", "Deployment", " \t", "aro-hcp-backend"},
		{"unknown workload", "aro-hcp", "Deployment", "unknown", "aro-hcp-backend"},
		{"unknown workload case", "aro-hcp", "Deployment", "UNKNOWN", "aro-hcp-backend"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if targets.Editable(tc.namespace, tc.kind, tc.workload, tc.container, false) {
				t.Fatal("unsupported identity was accepted")
			}
		})
	}
}

func TestCatalogCopies(t *testing.T) {
	services, minimal := targets.ServiceTargets(), targets.MinimalTargets()
	wantServices, wantMinimal := slices.Clone(services), slices.Clone(minimal)
	clear(services)
	clear(minimal)
	if !slices.Equal(targets.ServiceTargets(), wantServices) || !slices.Equal(targets.MinimalTargets(), wantMinimal) {
		t.Fatal("mutating a returned slice changed the catalog")
	}
	if !targets.Editable("aro-hcp", "Deployment", "backend", "aro-hcp-backend", false) ||
		!targets.Editable("ocm-arohcpint-hcp", "StatefulSet", "etcd", "etcd", false) {
		t.Fatal("mutating a returned slice changed editability")
	}
}

func TestAdditionalMinimalTargetsParity(t *testing.T) {
	data, err := os.ReadFile("../../../../hypershiftoperator/deploy/regular-resource-targets.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var catalog map[string][]string
	if err := yaml.Unmarshal(data, &catalog); err != nil {
		t.Fatal(err)
	}
	want := map[targets.MinimalTarget]bool{}
	for workload, containers := range catalog {
		kind := "Deployment"
		if workload == "etcd" {
			kind = "StatefulSet"
		}
		for _, container := range containers {
			target := targets.MinimalTarget{Kind: kind, Workload: workload, Container: container}
			if want[target] {
				t.Fatalf("duplicate YAML target: %+v", target)
			}
			want[target] = true
		}
	}
	for _, target := range targets.MinimalTargets() {
		delete(want, target)
	}
	got := map[targets.MinimalTarget]bool{}
	for _, target := range targets.AdditionalMinimalTargets() {
		if got[target] {
			t.Fatalf("duplicate Go target: %+v", target)
		}
		err := targets.ValidateResourceRequestOverride(target.Workload, target.Container)
		if (err != nil) != (target.Workload == "openshift-route-controller-manager" || target.Workload == "csi-snapshot-controller-operator") {
			t.Fatalf("unexpected annotation validity for %+v: %v", target, err)
		}
		got[target] = true
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("additional Go catalog differs from audited YAML minus original seven: got %+v, want %+v", got, want)
	}
	copy := targets.AdditionalMinimalTargets()
	original := slices.Clone(copy)
	clear(copy)
	if !slices.Equal(targets.AdditionalMinimalTargets(), original) {
		t.Fatal("mutating returned slice changed additional catalog")
	}
}

func TestValidateResourceRequestOverride(t *testing.T) {
	for _, tc := range []struct {
		name, workload, container string
		valid                     bool
	}{
		{"63 characters", strings.Repeat("a", 31), strings.Repeat("b", 31), true},
		{"64 characters", strings.Repeat("a", 32), strings.Repeat("b", 31), false},
		{"route controller", "openshift-route-controller-manager", "openshift-route-controller-manager", false},
		{"invalid character", "workload", "container!", false},
		{"extra slash", "work/load", "container", false},
		{"empty workload", "", "container", false},
		{"empty container", "workload", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := targets.ValidateResourceRequestOverride(tc.workload, tc.container)
			if (err == nil) != tc.valid {
				t.Fatalf("annotation validity = %v, want valid=%t", err, tc.valid)
			}
			if err != nil && !strings.Contains(err.Error(), "resource-request-override.hypershift.openshift.io/") {
				t.Fatalf("missing full annotation key: %v", err)
			}
		})
	}
}

func TestAdditionalMinimalTargetCapabilities(t *testing.T) {
	for _, target := range targets.AdditionalMinimalTargets() {
		t.Run(target.Workload+"/"+target.Container, func(t *testing.T) {
			for _, namespace := range []string{"ocm-arohcpci01-hcp", "ocm-arohcpint-hcp", "ocm-arohcpstg-hcp", "ocm-arohcpprod-hcp", "ocm-arohcp", "ocm-other-hcp", "xocm-arohcpint-hcp", "hypershift"} {
				for _, cluster := range []string{"int-eastus-mgmt-1", "int-eastus-svc-1", "opstool", "", "unknown", "x-mgmt-1-copy"} {
					for _, kind := range []string{"Deployment", "StatefulSet", "DaemonSet", "Pod", "Unknown", ""} {
						want := strings.HasPrefix(namespace, "ocm-arohcp") && namespace != "ocm-arohcp" && cluster == "int-eastus-mgmt-1" && kind == target.Kind
						if got := targets.EditableInCluster(namespace, kind, target.Workload, target.Container, cluster, false); got != want {
							t.Fatalf("EditableInCluster(%s, %s, %s) = %v, want %v", namespace, kind, cluster, got, want)
						}
						if got := targets.RequiresConfiguredBaseline(namespace, kind, target.Workload, target.Container, cluster, false); got != want {
							t.Fatalf("baseline predicate = %v, want %v", got, want)
						}
					}
				}
			}
			if targets.EditableInCluster("ocm-arohcpint-hcp", target.Kind, target.Workload, target.Container, "mgmt-1", true) ||
				targets.EditableInCluster("ocm-arohcpint-hcp", target.Kind, "other-"+target.Workload, target.Container, "mgmt-1", false) ||
				targets.EditableInCluster("ocm-arohcpint-hcp", target.Kind, target.Workload, target.Container+"-other", "mgmt-1", false) {
				t.Fatal("accepted init container or inexact workload/container")
			}
			if _, ok := targets.Resolve("ocm-arohcpint-hcp", target.Kind, target.Workload, target.Container, "mgmt-1", false); ok {
				t.Fatal("additional capability must not resolve as a service config path")
			}
		})
	}
	guest, ok := targets.Resolve("ocm-arohcpint-hcp", "Deployment", "kube-state-metrics-hcp", "kube-state-metrics", "mgmt-1", false)
	if !ok || guest.ResourcePath != "mgmtAgent.guestKSMResources" || targets.RequiresConfiguredBaseline("ocm-arohcpint-hcp", "Deployment", "kube-state-metrics-hcp", "kube-state-metrics", "mgmt-1", false) {
		t.Fatal("guest KSM must retain its distinct service configuration")
	}
}
