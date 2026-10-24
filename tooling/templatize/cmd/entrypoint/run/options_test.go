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

package run

import (
	"strings"
	"testing"
)

func TestValidateNewResourceGroupTags(t *testing.T) {
	tests := []struct {
		name      string
		tags      map[string]string
		wantError string
	}{
		{name: "none"},
		{name: "job id", tags: map[string]string{"jobID.aro-hcp-ci.redhat.com": "2097011220782518272"}},
		{name: "blank name", tags: map[string]string{" ": "value"}, wantError: "must not be blank"},
		{name: "persist", tags: map[string]string{"Persist": "true"}, wantError: "--persist-tag"},
		{name: "invalid character", tags: map[string]string{"job/id": "1"}, wantError: "must not contain"},
		{name: "long name", tags: map[string]string{strings.Repeat("k", 513): "1"}, wantError: "exceeds 512"},
		{name: "blank value", tags: map[string]string{"jobID": " "}, wantError: "must have a value"},
		{name: "long value", tags: map[string]string{"jobID": strings.Repeat("v", 257)}, wantError: "exceeds 256"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateNewResourceGroupTags(tt.tags)
			if tt.wantError == "" {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("expected error containing %q, got %v", tt.wantError, err)
			}
		})
	}
}
