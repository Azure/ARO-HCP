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
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

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
