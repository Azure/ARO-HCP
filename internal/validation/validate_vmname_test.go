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

package validation

import (
	"testing"
)

func TestIsValidAzureVMName(t *testing.T) {
	tests := []struct {
		name     string
		vmName   string
		expected bool
	}{
		// Valid cases
		{
			name:     "single alphanumeric character",
			vmName:   "a",
			expected: true,
		},
		{
			name:     "single digit",
			vmName:   "1",
			expected: true,
		},
		{
			name:     "simple alphanumeric name",
			vmName:   "testvm",
			expected: true,
		},
		{
			name:     "name with hyphen",
			vmName:   "test-vm",
			expected: true,
		},
		{
			name:     "name with underscore",
			vmName:   "test_vm",
			expected: true,
		},
		{
			name:     "name with period",
			vmName:   "test.vm",
			expected: true,
		},
		{
			name:     "name with all allowed characters",
			vmName:   "test-vm_01.master",
			expected: true,
		},
		{
			name:     "name ending with underscore",
			vmName:   "test_vm_",
			expected: true,
		},
		{
			name:     "name ending with digit",
			vmName:   "testvm01",
			expected: true,
		},
		{
			name:     "64 character name",
			vmName:   "a234567890123456789012345678901234567890123456789012345678901234",
			expected: true,
		},
		{
			name:     "uppercase alphanumeric",
			vmName:   "TestVM",
			expected: true,
		},
		{
			name:     "mixed case with special chars",
			vmName:   "Test-VM_01",
			expected: true,
		},

		// Invalid cases
		{
			name:     "empty string",
			vmName:   "",
			expected: false,
		},
		{
			name:     "starts with hyphen",
			vmName:   "-testvm",
			expected: false,
		},
		{
			name:     "starts with underscore",
			vmName:   "_testvm",
			expected: false,
		},
		{
			name:     "starts with period",
			vmName:   ".testvm",
			expected: false,
		},
		{
			name:     "ends with hyphen",
			vmName:   "testvm-",
			expected: false,
		},
		{
			name:     "ends with period",
			vmName:   "testvm.",
			expected: false,
		},
		{
			name:     "contains space",
			vmName:   "test vm",
			expected: false,
		},
		{
			name:     "contains special character @",
			vmName:   "test@vm",
			expected: false,
		},
		{
			name:     "contains special character #",
			vmName:   "test#vm",
			expected: false,
		},
		{
			name:     "too long (65 characters)",
			vmName:   "a2345678901234567890123456789012345678901234567890123456789012345",
			expected: false,
		},
		{
			name:     "only hyphen",
			vmName:   "-",
			expected: false,
		},
		{
			name:     "only underscore",
			vmName:   "_",
			expected: false,
		},
		{
			name:     "only period",
			vmName:   ".",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsValidAzureVMName(tt.vmName)
			if result != tt.expected {
				t.Errorf("IsValidAzureVMName(%q) = %v, expected %v", tt.vmName, result, tt.expected)
			}
		})
	}
}
