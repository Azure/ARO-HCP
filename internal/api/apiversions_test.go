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

package api

import "testing"

func TestAPIVersionComparisons(t *testing.T) {
	v2024 := []string{APIVersionOption(APIVersionV20240610Preview)}
	v2025 := []string{APIVersionOption(APIVersionV20251223Preview)}
	v2026 := []string{APIVersionOption(APIVersionV20260630Preview)}
	noVersion := []string{"some-other-option"}

	tests := []struct {
		name    string
		fn      func([]string, string) bool
		options []string
		version string
		want    bool
	}{
		// EQ
		{"EQ same", APIVersionEQ, v2026, APIVersionV20260630Preview, true},
		{"EQ different", APIVersionEQ, v2024, APIVersionV20260630Preview, false},
		{"EQ no version", APIVersionEQ, noVersion, APIVersionV20260630Preview, false},

		// NE
		{"NE same", APIVersionNE, v2026, APIVersionV20260630Preview, false},
		{"NE different", APIVersionNE, v2024, APIVersionV20260630Preview, true},
		{"NE no version", APIVersionNE, noVersion, APIVersionV20260630Preview, false},

		// LT
		{"LT less", APIVersionLT, v2024, APIVersionV20260630Preview, true},
		{"LT equal", APIVersionLT, v2026, APIVersionV20260630Preview, false},
		{"LT greater", APIVersionLT, v2026, APIVersionV20240610Preview, false},
		{"LT no version", APIVersionLT, noVersion, APIVersionV20260630Preview, false},

		// LE
		{"LE less", APIVersionLE, v2024, APIVersionV20260630Preview, true},
		{"LE equal", APIVersionLE, v2026, APIVersionV20260630Preview, true},
		{"LE greater", APIVersionLE, v2026, APIVersionV20240610Preview, false},
		{"LE no version", APIVersionLE, noVersion, APIVersionV20260630Preview, false},

		// GT
		{"GT less", APIVersionGT, v2024, APIVersionV20260630Preview, false},
		{"GT equal", APIVersionGT, v2026, APIVersionV20260630Preview, false},
		{"GT greater", APIVersionGT, v2026, APIVersionV20240610Preview, true},
		{"GT no version", APIVersionGT, noVersion, APIVersionV20260630Preview, false},

		// GE
		{"GE less", APIVersionGE, v2024, APIVersionV20260630Preview, false},
		{"GE equal", APIVersionGE, v2026, APIVersionV20260630Preview, true},
		{"GE greater", APIVersionGE, v2026, APIVersionV20240610Preview, true},
		{"GE no version", APIVersionGE, noVersion, APIVersionV20260630Preview, false},

		// middle version
		{"GE v2025 >= v2024", APIVersionGE, v2025, APIVersionV20240610Preview, true},
		{"LT v2025 < v2026", APIVersionLT, v2025, APIVersionV20260630Preview, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.fn(tt.options, tt.version); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}
