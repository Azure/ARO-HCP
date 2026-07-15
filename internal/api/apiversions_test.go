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

func TestAPIVersionFromOptions(t *testing.T) {
	tests := []struct {
		name    string
		options []string
		want    APIVersion
	}{
		{"extracts version", []string{APIVersionOption(APIVersionV20260630Preview)}, APIVersionV20260630Preview},
		{"no version option", []string{"some-other-option"}, APIVersion("")},
		{"empty options", []string{}, APIVersion("")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := APIVersionFromOptions(tt.options); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAPIVersionComparisons(t *testing.T) {
	tests := []struct {
		name  string
		v     APIVersion
		other APIVersion
		eq    bool
		ne    bool
		lt    bool
		le    bool
		gt    bool
		ge    bool
	}{
		{
			name: "equal versions",
			v:    APIVersionV20260630Preview, other: APIVersionV20260630Preview,
			eq: true, ne: false, lt: false, le: true, gt: false, ge: true,
		},
		{
			name: "less than",
			v:    APIVersionV20240610Preview, other: APIVersionV20260630Preview,
			eq: false, ne: true, lt: true, le: true, gt: false, ge: false,
		},
		{
			name: "greater than",
			v:    APIVersionV20260630Preview, other: APIVersionV20240610Preview,
			eq: false, ne: true, lt: false, le: false, gt: true, ge: true,
		},
		{
			name: "middle version v2025 vs v2024",
			v:    APIVersionV20251223Preview, other: APIVersionV20240610Preview,
			eq: false, ne: true, lt: false, le: false, gt: true, ge: true,
		},
		{
			name: "middle version v2025 vs v2026",
			v:    APIVersionV20251223Preview, other: APIVersionV20260630Preview,
			eq: false, ne: true, lt: true, le: true, gt: false, ge: false,
		},
		{
			name: "stable same date ranks after preview",
			v:    APIVersion("2026-06-30"), other: APIVersionV20260630Preview,
			eq: false, ne: true, lt: false, le: false, gt: true, ge: true,
		},
		{
			name: "preview same date ranks before stable",
			v:    APIVersionV20260630Preview, other: APIVersion("2026-06-30"),
			eq: false, ne: true, lt: true, le: true, gt: false, ge: false,
		},
		{
			name: "stable later date vs preview",
			v:    APIVersion("2026-07-01"), other: APIVersionV20260630Preview,
			eq: false, ne: true, lt: false, le: false, gt: true, ge: true,
		},
		{
			name: "stable earlier date vs preview",
			v:    APIVersion("2026-06-29"), other: APIVersionV20260630Preview,
			eq: false, ne: true, lt: true, le: true, gt: false, ge: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.v.EQ(tt.other); got != tt.eq {
				t.Errorf("EQ: got %v, want %v", got, tt.eq)
			}
			if got := tt.v.NE(tt.other); got != tt.ne {
				t.Errorf("NE: got %v, want %v", got, tt.ne)
			}
			if got := tt.v.LT(tt.other); got != tt.lt {
				t.Errorf("LT: got %v, want %v", got, tt.lt)
			}
			if got := tt.v.LE(tt.other); got != tt.le {
				t.Errorf("LE: got %v, want %v", got, tt.le)
			}
			if got := tt.v.GT(tt.other); got != tt.gt {
				t.Errorf("GT: got %v, want %v", got, tt.gt)
			}
			if got := tt.v.GE(tt.other); got != tt.ge {
				t.Errorf("GE: got %v, want %v", got, tt.ge)
			}
		})
	}
}
