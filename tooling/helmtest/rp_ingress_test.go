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

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"helm.sh/helm/v4/pkg/chart/common"
	"helm.sh/helm/v4/pkg/chart/common/util"
	"helm.sh/helm/v4/pkg/chart/v2/loader"
	"helm.sh/helm/v4/pkg/engine"
	releaseutil "helm.sh/helm/v4/pkg/release/v1/util"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/yaml"

	"github.com/Azure/ARO-Tools/config"
	"github.com/Azure/ARO-Tools/config/ev2config"
	"github.com/Azure/ARO-Tools/config/types"
	pipelinetypes "github.com/Azure/ARO-Tools/pipelines/types"
)

func renderRPIngress(t *testing.T, enabled, activate bool) (map[string]string, error) {
	t.Helper()
	raw, err := os.ReadFile("../../config/rendered/dev/dev/westus3.yaml")
	require.NoError(t, err)
	var cfg types.Configuration
	require.NoError(t, yaml.Unmarshal(raw, &cfg))
	cfg = types.MergeConfiguration(cfg, map[string]any{"svc": map[string]any{"rpIngress": map[string]any{
		"enabled": enabled, "activate": activate,
	}}})
	values, err := config.PreprocessFile("../../frontend/values.yaml", cfg)
	require.NoError(t, err)
	var overrides map[string]any
	require.NoError(t, yaml.Unmarshal(values, &overrides))
	chart, err := loader.Load("../../frontend/deploy")
	require.NoError(t, err)
	renderValues, err := util.ToRenderValues(chart, overrides, common.ReleaseOptions{
		Name: "aro-hcp-frontend-dev", Namespace: "aro-hcp", IsInstall: true,
	}, nil)
	require.NoError(t, err)
	return engine.Render(chart, renderValues)
}

func ingressTemplate(t *testing.T, manifests map[string]string, suffix string) string {
	t.Helper()
	for name, body := range manifests {
		if strings.HasSuffix(name, "/templates/"+suffix) {
			return body
		}
	}
	t.Fatalf("template %s not found", suffix)
	return ""
}

func ingressDocument(t *testing.T, body, kind, name string) string {
	t.Helper()
	for _, document := range releaseutil.SplitManifests(body) {
		var meta struct {
			Kind     string
			Metadata metav1.ObjectMeta
		}
		require.NoError(t, yaml.Unmarshal([]byte(document), &meta))
		if meta.Kind == kind && meta.Metadata.Name == name {
			return document
		}
	}
	t.Fatalf("%s/%s not found", kind, name)
	return ""
}

