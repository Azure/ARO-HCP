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

package testrunner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"sigs.k8s.io/yaml"

	"github.com/Azure/ARO-HCP/tooling/helmtest/internal"
)

func TestArobitTenantRouting(t *testing.T) {
	cases, err := getCustomTestCases("../../../observability/arobit/deploy")
	require.NoError(t, err)
	for _, test := range []struct {
		name        string
		clusterType string
		mdsd        bool
	}{
		{name: "management", clusterType: "mgmt", mdsd: true},
		{name: "management-without-shoebox", clusterType: "mgmt"},
		{name: "service", clusterType: "svc", mdsd: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var manifest string
			for _, tc := range cases {
				if tc.Name != "helmtest-mdsd-and-kusto-enabled-"+test.clusterType {
					continue
				}
				tc.TestData = map[string]any{"arobit": map[string]any{
					"kusto": map[string]any{"enabled": true},
					"mdsd":  map[string]any{"enabled": test.mdsd},
				}}
				manifest, err = runTest(t.Context(), &internal.Settings{ConfigPath: "../../../config/rendered/dev/dev/westus3.yaml"}, tc)
				require.NoError(t, err)
			}
			require.NotEmpty(t, manifest)
			var data map[string]string
			for _, document := range strings.Split(manifest, "\n---") {
				var resource struct {
					Kind string
					Data map[string]string
				}
				require.NoError(t, yaml.Unmarshal([]byte(document), &resource))
				if resource.Kind == "ConfigMap" && resource.Data["input.conf"] != "" {
					data = resource.Data
				}
			}
			require.NotEmpty(t, data)
			require.NotContains(t, data["filter.conf"], "re_emitted_namespace_router",
				"tenant routing must not depend on a namespace rewrite queue")

			image := os.Getenv("FLUENT_BIT_IMAGE")
			if image == "" {
				t.Skip("set FLUENT_BIT_IMAGE to execute Fluent Bit routing tests")
			}
			runtime := os.Getenv("CONTAINER_RUNTIME")
			if runtime == "" {
				runtime = "podman"
			}
			property := func(block, key string) string {
				match := regexp.MustCompile(`(?im)^[\t ]*` + key + `[\t ]+([^\r\n]+)`).FindStringSubmatch(block)
				if len(match) == 0 {
					return ""
				}
				return strings.TrimSpace(match[1])
			}
			blocks := func(config string) []string {
				parts := strings.Split("\n"+config, "\n[")[1:]
				for i := range parts {
					parts[i] = "\n[" + parts[i] + "\n"
				}
				return parts
			}
			dir := t.TempDir()
			write := func(name, contents string) {
				require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(contents), 0600))
			}
			for name, contents := range data {
				write(name, strings.ReplaceAll(contents, "/forwarder/etc", dir))
			}
			inputDir := filepath.Join(dir, "input")
			require.NoError(t, os.Mkdir(inputDir, 0700))
			var input string
			for _, block := range blocks(data["input.conf"]) {
				if property(block, "Alias") == "tail.container" {
					input = block
				}
			}
			require.Equal(t, "kubernetes.*", property(input, "Tag"))
			require.Equal(t, "/var/log/containers/*.log", property(input, "Path"))
			input = regexp.MustCompile(`(?m)^[\t ]*multiline\.parser[\t ]+[^\r\n]+`).ReplaceAllString(input, "    Parser synthetic")
			input = strings.ReplaceAll(input, "/var/log/flb-tail.db", filepath.Join(dir, "tail.db"))
			write("synthetic-parser.conf", "[PARSER]\n    Name synthetic\n    Format json\n")
			var filters strings.Builder
			for _, block := range blocks(data["filter.conf"] + "\n" + data["filter-shoebox.conf"]) {
				// Synthetic records start after CRI/multiline assembly and metadata
				// lookup; all subsequent production filters and Lua scripts run unchanged.
				if name := property(block, "Name"); name != "kubernetes" && name != "multiline" {
					filters.WriteString(block)
				}
			}
			var outputs strings.Builder
			paths := map[string]string{}
			for _, block := range blocks(data["output.conf"] + "\n" + data["output-shoebox.conf"]) {
				table := property(block, "Table_Name")
				destination := property(block, "Database_Name") + "." + table
				if property(block, "Alias") == "forward.shoebox" {
					destination = "shoebox"
				} else if table == "" {
					continue
				}
				var selectors []string
				for _, key := range []string{"Match", "Match_Regex"} {
					if value := property(block, key); value != "" {
						selectors = append(selectors, key+" "+value)
					}
				}
				require.Len(t, selectors, 1, "one production selector for %s", destination)
				paths[destination] = filepath.Join(dir, destination+".jsonl")
				fmt.Fprintf(&outputs, "\n[OUTPUT]\n    Name file\n    %s\n    Path %s\n    File %s.jsonl\n    Format plain\n",
					selectors[0], dir, destination)
			}

			const resourceID = "/subscriptions/test-subscription/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test"
			const tenantDestination = "HostedControlPlaneLogs.containerLogs"
			const genericDestination = "ServiceLogs.containerLogs"
			want := map[string]map[string]int{}
			records := map[string]map[string]any{}
			add := func(id, namespace, container, pod string, log any, copies int, destinations ...string) {
				pod += "-" + id
				record := map[string]any{
					"case": id, "log": log, "time": "2026-01-02T03:04:05.123Z",
					"kubernetes": map[string]any{
						"namespace_name": namespace, "container_name": container,
						"pod_name": pod, "host": "test-node", "labels": map[string]any{"app": "test"},
					},
					"kubernetes_namespace": map[string]any{"annotations": map[string]any{
						"azure.microsoft.com/hcp-cluster-azure-resource-id": resourceID,
					}},
				}
				if id == "tenant-no-annotation" || id == "tenant-no-metadata" {
					delete(record, "kubernetes_namespace")
				}
				if id == "tenant-no-metadata" {
					delete(record, "kubernetes")
				}
				raw, err := json.Marshal(record)
				require.NoError(t, err)
				filename := fmt.Sprintf("%s_%s_%s-test.log", pod, namespace, container)
				require.NoError(t, os.WriteFile(filepath.Join(inputDir, filename), bytes.Repeat(append(raw, '\n'), copies), 0600))
				records[id] = record
				for _, destination := range destinations {
					require.Contains(t, paths, destination)
					if want[destination] == nil {
						want[destination] = map[string]int{}
					}
					want[destination][id] += copies
				}
			}
			var tenantOutputs []string
			if test.clusterType == "mgmt" {
				tenantOutputs = []string{tenantDestination}
			}
			for _, id := range []string{"tenant", "tenant-no-annotation", "tenant-no-metadata"} {
				add(id, "ocm-test", "control-plane-operator", "controller", "ordinary tenant message", 1, tenantOutputs...)
			}
			for _, namespace := range []string{"hypershift", "not-ocm-test", "ocmtest"} {
				add(namespace, namespace, "operator", "ocm-pod", "ordinary service message", 1, genericDestination)
			}
			components := []struct{ container, table string }{
				{"aro-hcp-frontend", "frontendLogs"}, {"aro-hcp-backend", "backendLogs"},
				{"clusters-service", "clustersServiceLogs"}, {"fleet-controller", "fleetLogs"},
				{"kube-applier", "kubeApplierLogs"}, {"kube-events", "kubernetesEvents"},
				{"mgmt-agent-controller", "containerLogs"}, {"swift-recorder", "containerLogs"},
			}
			for _, component := range components {
				add("service-"+component.container, "platform", component.container, "service",
					`{"time":"2026-01-02T03:04:05.123Z","msg":"structured service message"}`, 1, "ServiceLogs."+component.table)
				add("tenant-"+component.container, "ocm-test", component.container, "tenant",
					"tenant message must bypass service-specific routers", 1, tenantOutputs...)
			}
			for _, snapshot := range []struct{ container, kind, retainedTable string }{
				{"aro-hcp-frontend", "cosmos", ""}, {"aro-hcp-backend", "cosmos", ""},
				{"fleet-controller", "cosmos", "fleetLogs"}, {"mgmt-agent-controller", "kubernetes", "containerLogs"},
				{"kube-applier", "cosmos", ""},
			} {
				var destinations []string
				if snapshot.container != "kube-applier" {
					table := "cosmosResourceSnapshots"
					if snapshot.kind == "kubernetes" {
						table = "kubernetesResourceSnapshots"
					}
					destinations = append(destinations, "ServiceLogs."+table)
				}
				if snapshot.retainedTable != "" {
					destinations = append(destinations, "ServiceLogs."+snapshot.retainedTable)
				}
				add("snapshot-"+snapshot.container, "platform", snapshot.container, "service",
					fmt.Sprintf(`{"time":"2026-01-02T03:04:05.123Z","snapshotType":%q}`, snapshot.kind), 1, destinations...)
			}
			for _, shoebox := range []struct{ id, pod, container string }{
				{"kube-apiserver", "apiserver", "kube-apiserver"},
				{"capi-provider", "capi-provider-test", "manager"},
			} {
				destinations := append([]string{}, tenantOutputs...)
				if test.clusterType == "mgmt" && test.mdsd {
					destinations = append(destinations, "shoebox")
				}
				add(shoebox.id, "ocm-test", shoebox.container, shoebox.pod,
					map[string]any{"case": shoebox.id, "raw": "synthetic diagnostic"}, 1, destinations...)
			}
			if test.name == "management" {
				const copies = 32768
				log := strings.Repeat("x", 1024)
				require.Greater(t, len(log)*copies, 20*1024*1024, "exceed the removed namespace emitter's limit")
				add("tenant-burst", "ocm-test", "control-plane-operator", "controller", log, copies, tenantDestination)
			}
			write("fluent-bit.conf", fmt.Sprintf(`[SERVICE]
    Flush 1
    Grace 3
    Parsers_File %s/parsers_custom.conf
    Parsers_File %s/synthetic-parser.conf
    storage.path %s/storage
%s
%s
%s
`, dir, dir, dir, input, strings.ReplaceAll(filters.String(), "/forwarder/etc", dir), outputs.String()))
			log, err := os.Create(filepath.Join(dir, "diagnostics.log"))
			require.NoError(t, err)
			ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
			defer cancel()
			name := "arobit-routing-" + filepath.Base(filepath.Dir(dir))
			cmd := exec.CommandContext(ctx, runtime, "run", "--rm", "--name", name, "--network=none",
				"--user", "0", "-e", "NODE_NAME=test-node", "-v", dir+":"+dir+":Z",
				"-v", inputDir+":/var/log/containers:ro,Z",
				"--entrypoint", "/fluent-bit/bin/fluent-bit", image, "-c", filepath.Join(dir, "fluent-bit.conf"))
			cmd.Stdout, cmd.Stderr = log, log
			stopped := false
			t.Cleanup(func() {
				if !stopped {
					_ = exec.Command(runtime, "rm", "-f", name).Run()
					_ = cmd.Wait()
				}
				_ = log.Close()
				if t.Failed() {
					raw, err := os.ReadFile(log.Name())
					require.NoError(t, err)
					t.Logf("Fluent Bit diagnostics:\n%s", raw)
				}
			})
			require.NoError(t, cmd.Start())
			require.Eventually(t, func() bool {
				for destination, counts := range want {
					expected := 0
					for _, count := range counts {
						expected += count
					}
					raw, err := os.ReadFile(paths[destination])
					if os.IsNotExist(err) {
						return false
					}
					require.NoError(t, err)
					if bytes.Count(raw, []byte("\n")) < expected {
						return false
					}
				}
				return true
			}, 30*time.Second, 200*time.Millisecond, "all records must reach their production-selected outputs")
			out, err := exec.Command(runtime, "stop", "-t", "5", name).CombinedOutput()
			require.NoError(t, err, "%s", out)
			require.NoError(t, cmd.Wait())
			stopped = true
			for destination, path := range paths {
				got := map[string]int{}
				raw, err := os.ReadFile(path)
				if os.IsNotExist(err) {
					require.Empty(t, want[destination], "missing output %s", destination)
					continue
				}
				require.NoError(t, err)
				scanner := bufio.NewScanner(bytes.NewReader(raw))
				for scanner.Scan() {
					var record map[string]any
					require.NoError(t, json.Unmarshal(scanner.Bytes(), &record))
					id, _ := record["case"].(string)
					if destination == "shoebox" {
						properties, ok := record["properties"].(map[string]any)
						require.True(t, ok)
						id, _ = properties["case"].(string)
						require.Equal(t, strings.ToUpper(resourceID), record["resourceId"])
						require.Equal(t, id, record["category"])
					}
					require.Contains(t, records, id, "unexpected record in %s", destination)
					if destination == tenantDestination {
						require.Equal(t, records[id]["log"], record["log"], "tenant payload unchanged")
						if id == "tenant-no-annotation" || id == "tenant-no-metadata" {
							require.NotContains(t, record, "cluster_id")
						} else {
							require.Equal(t, strings.ToUpper(resourceID), record["cluster_id"])
							require.Equal(t, "TEST-RG", record["resource_group"])
							require.NotContains(t, record, "kubernetes_namespace", "strip annotations after enrichment")
						}
					}
					got[id]++
				}
				require.NoError(t, scanner.Err())
				require.Equal(t, want[destination], got, "no dropped, duplicated or misrouted records in %s", destination)
			}
		})
	}
}
