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

package framework

import (
	"encoding/json"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestCiliumConflistJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		conflist map[string]any
		wantName string
		plugins  []string
	}{
		{
			name:     "none",
			conflist: CiliumConflistNone,
			wantName: "cilium",
			plugins:  []string{"cilium-cni"},
		},
		{
			name:     "portmap",
			conflist: CiliumConflistPortmap,
			wantName: "portmap",
			plugins:  []string{"cilium-cni", "portmap"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			raw, err := json.Marshal(tt.conflist)
			if err != nil {
				t.Fatalf("json.Marshal(%s) failed: %v", tt.name, err)
			}

			var parsed map[string]any
			if err := json.Unmarshal(raw, &parsed); err != nil {
				t.Fatalf("marshaled %s conflist is not valid JSON: %v\n%s", tt.name, err, raw)
			}

			if got := parsed["cniVersion"]; got != MultusCompatibleCNIVersion {
				t.Errorf("cniVersion = %v, want %s", got, MultusCompatibleCNIVersion)
			}
			if got := parsed["name"]; got != tt.wantName {
				t.Errorf("name = %v, want %s", got, tt.wantName)
			}

			plugins, ok := parsed["plugins"].([]any)
			if !ok {
				t.Fatalf("plugins is %T, want []any", parsed["plugins"])
			}
			if len(plugins) != len(tt.plugins) {
				t.Fatalf("len(plugins) = %d, want %d", len(plugins), len(tt.plugins))
			}
			for i, wantType := range tt.plugins {
				plugin, ok := plugins[i].(map[string]any)
				if !ok {
					t.Fatalf("plugins[%d] is %T, want map[string]any", i, plugins[i])
				}
				if got := plugin["type"]; got != wantType {
					t.Errorf("plugins[%d].type = %v, want %s", i, got, wantType)
				}
			}
		})
	}
}

func TestIsRetryableCiliumNetworkPolicyCreateError(t *testing.T) {
	t.Parallel()

	cnpGVR := schema.GroupResource{Group: "cilium.io", Resource: "ciliumnetworkpolicies"}

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "CRD not found is retryable",
			err:  apierrors.NewNotFound(cnpGVR, "dns-allow-host-apiserver"),
			want: true,
		},
		{
			name: "GVR not in discovery is retryable",
			err: &meta.NoKindMatchError{
				GroupKind:        schema.GroupKind{Group: "cilium.io", Kind: "CiliumNetworkPolicy"},
				SearchedVersions: []string{"v2"},
			},
			want: true,
		},
		{
			name: "AlreadyExists is not retryable",
			err:  apierrors.NewAlreadyExists(cnpGVR, "dns-allow-host-apiserver"),
			want: false,
		},
		{
			name: "Forbidden is not retryable",
			err:  apierrors.NewForbidden(cnpGVR, "dns-allow-host-apiserver", nil),
			want: false,
		},
		{
			name: "Invalid spec is not retryable",
			err:  apierrors.NewInvalid(schema.GroupKind{Group: "cilium.io", Kind: "CiliumNetworkPolicy"}, "dns-allow-host-apiserver", nil),
			want: false,
		},
		{
			name: "Unauthorized is not retryable",
			err:  apierrors.NewUnauthorized("not authorized"),
			want: false,
		},
		{
			name: "BadRequest is not retryable",
			err:  apierrors.NewBadRequest("invalid CiliumNetworkPolicy"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := isRetryableCiliumNetworkPolicyCreateError(tt.err)
			if got != tt.want {
				t.Errorf("isRetryableCiliumNetworkPolicyCreateError() = %v, want %v (err=%v)", got, tt.want, tt.err)
			}
		})
	}
}

func TestIsRetryableCiliumNetworkPolicyCreateError_nil(t *testing.T) {
	t.Parallel()
	if isRetryableCiliumNetworkPolicyCreateError(nil) {
		t.Fatal("nil error must not be treated as retryable")
	}
}
