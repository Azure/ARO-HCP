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

package visualize

import (
	"testing"
	"time"
)

func TestParseISO8601Duration(t *testing.T) {
	for _, test := range []struct {
		input    string
		expected time.Duration
	}{
		{
			input:    "PT2M57.4255386S",
			expected: (time.Minute * 2) + (time.Nanosecond * 57425538600),
		},
		{
			input:    "PT11M19.4587032S",
			expected: (time.Minute * 11) + (time.Nanosecond * 19458703200),
		},
		{
			input:    "PT0.2284242S",
			expected: time.Nanosecond * 228424200,
		},
		{
			input:    "PT1H2M29.7820439S",
			expected: (time.Hour * 1) + (time.Minute * 2) + (time.Nanosecond * 29782043900),
		},
	} {
		t.Run(test.input, func(t *testing.T) {
			got, err := parseISO8601Duration(test.input)
			if err != nil {
				t.Error(err)
			}
			if got != test.expected {
				t.Errorf("expected: %v, got: %v", test.expected, got)
			}
		})
	}
}
