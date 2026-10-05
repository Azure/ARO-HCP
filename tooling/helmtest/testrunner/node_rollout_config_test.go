// Copyright 2025 Microsoft Corporation
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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	sigsyaml "sigs.k8s.io/yaml"
)

func TestNodeRolloutConfigExtraction(t *testing.T) {
	const flags = ` \
  --enable-conversion-webhook=false \
  --managed-service ARO-HCP \
  --registry-overrides "quay.io/b=mirror/b,quay.io/a=mirror/a" \
  --hypershift-image mirror/hypershift@sha256:1234 \
  --additional-operator-env-vars IMAGE_SHARED_INGRESS_HAPROXY=mirror/haproxy@sha256:5678 \
  --additional-operator-env-vars SHARED_INGRESS_AZURE_PIP_IP_TAGS=tag1 \
  --metrics-set=SRE \
  --enable-cpo-overrides`
	oldInstall := corev1.Container{
		Name:    "install",
		Command: []string{"/bin/sh", "-c", "hypershift install --install-scope=resources" + flags},
	}
	render := corev1.Container{
		Name: "render",
		Command: []string{"/bin/sh", "-c", "set -eu\nhypershift install render" +
			" --outputs=resources --render-sensitive --output-file=/manifests/hypershift.yaml" + flags + "\n"},
	}
	apply := corev1.Container{
		Name: "install",
		Command: []string{"/bin/bash", "-c", `set -euo pipefail
echo 'hypershift install --not-an-install-command'
kubectl create --dry-run=client --validate=false -f /manifests/rendered.yaml -o json
kubectl apply --server-side --force-conflicts --field-manager=hypershift -f /manifests/apply.json
`},
	}
	renderArgs := render.DeepCopy()
	renderArgs.Args = renderArgs.Command[2:]
	renderArgs.Command = renderArgs.Command[:2]
	withNeighbors := render.DeepCopy()
	withNeighbors.Command[2] = apply.Command[2] + render.Command[2] + apply.Command[2]
	want := &NodeRolloutConfig{
		RegistryOverrides:         []string{"quay.io/a=mirror/a", "quay.io/b=mirror/b"},
		SharedIngressHAProxyImage: "mirror/haproxy@sha256:5678",
		AdditionalInstallArgs:     []string{"--enable-cpo-overrides", "--metrics-set=SRE"},
	}
	tests := []struct {
		name       string
		init       []corev1.Container
		containers []corev1.Container
		jobName    string
		wantError  string
	}{
		{name: "old install", containers: []corev1.Container{oldInstall}},
		{name: "init render and regular apply", init: []corev1.Container{render}, containers: []corev1.Container{apply}},
		{name: "script in args", init: []corev1.Container{*renderArgs}, containers: []corev1.Container{apply}},
		{name: "not the first regular container", containers: []corev1.Container{apply, render}},
		{name: "ignore neighboring commands", init: []corev1.Container{*withNeighbors}, containers: []corev1.Container{apply}},
		{name: "no render command", containers: []corev1.Container{apply}, wantError: "found 0"},
		{name: "no containers", wantError: "found 0"},
		{name: "wrong job", jobName: "other-hypershift", containers: []corev1.Container{render}, wantError: "Job not found"},
		{name: "ambiguous containers", init: []corev1.Container{render}, containers: []corev1.Container{oldInstall}, wantError: "found 2"},
		{name: "ambiguous script", containers: []corev1.Container{{Name: "render", Command: []string{"/bin/sh", "-c", render.Command[2] + render.Command[2]}}}, wantError: "found 2"},
		{name: "commands on same line", containers: []corev1.Container{{Name: "render", Command: []string{"/bin/sh", "-c", "hypershift install render; hypershift install render"}}}, wantError: "unsupported shell expression"},
		{name: "piped command", containers: []corev1.Container{{Name: "render", Command: []string{"/bin/sh", "-c", "hypershift install render | kubectl apply --server-side -f -"}}}, wantError: "unsupported shell expression"},
		{name: "non shell argument", containers: []corev1.Container{{Name: "other", Command: []string{"echo", "-c", render.Command[2]}}}, wantError: "found 0"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			name := tc.jobName
			if name == "" {
				name = "install-hypershift"
			}
			job := batchv1.Job{
				TypeMeta:   metav1.TypeMeta{APIVersion: "batch/v1", Kind: "Job"},
				ObjectMeta: metav1.ObjectMeta{Name: name},
				Spec: batchv1.JobSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
					InitContainers: tc.init,
					Containers:     tc.containers,
				}}},
			}
			manifest, err := sigsyaml.Marshal(job)
			require.NoError(t, err)
			crds := job.DeepCopy()
			crds.Name = "install-hypershift-crds"
			crds.Spec.Template.Spec = corev1.PodSpec{Containers: []corev1.Container{{
				Name:    "install",
				Command: []string{"/bin/sh", "-c", "hypershift install --install-scope=crds --registry-overrides wrong=wrong"},
			}}}
			crdsManifest, err := sigsyaml.Marshal(crds)
			require.NoError(t, err)
			// A matching command in the CRD Job must neither supply nor obscure the resource install config.
			config, err := extractNodeRolloutConfig(string(crdsManifest) + "---\n" + string(manifest))
			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
				assert.Nil(t, config)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, want, config)
		})
	}
}

func TestCollectUnknownFlags(t *testing.T) {
	tests := []struct {
		name     string
		script   string
		expected []string
	}{
		{
			name: "one flag per line with continuations",
			script: "hypershift install \\\n" +
				"  --enable-conversion-webhook=false \\\n" +
				"  --metrics-set=SRE \\\n" +
				"  --enable-cpo-overrides",
			expected: []string{
				"--metrics-set=SRE",
				"--enable-cpo-overrides",
			},
		},
		{
			name:   "multiple flags on a single line",
			script: "hypershift install --enable-conversion-webhook=false --metrics-set=SRE --enable-cpo-overrides",
			expected: []string{
				"--metrics-set=SRE",
				"--enable-cpo-overrides",
			},
		},
		{
			name: "space-separated value",
			script: "hypershift install \\\n" +
				"  --managed-service ARO-HCP \\\n" +
				"  --unknown-flag somevalue",
			expected: []string{
				"--unknown-flag somevalue",
			},
		},
		{
			name: "classified env var is skipped",
			script: "hypershift install \\\n" +
				"  --additional-operator-env-vars SHARED_INGRESS_AZURE_PIP_IP_TAGS=tag1",
			expected: nil,
		},
		{
			name: "unclassified env var is collected",
			script: "hypershift install \\\n" +
				"  --additional-operator-env-vars NEW_ENV=value",
			expected: []string{
				"--additional-operator-env-vars NEW_ENV=value",
			},
		},
		{
			name: "all flags classified returns nil",
			script: "hypershift install \\\n" +
				"  --enable-conversion-webhook=false \\\n" +
				"  --managed-service ARO-HCP \\\n" +
				"  --platform-monitoring=None",
			expected: nil,
		},
		{
			name: "boolean flag without value",
			script: "hypershift install \\\n" +
				"  --enable-conversion-webhook=false \\\n" +
				"  --enable-cpo-overrides",
			expected: []string{
				"--enable-cpo-overrides",
			},
		},
		{
			name: "quoted value for classified flag",
			script: "hypershift install \\\n" +
				`  --registry-overrides "quay.io/a=arohcp.azurecr.io/a,quay.io/b=arohcp.azurecr.io/b"`,
			expected: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := collectUnknownFlags(tc.script)
			assert.Equal(t, tc.expected, result)
		})
	}
}
