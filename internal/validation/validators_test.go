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

package validation

import (
	"context"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/api/operation"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/utils/ptr"
)

func TestHostPort(t *testing.T) {
	ctx := context.Background()
	op := operation.Operation{Type: operation.Create}
	fldPath := field.NewPath("target")

	tests := []struct {
		name        string
		value       *string
		expectError bool
		errContains string
	}{
		{
			name:  "nil value accepted",
			value: nil,
		},
		{
			name:  "empty value accepted",
			value: ptr.To(""),
		},
		{
			name:  "valid DNS host with port",
			value: ptr.To("maestro.example.com:8090"),
		},
		{
			name:  "valid short DNS host with port",
			value: ptr.To("maestro:8090"),
		},
		{
			name:  "valid IPv4 with port",
			value: ptr.To("10.0.0.1:8090"),
		},
		{
			name:  "valid IPv6 with port",
			value: ptr.To("[::1]:8090"),
		},
		{
			name:        "missing port rejected",
			value:       ptr.To("maestro.example.com"),
			expectError: true,
			errContains: "must be host:port",
		},
		{
			name:        "empty host rejected",
			value:       ptr.To(":8090"),
			expectError: true,
			errContains: "host must not be empty",
		},
		{
			name:        "underscore in host rejected",
			value:       ptr.To("not_valid:8090"),
			expectError: true,
			errContains: "invalid host",
		},
		{
			name:        "uppercase in host rejected",
			value:       ptr.To("NOT-VALID:8090"),
			expectError: true,
			errContains: "invalid host",
		},
		{
			name:        "trailing dot in host rejected",
			value:       ptr.To("invalid.:8090"),
			expectError: true,
			errContains: "invalid host",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := HostPort(ctx, op, fldPath, tt.value, nil)
			if tt.expectError {
				if len(errs) == 0 {
					t.Errorf("expected error containing %q, got none", tt.errContains)
					return
				}
				found := false
				for _, e := range errs {
					if strings.Contains(e.Error(), tt.errContains) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected error containing %q, got %v", tt.errContains, errs)
				}
			} else {
				if len(errs) != 0 {
					t.Errorf("expected no errors, got %v", errs)
				}
			}
		})
	}
}
