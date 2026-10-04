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

package rightsize

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"reflect"
	"slices"
	"strings"
	"testing"
	"text/template"

	"gopkg.in/yaml.v3"

	"github.com/Azure/ARO-HCP/tooling/rightsize-requests/internal/editor"
	"github.com/Azure/ARO-HCP/tooling/rightsize-requests/pkg/targets"
)

func TestServiceTargetsConfigPaths(t *testing.T) {
	config, err := editor.New("../../../../config/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range targets.ServiceTargets() {
		kind, workload := target.Kind, target.Workload
		if kind == "" {
			kind = "Deployment"
		}
		if workload == "" {
			workload = "workload"
		}
		mapped, ok := Resolve(strings.ReplaceAll(target.Namespace, "*", "test-hcp"), kind, workload, target.Container, "test-"+target.ClusterRole+"-1", target.InitContainer)
		if !ok || mapped.ResourcePath != target.ResourcePath {
			t.Fatalf("config target source path differs from catalog: %+v, %+v", target, mapped)
		}
		for _, resource := range []string{"cpu", "memory"} {
			path := "defaults." + target.ResourcePath + ".requests." + resource
			if mapped.requestPath("defaults", resource) != path || mapped.limitPath("defaults", resource) != "defaults."+target.ResourcePath+".limits."+resource {
				t.Fatalf("incorrect request/limit source paths for %+v", target)
			}
			if value, _, err := config.Get(path); err != nil || value == "" {
				t.Errorf("catalog target %s/%s has no configured request at %s: %q, %v", target.Workload, target.Container, path, value, err)
			}
		}
	}
}

