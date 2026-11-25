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
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"

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

func TestOperationsForLiftsTimedDescendantsToTopLevel(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	later := start.Add(time.Minute)
	ops, err := operationsFor([]pipeline.Operation{
		{OperationType: "Read", StartTimestamp: later.Format(time.RFC3339), Duration: "PT1S"},
		{
			Duration: "PT5S", // No timestamp.
			Children: []pipeline.Operation{
				{OperationType: "Create", StartTimestamp: start.Format(time.RFC3339), Duration: "PT2S"},
			},
		},
		{}, // No timing or descendants.
	})
	require.NoError(t, err)
	require.Equal(t, []ARMOperation{
		{OperationType: "Create", StartTime: start, Duration: 2 * time.Second},
		{OperationType: "Read", StartTime: later, Duration: time.Second},
	}, ops)
}

func TestOperationsForLiftsTimedDescendantsToTimedParent(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// Give siblings the same start time to check that promoted rows keep their position on a tie.
	timestamp := start.Format(time.RFC3339)
	before := pipeline.Operation{OperationType: "Read", StartTimestamp: timestamp, Duration: "PT1S"}
	after := pipeline.Operation{OperationType: "EvaluateDeploymentOutput", StartTimestamp: timestamp, Duration: "PT1S"}
	resource := pipeline.Operation{
		OperationType:  "Create",
		StartTimestamp: timestamp,
		Duration:       "PT0S",
		Resource:       &pipeline.Resource{ResourceType: "Microsoft.KeyVault/vaults", Name: "vault"},
	}
	const deploymentType = "Microsoft.Resources/deployments"
	untimedDeployment := pipeline.Operation{
		StartTimestamp: timestamp, // No duration.
		Resource:       &pipeline.Resource{ResourceType: deploymentType, Name: "untimed-deployment"},
		Children:       []pipeline.Operation{resource},
	}
	timedDeployment := pipeline.Operation{
		OperationType:  "Create",
		StartTimestamp: timestamp,
		Duration:       "PT2S",
		Resource:       &pipeline.Resource{ResourceType: deploymentType, Name: "timed-deployment"},
		Children:       []pipeline.Operation{before, untimedDeployment, after},
	}
	ops, err := operationsFor([]pipeline.Operation{timedDeployment})
	require.NoError(t, err)
	require.Equal(t, []ARMOperation{
		{
			OperationType: "Create",
			StartTime:     start,
			Duration:      2 * time.Second,
			Resource:      &ARMResource{ResourceType: deploymentType, Name: "timed-deployment"},
			Children: []ARMOperation{
				{OperationType: "Read", StartTime: start, Duration: time.Second},
				{
					OperationType: "Create",
					StartTime:     start,
					Resource:      &ARMResource{ResourceType: "Microsoft.KeyVault/vaults", Name: "vault"},
				},
				{OperationType: "EvaluateDeploymentOutput", StartTime: start, Duration: time.Second},
			},
		},
	}, ops)
}

func TestOperationsForRejectsMalformedTiming(t *testing.T) {
	for _, test := range []struct {
		name string
		op   pipeline.Operation
		want string
	}{
		{name: "malformed timestamp without duration", op: pipeline.Operation{StartTimestamp: "invalid"}, want: "failed to parse start date"},
		{name: "malformed duration without timestamp", op: pipeline.Operation{Duration: "invalid"}, want: "failed to parse duration"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := operationsFor([]pipeline.Operation{{Children: []pipeline.Operation{test.op}}})
			require.ErrorContains(t, err, test.want)
		})
	}
}

func TestVisualizeARMLabelsMissingOperationType(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ops, err := operationsFor([]pipeline.Operation{{
		StartTimestamp: start.Format(time.RFC3339),
		Duration:       "PT1S",
		Resource:       &pipeline.Resource{ResourceType: "Microsoft.KeyVault/vaults", ResourceGroup: "rg", Name: "vault"},
	}})
	require.NoError(t, err)

	o := Options{completedOptions: &completedOptions{OutputDir: t.TempDir()}}
	err = o.visualizeARM(logr.NewContext(t.Context(), logr.Discard()), NodeInfo{
		Info:    ExecutionInfo{StartedAt: start},
		Details: ExecutionDetails{ARM: ARMExecutionDetails{Operations: ops}},
	})
	require.NoError(t, err)
	chart, err := os.ReadFile(filepath.Join(o.OutputDir, "arm.html"))
	require.NoError(t, err)
	require.Contains(t, string(chart), `"name":"Unknown operation"`)
	require.Contains(t, string(chart), "Unknown operation Microsoft.KeyVault/vaults rg/vault")
}
