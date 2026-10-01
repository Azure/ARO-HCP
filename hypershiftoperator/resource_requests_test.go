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

package hypershiftoperator

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/Azure/ARO-Tools/config"
)

func render(t *testing.T, values string) ([]map[string]interface{}, error) {
	t.Helper()
	cmd := exec.Command("helm", "template", "hypershift", "deploy", "--namespace=hypershift", "--values=-")
	cmd.Stdin = strings.NewReader(`image: hypershift
imageDigest: sha256:test
installJobImage: runtime:test
azureKeyVaultClientId: test
operatorEnvVars: {}
metricsSet: {performanceMetrics: false}
` + values)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("helm: %w: %s", err, output)
	}
	var objects []map[string]interface{}
	decoder := yaml.NewDecoder(bytes.NewReader(output))
	for {
		var object map[string]interface{}
		if err := decoder.Decode(&object); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		objects = append(objects, object)
	}
	return objects, nil
}

func sizingRequests(objects []map[string]interface{}) []interface{} {
	for _, object := range objects {
		if object["kind"] == "ClusterSizingConfiguration" {
			sizes := object["spec"].(map[string]interface{})["sizes"].([]interface{})
			return sizes[0].(map[string]interface{})["effects"].(map[string]interface{})["resourceRequests"].([]interface{})
		}
	}
	return nil
}

func TestResourceRequests(t *testing.T) {
	baseline, err := render(t, "limitClusterSizes: true\n")
	if err != nil {
		t.Fatal(err)
	}
	if got := len(sizingRequests(baseline)); got != 7 {
		t.Fatalf("default requests = %d, want 7", got)
	}
	for _, tt := range []struct {
		name, values, wantError string
		count                   int
	}{
		{"empty map", "additionalMinimalResourceRequests: {}\n", "", 7},
		{"empty resources", "additionalMinimalResourceRequests: {cpo: {deploymentName: control-plane-operator, containerName: control-plane-operator}}\n", "", 7},
		{"cpo and etcd sidecar", "additionalMinimalResourceRequests: {cpo: {deploymentName: control-plane-operator, containerName: control-plane-operator, cpu: 25m}, etcdMetrics: {deploymentName: etcd, containerName: etcd-metrics, memory: 32Mi}}\n", "", 9},
		{"unknown component", "additionalMinimalResourceRequests: {bad: {deploymentName: multus-admission-controller, containerName: multus-admission-controller, cpu: 25m}}\n", "unsupported regular-container", 0},
		{"annotation suffix too long", "additionalMinimalResourceRequests: {route: {deploymentName: openshift-route-controller-manager, containerName: openshift-route-controller-manager, cpu: 10m}}\n", "name part must be no more than 63 characters", 0},
		{"snapshot annotation suffix too long", "additionalMinimalResourceRequests: {snapshot: {deploymentName: csi-snapshot-controller-operator, containerName: csi-snapshot-controller-operator, cpu: 10m}}\n", "name part must be no more than 63 characters", 0},
		{"empty resources still invalid", "additionalMinimalResourceRequests: {route: {deploymentName: openshift-route-controller-manager, containerName: openshift-route-controller-manager}}\n", "name part must be no more than 63 characters", 0},
		{"init excluded", "additionalMinimalResourceRequests: {bad: {deploymentName: etcd, containerName: ensure-dns, cpu: 25m}}\n", "unsupported regular-container", 0},
		{"main duplicate", "additionalMinimalResourceRequests: {bad: {deploymentName: etcd, containerName: etcd, cpu: 25m}}\n", "duplicate resource target", 0},
		{"sidecar duplicate", "additionalMinimalResourceRequests: {one: {deploymentName: etcd, containerName: healthz, cpu: 25m}, two: {deploymentName: etcd, containerName: healthz, memory: 32Mi}}\n", "duplicate resource target", 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			objects, err := render(t, "limitClusterSizes: true\n"+tt.values)
			if tt.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("render error = %v, want %s", err, tt.wantError)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			requests := sizingRequests(objects)
			if len(requests) != tt.count {
				t.Fatalf("requests = %v, want %d entries", requests, tt.count)
			}
			if !reflect.DeepEqual(requests[:7], sizingRequests(baseline)) {
				t.Fatal("existing seven entries changed")
			}
			if tt.count == 9 {
				if _, ok := requests[7].(map[string]interface{})["memory"]; ok {
					t.Fatal("CPU-only override added memory")
				}
				if _, ok := requests[8].(map[string]interface{})["cpu"]; ok {
					t.Fatal("memory-only override added CPU")
				}
			}
		})
	}
	baseline, err = render(t, "limitClusterSizes: false\n")
	if err != nil {
		t.Fatal(err)
	}
	withOverrides, err := render(t, "limitClusterSizes: false\nadditionalMinimalResourceRequests: {cpo: {deploymentName: control-plane-operator, containerName: control-plane-operator, cpu: 25m}}\n")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(baseline, withOverrides) {
		t.Fatal("overrides changed unlimited branch")
	}
}