func TestRPIngressMigration(t *testing.T) {
	legacy, err := renderRPIngress(t, false, false)
	require.NoError(t, err)
	for _, tc := range []struct {
		name              string
		enabled, activate bool
	}{
		{"disabled", false, false},
		{"prepare", true, false},
		{"activate", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manifests, err := renderRPIngress(t, tc.enabled, tc.activate)
			require.NoError(t, err)
			for _, template := range []string{"frontend.gateway.yaml", "frontend.virtualservice.yaml", "ext-authz.authorizationpolicy.yaml", "allow-ingress.authorizationpolicy.yaml"} {
				require.Equal(t, ingressTemplate(t, legacy, template), ingressTemplate(t, manifests, template), "retain the legacy path and authorization during preparation/cutover")
			}
			gateway := ingressTemplate(t, manifests, "rp-ingress.gateway.yaml")
			route := ingressTemplate(t, manifests, "frontend.httproute.yaml")
			check := ingressTemplate(t, manifests, "rp-ingress-readiness.yaml")
			if !tc.enabled {
				require.Empty(t, strings.TrimSpace(gateway))
				require.Empty(t, strings.TrimSpace(route))
				require.Empty(t, strings.TrimSpace(check))
				return
			}
			var cm corev1.ConfigMap
			require.NoError(t, yaml.Unmarshal([]byte(ingressDocument(t, gateway, "ConfigMap", "rp-ingress-options")), &cm))
			var service corev1.Service
			require.NoError(t, yaml.Unmarshal([]byte(cm.Data["service"]), &service))
			require.Equal(t, corev1.ServiceTypeLoadBalancer, service.Spec.Type)
			var deployment appsv1.Deployment
			require.NoError(t, yaml.Unmarshal([]byte(cm.Data["deployment"]), &deployment))
			spread := deployment.Spec.Template.Spec.TopologySpreadConstraints
			require.Len(t, spread, 2)
			require.Equal(t, "kubernetes.io/hostname", spread[0].TopologyKey)
			require.Equal(t, corev1.DoNotSchedule, spread[0].WhenUnsatisfiable)
			require.Equal(t, int32(1), spread[0].MaxSkew)
			require.NotNil(t, spread[0].MinDomains)
			require.Equal(t, int32(2), *spread[0].MinDomains)
			require.NotNil(t, spread[0].NodeTaintsPolicy)
			require.Equal(t, corev1.NodeInclusionPolicyHonor, *spread[0].NodeTaintsPolicy)
			require.Equal(t, map[string]string{"gateway.networking.k8s.io/gateway-name": "rp-ingress-gateway"}, spread[0].LabelSelector.MatchLabels)
			require.Equal(t, "topology.kubernetes.io/zone", spread[1].TopologyKey)
			require.Equal(t, corev1.ScheduleAnyway, spread[1].WhenUnsatisfiable)
			var hpa autoscalingv2.HorizontalPodAutoscaler
			require.NoError(t, yaml.Unmarshal([]byte(cm.Data["horizontalPodAutoscaler"]), &hpa))
			require.Equal(t, int32(2), *hpa.Spec.MinReplicas)
			require.Equal(t, int32(5), hpa.Spec.MaxReplicas)
			var pdb policyv1.PodDisruptionBudget
			require.NoError(t, yaml.Unmarshal([]byte(cm.Data["podDisruptionBudget"]), &pdb))
			require.Equal(t, 1, pdb.Spec.MinAvailable.IntValue())
			require.Contains(t, gateway, "name: rp-ingress-options")
			require.Contains(t, gateway, "name: frontend-credential")
			require.Contains(t, gateway, "kubernetes.io/metadata.name: \"aro-hcp\"")
			require.Contains(t, route, "name: rp-ingress-gateway")
			require.Contains(t, route, "sectionName: rp-https")
			require.Contains(t, route, "name: mise-inbound-policies-to-filter")
			require.Contains(t, route, "name: aro-hcp-frontend")
			require.Contains(t, route, "port: 8443")
			if tc.activate {
				var job batchv1.Job
				require.NoError(t, yaml.Unmarshal([]byte(ingressDocument(t, check, "Job", "rp-ingress-check")), &job))
				require.Equal(t, "post-install,post-upgrade", job.Annotations["helm.sh/hook"])
				require.Equal(t, "false", job.Spec.Template.Annotations["sidecar.istio.io/inject"])
				require.Equal(t, "rp-ingress-check", job.Spec.Template.Spec.ServiceAccountName)
			} else {
				require.Empty(t, strings.TrimSpace(check), "prepare must not require or initiate DNS cutover")
			}
		})
	}
	_, err = renderRPIngress(t, false, true)
	require.ErrorContains(t, err, "RP ingress activation requires")
}

