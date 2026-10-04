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

package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestKSMResourceOptions(t *testing.T) {
	for _, tt := range []struct {
		name                    string
		args                    []string
		cpu, memory, limit, err string
	}{
		{name: "defaults", cpu: "10m", memory: "64Mi", limit: "256Mi"},
		{name: "overrides", args: []string{"--hcp-kube-state-metrics-cpu-request=25m", "--hcp-kube-state-metrics-memory-request=96Mi", "--hcp-kube-state-metrics-memory-limit=384Mi"}, cpu: "25m", memory: "96Mi", limit: "384Mi"},
		{name: "invalid", args: []string{"--hcp-kube-state-metrics-cpu-request=invalid"}, err: "--hcp-kube-state-metrics-cpu-request"},
		{name: "empty", args: []string{"--hcp-kube-state-metrics-memory-request="}, err: "--hcp-kube-state-metrics-memory-request"},
		{name: "negative", args: []string{"--hcp-kube-state-metrics-memory-request=-1Mi"}, err: "must be positive"},
		{name: "zero", args: []string{"--hcp-kube-state-metrics-memory-limit=0"}, err: "must be positive"},
		{name: "limit below request", args: []string{"--hcp-kube-state-metrics-memory-limit=32Mi"}, err: "must not exceed"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			o := DefaultControllerOptions()
			command := &cobra.Command{}
			if err := o.BindFlags(command); err != nil {
				t.Fatal(err)
			}
			if err := command.ParseFlags(tt.args); err != nil {
				t.Fatal(err)
			}
			o.Namespace = "mgmt-agent"
			validated, err := o.Validate(t.Context())
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) {
					t.Fatalf("Validate() error = %v, want %s", err, tt.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			r := validated.ksmResources
			if r.Requests.Cpu().String() != tt.cpu || r.Requests.Memory().String() != tt.memory || r.Limits.Memory().String() != tt.limit {
				t.Fatalf("unexpected resources: %+v", r)
			}
		})
	}
}