func TestArobitValuesCatalogParity(t *testing.T) {
	data, err := os.ReadFile("../../../../config/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var config struct{ Defaults yaml.Node }
	if err := yaml.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	defaults := map[string]any{}
	for i := 0; i < len(config.Defaults.Content); i += 2 {
		key := config.Defaults.Content[i].Value
		if !slices.Contains([]string{"svc", "mgmt", "arobit", "geneva", "aksCommandRuntime", "kusto", "serviceKeyVault", "mgmtKeyVault"}, key) {
			continue
		}
		var value any
		if err := config.Defaults.Content[i+1].Decode(&value); err != nil {
			t.Fatal(err)
		}
		defaults[key] = value
	}
	want := map[string]map[string]any{}
	for i, role := range []string{"svc", "mgmt"} {
		target, ok := Resolve("arobit", "DaemonSet", "arobit-forwarder", "fluentbit", "test-"+role+"-1", false)
		if !ok {
			t.Fatalf("missing arobit mapping for %s", role)
		}
		resources := defaults
		for _, part := range strings.Split(target.ResourcePath, ".") {
			resources, ok = resources[part].(map[string]any)
			if !ok {
				t.Fatalf("missing config mapping %s at %s", target.ResourcePath, part)
			}
		}
		// Distinct role values catch templates accidentally reading the other root.
		resources["requests"] = map[string]any{"cpu": fmt.Sprintf("%dm", 123+i*100), "memory": fmt.Sprintf("%dMi", 456+i*100)}
		resources["limits"] = map[string]any{"memory": fmt.Sprintf("%dMi", 789+i*100)}
		want[role] = resources
	}
	for _, role := range []string{"svc", "mgmt"} {
		t.Run(role, func(t *testing.T) {
			tmpl, err := template.ParseFiles("../../../../observability/arobit/values-" + role + ".yaml")
			if err != nil {
				t.Fatal(err)
			}
			var rendered bytes.Buffer
			if err := tmpl.Execute(&rendered, defaults); err != nil {
				t.Fatal(err)
			}
			var values struct {
				Forwarder struct {
					Fluentbit struct{ Resources map[string]any }
				}
			}
			if err := yaml.Unmarshal(rendered.Bytes(), &values); err != nil {
				t.Fatal(err)
			}
			if got := values.Forwarder.Fluentbit.Resources; !reflect.DeepEqual(got, want[role]) {
				t.Fatalf("rendered resources = %v, want %v", got, want[role])
			}
		})
	}
}

func TestPrometheusValuesCatalogParity(t *testing.T) {
	data, err := os.ReadFile("../../../../config/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var config struct{ Defaults yaml.Node }
	if err := yaml.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	defaults := map[string]any{"azureRegionAvailabilityZoneCount": 1, "region": "westus3", "environmentName": "test"}
	for i := 0; i < len(config.Defaults.Content); i += 2 {
		key := config.Defaults.Content[i].Value
		if !slices.Contains([]string{"svc", "mgmt", "acr", "acrDNSSuffix", "clustersService"}, key) {
			continue
		}
		var value any
		if err := config.Defaults.Content[i+1].Decode(&value); err != nil {
			t.Fatal(err)
		}
		defaults[key] = value
	}
	lookup := func(root map[string]any, path string) map[string]any {
		t.Helper()
		for _, part := range strings.Split(path, ".") {
			next, ok := root[part].(map[string]any)
			if !ok {
				t.Fatalf("missing mapping %s at %s", path, part)
			}
			root = next
		}
		return root
	}
	// Give every declared field a distinct value, including limits, so wrong
	// source paths cannot pass just because defaults use the same sentinel.
	seen := map[string]bool{}
	for _, target := range targets.ServiceTargets() {
		if target.Namespace != "prometheus" || seen[target.ResourcePath] {
			continue
		}
		seen[target.ResourcePath] = true
		resources := lookup(defaults, target.ResourcePath)
		for _, kind := range []string{"requests", "limits"} {
			fields, ok := resources[kind].(map[string]any)
			if !ok {
				continue // Request-only policies do not gain limits.
			}
			for _, resource := range []string{"cpu", "memory"} {
				if _, ok := fields[resource]; !ok {
					continue
				}
				suffix := "m"
				if resource == "memory" {
					suffix = "Mi"
				}
				fields[resource] = fmt.Sprintf("%d%s", len(seen)*10+len(kind), suffix)
			}
		}
	}
	// Opstool image/cluster settings are supplied outside the shared defaults.
	defaults["opstool"] = defaults["svc"]
	for _, role := range []string{"svc", "mgmt", "opstool"} {
		t.Run(role, func(t *testing.T) {
			valuesPath := "../../../../observability/prometheus/values-" + role + ".yaml"
			tmpl, err := template.ParseFiles(valuesPath)
			if err != nil {
				t.Fatal(err)
			}
			var rendered bytes.Buffer
			if err := tmpl.Execute(&rendered, defaults); err != nil {
				t.Fatal(err)
			}
			var values map[string]any
			if err := yaml.Unmarshal(rendered.Bytes(), &values); err != nil {
				t.Fatalf("decode %s: %v", valuesPath, err)
			}
			for _, target := range targets.ServiceTargets() {
				if target.Namespace != "prometheus" || target.ClusterRole != role {
					continue
				}
				path := ""
				switch target.Container {
				case "kube-state-metrics":
					path = "kube-prometheus-stack.kube-state-metrics.resources"
				case "kube-prometheus-stack":
					path = "kube-prometheus-stack.prometheusOperator.resources"
				case "config-reloader", "init-config-reloader":
					path = "kube-prometheus-stack.prometheusOperator.prometheusConfigReloader.resources"
				case "prometheus":
					path = "prometheusSpec.resources"
				default:
					t.Fatalf("untested Prometheus target %+v", target)
				}
				got, want := lookup(values, path), lookup(defaults, target.ResourcePath)
				if !reflect.DeepEqual(got, want) {
					t.Errorf("%s/%s: rendered %s = %v; catalog source %s = %v", target.Workload, target.Container, path, got, target.ResourcePath, want)
				}
			}
			t.Run("helm", func(t *testing.T) {
				helm, err := exec.LookPath("helm")
				if err != nil {
					t.Skip("helm is required for chart identity tests")
				}
				cmd := exec.Command(helm, "template", "arohcp-monitor", "../../../../observability/prometheus/deploy", "--namespace", "prometheus", "--values", "-", "--set", "kube-prometheus-stack.crds.enabled=false")
				cmd.Stdin = bytes.NewReader(rendered.Bytes())
				output, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("helm template: %v\n%s", err, output)
				}
				decoder := yaml.NewDecoder(bytes.NewReader(output))
				found := map[string]bool{}
				for {
					var obj struct {
						Kind     string
						Metadata struct{ Name, Namespace string }
						Spec     struct {
							Resources map[string]any
							Template  struct {
								Spec struct {
									Containers []struct {
										Name      string
										Args      []string
										Resources map[string]any
									}
								}
							}
						}
					}
					if err := decoder.Decode(&obj); err == io.EOF {
						break
					} else if err != nil {
						t.Fatal(err)
					}
					check := func(kind, workload, container string, init bool, resources map[string]any) {
						t.Helper()
						target, ok := Resolve(obj.Metadata.Namespace, kind, workload, container, "test-"+role+"-1", init)
						if !ok || !reflect.DeepEqual(resources, lookup(defaults, target.ResourcePath)) {
							t.Fatalf("rendered %s/%s does not match catalog %+v: %v", workload, container, target, resources)
						}
						found[container] = true
					}
					if obj.Kind == "PrometheusAgent" && obj.Metadata.Name == "prometheus" {
						check("StatefulSet", "prom-agent-"+obj.Metadata.Name, "prometheus", false, obj.Spec.Resources)
					}
					if obj.Kind != "Deployment" {
						continue
					}
					for _, c := range obj.Spec.Template.Spec.Containers {
						if c.Name != "kube-state-metrics" && c.Name != "kube-prometheus-stack" {
							continue
						}
						check(obj.Kind, obj.Metadata.Name, c.Name, false, c.Resources)
						if c.Name == "kube-prometheus-stack" {
							resources := lookup(values, "kube-prometheus-stack.prometheusOperator.prometheusConfigReloader.resources")
							for _, kind := range []string{"requests", "limits"} {
								for resource, value := range resources[kind].(map[string]any) {
									flag := fmt.Sprintf("--config-reloader-%s-%s=%s", resource, strings.TrimSuffix(kind, "s"), value)
									if !slices.Contains(c.Args, flag) {
										t.Errorf("missing reloader policy flag %s", flag)
									}
								}
							}
							check("StatefulSet", "prom-agent-prometheus", "config-reloader", false, resources)
							check("StatefulSet", "prom-agent-prometheus", "init-config-reloader", true, resources)
						}
					}
				}
				if len(found) != 5 {
					t.Fatalf("missing rendered Prometheus targets: %v", found)
				}
			})
		})
	}
}

func TestRenderedServiceTargets(t *testing.T) {
	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm is required for chart identity tests")
	}
	for _, tc := range []struct {
		name, chart, namespace, role, values string
		want                                 map[string]string
	}{
		{
			name: "arohcp-monitor", chart: "observability/prometheus/deploy/charts/kube-prometheus-stack-70.4.1.tgz", namespace: "prometheus", role: "svc",
			values: `{"fullnameOverride":"prometheus","crds":{"enabled":false},"grafana":{"enabled":false},"prometheusOperator":{"resources":{"requests":{"cpu":"123m"}}},"kube-state-metrics":{"resources":{"requests":{"cpu":"234m"}}}}`,
			want: map[string]string{
				"Deployment/arohcp-monitor-kube-state-metrics/kube-state-metrics": "svc.prometheus.kubeStateMetrics.resources=234m",
				"Deployment/prometheus-operator/kube-prometheus-stack":            "svc.prometheus.prometheusOperator.resources=123m",
			},
		},
		{
			name: "prometheus", chart: "observability/prometheus/deploy/charts/kube-prometheus-stack-70.4.1.tgz", namespace: "prometheus", role: "mgmt",
			values: `{"fullnameOverride":"prometheus","crds":{"enabled":false},"grafana":{"enabled":false},"kube-state-metrics":{"resources":{"requests":{"cpu":"345m"}}}}`,
			want:   map[string]string{"Deployment/prometheus-kube-state-metrics/kube-state-metrics": "mgmt.prometheus.kubeStateMetrics.resources=345m"},
		},
		{
			name: "velero", chart: "velero/deploy", namespace: "velero", role: "mgmt",
			values: `veleroServer:
  resources: &resources
    requests: {cpu: 100m, memory: 128Mi}
    limits: {cpu: NONE, memory: 256Mi}
nodeAgent:
  resources: *resources
azurePlugin:
  resources:
    <<: *resources
    requests: {cpu: 11m, memory: 32Mi}
hypershiftPlugin:
  resources:
    <<: *resources
    requests: {cpu: 22m, memory: 32Mi}
installer:
  generate:
    resources:
      <<: *resources
      requests: {cpu: 33m, memory: 64Mi}
  apply:
    resources:
      <<: *resources
      requests: {cpu: 44m, memory: 64Mi}
`,
			want: map[string]string{
				"Deployment/velero/init/oadp-oadp-velero-plugin-for-microsoft-azure-rhel9": "velero.azurePlugin.resources=11m",
				"Deployment/velero/init/oadp-oadp-hypershift-velero-plugin-rhel9":          "velero.hypershiftPlugin.resources=22m",
				"Job/velero-install/init/generate-manifest":                                "velero.installer.generate.resources=33m",
				"Job/velero-install/apply-manifest":                                        "velero.installer.apply.resources=44m",
			},
		},
		{
			name: "istio", chart: "istio/deploy", namespace: "aks-istio-ingress", role: "svc",
			values: `{"opsIngress":{"resources":{"requests":{"cpu":"55m"}},"adminApi":{},"sessiongate":{}}}`,
			want:   map[string]string{"Deployment/ops-ingress-gateway-istio/istio-proxy": "svc.opsIngress.gateway.resources=55m"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(helm, "template", tc.name, "../../../../"+tc.chart, "--namespace", tc.namespace, "--values", "-")
			cmd.Stdin = strings.NewReader(tc.values)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("helm template: %v\n%s", err, output)
			}
			type container struct {
				Name      string
				Resources struct{ Requests map[string]string }
			}
			type object struct {
				Kind     string
				Metadata struct{ Name, Namespace string }
				Data     map[string]string
				Spec     struct {
					Template struct {
						Spec struct {
							Containers     []container
							InitContainers []container `yaml:"initContainers"`
						}
					}
				}
			}
			var objects []object
			decode := func(data []byte) {
				t.Helper()
				decoder := yaml.NewDecoder(bytes.NewReader(data))
				for {
					var obj object
					if err := decoder.Decode(&obj); err == io.EOF {
						break
					} else if err != nil {
						t.Fatal(err)
					}
					objects = append(objects, obj)
				}
			}
			decode(output)
			gateway := false
			for _, obj := range objects {
				if obj.Kind == "Gateway" && obj.Metadata.Name == "ops-ingress-gateway" {
					gateway = true
				}
				if obj.Kind != "ConfigMap" {
					continue
				}
				if patch := obj.Data["scheduling-patch.yaml"]; patch != "" {
					decode([]byte(patch))
				}
				if patch := obj.Data["deployment"]; obj.Metadata.Name == "ops-ingress-gateway-options" && patch != "" {
					// Istio generates the Deployment name from the Gateway name.
					decode([]byte("kind: Deployment\nmetadata:\n  name: ops-ingress-gateway-istio\n  namespace: aks-istio-ingress\n" + patch))
				}
			}
			if tc.name == "istio" && !gateway {
				t.Fatal("ops ingress Gateway missing")
			}
			seen := map[string]bool{}
			for _, obj := range objects {
				for _, init := range []bool{false, true} {
					containers := obj.Spec.Template.Spec.Containers
					prefix := obj.Kind + "/" + obj.Metadata.Name + "/"
					if init {
						containers = obj.Spec.Template.Spec.InitContainers
						prefix += "init/"
					}
					for _, c := range containers {
						key := prefix + c.Name
						want, exists := tc.want[key]
						if !exists {
							continue
						}
						ns := obj.Metadata.Namespace
						if ns == "" {
							ns = tc.namespace
						}
						target, ok := Resolve(ns, obj.Kind, obj.Metadata.Name, c.Name, "int-uksouth-"+tc.role+"-1", init)
						if got := target.ResourcePath + "=" + c.Resources.Requests["cpu"]; !ok || got != want || seen[key] {
							t.Errorf("rendered %s resolved to %s, %v (duplicate=%v); want %s", key, got, ok, seen[key], want)
						}
						seen[key] = true
					}
				}
			}
			for key := range tc.want {
				if !seen[key] {
					t.Errorf("expected rendered container %s missing", key)
				}
			}
		})
	}
}

