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
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSwiftPodMitigationDeploymentGate(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("KUBERNETES_SERVICE_PORT", "")
	t.Setenv("AZURE_TOKEN_CREDENTIALS", "AzureCLICredential")
	kubeconfig := filepath.Join(t.TempDir(), "kubeconfig")
	if err := os.WriteFile(kubeconfig, []byte(`apiVersion: v1
kind: Config
clusters:
- name: test
  cluster:
    server: https://127.0.0.1:1
contexts:
- name: test
  context:
    cluster: test
    user: test
current-context: test
users:
- name: test
  user:
    token: test
`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, enabled := range []bool{false, true} {
		name := "disabled"
		if enabled {
			name = "enabled"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			raw := DefaultControllerOptions()
			raw.Namespace = "mgmt-agent"
			raw.Kubeconfig = kubeconfig
			raw.SwiftPodMitigationEnabled = enabled
			validated, err := raw.Validate(ctx)
			if err != nil {
				t.Fatal(err)
			}
			completed, err := validated.Complete(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if (completed.swiftPod != nil) != enabled {
				t.Errorf("SWIFT controller constructed = %t, want %t", completed.swiftPod != nil, enabled)
			}
			if (completed.swiftPodCMInformers != nil) != enabled {
				t.Errorf("SWIFT configuration informer constructed = %t, want %t", completed.swiftPodCMInformers != nil, enabled)
			}
			if completed.nodeHealth == nil || completed.nodeHealthInformers == nil || completed.nodeHealthCMInformers == nil {
				t.Fatal("SWIFT deployment gate must not disable node-health")
			}
			if completed.kubeInformers == nil || completed.clusterWideKubeInformers == nil {
				t.Fatal("SWIFT deployment gate must not disable shared informers")
			}
		})
	}
}
