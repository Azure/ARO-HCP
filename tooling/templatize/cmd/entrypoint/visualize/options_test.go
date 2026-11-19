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
	"strings"
	"testing"
	"time"

	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/pipeline"
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

func TestOperationsWithoutTimingAreSkipped(t *testing.T) {
	op := pipeline.Operation{
		OperationType:  "Create",
		StartTimestamp: "2025-01-01T00:00:00Z",
		Duration:       "PT1M",
		Children: []pipeline.Operation{
			{OperationType: "Create", StartTimestamp: "2025-01-01T00:00:10Z", Duration: "PT10S"},
			// ARM omitted the timing; the operation is kept in the timing file for its diagnostics only.
			{ProvisioningState: "Failed", Error: &pipeline.OperationError{Code: "SyntheticCause"}},
			// An untimed nested deployment must not hide its timed operations.
			{Children: []pipeline.Operation{{OperationType: "Read", StartTimestamp: "2025-01-01T00:00:20Z", Duration: "PT1S"}}},
		},
	}
	parsed, err := operationFor(op)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var types []string
	for _, child := range parsed.Children {
		types = append(types, child.OperationType)
	}
	if strings.Join(types, ",") != "Create,Read" {
		t.Fatalf("expected the untimed operation skipped and the untimed deployment's timed child lifted, got %v", types)
	}
	// At the top level and through several untimed levels, timed operations are lifted in order.
	top, err := operationsFor([]pipeline.Operation{
		{Children: []pipeline.Operation{{Children: []pipeline.Operation{{OperationType: "Deep", StartTimestamp: "2025-01-01T00:00:00Z", Duration: "PT1S"}}}}},
		{},
		{OperationType: "Top", StartTimestamp: "2025-01-01T00:00:01Z", Duration: "PT1S"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	types = nil
	for _, op := range top {
		types = append(types, op.OperationType)
	}
	if strings.Join(types, ",") != "Deep,Top" {
		t.Fatalf("expected timed operations lifted from untimed ancestors at the top level, got %v", types)
	}
}
