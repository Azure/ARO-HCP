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

// Opt in with the pinned FLUENT_BIT_IMAGE; see swift-recorder/README.md.
func TestSwiftRecorderFluentBit(t *testing.T) {
	image := os.Getenv("FLUENT_BIT_IMAGE")
	if image == "" {
		t.Skip("set FLUENT_BIT_IMAGE to execute Fluent Bit")
	}
	runtime := os.Getenv("CONTAINER_RUNTIME")
	if runtime == "" {
		runtime = "podman"
	}
	cases, err := getCustomTestCases("../../../observability/arobit/deploy")
	require.NoError(t, err)
	for _, clusterType := range []string{"mgmt", "svc"} {
		t.Run(clusterType, func(t *testing.T) {
			var manifest string
			for _, tc := range cases {
				if tc.Name != "helmtest-mdsd-and-kusto-enabled-"+clusterType {
					continue
				}
				manifest, err = runTest(t.Context(), &internal.Settings{ConfigPath: "../../../config/rendered/dev/dev/westus3.yaml"}, tc)
				require.NoError(t, err)
			}
			require.NotEmpty(t, manifest, "render the existing arobit test case")
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
			require.NotEmpty(t, data, "rendered forwarder ConfigMap")
			property := func(block, key string) string {
				match := regexp.MustCompile(`(?m)^\s*` + key + `\s+(\S+)\s*$`).FindStringSubmatch(block)
				if len(match) == 0 {
					return ""
				}
				return match[1]
			}
			var filters string
			for _, block := range strings.Split("\n"+data["filter.conf"], "\n[")[1:] {
				// Synthetic input starts after CRI/multiline assembly and Kubernetes
				// enrichment. Exercise all remaining production filters, including re-tags.
				if name := property(block, "Name"); name != "kubernetes" && name != "multiline" {
					filters += "[" + block
				}
			}
			var outputMatch string
			for _, block := range strings.Split("\n"+data["output.conf"], "\n[")[1:] {
				if property(block, "Table_Name") == "containerLogs" && property(block, "Match") == "kubernetes.*" {
					outputMatch = property(block, "Match")
				}
			}
			require.NotEmpty(t, outputMatch, "containerLogs must receive the recorder tag")
			mapping, err := os.ReadFile("../../../dev-infrastructure/modules/logs/kusto/tables/containerLogs.kql")
			require.NoError(t, err)
			require.Contains(t, string(mapping), `{"column":"log","Properties":{"path":"$.log.log"}}`)

			dir := t.TempDir()
			write := func(name, contents string) {
				require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(contents), 0600))
			}
			for name, contents := range data {
				write(name, strings.ReplaceAll(contents, "/forwarder/etc", dir))
			}
			write("input-parser.conf", "[PARSER]\n    Name synthetic\n    Format json\n")
			write("fluent-bit.conf", fmt.Sprintf(`[SERVICE]
    Flush 1
    Grace 3
    Parsers_File %s/parsers_custom.conf
    Parsers_File %s/input-parser.conf
    storage.path %s/storage
[INPUT]
    Name tail
    Tag kubernetes.var.log.containers.swift-recorder-test
    Path %s/input.jsonl
    Parser synthetic
    Read_from_Head On
    Exit_On_Eof On
%s
[OUTPUT]
    Name stdout
    Match %s
    Format json_lines
    Json_Date_Key false
`, dir, dir, dir, dir, strings.ReplaceAll(filters, "/forwarder/etc", dir), outputMatch))

			const structured = `{"time":"2026-01-02T03:04:05.123Z","level":"INFO","source":{"function":"record","file":"recorder.go","line":579},"msg":"SWIFT startup record","controller":"swift-startup-recorder","node_name":"test-node","cluster_name":"payload-cluster","region":"payload-region","environment":"payload-environment","boot_id":"test-boot","capture_mode":"slow","future_field":{"retain":true},"record":{"event":"snapshot","snapshot":{"state":{"links":[{"name":"eth0","mtu":1500}],"routes":[],"optional":null}}}}`
			logs := []string{structured, "plain startup diagnostic", `{"record":`, structured}
			var input bytes.Buffer
			var expected []map[string]any
			for i, log := range logs {
				container := "swift-recorder"
				if i == len(logs)-1 {
					container = "other-container"
				}
				record := map[string]any{
					"log": log, "stream": "stdout", "time": "2026-01-02T03:04:05.456Z",
					"environment": "outer-environment", "region": "outer-region", "cluster": "outer-cluster",
					"kubernetes": map[string]any{"container_name": container, "namespace_name": "swift-recorder", "pod_name": "swift-recorder-test", "host": "test-node", "labels": map[string]any{"app": "swift-recorder"}},
				}
				require.NoError(t, json.NewEncoder(&input).Encode(record))
				expected = append(expected, record)
			}
			write("input.jsonl", input.String())
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			name := "swift-recorder-" + filepath.Base(filepath.Dir(dir))
			cmd := exec.CommandContext(ctx, runtime, "run", "--rm", "--name", name, "--network=none",
				"--user", "0", "-v", dir+":"+dir+":Z", "--entrypoint", "/fluent-bit/bin/fluent-bit", image,
				"-c", filepath.Join(dir, "fluent-bit.conf"))
			t.Cleanup(func() { _ = exec.Command(runtime, "rm", "-f", name).Run() })
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			require.NoError(t, cmd.Run(), "Fluent Bit diagnostics:\n%s", &stderr)
			var got []map[string]any
			for _, line := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
				var record map[string]any
				require.NoError(t, json.Unmarshal([]byte(line), &record), "stdout must contain JSON only: %s", &stderr)
				got = append(got, record)
			}
			require.Len(t, got, len(expected), "no dropped or duplicated records")
			var payload map[string]any
			require.NoError(t, json.Unmarshal([]byte(structured), &payload))
			expected[0]["log"] = payload
			// azure_kusto wraps each Fluent Bit record in $.log. Equality proves
			// $.log.log is an object with nested record data, not a JSON string,
			// while retaining all outer metadata and leaving other containers alone.
			require.ElementsMatch(t, expected, got)
		})
	}
}
