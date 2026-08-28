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

package verifiers

import (
	"strings"
	"testing"

	"github.com/blang/semver/v4"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestNodeVersionInMinor(t *testing.T) {
	tests := []struct {
		name            string
		nodeName        string
		criVersion      string
		kubeletVersion  string
		expectedVersion string
		wantEmpty       bool
		wantSubstring   string
	}{
		{
			name:            "old rhaos format matching version",
			nodeName:        "node-1",
			criVersion:      "cri-o://1.33.9-3.rhaos4.20.git0abcdef.el9",
			kubeletVersion:  "v1.33.6+abc123",
			expectedVersion: "4.20",
			wantEmpty:       true,
		},
		{
			name:            "old rhaos format wrong minor",
			nodeName:        "node-2",
			criVersion:      "cri-o://1.33.9-3.rhaos4.19.git0abcdef.el9",
			kubeletVersion:  "v1.32.6+abc123",
			expectedVersion: "4.20",
			wantEmpty:       false,
			wantSubstring:   "version 4.19 not in same minor as expected 4.20",
		},
		{
			name:            "new clean CRI-O format with matching kubelet version (OCP 4.22 = k8s 1.35)",
			nodeName:        "node-3",
			criVersion:      "cri-o://1.35.6",
			kubeletVersion:  "v1.35.6+abc123",
			expectedVersion: "4.22",
			wantEmpty:       true,
		},
		{
			name:            "new clean CRI-O format with wrong kubelet minor",
			nodeName:        "node-4",
			criVersion:      "cri-o://1.35.6",
			kubeletVersion:  "v1.34.6+abc123",
			expectedVersion: "4.22",
			wantEmpty:       false,
			wantSubstring:   "KubeletVersion v1.34.6+abc123 has minor 34, expected 35 for OCP 4.22",
		},
		{
			name:            "new clean CRI-O format with unparseable kubelet version",
			nodeName:        "node-5",
			criVersion:      "cri-o://1.35.6",
			kubeletVersion:  "not-a-version",
			expectedVersion: "4.22",
			wantEmpty:       false,
			wantSubstring:   "cannot parse KubeletVersion",
		},
		{
			name:            "old rhaos format OCP 4.14 = k8s 1.27",
			nodeName:        "node-6",
			criVersion:      "cri-o://1.27.5-3.rhaos4.14.gitabc.el9",
			kubeletVersion:  "v1.27.5+abc",
			expectedVersion: "4.14",
			wantEmpty:       true,
		},
		{
			name:            "rhaos tag takes priority over kubelet fallback",
			nodeName:        "node-8",
			criVersion:      "cri-o://1.33.9-3.rhaos4.20.git0abcdef.el9",
			kubeletVersion:  "v1.99.0+bogus",
			expectedVersion: "4.20",
			wantEmpty:       true,
		},
		{
			name:            "new CRI-O format OCP 4.23 = k8s 1.36",
			nodeName:        "node-7",
			criVersion:      "cri-o://1.36.0",
			kubeletVersion:  "v1.36.0+xyz",
			expectedVersion: "4.23",
			wantEmpty:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: tt.nodeName,
				},
				Status: corev1.NodeStatus{
					NodeInfo: corev1.NodeSystemInfo{
						ContainerRuntimeVersion: tt.criVersion,
						KubeletVersion:          tt.kubeletVersion,
					},
				},
			}

			expectedSemver, err := semver.ParseTolerant(tt.expectedVersion)
			if err != nil {
				t.Fatalf("failed to parse expectedVersion %q: %v", tt.expectedVersion, err)
			}

			v := verifyNodePoolUpgrade{
				expectedVersion: tt.expectedVersion,
			}

			got := v.nodeVersionInMinor(node, expectedSemver)

			if tt.wantEmpty && got != "" {
				t.Errorf("expected empty result, got %q", got)
			}
			if !tt.wantEmpty && got == "" {
				t.Errorf("expected non-empty result containing %q, got empty", tt.wantSubstring)
			}
			if !tt.wantEmpty && tt.wantSubstring != "" {
				if !strings.Contains(got, tt.wantSubstring) {
					t.Errorf("expected result to contain %q, got %q", tt.wantSubstring, got)
				}
			}
		})
	}
}

func TestOcpToK8sMinorOffset(t *testing.T) {
	// Verify the well-known offset between OCP and Kubernetes minor versions.
	knownMappings := []struct {
		ocpMinor uint64
		k8sMinor uint64
	}{
		{14, 27}, // OCP 4.14 = k8s 1.27
		{15, 28}, // OCP 4.15 = k8s 1.28
		{16, 29}, // OCP 4.16 = k8s 1.29
		{17, 30}, // OCP 4.17 = k8s 1.30
		{18, 31}, // OCP 4.18 = k8s 1.31
		{19, 32}, // OCP 4.19 = k8s 1.32
		{20, 33}, // OCP 4.20 = k8s 1.33
		{21, 34}, // OCP 4.21 = k8s 1.34
		{22, 35}, // OCP 4.22 = k8s 1.35
	}

	for _, m := range knownMappings {
		got := m.ocpMinor + ocpToK8sMinorOffset
		if got != m.k8sMinor {
			t.Errorf("OCP 4.%d: expected k8s minor %d, got %d", m.ocpMinor, m.k8sMinor, got)
		}
	}
}