func TestLookupCatalog(t *testing.T) {
	for _, entry := range targets.ServiceTargets() {
		got, ok := Lookup(entry.Namespace, entry.Container)
		if entry.ClusterRole != "" || entry.InitContainer {
			if ok {
				t.Fatalf("legacy lookup guessed role/init target %+v", got)
			}
			continue
		}
		want := Target{Service: entry.Service, ResourcePath: entry.ResourcePath}
		if !ok || got != want {
			t.Fatalf("Lookup(%q, %q) = %+v, %t; want %+v", entry.Namespace, entry.Container, got, ok, want)
		}
		if got.requestPath("clouds.dev.defaults", "cpu") != "clouds.dev.defaults."+entry.ResourcePath+".requests.cpu" ||
			got.limitPath("defaults", "memory") != "defaults."+entry.ResourcePath+".limits.memory" {
			t.Fatalf("incorrect scalar paths for %+v", got)
		}
		for _, pair := range [][2]string{{entry.Namespace + "-other", entry.Container}, {entry.Namespace, entry.Container + "-sidecar"}} {
			if got, ok := Lookup(pair[0], pair[1]); ok || got != (Target{}) {
				t.Fatalf("unknown pair %v mapped to %+v", pair, got)
			}
		}
	}
}

func TestNamespacesCatalog(t *testing.T) {
	want := []string{"acrpull", "aro-hcp", "aro-hcp-admin-api", "aro-hcp-exporter", "clusters-service", "fleet", "kube-applier", "maestro", "mgmt-agent", "monitoring", "secret-sync-controller", "sessiongate", "swift-recorder"}
	got := Namespaces()
	if !slices.Equal(got, want) {
		t.Fatalf("Namespaces = %v, want %v", got, want)
	}
	clear(got)
	if !slices.Equal(Namespaces(), want) {
		t.Fatal("mutating returned namespaces changed the catalog")
	}
}
