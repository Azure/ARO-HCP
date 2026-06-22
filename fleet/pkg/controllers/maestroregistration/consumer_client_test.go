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

package maestroregistration

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetConsumer_DNS1123Validation(t *testing.T) {
	tests := []struct {
		name            string
		consumerName    string
		wantErrContains string
	}{
		{
			name:            "uppercase rejected",
			consumerName:    "INVALID",
			wantErrContains: "invalid consumer name",
		},
		{
			name:            "special characters rejected",
			consumerName:    "name'; DROP TABLE consumers; --",
			wantErrContains: "invalid consumer name",
		},
		{
			name:            "dots rejected",
			consumerName:    "not.a.label",
			wantErrContains: "invalid consumer name",
		},
		{
			name:            "leading hyphen rejected",
			consumerName:    "-leading",
			wantErrContains: "invalid consumer name",
		},
		{
			name:            "empty string rejected",
			consumerName:    "",
			wantErrContains: "invalid consumer name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &maestroConsumerClient{api: nil}

			_, err := client.GetConsumer(context.Background(), tt.consumerName)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErrContains)
		})
	}
}
