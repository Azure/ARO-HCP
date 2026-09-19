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
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"helm.sh/helm/v4/pkg/chart/common"
	"helm.sh/helm/v4/pkg/chart/loader"
	"helm.sh/helm/v4/pkg/engine"

	appsv1 "k8s.io/api/apps/v1"

	"sigs.k8s.io/yaml"
)

func TestSystemReserved(t *testing.T) {
	chart, err := loader.Load("../../../mgmt-fixes/deploy/kubelet-ds")
	require.NoError(t, err)
	rendered, err := engine.Render(chart, common.Values{"Values": map[string]any{"enabled": true}})
	require.NoError(t, err)
	require.Len(t, rendered, 1)
	var ds appsv1.DaemonSet
	for _, manifest := range rendered {
		// Strict decoding also catches duplicate updateStrategy keys.
		require.NoError(t, yaml.UnmarshalStrict([]byte(manifest), &ds))
		for _, line := range strings.Split(manifest, "\n") {
			require.Equal(t, strings.TrimRight(line, " \t"), line, "rendered manifest must not have trailing whitespace")
		}
	}
	require.Equal(t, int32(1), ds.Spec.UpdateStrategy.RollingUpdate.MaxUnavailable.IntVal)
	require.Equal(t, int32(30), ds.Spec.MinReadySeconds)
	require.Equal(t, int32(0), ds.Spec.UpdateStrategy.RollingUpdate.MaxSurge.IntVal)
	require.Len(t, ds.Spec.Template.Spec.Containers, 1)
	require.Len(t, ds.Spec.Template.Spec.InitContainers, 1)
	command := ds.Spec.Template.Spec.InitContainers[0].Command
	require.Equal(t, []string{"nsenter", "--target", "1", "--mount", "--uts", "--ipc", "--net", "--pid", "--", "sh", "-c"}, command[:len(command)-1])
	script := command[len(command)-1]
	mv, err := exec.LookPath("mv")
	require.NoError(t, err)
	require.Contains(t, script, "set -eu")
	require.NotContains(t, script, "sleep infinity")
	syntaxOutput, err := exec.CommandContext(t.Context(), "sh", "-n", "-c", script).CombinedOutput()
	require.NoError(t, err, "rendered script syntax: %s", syntaxOutput)
	main := ds.Spec.Template.Spec.Containers[0]
	require.Equal(t, []string{"sleep", "infinity"}, main.Command)
	require.NotNil(t, main.ReadinessProbe)
	probe := main.ReadinessProbe.Exec.Command
	require.Equal(t, command[:9], probe[:9], "readiness must check host health through nsenter")
	require.Equal(t, []string{"curl", "--fail", "--silent", "--show-error", "--noproxy", "*", "--connect-timeout", "2", "--max-time", "5", "http://127.0.0.1:10248/healthz"}, probe[9:])
	require.Equal(t, int32(1), main.ReadinessProbe.FailureThreshold)
	require.Greater(t, main.ReadinessProbe.TimeoutSeconds, int32(5))
	require.True(t, *ds.Spec.Template.Spec.InitContainers[0].SecurityContext.Privileged)
	require.True(t, *main.SecurityContext.Privileged)
	require.True(t, ds.Spec.Template.Spec.HostPID)

	const fresh = "--kube-reserved=cpu=100m,memory=1Gi,pid=500"
	const legacy = "--system-reserved=cpu=3000m,memory=7550Mi,pid=1000"
	const small = "--system-reserved=cpu=1500m,memory=1888Mi,pid=1000"
	const config = "# AKS defaults\nUNRELATED=\"$(exit 42)\"\nKUBELET_FLAGS=\"--node-labels=a=b " + fresh + " --v=2\"\n"
	// Derived from AgentBaker ensureKubelet at bb4a739f9699efb521f7b2c1bf8f063dc669606e.
	const aksReservation = "--kube-reserved=cpu=100m,memory=1638Mi"
	const aksConfig = "KUBELET_FLAGS=--address=0.0.0.0 " + aksReservation + " --max-pods=110 --eviction-hard=memory.available<100Mi,nodefs.available<10%,nodefs.inodesFree<5% --eviction-minimum-reclaim=imagefs.available=2Gi --cloud-provider=external\n" +
		"KUBELET_REGISTER_SCHEDULABLE=true\nNETWORK_POLICY=azure\nKUBELET_IMAGE=\nKUBELET_NODE_LABELS=kubernetes.azure.com/agentpool=workers,aro-hcp.azure.com/role=worker\n"
	tests := []struct {
		name        string
		cpu         string
		memory      string
		config      string
		want        string
		fail        bool
		failCommand string
		failFirst   string
		marker      string
	}{
		{name: "AgentBaker fresh", config: aksConfig, want: strings.ReplaceAll(aksConfig, aksReservation, small)},
		{name: "AgentBaker legacy", config: strings.ReplaceAll(aksConfig, aksReservation, legacy), want: strings.ReplaceAll(aksConfig, aksReservation, small)},
		{name: "AgentBaker unquoted final argument", config: "KUBELET_FLAGS=--address=0.0.0.0 " + aksReservation + "\n", want: "KUBELET_FLAGS=--address=0.0.0.0 " + small + "\n"},
		{name: "recover failed reload", config: aksConfig, want: strings.ReplaceAll(aksConfig, aksReservation, small), failFirst: "daemon-reload"},
		{name: "recover failed restart", config: aksConfig, want: strings.ReplaceAll(aksConfig, aksReservation, small), failFirst: "restart kubelet"},
		{name: "recover failed health", config: aksConfig, want: strings.ReplaceAll(aksConfig, aksReservation, small), failFirst: "health"},
		{name: "interrupted after replacement", config: strings.ReplaceAll(aksConfig, aksReservation, small), want: strings.ReplaceAll(aksConfig, aksReservation, small), marker: "pending"},
		{name: "interrupted before replacement", config: aksConfig, want: strings.ReplaceAll(aksConfig, aksReservation, small), marker: "pending"},
		{name: "unknown marker content", config: aksConfig, marker: "unknown", fail: true},
		{name: "unknown marker permissions", config: aksConfig, marker: "permissions", fail: true},
		{name: "symlink marker", config: aksConfig, marker: "symlink", fail: true},
		{name: "fresh baseline", cpu: "32", memory: "268435456", config: config, want: strings.ReplaceAll(config, fresh, legacy)},
		{name: "fresh 16 vCPU 64 GiB", config: config, want: strings.ReplaceAll(config, fresh, small)},
		{name: "fresh 8 vCPU 32 GiB", cpu: "8", memory: "33554432", config: config, want: strings.ReplaceAll(config, fresh, "--system-reserved=cpu=750m,memory=944Mi,pid=1000")},
		{name: "mixed 8 vCPU 256 GiB", cpu: "8", memory: "268435456", config: config, want: strings.ReplaceAll(config, fresh, "--system-reserved=cpu=750m,memory=7550Mi,pid=1000")},
		{name: "mixed 32 vCPU 32 GiB", cpu: "32", memory: "33554432", config: config, want: strings.ReplaceAll(config, fresh, "--system-reserved=cpu=3000m,memory=944Mi,pid=1000")},
		{name: "round fractional CPU upward", cpu: "1", config: config, want: strings.ReplaceAll(config, fresh, "--system-reserved=cpu=94m,memory=1888Mi,pid=1000")},
		{name: "OS visible memory", cpu: "32", memory: "267386880", config: config, want: strings.ReplaceAll(config, fresh, "--system-reserved=cpu=3000m,memory=7521Mi,pid=1000")},
		{name: "legacy migration", config: strings.ReplaceAll(config, fresh, legacy), want: strings.ReplaceAll(config, fresh, small)},
		{name: "legacy baseline unchanged", cpu: "32", memory: "268435456", config: strings.ReplaceAll(config, fresh, legacy), want: strings.ReplaceAll(config, fresh, legacy)},
		{name: "already proportional", config: strings.ReplaceAll(config, fresh, small), want: strings.ReplaceAll(config, fresh, small)},
		{name: "quoted final argument", config: "KUBELET_FLAGS=\"--v=2 " + fresh + "\"\n", want: "KUBELET_FLAGS=\"--v=2 " + small + "\"\n"},
		{name: "single quotes and whitespace", config: "  KUBELET_FLAGS='\t" + fresh + "'  \n", want: "  KUBELET_FLAGS='\t" + small + "'  \n"},
		{name: "unquoted assignment", config: "KUBELET_FLAGS=" + fresh + "\n", want: "KUBELET_FLAGS=" + small + "\n"},
		{name: "preserve other reservations", config: "KUBELET_FLAGS=\"--kube-reserved=ephemeral-storage=5Gi,cpu=100m,hugepages-2Mi=4Mi,memory=1Gi --system-reserved-cgroup=/system.slice --enforce-node-allocatable=pods\"\n", want: "KUBELET_FLAGS=\"" + small + ",ephemeral-storage=5Gi,hugepages-2Mi=4Mi --system-reserved-cgroup=/system.slice --enforce-node-allocatable=pods\"\n"},
		{name: "commented reservation ignored", config: "# " + legacy + "\n" + config, want: "# " + legacy + "\n" + strings.ReplaceAll(config, fresh, small)},
		{name: "zero CPU", cpu: "0", config: config, fail: true},
		{name: "negative CPU", cpu: "-1", config: config, fail: true},
		{name: "invalid CPU", cpu: "no CPU", config: config, fail: true},
		{name: "fractional CPU", cpu: "1.5", config: config, fail: true},
		{name: "CPU overflow", cpu: "99999999999999999999", config: config, fail: true},
		{name: "zero memory", memory: "0", config: config, fail: true},
		{name: "invalid memory", memory: "bad", config: config, fail: true},
		{name: "memory overflow", memory: "99999999999999999999", config: config, fail: true},
		{name: "duplicate memory", memory: "67108864 kB\nMemTotal: 67108864", config: config, fail: true},
		{name: "missing flag", config: "KUBELET_FLAGS=\"--v=2\"\n", fail: true},
		{name: "only commented flag", config: "# " + fresh + "\n", fail: true},
		{name: "both categories", config: "KUBELET_FLAGS=\"" + fresh + " " + legacy + "\"\n", fail: true},
		{name: "duplicate flag", config: "KUBELET_FLAGS=\"" + fresh + " " + fresh + "\"\n", fail: true},
		{name: "separate assignments", config: "FIRST='" + fresh + "'\nSECOND='" + legacy + "'\n", fail: true},
		{name: "overridden assignment", config: config + "KUBELET_FLAGS=\"--v=2\"\n", fail: true},
		{name: "assignment inside multiline quote", config: "OTHER='\nKUBELET_FLAGS=\"" + fresh + "\"\n'\n", fail: true},
		{name: "duplicate reservation key", config: strings.ReplaceAll(config, fresh, fresh+",cpu=200m"), fail: true},
		{name: "empty flag", config: "KUBELET_FLAGS=\"--kube-reserved=\"\n", fail: true},
		{name: "unterminated quote", config: "KUBELET_FLAGS=\"" + fresh + "\n", fail: true},
		{name: "escaped multiline", config: "KUBELET_FLAGS=\"" + fresh + " \\\n--v=2\"\n", fail: true},
		{name: "not an assignment", config: fresh + "\n", fail: true},
		{name: "embedded flag", config: "KUBELET_FLAGS=\"--other=" + fresh + "\"\n", fail: true},
		{name: "nested quoting", config: "KUBELET_FLAGS=\"'" + fresh + "'\"\n", fail: true},
		{name: "shell expansion", config: "KUBELET_FLAGS=\"$(exit 42) " + fresh + "\"\n", fail: true},
		{name: "backtick expansion", config: "KUBELET_FLAGS=\"`exit 42` " + fresh + "\"\n", fail: true},
		{name: "invalid shell elsewhere", config: config + "OTHER=\"unterminated\n", fail: true},
		{name: "capacity command failure", config: config, failCommand: "getconf", fail: true},
		{name: "copy failure", config: config, failCommand: "cp", fail: true},
		{name: "comparison failure", config: config, failCommand: "cmp", fail: true},
		{name: "replacement failure", config: config, failCommand: "mv", fail: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "bin")
			require.NoError(t, os.Mkdir(bin, 0700))
			if tt.cpu == "" {
				tt.cpu = "16"
			}
			if tt.memory == "" {
				tt.memory = "67108864"
			}
			paths := map[string]string{
				"/etc/default/kubelet":                    filepath.Join(dir, "kubelet"),
				"/proc/meminfo":                           filepath.Join(dir, "meminfo"),
				"/usr/local/bin/node-exporter-startup.sh": filepath.Join(dir, "node-exporter"),
			}
			sandboxed := script
			for original, sandbox := range paths {
				sandboxed = strings.ReplaceAll(sandboxed, original, sandbox)
			}
			kubelet := paths["/etc/default/kubelet"]
			require.NoError(t, os.WriteFile(kubelet, []byte(tt.config), 0640))
			pending := kubelet + ".system-reserved-restart-pending"
			if tt.marker != "" {
				if tt.marker == "symlink" {
					require.NoError(t, os.Symlink(kubelet, pending))
				} else {
					content := ""
					mode := os.FileMode(0600)
					if tt.marker == "unknown" {
						content = "do not overwrite\n"
					}
					if tt.marker == "permissions" {
						mode = 0644
					}
					require.NoError(t, os.WriteFile(pending, []byte(content), mode))
				}
			}
			require.NoError(t, os.WriteFile(paths["/proc/meminfo"], fmt.Appendf(nil, "MemTotal: %s kB\n", tt.memory), 0600))
			require.NoError(t, os.WriteFile(paths["/usr/local/bin/node-exporter-startup.sh"], []byte("--collector.netclass.netlink\n"), 0600))
			log := filepath.Join(dir, "systemctl.log")
			require.NoError(t, os.WriteFile(log, nil, 0600))
			for name, body := range map[string]string{
				"getconf":   "[ \"$*\" = _NPROCESSORS_ONLN ] || exit 1\nprintf '%s\\n' \"$TEST_CPU\"\n",
				"systemctl": "printf '%s\\n' \"$*\" >> \"$TEST_SYSTEMCTL_LOG\"\n[ -f \"$TEST_PENDING\" ]\n[ \"$*\" != \"$TEST_FAIL\" ]\n",
				"curl":      "printf '%s\\n' \"$*\" >> \"$TEST_CURL_LOG\"\n[ \"$TEST_FAIL\" != health ]\n",
				"mv":        "[ -f \"$TEST_PENDING\" ]\n[ \"$(stat -c '%a:%s' \"$TEST_PENDING\")\" = 600:0 ]\nexec \"$TEST_MV\" \"$@\"\n",
				"sleep":     "[ \"$*\" = infinity ]\n",
				"nsenter":   "exit 99\n",
			} {
				require.NoError(t, os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nset -eu\n"+body), 0700))
			}
			if tt.failCommand != "" {
				require.NoError(t, os.WriteFile(filepath.Join(bin, tt.failCommand), []byte("#!/bin/sh\nexit 2\n"), 0700))
			}
			wantCalls := ""
			curlLog := filepath.Join(dir, "curl.log")
			for run := range 3 {
				before, err := os.Stat(kubelet)
				require.NoError(t, err)
				ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
				cmd := exec.CommandContext(ctx, "sh", "-c", sandboxed)
				failStep := ""
				if run == 0 {
					failStep = tt.failFirst
				}
				cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "TEST_CPU="+tt.cpu, "TEST_SYSTEMCTL_LOG="+log,
					"TEST_FAIL="+failStep, "TEST_PENDING="+pending, "TEST_CURL_LOG="+curlLog, "TEST_MV="+mv)
				output, err := cmd.CombinedOutput()
				cancel()
				if tt.fail || failStep != "" {
					require.Error(t, err, "script should reject input: %s", output)
				} else {
					require.NoError(t, err, "script output: %s", output)
				}
				actual, err := os.ReadFile(kubelet)
				require.NoError(t, err)
				want := tt.want
				if tt.fail {
					want = tt.config
				}
				require.Equal(t, want, string(actual), "host configuration after run %d", run)
				calls, err := os.ReadFile(log)
				require.NoError(t, err)
				if !tt.fail && (run == 0 && (tt.want != tt.config || tt.marker == "pending") || run == 1 && tt.failFirst != "") {
					wantCalls += "daemon-reload\n"
					if failStep != "daemon-reload" {
						wantCalls += "restart kubelet\n"
					}
				}
				require.Equal(t, wantCalls, string(calls), "restart only for changed config or pending recovery")
				info, err := os.Stat(kubelet)
				require.NoError(t, err)
				require.Equal(t, os.FileMode(0640), info.Mode().Perm())
				if run > 0 || tt.fail || tt.config == tt.want {
					require.True(t, os.SameFile(before, info), "unchanged configuration must not be replaced")
					require.Equal(t, before.ModTime(), info.ModTime(), "unchanged configuration must not be written")
				}
				markerRemains := failStep != "" || tt.failCommand == "mv" || tt.fail && tt.marker != ""
				markerInfo, markerErr := os.Lstat(pending)
				if markerRemains {
					require.NoError(t, markerErr)
					switch tt.marker {
					case "symlink":
						require.NotZero(t, markerInfo.Mode()&os.ModeSymlink)
					case "unknown":
						content, err := os.ReadFile(pending)
						require.NoError(t, err)
						require.Equal(t, "do not overwrite\n", string(content))
					case "permissions":
						require.Equal(t, os.FileMode(0644), markerInfo.Mode().Perm())
					default:
						require.Equal(t, os.FileMode(0600), markerInfo.Mode().Perm())
						require.Zero(t, markerInfo.Size())
					}
				} else {
					require.True(t, os.IsNotExist(markerErr), "no pending restart after successful health check")
				}
				temporary, err := filepath.Glob(kubelet + ".*")
				require.NoError(t, err)
				if markerRemains {
					require.Equal(t, []string{pending}, temporary, "only the persistent marker may remain")
				} else {
					require.Empty(t, temporary, "temporary files must be cleaned up")
				}
				if !tt.fail && failStep == "" {
					curlCalls, err := os.ReadFile(curlLog)
					require.NoError(t, err)
					require.Contains(t, string(curlCalls), "--fail --silent --show-error --noproxy * --connect-timeout 2 --max-time 5 --retry 60 --retry-delay 2 --retry-max-time 120 --retry-all-errors http://127.0.0.1:10248/healthz\n")
				}
			}
			// Execute the rendered readiness command after the nsenter boundary,
			// using the curl stub so no host namespace or network is accessed.
			for _, healthFailure := range []bool{false, true} {
				probeCommand := exec.CommandContext(t.Context(), filepath.Join(bin, probe[9]), probe[10:]...)
				failure := ""
				if healthFailure {
					failure = "health"
				}
				probeCommand.Env = append(os.Environ(), "TEST_FAIL="+failure, "TEST_CURL_LOG="+curlLog)
				output, err := probeCommand.CombinedOutput()
				if healthFailure {
					require.Error(t, err, "readiness must fail for unhealthy kubelet: %s", output)
				} else {
					require.NoError(t, err, "readiness must pass for healthy kubelet: %s", output)
				}
			}
		})
	}
}
