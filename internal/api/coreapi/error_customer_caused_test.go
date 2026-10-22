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

package coreapi

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsCustomerCaused(t *testing.T) {
	tests := []struct {
		code string
		want bool
	}{
		{CloudErrorCodeQuotaExceeded, true},

		// Everything else is the system's problem until explicitly listed.
		{CloudErrorCodeInternalServerError, false},
		{CloudErrorCodeDeadlineExceeded, false},
		{CloudErrorCodeServiceUnavailable, false},
		{CloudErrorCodeInvalidRequestContent, false},
		{"", false},
		{"SomethingNew", false},
	}
	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			require.Equal(t, tt.want, IsCustomerCaused(tt.code))
		})
	}
}
