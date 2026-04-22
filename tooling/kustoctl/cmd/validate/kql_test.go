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

package validate

import (
	"testing"
)

func TestSplitCommands(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "empty input",
			input:    "",
			expected: nil,
		},
		{
			name:  "single command",
			input: ".create-merge table foo (col1: string)",
			expected: []string{
				".create-merge table foo (col1: string)",
			},
		},
		{
			name: "two simple commands",
			input: `.create-merge table foo (col1: string)

.create-merge table bar (col2: int)`,
			expected: []string{
				".create-merge table foo (col1: string)",
				".create-merge table bar (col2: int)",
			},
		},
		{
			name: "multiline table definition with ingestion mapping",
			input: `.create-merge table backendLogs (
  timestamp: datetime,
  log: dynamic,
  environment: string
)

.create-or-alter table backendLogs ingestion json mapping 'ingestionMapping'
` + "```" + `
[
  {"column":"log","Properties":{"path":"$.log.log"}},
  {"column":"timestamp","Properties":{"path":"$.timestamp"}}
]
` + "```",
			expected: []string{
				".create-merge table backendLogs (\n  timestamp: datetime,\n  log: dynamic,\n  environment: string\n)",
				".create-or-alter table backendLogs ingestion json mapping 'ingestionMapping'\n```\n[\n  {\"column\":\"log\",\"Properties\":{\"path\":\"$.log.log\"}},\n  {\"column\":\"timestamp\",\"Properties\":{\"path\":\"$.timestamp\"}}\n]\n```",
			},
		},
		{
			name: "aksEvents pattern with function and policy",
			input: `.create-merge table rawAksEvents (
    records: dynamic
)
with (docstring = "temp table")

.alter-merge table rawAksEvents policy retention softdelete = 0s

.create-or-alter function
with (docstring = 'process events')
rawAksEventsTransform() {
  rawAksEvents
  | mv-expand (records)
}

.alter table aksEvents policy update @'[{"IsEnabled": true}]'`,
			expected: []string{
				".create-merge table rawAksEvents (\n    records: dynamic\n)\nwith (docstring = \"temp table\")",
				".alter-merge table rawAksEvents policy retention softdelete = 0s",
				".create-or-alter function\nwith (docstring = 'process events')\nrawAksEventsTransform() {\n  rawAksEvents\n  | mv-expand (records)\n}",
				".alter table aksEvents policy update @'[{\"IsEnabled\": true}]'",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitCommands(tt.input)
			if len(got) != len(tt.expected) {
				t.Fatalf("got %d commands, want %d\ngot: %v", len(got), len(tt.expected), got)
			}
			for i := range got {
				if got[i] != tt.expected[i] {
					t.Errorf("command %d:\ngot:  %q\nwant: %q", i, got[i], tt.expected[i])
				}
			}
		})
	}
}
