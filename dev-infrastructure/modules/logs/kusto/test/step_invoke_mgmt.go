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

package kustotest

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// invokeMgmtStep runs a management command and compares the result.
//
// Step directory contents:
//   - 00-key.json: {"database": "DatabaseName", "command": ".show functions | project Name, Folder"}
//   - expected-result.json (optional): array of expected row objects
//   - expected-error.txt (optional): expected error substring
type invokeMgmtStep struct {
	id            stepID
	database      string
	command       string
	expectedRows  []map[string]any
	hasExpected   bool
	expectedError string
}

type mgmtKey struct {
	Database string `json:"database"`
	Command  string `json:"command"`
}

func newInvokeMgmtStep(id stepID, stepDir fs.FS) (*invokeMgmtStep, error) {
	keyContent, err := fs.ReadFile(stepDir, "00-key.json")
	if err != nil {
		return nil, fmt.Errorf("missing 00-key.json: %w", err)
	}
	var key mgmtKey
	if err := json.Unmarshal(keyContent, &key); err != nil {
		return nil, fmt.Errorf("failed to parse 00-key.json: %w", err)
	}
	if len(key.Database) == 0 {
		return nil, fmt.Errorf("00-key.json must specify 'database'")
	}
	if len(key.Command) == 0 {
		return nil, fmt.Errorf("00-key.json must specify 'command'")
	}

	step := &invokeMgmtStep{
		id:       id,
		database: key.Database,
		command:  key.Command,
	}

	if resultContent, err := fs.ReadFile(stepDir, "expected-result.json"); err == nil {
		if err := json.Unmarshal(resultContent, &step.expectedRows); err != nil {
			return nil, fmt.Errorf("failed to parse expected-result.json: %w", err)
		}
		step.hasExpected = true
	}

	if errContent, err := fs.ReadFile(stepDir, "expected-error.txt"); err == nil {
		step.expectedError = strings.TrimSpace(string(errContent))
	}

	return step, nil
}

func (s *invokeMgmtStep) StepID() stepID { return s.id }

func (s *invokeMgmtStep) Run(ctx context.Context, t *testing.T, clients map[string]*emulatorClient) {
	t.Helper()
	client, ok := clients[s.database]
	require.True(t, ok, "%s: database %q not found in client map", s.id, s.database)

	debugf(t, "%s: [%s] %s", s.id, s.database, s.command)

	ds, err := client.mgmt(ctx, s.command)
	if len(s.expectedError) != 0 {
		require.ErrorContains(t, err, s.expectedError,
			"expected command to fail with %q", s.expectedError)
		return
	}
	require.NoError(t, err, "management command failed")

	actualRows := mgmtDatasetToRows(t, ds)

	if !s.hasExpected {
		debugf(t, "%s: returned %d rows (smoke test)", s.id, len(actualRows))
		return
	}

	compareRows(t, "command", s.expectedRows, actualRows)

	debugf(t, "%s: %d rows matched", s.id, len(actualRows))
}