func TestOperatorResourcePatch(t *testing.T) {
	objects, err := render(t, "limitClusterSizes: true\noperatorResources: {requests: {cpu: 25m, memory: 200Mi}}\n")
	if err != nil {
		t.Fatal(err)
	}
	var overlay string
	for _, object := range objects {
		if object["kind"] == "ConfigMap" && object["metadata"].(map[string]interface{})["name"] == "hypershift-operator-resources" {
			overlay = object["data"].(map[string]interface{})["kustomization.yaml"].(string)
		}
	}
	if overlay == "" {
		t.Fatal("missing operator resource overlay")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "kustomization.yaml"), []byte(overlay), 0600); err != nil {
		t.Fatal(err)
	}
	manifest := `apiVersion: apps/v1
kind: Deployment
metadata: {name: operator, namespace: hypershift}
spec:
  template:
    spec:
      initContainers:
      - {name: init-environment, image: test}
      containers:
      - name: operator
        image: test
        resources: {requests: {cpu: 10m, memory: 150Mi}}
      - {name: other, image: test}
`
	if err := os.WriteFile(filepath.Join(dir, "hypershift.yaml"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command("kubectl", "kustomize", dir).CombinedOutput()
	if err != nil {
		t.Fatalf("kustomize: %v: %s", err, output)
	}
	var deployment map[string]interface{}
	if err := yaml.Unmarshal(output, &deployment); err != nil {
		t.Fatal(err)
	}
	spec := deployment["spec"].(map[string]interface{})["template"].(map[string]interface{})["spec"].(map[string]interface{})
	containers := spec["containers"].([]interface{})
	resources := containers[0].(map[string]interface{})["resources"].(map[string]interface{})
	if !reflect.DeepEqual(resources, map[string]interface{}{"requests": map[string]interface{}{"cpu": "25m", "memory": "200Mi"}}) {
		t.Fatalf("unexpected resources: %v", resources)
	}
	if _, ok := containers[1].(map[string]interface{})["resources"]; ok {
		t.Fatal("patch changed other container")
	}
	if _, ok := spec["initContainers"].([]interface{})[0].(map[string]interface{})["resources"]; ok {
		t.Fatal("patch changed init container")
	}
}

func TestAllRegularTargets(t *testing.T) {
	content, err := os.ReadFile("deploy/regular-resource-targets.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var targets map[string][]string
	if err := yaml.Unmarshal(content, &targets); err != nil {
		t.Fatal(err)
	}
	baseline, err := render(t, "limitClusterSizes: true\n")
	if err != nil {
		t.Fatal(err)
	}
	existing := map[string]bool{}
	for _, item := range sizingRequests(baseline) {
		entry := item.(map[string]interface{})
		existing[entry["deploymentName"].(string)+"/"+entry["containerName"].(string)] = true
	}
	requests := map[string]interface{}{}
	for deployment, containers := range targets {
		for _, container := range containers {
			if (deployment == "openshift-route-controller-manager" || deployment == "csi-snapshot-controller-operator") && container == deployment {
				continue // Audited identities with overlong annotation suffixes, tested above.
			}
			if existing[deployment+"/"+container] {
				continue
			}
			requests[deployment+"-"+container] = map[string]interface{}{
				"deploymentName": deployment, "containerName": container, "cpu": "1m",
			}
		}
	}
	values, err := yaml.Marshal(map[string]interface{}{"limitClusterSizes": true, "additionalMinimalResourceRequests": requests})
	if err != nil {
		t.Fatal(err)
	}
	objects, err := render(t, string(values))
	if err != nil {
		t.Fatal(err)
	}
	if got := len(sizingRequests(objects)); got != 7+len(requests) {
		t.Fatalf("rendered %d targets, want %d", got, 7+len(requests))
	}
}

func TestResourceValuesExpansion(t *testing.T) {
	content, err := os.ReadFile("values.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, requests := range []map[string]interface{}{
		{},
		{"cpo": map[string]interface{}{"deploymentName": "control-plane-operator", "containerName": "control-plane-operator", "cpu": "25m"}},
	} {
		vars := map[string]interface{}{
			"acr":               map[string]interface{}{"svc": map[string]interface{}{"name": "svc"}, "ocp": map[string]interface{}{"name": "ocp"}},
			"acrDNSSuffix":      "azurecr.io",
			"aksCommandRuntime": map[string]interface{}{"image": map[string]interface{}{"registry": "mcr.microsoft.com", "repository": "aks/command/runtime", "digest": "sha256:test"}},
			"hypershift": map[string]interface{}{
				"image":                map[string]interface{}{"repository": "hypershift", "digest": "sha256:test"},
				"additionalInstallArg": "", "sharedIngressIPTag": "",
				"sharedIngressImage":                map[string]interface{}{"repository": "ingress", "digest": "sha256:test"},
				"limitClusterSizes":                 true,
				"metricsSet":                        map[string]interface{}{"performanceMetrics": false},
				"additionalMinimalResourceRequests": requests,
				"operatorResources":                 map[string]interface{}{"requests": map[string]interface{}{"cpu": "10m", "memory": "150Mi"}},
			},
		}
		values, err := config.PreprocessContent(content, vars)
		if err != nil {
			t.Fatal(err)
		}
		objects, err := render(t, string(values))
		if err != nil {
			t.Fatal(err)
		}
		if got := len(sizingRequests(objects)); got != 7+len(requests) {
			t.Fatalf("requests = %d, want %d", got, 7+len(requests))
		}
	}
}

func TestInstallerPriorityClasses(t *testing.T) {
	objects, err := render(t, "limitClusterSizes: true\n")
	if err != nil {
		t.Fatal(err)
	}
	var script string
	for _, object := range objects {
		if object["kind"] == "Job" && object["metadata"].(map[string]interface{})["name"] == "install-hypershift" {
			spec := object["spec"].(map[string]interface{})["template"].(map[string]interface{})["spec"].(map[string]interface{})
			script = spec["containers"].([]interface{})[0].(map[string]interface{})["command"].([]interface{})[2].(string)
		}
	}
	if script == "" {
		t.Fatal("missing installer script")
	}
	for _, scenario := range []string{"absent", "existing", "concurrent-create", "forbidden", "connection-error", "invalid", "render-error", "decode-error", "apply-error"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			// Exercise the rendered script without cluster access. jq remains real.
			fake := `#!/bin/bash
set -euo pipefail
printf '%s\n' "$*" >> "$TEST_DIR/calls"
case "$1" in
  kustomize)
    [[ "$SCENARIO" != render-error ]] || exit 1
    printf '%s\n' 'rendered manifests'
    ;;
  create)
    if [[ "$2" == --dry-run=client ]]; then
      [[ "$SCENARIO" != decode-error ]] || exit 1
      # kubectl can print a stream of objects or a List; normalize both.
      printf '%s\n' '{"apiVersion":"v1","kind":"List","items":[{"apiVersion":"scheduling.k8s.io/v1","kind":"PriorityClass","metadata":{"name":"hypershift-control-plane"},"value":1000}]}'
      printf '%s\n' '{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"operator","namespace":"hypershift"}}'
      exit 0
    fi
    jq -e '.kind == "PriorityClass" and .metadata.name == "hypershift-control-plane" and .value == 1000' > /dev/null
    case "$SCENARIO" in
      existing|concurrent-create)
        printf '%s\n' 'Error from server (AlreadyExists): priorityclasses.scheduling.k8s.io "hypershift-control-plane" already exists' >&2
        exit 1
        ;;
      forbidden|invalid|connection-error)
        printf '%s\n' "$SCENARIO" >&2
        exit 1
        ;;
    esac
    printf '%s\n' 'priorityclass.scheduling.k8s.io/hypershift-control-plane created'
    ;;
  apply)
    [[ "$2 $3 $4" == '--server-side --force-conflicts --field-manager=hypershift' ]]
    jq -e '.kind == "List" and (.items | length == 1) and .items[0].kind == "Deployment"' "${@: -1}" > /dev/null
    [[ "$SCENARIO" != apply-error ]] || exit 1
    touch "$TEST_DIR/applied"
    ;;
  *) exit 99 ;;
esac
`
			if err := os.WriteFile(filepath.Join(dir, "kubectl"), []byte(fake), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "overlay.yaml"), []byte("unused by fake kustomize"), 0600); err != nil {
				t.Fatal(err)
			}
			localScript := strings.ReplaceAll(script, "/overlay/kustomization.yaml", filepath.Join(dir, "overlay.yaml"))
			localScript = strings.ReplaceAll(localScript, "/manifests", dir)
			cmd := exec.Command("bash", "-c", localScript)
			cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "TEST_DIR="+dir, "SCENARIO="+scenario)
			output, err := cmd.CombinedOutput()
			wantSuccess := scenario == "absent" || scenario == "existing" || scenario == "concurrent-create"
			if (err == nil) != wantSuccess {
				t.Fatalf("installer error = %v, want success %t: %s", err, wantSuccess, output)
			}
			_, appliedErr := os.Stat(filepath.Join(dir, "applied"))
			if (appliedErr == nil) != wantSuccess {
				t.Fatalf("unexpected apply result: %v", appliedErr)
			}
			calls, err := os.ReadFile(filepath.Join(dir, "calls"))
			if err != nil {
				t.Fatal(err)
			}
			if wantSuccess && strings.Count(string(calls), "create -f -\n") != 1 {
				t.Fatalf("PriorityClass must be created exactly once, never applied: %s", calls)
			}
		})
	}
}
