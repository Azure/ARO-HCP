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
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"sigs.k8s.io/yaml"

	"github.com/Azure/ARO-HCP/tooling/helmtest/internal"
)

// Opt-in Linux integration test; see azure_vnet_integration_test.md for commands.
func TestAzureVNetFluentBit(t *testing.T) {
	binary, image := os.Getenv("FLUENT_BIT_BINARY"), os.Getenv("FLUENT_BIT_IMAGE")
	if binary == "" && image == "" {
		t.Skip("set FLUENT_BIT_BINARY or FLUENT_BIT_IMAGE to execute Fluent Bit")
	}
	require.False(t, binary != "" && image != "", "choose binary or container execution")
	cases, err := getCustomTestCases("../../../observability/arobit/deploy")
	require.NoError(t, err)
	var manifest string
	for _, tc := range cases {
		if tc.Name != "helmtest-mdsd-and-kusto-enabled-svc" {
			continue
		}
		tc.TestData = map[string]any{
			"arobit": map[string]any{"kusto": map[string]any{"enabled": true, "environmentName": "test-environment"}},
			"region": "test-region", "svc": map[string]any{"aks": map[string]any{"name": "test-cluster"}},
		}
		manifest, err = runTest(t.Context(), &internal.Settings{ConfigPath: "../../../config/rendered/dev/dev/westus3.yaml"}, tc)
		require.NoError(t, err)
	}
	require.NotEmpty(t, manifest, "render the existing arobit service-cluster test case")
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
	selectBlocks := func(config, key, value string) string {
		var selected string
		for _, block := range strings.Split("\n"+config, "\n[")[1:] {
			if regexp.MustCompile(`(?m)^\s*` + key + `\s+` + regexp.QuoteMeta(value) + `\s*$`).MatchString(block) {
				selected += "[" + block
			}
		}
		require.NotEmpty(t, selected, "%s %s in rendered configuration", key, value)
		return selected
	}
	input := selectBlocks(data["input.conf"], "Tag", "azure-vnet.logs")
	require.Regexp(t, `(?m)^\s*Path\s+/var/log/azure-vnet\.log\s*$`, input, "only the active path, never a rotation glob")
	parserName := regexp.MustCompile(`(?m)^\s*Parser\s+(\S+)`).FindStringSubmatch(input)
	require.Len(t, parserName, 2)
	parser := selectBlocks(data["parsers_custom.conf"], "Name", parserName[1])
	filters := selectBlocks(data["filter.conf"], "Match", "azure-vnet.logs")

	dir := t.TempDir()
	write := func(name, contents string) {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(contents), 0600))
	}
	appendLine := func(name, line string) {
		f, err := os.OpenFile(filepath.Join(dir, name), os.O_APPEND|os.O_WRONLY, 0600)
		require.NoError(t, err)
		_, err = fmt.Fprintln(f, line)
		require.NoError(t, err)
		require.NoError(t, f.Close())
	}
	// Only relocate paths. Keep the chart's parser, filter, cursor and rotation settings.
	input = strings.ReplaceAll(input, "/var/log/", dir+"/")
	write("parsers.conf", parser)
	write("fluent-bit.conf", fmt.Sprintf(`[SERVICE]
    Flush 1
    Grace 1
    Log_Level debug
    Parsers_File %s/parsers.conf
    storage.path %s/storage
%s
%s
[OUTPUT]
    Name stdout
    Match azure-vnet.logs
    Format json_lines
    Json_Date_Key eventTime
    Json_Date_Format double
`, dir, dir, input, filters))
	write("azure-vnet.log", "preexisting-active-must-not-replay\n")
	write("azure-vnet.log.1", "numeric-backup-must-not-appear\n")
	write("azure-vnet-2026-01-02T03-04-05.log", "timestamp-backup-must-not-appear\n")
	out, err := os.Create(filepath.Join(dir, "output.jsonl"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = out.Close() })
	waitFor := func(description string, condition func() bool) {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			if condition() {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for %s", description)
	}
	start := func() func() {
		log, err := os.CreateTemp(dir, "fluent-bit-*.log")
		require.NoError(t, err)
		cmd := exec.CommandContext(t.Context(), binary, "-c", filepath.Join(dir, "fluent-bit.conf"))
		cmd.Env = append(os.Environ(), "NODE_NAME=test-node")
		if image != "" {
			runtime := os.Getenv("CONTAINER_RUNTIME")
			if runtime == "" {
				runtime = "podman"
			}
			name := "azure-vnet-" + filepath.Base(log.Name())
			cmd = exec.CommandContext(t.Context(), runtime, "run", "--rm", "--name", name, "--network=none",
				"--user", "0", "-v", dir+":"+dir+":Z", "-e", "NODE_NAME=test-node",
				"--entrypoint", "/fluent-bit/bin/fluent-bit", image, "-c", filepath.Join(dir, "fluent-bit.conf"))
			t.Cleanup(func() { _ = exec.Command(runtime, "rm", "-f", name).Run() })
		}
		cmd.Stdout, cmd.Stderr = out, log
		require.NoError(t, cmd.Start())
		stopped := false
		stop := func() {
			if stopped {
				return
			}
			stopped = true
			_ = cmd.Process.Signal(syscall.SIGTERM)
			kill := time.AfterFunc(10*time.Second, func() { _ = cmd.Process.Kill() })
			err := cmd.Wait()
			kill.Stop()
			_ = log.Close()
			logs, readErr := os.ReadFile(log.Name())
			require.NoError(t, readErr)
			if t.Failed() || err != nil {
				t.Logf("Fluent Bit diagnostics:\n%s", logs)
			}
			require.NoError(t, err, "Fluent Bit must shut down cleanly")
		}
		t.Cleanup(stop)
		waitFor("active file watch", func() bool {
			logs, err := os.ReadFile(log.Name())
			require.NoError(t, err)
			return strings.Contains(string(logs), "inotify_fs_add():") && strings.Contains(string(logs), dir+"/azure-vnet.log")
		})
		return stop
	}
	records := func() []map[string]any {
		raw, err := os.ReadFile(out.Name())
		require.NoError(t, err)
		var result []map[string]any
		lines := strings.Split(string(raw), "\n")
		for _, line := range lines[:len(lines)-1] { // Ignore a partially written final line while polling.
			if line == "" {
				continue
			}
			var record map[string]any
			require.NoError(t, json.Unmarshal([]byte(line), &record), "stdout must contain JSON records only")
			result = append(result, record)
		}
		return result
	}
	waitCount := func(n int) {
		waitFor(fmt.Sprintf("%d records", n), func() bool { return len(records()) >= n })
		require.Len(t, records(), n, "unexpected replay or backup records")
	}
	stop := start()
	const timestamp = "2026-01-02T03:04:05.123Z"
	appendLine("azure-vnet.log", `{"ts":"`+timestamp+`","level":"error","msg":"structured","error":"synthetic failure","component":"cni","count":7,"stdinData":{"synthetic":true},"args":["synthetic"],"environment":"spoof","region":"spoof","cluster":"spoof","hostname":"spoof"}`)
	appendLine("azure-vnet.log", "plain synthetic text")
	appendLine("azure-vnet.log", `{"msg":"malformed"`)
	appendLine("azure-vnet.log", `{"stdinData":{"synthetic":"must-not-export"},"msg":"truncated"`)
	appendLine("azure-vnet.log", `"args":["synthetic-must-not-export"]`)
	waitCount(3)
	structured := records()[0]
	for key, value := range map[string]any{"ts": timestamp, "level": "error", "msg": "structured", "error": "synthetic failure", "component": "cni", "count": float64(7)} {
		require.Equal(t, value, structured[key], "retained field %s", key)
	}
	parsed, err := time.Parse(time.RFC3339Nano, timestamp)
	require.NoError(t, err)
	require.InDelta(t, float64(parsed.UnixMilli())/1000, structured["eventTime"], 0.00001, "event timestamp preserves milliseconds")

	require.NoError(t, os.Rename(filepath.Join(dir, "azure-vnet.log"), filepath.Join(dir, "azure-vnet.log.2")))
	write("azure-vnet.log", "immediate-new-active\n")
	// Exercise writes made before the unchanged Refresh_Interval discovers the file.
	time.Sleep(6 * time.Second)
	appendLine("azure-vnet.log", "new-active")
	waitCount(5)
	appendLine("azure-vnet.log.2", "late-old-inode")
	appendLine("azure-vnet.log.1", "numeric-backup-append-must-not-appear")
	appendLine("azure-vnet-2026-01-02T03-04-05.log", "timestamp-backup-append-must-not-appear")
	waitCount(6)
	stop()
	// An unread line written while stopped distinguishes cursor resume from seeking EOF.
	appendLine("azure-vnet.log", "written-while-stopped")
	stop = start()
	waitCount(7)
	appendLine("azure-vnet.log", "after-restart")
	waitCount(8)
	time.Sleep(6 * time.Second) // Observe another scan/flush for duplicate or backup ingestion.
	stop()
	got := records()
	require.Len(t, got, 8, "restart must not replay consumed lines")
	var messages []string
	for _, record := range got {
		for key, value := range map[string]string{"environment": "test-environment", "region": "test-region", "cluster": "test-cluster", "hostname": "test-node"} {
			require.Equal(t, value, record[key], "trusted metadata %s", key)
		}
		for _, key := range []string{"stdinData", "args", "log"} {
			require.NotContains(t, record, key)
		}
		require.NotEmpty(t, record["sourcePath"])
		messages = append(messages, fmt.Sprint(record["msg"]))
	}
	require.ElementsMatch(t, []string{"structured", "plain synthetic text", `{"msg":"malformed"`, "immediate-new-active", "new-active", "late-old-inode", "written-while-stopped", "after-restart"}, messages)
}
