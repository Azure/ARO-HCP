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
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"sigs.k8s.io/yaml"
)

// Run with FLUENT_BIT_IMAGE set to the forwarder image pinned in config.yaml.
func TestArobitRoutingMetricsFluentBit(t *testing.T) {
	image := os.Getenv("FLUENT_BIT_IMAGE")
	if image == "" {
		t.Skip("set FLUENT_BIT_IMAGE to execute Fluent Bit")
	}
	runtime := os.Getenv("CONTAINER_RUNTIME")
	if runtime == "" {
		runtime = "podman"
	}
	fixture, err := os.ReadFile("../../../observability/arobit/testdata/zz_fixture_TestHelmTemplate_helmtest_mdsd_and_kusto_enabled_mgmt.yaml")
	require.NoError(t, err)
	var data map[string]string
	for _, document := range strings.Split(string(fixture), "\n---") {
		var resource struct {
			Kind string
			Data map[string]string
		}
		require.NoError(t, yaml.Unmarshal([]byte(document), &resource))
		if resource.Kind == "ConfigMap" && resource.Data["filter.conf"] != "" {
			data = resource.Data
		}
	}
	require.NotEmpty(t, data, "load the rendered production routing filters")
	property := func(block, key string) string {
		m := regexp.MustCompile(`(?mi)^\s*` + regexp.QuoteMeta(key) + `\s+(\S+)`).FindStringSubmatch(block)
		if len(m) == 0 {
			return ""
		}
		return m[1]
	}
	// Inputs below already contain parsed application fields and Kubernetes
	// metadata. Exercise the production routing and measurement blocks unchanged.
	var routingBlocks []string
	for _, config := range []string{data["filter.conf"], data["filter-shoebox.conf"]} {
		for _, block := range strings.Split("\n"+config, "\n[")[1:] {
			if name := property(block, "Name"); name == "rewrite_tag" || name == "log_to_metrics" {
				routingBlocks = append(routingBlocks, "["+block)
			}
		}
	}
	require.Len(t, routingBlocks, 16, "seven routers and nine disjoint reroute/copy counters")
	type record struct {
		ID           string            `json:"id"`
		SnapshotType string            `json:"snapshotType,omitempty"`
		Kubernetes   map[string]string `json:"kubernetes"`
	}
	makeRecord := func(id, namespace, container, pod, snapshot string) record {
		return record{id, snapshot, map[string]string{"namespace_name": namespace, "container_name": container, "pod_name": pod}}
	}
	for _, tc := range []struct {
		name       string
		tag        string
		failRouter string
		records    []record
		matched    map[string]int
		emitted    map[string]int
		logCount   int
	}{
		{
			name: "all routes including copies and overlapping Shoebox rules",
			tag:  "kubernetes.var.log.containers.synthetic",
			records: []record{
				makeRecord("hcp", "ocm-test", "kube-apiserver", "hcp", ""),
				makeRecord("capi", "ocm-test", "manager", "capi-provider-test", ""),
				makeRecord("overlap", "ocm-test", "kube-apiserver", "capi-provider-test", ""),
				makeRecord("backend", "aro-hcp", "aro-hcp-backend", "backend", "cosmos"),
				makeRecord("frontend", "aro-hcp", "aro-hcp-frontend", "frontend", "cosmos"),
				makeRecord("fleet", "fleet", "fleet-controller", "fleet", "cosmos"),
				makeRecord("agent", "mgmt-agent", "mgmt-agent-controller", "agent", "kubernetes"),
				makeRecord("swift", "swift-recorder", "swift-recorder", "swift", ""),
				makeRecord("clusters-service", "clusters-service", "clusters-service-server", "cs", ""),
				makeRecord("kube-applier", "kube-applier", "kube-applier", "applier", ""),
				makeRecord("events", "monitoring", "kube-events", "events", ""),
				makeRecord("swift-lookalike", "swift-recorder", "swift-recorder-debug", "swift", ""),
				makeRecord("unmatched", "default", "other", "other", ""),
			},
			matched: map[string]int{"reroute_namespace_router": 3, "reroute_container_name_router": 7, "copy_container_name_router": 1, "reroute_resource_snapshot_router": 1,
				"reroute_cosmos_snapshot_router": 1, "reroute_cosmos_snapshot_router_frontend": 1, "copy_cosmos_snapshot_router_fleet": 1,
				"copy_shoebox_router_container": 2, "copy_shoebox_router_pod": 1},
			emitted: map[string]int{"namespace_router": 3, "container_name_router": 8, "resource_snapshot_router": 1,
				"cosmos_snapshot_router": 1, "cosmos_snapshot_router_frontend": 1, "cosmos_snapshot_router_fleet": 1, "shoebox_router": 3},
			logCount: 18,
		},
		{
			name: "failed exclusive rewrite is measured and preserves original",
			tag:  "mgmt-agent.logs", failRouter: "resource_snapshot_router",
			records: []record{makeRecord("failed-exclusive", "mgmt-agent", "mgmt-agent-controller", "agent", "kubernetes")},
			matched: map[string]int{"reroute_resource_snapshot_router": 1}, emitted: map[string]int{"resource_snapshot_router": 0}, logCount: 1,
		},
		{
			name: "failed copy is measured despite intentionally retained original",
			tag:  "fleet-controller.logs", failRouter: "cosmos_snapshot_router_fleet",
			records: []record{makeRecord("failed-copy", "fleet", "fleet-controller", "fleet", "cosmos")},
			matched: map[string]int{"copy_cosmos_snapshot_router_fleet": 1}, emitted: map[string]int{"cosmos_snapshot_router_fleet": 0}, logCount: 1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			write := func(name, contents string) {
				require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(contents), 0600))
			}
			var input bytes.Buffer
			for _, r := range tc.records {
				require.NoError(t, json.NewEncoder(&input).Encode(r))
			}
			write("input.jsonl", input.String())
			write("parsers.conf", "[PARSER]\n    Name synthetic\n    Format json\n")
			var filters string
			for _, block := range routingBlocks {
				if tc.failRouter != "" && property(block, "Alias") == "filter."+tc.failRouter {
					// Force the real emitter's rejection path without changing its rules.
					block = regexp.MustCompile(`Emitter_Mem_Buf_Limit\s+\S+`).ReplaceAllString(block, "Emitter_Mem_Buf_Limit 1")
				}
				filters += block + "\n"
			}
			listeners := make([]net.Listener, 2)
			ports := make([]int, 2)
			for i := range listeners {
				listeners[i], err = net.Listen("tcp", "127.0.0.1:0")
				require.NoError(t, err)
				ports[i] = listeners[i].Addr().(*net.TCPAddr).Port
			}
			for _, listener := range listeners {
				require.NoError(t, listener.Close())
			}
			metricsOutput := strings.Replace(data["output-metrics.conf"], "Port  9090", fmt.Sprintf("Port  %d", ports[1]), 1)
			write("fluent-bit.conf", fmt.Sprintf(`[SERVICE]
    Flush 1
    Grace 1
    HTTP_Server On
    HTTP_Listen 127.0.0.1
    HTTP_Port %d
    Parsers_File %s/parsers.conf
    storage.path %s/storage
[INPUT]
    Name tail
    Path %s/input.jsonl
    Tag %s
    Parser synthetic
    Read_From_Head On
%s
%s
[OUTPUT]
    Name stdout
    Match_Regex ^(?!metrics\.).*
    Format json_lines
    Json_Date_Key false
`, ports[0], dir, dir, dir, tc.tag, filters, metricsOutput))
			stdout, err := os.Create(filepath.Join(dir, "stdout"))
			require.NoError(t, err)
			t.Cleanup(func() { _ = stdout.Close() })
			stderr, err := os.Create(filepath.Join(dir, "stderr"))
			require.NoError(t, err)
			t.Cleanup(func() { _ = stderr.Close() })
			name := "routing-metrics-" + filepath.Base(filepath.Dir(dir))
			cmd := exec.CommandContext(t.Context(), runtime, "run", "--rm", "--name", name, "--network=host",
				"--user", "0", "-v", dir+":"+dir+":Z", "--entrypoint", "/fluent-bit/bin/fluent-bit", image,
				"-c", filepath.Join(dir, "fluent-bit.conf"))
			cmd.Stdout, cmd.Stderr = stdout, stderr
			require.NoError(t, cmd.Start())
			t.Cleanup(func() { _ = exec.Command(runtime, "rm", "-f", name).Run() })
			client := &http.Client{Timeout: time.Second}
			fetch := func(port int, path string) string {
				r, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d%s", port, path))
				if err != nil {
					return ""
				}
				defer r.Body.Close()
				b, _ := io.ReadAll(r.Body)
				return string(b)
			}
			metric := func(body, name, label string) int {
				for _, line := range strings.Split(body, "\n") {
					if (strings.HasPrefix(line, name+" ") || strings.HasPrefix(line, name+"{")) && strings.Contains(line, label) {
						fields := strings.Fields(line)
						value, _ := strconv.ParseFloat(fields[len(fields)-1], 64)
						return int(value)
					}
				}
				// Fluent Bit creates the emitted series on its first increment.
				return 0
			}
			var exported, native string
			ready := false
			deadline := time.Now().Add(20 * time.Second)
			for time.Now().Before(deadline) {
				exported, native = fetch(ports[1], "/metrics"), fetch(ports[0], "/api/v2/metrics/prometheus")
				ready = true
				for route, n := range tc.matched {
					ready = ready && metric(exported, "arobit_routing_"+route+"_matched_total", "") == n
				}
				for route, n := range tc.emitted {
					ready = ready && metric(native, "fluentbit_filter_emit_records_total", `name="filter.`+route+`"`) == n
				}
				if ready {
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
			_ = cmd.Process.Signal(syscall.SIGTERM)
			kill := time.AfterFunc(10*time.Second, func() { _ = cmd.Process.Kill() })
			err = cmd.Wait()
			kill.Stop()
			logs, readErr := os.ReadFile(stderr.Name())
			require.NoError(t, readErr)
			require.NoError(t, err, "Fluent Bit diagnostics:\n%s", logs)
			require.True(t, ready, "candidate and emitted counters must match expectations\nexported:\n%s\nnative:\n%s\ndiagnostics:\n%s", exported, native, logs)
			output, err := os.ReadFile(stdout.Name())
			require.NoError(t, err)
			lines := strings.Split(strings.TrimSpace(string(output)), "\n")
			require.Len(t, lines, tc.logCount, "measurement must preserve log and intentional copy counts")
			for _, line := range lines {
				var r record
				require.NoError(t, json.Unmarshal([]byte(line), &r))
				require.NotEmpty(t, r.ID, "generated metrics must not become log records")
			}
		})
	}
}