func TestRPIngressReadinessGate(t *testing.T) {
	manifests, err := renderRPIngress(t, true, true)
	require.NoError(t, err)
	var cm corev1.ConfigMap
	body := ingressTemplate(t, manifests, "rp-ingress-readiness.yaml")
	require.NoError(t, yaml.Unmarshal([]byte(ingressDocument(t, body, "ConfigMap", "rp-ingress-check")), &cm))
	for _, tc := range []struct {
		name, route, nodes, serviceType, ip string
		failWait, success                   bool
	}{
		{"two hosts", "1 True 1 True 1", "node-a\nnode-b\n", "LoadBalancer", "192.0.2.1", false, true},
		{"dual stack deduplicated", "1 True 1 True 1", "node-a\nnode-a\nnode-b\n", "LoadBalancer", "192.0.2.1", false, true},
		{"colocated endpoints", "1 True 1 True 1", "node-a\nnode-a\n", "LoadBalancer", "192.0.2.1", false, false},
		{"no endpoint node names", "1 True 1 True 1", "\n\n", "LoadBalancer", "192.0.2.1", false, false},
		{"stale route", "2 True 1 True 1", "node-a\nnode-b\n", "LoadBalancer", "192.0.2.1", false, false},
		{"unresolved backend", "1 True 1 False 1", "node-a\nnode-b\n", "LoadBalancer", "192.0.2.1", false, false},
		{"gateway not ready", "1 True 1 True 1", "node-a\nnode-b\n", "LoadBalancer", "192.0.2.1", true, false},
		{"not a load balancer", "1 True 1 True 1", "node-a\nnode-b\n", "ClusterIP", "192.0.2.1", false, false},
		{"no public IP", "1 True 1 True 1", "node-a\nnode-b\n", "LoadBalancer", "", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			script := filepath.Join(dir, "check.sh")
			require.NoError(t, os.WriteFile(script, []byte(cm.Data["check.sh"]), 0600))
			kubectl := `#!/bin/sh
case "$*" in
  *" wait "*) [ "$FAIL_WAIT" = false ] ;;
  *" rollout status "*) exit 0 ;;
  *" get service/"*"spec.type"*) printf '%s' "$SERVICE_TYPE" ;;
  *" get service/"*) printf '%s' "$SERVICE_IP" ;;
  *" get httproute/"*) printf '%s' "$ROUTE_STATUS" ;;
  *" get endpointslices "*) printf '%s' "$READY_NODES" ;;
  *) echo "unexpected kubectl invocation: $*" >&2; exit 1 ;;
esac
`
			require.NoError(t, os.WriteFile(filepath.Join(dir, "kubectl"), []byte(kubectl), 0700))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "sleep"), []byte("#!/bin/sh\nexit 0\n"), 0700))
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "/bin/sh", script)
			wait := "false"
			if tc.failWait {
				wait = "true"
			}
			cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "FRONTEND_NAMESPACE=aro-hcp", "ROUTE_STATUS="+tc.route, "READY_NODES="+tc.nodes, "FAIL_WAIT="+wait, "SERVICE_TYPE="+tc.serviceType, "SERVICE_IP="+tc.ip)
			output, err := cmd.CombinedOutput()
			require.NoError(t, ctx.Err(), "readiness check must terminate: %s", output)
			if tc.success {
				require.NoError(t, err, "%s", output)
			} else {
				require.Error(t, err, "DNS gate must fail closed: %s", output)
			}
		})
	}
}

func TestRPIngressConfiguration(t *testing.T) {
	provider, err := config.NewConfigProvider("../../config/config.yaml")
	require.NoError(t, err)
	ev2, err := ev2config.ResolveConfig("public", "westus3")
	require.NoError(t, err)
	resolver, err := provider.GetResolver(&config.ConfigReplacements{
		CloudReplacement: "dev", EnvironmentReplacement: "dev", RegionReplacement: "westus3",
		RegionShortReplacement: "usw3", StampReplacement: "1", Ev2Config: ev2,
	})
	require.NoError(t, err)
	for _, tc := range []struct {
		name              string
		enabled, activate bool
		valid             bool
	}{
		{"disabled", false, false, true},
		{"prepare", true, false, true},
		{"activate", true, true, true},
		{"activation without preparation", false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := resolver.GetRegionConfiguration("westus3")
			require.NoError(t, err)
			cfg = types.MergeConfiguration(cfg, map[string]any{"svc": map[string]any{"rpIngress": map[string]any{
				"enabled": tc.enabled, "activate": tc.activate,
			}}})
			err = resolver.ValidateSchema(cfg)
			if !tc.valid {
				require.ErrorContains(t, err, "enabled")
				return
			}
			require.NoError(t, err)
			infra, err := config.PreprocessFile("../../dev-infrastructure/configurations/svc-cluster.tmpl.bicepparam", cfg)
			require.NoError(t, err)
			dns, err := config.PreprocessFile("../../frontend/templates/ingress-dns.tmpl.bicepparam", cfg)
			require.NoError(t, err)
			activate := "false"
			if tc.activate {
				activate = "true"
			}
			require.Contains(t, string(infra), "param rpIngressActivate = "+activate)
			require.Contains(t, string(dns), "param activate = "+activate, "both DNS writers must use the same cutover switch")
			pipeline, err := pipelinetypes.NewPipelineFromFile("../../frontend/pipeline.yaml", cfg)
			require.NoError(t, err)
			foundDNS := false
			for _, rg := range pipeline.ResourceGroups {
				for _, step := range rg.Steps {
					arm, ok := step.(*pipelinetypes.ARMStep)
					if !ok || arm.Name != "ingress-dns" {
						continue
					}
					foundDNS = true
					require.Equal(t, "regional", rg.Name)
					require.Len(t, arm.DependsOn, 1)
					require.Equal(t, "service", arm.DependsOn[0].ResourceGroup)
					require.Equal(t, "deploy", arm.DependsOn[0].Step, "DNS must wait for the Helm post-install/post-upgrade hook")
				}
			}
			require.True(t, foundDNS)
		})
	}
}
