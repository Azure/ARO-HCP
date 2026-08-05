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

package v20260901preview

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/azureapi/v20260901preview/generated"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestNewSSHPublicKeys(t *testing.T) {
	validKey := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIApY9GkD07ixdNdt3J8cCKdYx5bwqkE903Zs4+YjDMj+ user@host"

	tests := []struct {
		name     string
		input    []coreapi.SshPublicKey
		expected []*generated.InlineSSHPublicKey
	}{
		{
			name:     "nil input returns nil",
			input:    nil,
			expected: nil,
		},
		{
			name:     "empty slice returns empty slice",
			input:    []coreapi.SshPublicKey{},
			expected: []*generated.InlineSSHPublicKey{},
		},
		{
			name: "single key sets inline kind and key",
			input: []coreapi.SshPublicKey{
				{Key: validKey},
			},
			expected: []*generated.InlineSSHPublicKey{
				{
					Kind: ptr.To(sshPublicKeyKindInline),
					Key:  ptr.To(validKey),
				},
			},
		},
		{
			name: "multiple keys preserve order",
			input: []coreapi.SshPublicKey{
				{Key: validKey},
				{Key: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJj/ylWtVtAQRw7GkVNI8M7+NUG6qBXfjEALlD8EP6Pg"},
			},
			expected: []*generated.InlineSSHPublicKey{
				{
					Kind: ptr.To(sshPublicKeyKindInline),
					Key:  ptr.To(validKey),
				},
				{
					Kind: ptr.To(sshPublicKeyKindInline),
					Key:  ptr.To("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJj/ylWtVtAQRw7GkVNI8M7+NUG6qBXfjEALlD8EP6Pg"),
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := newSSHPublicKeys(tt.input)
			if diff := cmp.Diff(tt.expected, got, cmp.Comparer(func(a, b *string) bool {
				if a == nil || b == nil {
					return a == b
				}
				return *a == *b
			})); diff != "" {
				t.Fatalf("newSSHPublicKeys() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestNormalizeSSHPublicKeys(t *testing.T) {
	fldPath := field.NewPath("properties", "nodeSshPublicKeys")
	validKey := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIApY9GkD07ixdNdt3J8cCKdYx5bwqkE903Zs4+YjDMj+ user@host"

	tests := []struct {
		name         string
		input        []*generated.InlineSSHPublicKey
		expected     []coreapi.SshPublicKey
		expectErrors []utils.ExpectedError
	}{
		{
			name:         "nil input clears output",
			input:        nil,
			expected:     nil,
			expectErrors: []utils.ExpectedError{},
		},
		{
			name:         "empty slice clears to empty slice",
			input:        []*generated.InlineSSHPublicKey{},
			expected:     []coreapi.SshPublicKey{},
			expectErrors: []utils.ExpectedError{},
		},
		{
			name: "inline key is normalized",
			input: []*generated.InlineSSHPublicKey{
				{
					Kind: ptr.To(sshPublicKeyKindInline),
					Key:  ptr.To(validKey),
				},
			},
			expected: []coreapi.SshPublicKey{
				{Key: validKey},
			},
			expectErrors: []utils.ExpectedError{},
		},
		{
			name: "null entry is rejected",
			input: []*generated.InlineSSHPublicKey{
				nil,
				{
					Kind: ptr.To(sshPublicKeyKindInline),
					Key:  ptr.To(validKey),
				},
			},
			expected: []coreapi.SshPublicKey{
				{Key: validKey},
			},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.nodeSshPublicKeys[0]", Message: "must not be null"},
			},
		},
		{
			name: "missing kind is rejected",
			input: []*generated.InlineSSHPublicKey{
				{
					Key: ptr.To(validKey),
				},
			},
			expected: []coreapi.SshPublicKey{},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.nodeSshPublicKeys[0].kind", Message: "kind is required"},
			},
		},
		{
			name: "unsupported kind is rejected",
			input: []*generated.InlineSSHPublicKey{
				{
					Kind: ptr.To("vault"),
					Key:  ptr.To(validKey),
				},
			},
			expected: []coreapi.SshPublicKey{},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.nodeSshPublicKeys[0].kind", Message: "Unsupported value"},
			},
		},
		{
			name: "missing key is accepted for downstream validation",
			input: []*generated.InlineSSHPublicKey{
				{
					Kind: ptr.To(sshPublicKeyKindInline),
				},
			},
			expected: []coreapi.SshPublicKey{
				{Key: ""},
			},
			expectErrors: []utils.ExpectedError{},
		},
		{
			name: "valid and invalid entries report errors for invalid only",
			input: []*generated.InlineSSHPublicKey{
				{
					Kind: ptr.To(sshPublicKeyKindInline),
					Key:  ptr.To(validKey),
				},
				{
					Kind: ptr.To("vault"),
					Key:  ptr.To(validKey),
				},
			},
			expected: []coreapi.SshPublicKey{
				{Key: validKey},
			},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.nodeSshPublicKeys[1].kind", Message: "Unsupported value"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out []coreapi.SshPublicKey
			errs := normalizeSSHPublicKeys(fldPath, tt.input, &out)
			utils.VerifyErrorsMatch(t, tt.expectErrors, errs)
			require.Equal(t, tt.expected, out)
		})
	}
}
