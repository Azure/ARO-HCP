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

package agent

import (
	"strconv"
	"testing"
)

func TestParseTokenCount(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	for _, tc := range []struct {
		value   string
		want    int
		wantErr bool
	}{
		{value: ""},
		{value: "0"},
		{value: "128000", want: 128000},
		{value: "128k", want: 128000},
		{value: "200K", want: 200000},
		{value: " 900k ", want: 900000},
		{value: strconv.Itoa(maxInt), want: maxInt},
		{value: strconv.Itoa(maxInt/1000) + "k", want: (maxInt / 1000) * 1000},
		{value: strconv.Itoa(maxInt/1000+1) + "k", wantErr: true},
		{value: "999999999999999999999999999999", wantErr: true},
		{value: "-1", wantErr: true},
		{value: "1.5k", wantErr: true},
		{value: "k", wantErr: true},
		{value: "1m", wantErr: true},
	} {
		t.Run(tc.value, func(t *testing.T) {
			got, err := ParseTokenCount(tc.value)
			if (err != nil) != tc.wantErr || got != tc.want {
				t.Fatalf("ParseTokenCount(%q) = %d, %v; want %d, wantErr %v", tc.value, got, err, tc.want, tc.wantErr)
			}
		})
	}
}
