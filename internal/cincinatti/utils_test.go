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

package cincinatti

import (
	"errors"
	"testing"

	"github.com/openshift/cluster-version-operator/pkg/cincinnati"
)

func TestIsCincinnatiVersionNotFoundError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "Cincinnati VersionNotFound error",
			err:      &cincinnati.Error{Reason: "VersionNotFound"},
			expected: true,
		},
		{
			name:     "Cincinnati error with different reason",
			err:      &cincinnati.Error{Reason: "InvalidChannel"},
			expected: false,
		},
		{
			name:     "Non-Cincinnati error",
			err:      errors.New("some other error"),
			expected: false,
		},
		{
			name:     "Nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "Wrapped Cincinnati VersionNotFound error",
			err:      errors.Join(errors.New("wrapper"), &cincinnati.Error{Reason: "VersionNotFound"}),
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsCincinnatiVersionNotFoundError(tt.err)
			if result != tt.expected {
				t.Errorf("IsCincinnatiVersionNotFoundError() = %v, expected %v", result, tt.expected)
			}
		})
	}
}
